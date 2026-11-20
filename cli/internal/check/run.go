package check

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Check is one of a door's checks, as the API hands it out. Exactly one of Ready, HTTP and
// Job is set. Checks only ever come from the API, never from the player's folder, so editing
// files can't make a door pass.
type Check struct {
	// ID names the check when its result is sent back.
	ID    string     `json:"id"`
	Ready []string   `json:"ready,omitempty"`
	HTTP  *HTTPCheck `json:"http,omitempty"`
	Job   string     `json:"job,omitempty"`
	// Goal is the line of the door's mission the check belongs to. aocn verify shows one line
	// per goal instead of each check, so it doesn't give away what to change.
	Goal string `json:"goal,omitempty"`
}

// HTTPCheck is a request from a workload, with what it should get. See aocn check http.
type HTTPCheck struct {
	Method string `json:"method"`
	To     string `json:"to"`
	From   string `json:"from"`
	Expect string `json:"expect"`
	Data   string `json:"data,omitempty"`
}

// Outcome is one check's result, as sent to the API.
type Outcome struct {
	ID     string `json:"id"`
	Passed bool   `json:"passed"`
	Got    string `json:"got,omitempty"`
}

// NotRun is what a check got when it didn't run, because a ready check before it failed.
const NotRun = "not run"

// step runs one check, once it's known what it is.
type step func(ctx context.Context, f *Fixtures) (Result, error)

// ran is a check's result, and whether it couldn't even start.
type ran struct {
	Result
	ran    bool
	broken bool
}

// Run runs the checks and returns one outcome per check, in their order. Ready checks run
// first, one after the other: if one fails, the rest would only fail because the workloads
// are still starting, so they count as failed without running. All other checks run at the
// same time, and are shown by goal once all are done. The error is only for Ctrl+C.
func Run(ctx context.Context, out io.Writer, checks []Check) ([]Outcome, error) {
	return run(ctx, out, checks, plan)
}

func run(ctx context.Context, out io.Writer, checks []Check, plan func(Check) (step, error)) ([]Outcome, error) {
	f := &Fixtures{}
	defer f.Close()
	results := make([]ran, len(checks))

	for i, c := range checks {
		if c.Ready == nil {
			continue
		}
		r, err := runOne(ctx, f, c, plan)
		if err != nil {
			return nil, err
		}
		results[i] = r
		Print(out, r.Result)
		if !r.Passed {
			return outcomes(checks, results), nil
		}
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var stopped error
	for i, c := range checks {
		if c.Ready != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := runOne(ctx, f, c, plan)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				stopped = err
				return
			}
			results[i] = r
		}()
	}
	wg.Wait()
	if stopped != nil {
		return nil, stopped
	}
	show(out, checks, results)
	return outcomes(checks, results), nil
}

// runOne runs one check. A check that can't be planned didn't pass, and says why.
func runOne(ctx context.Context, f *Fixtures, c Check, plan func(Check) (step, error)) (ran, error) {
	s, err := plan(c)
	if err != nil {
		// The door's fault, or an aocn too old for it. Either way the check didn't pass.
		return ran{Result: Result{Got: err.Error(), Message: err.Error()}, ran: true, broken: true}, nil
	}
	r, err := s(ctx, f)
	if err != nil {
		return ran{}, err
	}
	return ran{Result: r, ran: true}, nil
}

// show prints the checks that aren't ready checks, in their order: one line per goal, with how
// many of its checks failed, and each check without a goal on its own line. A check that
// couldn't start says so anyway: that's not the player's to fix.
func show(out io.Writer, checks []Check, results []ran) {
	shown := map[string]bool{}
	for i, c := range checks {
		if c.Ready != nil {
			continue
		}
		if c.Goal == "" {
			Print(out, results[i].Result)
			continue
		}
		if shown[c.Goal] {
			continue
		}
		shown[c.Goal] = true
		total, failed := 0, 0
		var broken []string
		for j, other := range checks {
			if other.Goal != c.Goal || other.Ready != nil {
				continue
			}
			total++
			if !results[j].Passed {
				failed++
			}
			if results[j].broken {
				broken = append(broken, results[j].Message)
			}
		}
		line := c.Goal
		if failed > 0 {
			line += fmt.Sprintf(" (%d of %d checks failed)", failed, total)
		}
		mark(out, failed == 0, line)
		for _, b := range broken {
			fmt.Fprintf(out, "     %s\n", b)
		}
	}
}

// outcomes turns results into what the API gets: one per check, NotRun for those that didn't.
func outcomes(checks []Check, results []ran) []Outcome {
	out := make([]Outcome, len(checks))
	for i, c := range checks {
		if !results[i].ran {
			out[i] = Outcome{ID: c.ID, Got: NotRun}
			continue
		}
		out[i] = Outcome{ID: c.ID, Passed: results[i].Passed, Got: results[i].Got}
	}
	return out
}

// plan works out what a check does, with the same rules as aocn check.
func plan(c Check) (step, error) {
	switch {
	case c.Ready != nil:
		return func(ctx context.Context, _ *Fixtures) (Result, error) {
			return Ready(ctx, c.Ready)
		}, nil

	case c.HTTP != nil:
		target, err := ParseTarget(c.HTTP.To)
		if err != nil {
			return nil, brokenCheck(err)
		}
		source, err := ParseSource(c.HTTP.From)
		if err != nil {
			return nil, brokenCheck(err)
		}
		expect, err := ParseExpect(c.HTTP.Expect)
		if err != nil {
			return nil, brokenCheck(err)
		}
		return func(ctx context.Context, f *Fixtures) (Result, error) {
			return HTTP(ctx, f, c.HTTP.Method, source, target, c.HTTP.Data, expect)
		}, nil

	case c.Job != "":
		source, err := ParseSource(c.Job)
		if err != nil || source.Kind != KindCronJob {
			return nil, brokenCheck(fmt.Errorf("job must be NAMESPACE/cronjob/NAME, got %q", c.Job))
		}
		return func(ctx context.Context, _ *Fixtures) (Result, error) {
			return Job(ctx, source)
		}, nil
	}
	// A kind of check this aocn doesn't know: the API is newer.
	return nil, fmt.Errorf("this door has a check your aocn doesn't know yet. Update aocn and try again")
}

func brokenCheck(err error) error {
	return fmt.Errorf("one of this door's checks is broken, which is not your fault: %w", err)
}

// Failed counts the checks that ran and didn't pass. Checks that didn't run aren't counted:
// the one that stopped the run is what to fix first.
func Failed(outcomes []Outcome) int {
	n := 0
	for _, o := range outcomes {
		if !o.Passed && o.Got != NotRun {
			n++
		}
	}
	return n
}
