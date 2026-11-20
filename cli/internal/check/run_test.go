package check

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// fakePlan passes every check whose ID starts with "pass". Checks run at the same time.
var mu sync.Mutex

func fakePlan(ran *[]string) func(Check) (step, error) {
	return func(c Check) (step, error) {
		return func(context.Context, *Fixtures) (Result, error) {
			mu.Lock()
			defer mu.Unlock()
			*ran = append(*ran, c.ID)
			return Result{Passed: strings.HasPrefix(c.ID, "pass"), Message: c.ID}, nil
		}, nil
	}
}

func TestRunGivesEveryCheckAnOutcome(t *testing.T) {
	var ran []string
	checks := []Check{{ID: "pass-ready", Ready: []string{"apps"}}, {ID: "pass-1", Job: "x"}, {ID: "fail-2", Job: "y"}}
	outcomes, err := run(context.Background(), io.Discard, checks, fakePlan(&ran), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Outcome{{ID: "pass-ready", Passed: true}, {ID: "pass-1", Passed: true}, {ID: "fail-2"}}
	if len(outcomes) != len(want) {
		t.Fatalf("got %+v", outcomes)
	}
	for i := range want {
		if outcomes[i] != want[i] {
			t.Errorf("outcome %d: got %+v, want %+v", i, outcomes[i], want[i])
		}
	}
	if Failed(outcomes) != 1 {
		t.Errorf("Failed = %d, want 1", Failed(outcomes))
	}
}

// A failed ready check stops the run, but every check still gets an outcome, so the API
// gets the whole set.
func TestRunStopsAfterAFailedReadyCheck(t *testing.T) {
	var ran []string
	checks := []Check{{ID: "fail-ready", Ready: []string{"apps"}}, {ID: "pass-1", Job: "x"}, {ID: "pass-2", Job: "y"}}
	outcomes, err := run(context.Background(), io.Discard, checks, fakePlan(&ran), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 {
		t.Errorf("ran %v, want only the ready check", ran)
	}
	if len(outcomes) != 3 || outcomes[1] != (Outcome{ID: "pass-1", Got: NotRun}) || outcomes[2].Passed {
		t.Errorf("got %+v", outcomes)
	}
	if Failed(outcomes) != 1 {
		t.Errorf("Failed = %d, want 1: only the ready check", Failed(outcomes))
	}
}

// A failed check that isn't a ready check doesn't stop anything.
func TestRunGoesOnAfterOtherFailures(t *testing.T) {
	var ran []string
	checks := []Check{{ID: "fail-1", Job: "x"}, {ID: "pass-2", Job: "y"}}
	if _, err := run(context.Background(), io.Discard, checks, fakePlan(&ran), nil); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 {
		t.Errorf("ran %v, want both", ran)
	}
}

func TestPlan(t *testing.T) {
	good := []Check{
		{Ready: []string{"apps"}},
		{HTTP: &HTTPCheck{Method: "GET", To: "apps/api/items", From: "apps/deploy/frontend", Expect: "2xx"}},
		{HTTP: &HTTPCheck{Method: "POST", To: "test-server", From: "apps/cronjob/cleanup", Expect: "blocked", Data: "{}"}},
		{Job: "apps/cronjob/cleanup"},
	}
	for _, c := range good {
		if _, err := plan(c); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}

	broken := map[string]Check{
		"bad target":     {HTTP: &HTTPCheck{Method: "GET", To: "apps", From: "apps/new-pod", Expect: "2xx"}},
		"bad source":     {HTTP: &HTTPCheck{Method: "GET", To: "apps/api", From: "apps/pod/x", Expect: "2xx"}},
		"bad expect":     {HTTP: &HTTPCheck{Method: "GET", To: "apps/api", From: "apps/new-pod", Expect: "ok"}},
		"job not a cron": {Job: "apps/deploy/frontend"},
		"unknown kind":   {ID: "from-a-newer-api"},
	}
	for name, c := range broken {
		if _, err := plan(c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Checks with a goal show as one line per goal, in the order the goals first come up, and
// don't say what they sent. Checks without one show their own line.
func TestRunShowsGoals(t *testing.T) {
	var ran []string
	checks := []Check{
		{ID: "pass-ready", Ready: []string{"apps"}},
		{ID: "pass-a", Job: "a", Goal: "First goal"},
		{ID: "fail-b", Job: "b", Goal: "Second goal"},
		{ID: "pass-c", Job: "c", Goal: "First goal"},
		{ID: "pass-d", Job: "d", Goal: "Second goal"},
		{ID: "fail-alone", Job: "e"},
	}
	var out bytes.Buffer
	if _, err := run(context.Background(), &out, checks, fakePlan(&ran), nil); err != nil {
		t.Fatal(err)
	}
	want := "  ✅ pass-ready\n  ✅ First goal\n  ❌ Second goal (1 of 2 checks failed)\n  ❌ fail-alone\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// A check that can't even start isn't the player's fault, so it says so under its goal.
func TestRunNamesBrokenChecksInAGoal(t *testing.T) {
	checks := []Check{{ID: "x", Goal: "A goal"}}
	var out bytes.Buffer
	broken := func(Check) (step, error) { return nil, errors.New("this check is broken") }
	if _, err := run(context.Background(), &out, checks, broken, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "❌ A goal (1 of 1 checks failed)\n     this check is broken") {
		t.Errorf("got %q", out.String())
	}
}

// No more than parallelChecks checks run at the same time, however many a door has.
func TestRunLimitsHowManyRunAtOnce(t *testing.T) {
	var mu sync.Mutex
	running, most := 0, 0
	release := make(chan struct{})
	plan := func(Check) (step, error) {
		return func(context.Context, *Fixtures) (Result, error) {
			mu.Lock()
			running++
			most = max(most, running)
			mu.Unlock()
			<-release
			mu.Lock()
			running--
			mu.Unlock()
			return Result{Passed: true}, nil
		}, nil
	}
	checks := make([]Check, 3*parallelChecks)
	for i := range checks {
		checks[i] = Check{ID: string(rune('a' + i)), Job: "x"}
	}
	done := make(chan error)
	go func() {
		_, err := run(context.Background(), io.Discard, checks, plan, nil)
		done <- err
	}()
	// The first ones fill every slot, and wait there. Then all are let through.
	for {
		mu.Lock()
		full := running == parallelChecks
		mu.Unlock()
		if full {
			break
		}
		runtime.Gosched()
	}
	for range checks {
		release <- struct{}{}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if most != parallelChecks {
		t.Errorf("%d ran at once, want %d", most, parallelChecks)
	}
}

// Progress counts the checks that aren't ready checks, from 0 to all of them.
func TestRunReportsProgress(t *testing.T) {
	var ran []string
	checks := []Check{
		{ID: "pass-ready", Ready: []string{"apps"}},
		{ID: "pass-a", Job: "a"},
		{ID: "fail-b", Job: "b"},
		{ID: "pass-c", Job: "c"},
	}
	var seen []int
	progress := func(done, total int) {
		if total != 3 {
			t.Errorf("total %d, want 3", total)
		}
		seen = append(seen, done)
	}
	if _, err := run(context.Background(), io.Discard, checks, fakePlan(&ran), progress); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 || seen[0] != 0 || seen[3] != 3 {
		t.Errorf("progress %v, want 0 to 3", seen)
	}
}
