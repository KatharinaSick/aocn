package results

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/db/dbtest"
	"github.com/katharinasick/aocn/api/internal/scoring"
	"github.com/katharinasick/aocn/api/internal/users"
)

// testStore gives each test an empty database of its own.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t, "results")
	return NewStore(pool), pool
}

func TestRecordKeepsEveryRunAndTheNewestLogin(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

	runs := []struct {
		user   users.User
		result Result
	}{
		{users.User{GitHubID: 42, Login: "octo", AvatarURL: "https://example.com/a.png"}, Result{Door: 1, Checks: []CheckResult{{ID: "a", Got: "403 Forbidden"}}, At: at}},
		// Renamed in between.
		{users.User{GitHubID: 42, Login: "octo-renamed", AvatarURL: "https://example.com/b.png"}, Result{Door: 1, Passed: true, Checks: []CheckResult{{ID: "a", Passed: true}}, At: at.Add(time.Hour)}},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r.user, r.result); err != nil {
			t.Fatal(err)
		}
	}

	var login string
	var createdAt time.Time
	if err := pool.QueryRow(ctx, "SELECT login, created_at FROM users WHERE github_user_id = 42").Scan(&login, &createdAt); err != nil {
		t.Fatal(err)
	}
	if login != "octo-renamed" || !createdAt.Equal(at) {
		t.Errorf("user: login %q, created %v", login, createdAt)
	}

	rows, err := pool.Query(ctx, "SELECT passed, checks->0->>'id', created_at FROM verify_results WHERE github_user_id = 42 AND door = 1 ORDER BY created_at")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []bool
	for rows.Next() {
		var passed bool
		var id string
		var created time.Time
		if err := rows.Scan(&passed, &id, &created); err != nil {
			t.Fatal(err)
		}
		if id != "a" {
			t.Errorf("checks: first ID %q", id)
		}
		got = append(got, passed)
	}
	if len(got) != 2 || got[0] || !got[1] {
		t.Errorf("runs: got %v, want [false true]", got)
	}
}

func TestFirstPassesKeepsTheFirstPassOfEachDoor(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)
	octo := users.User{GitHubID: 42, Login: "octo", AvatarURL: "a"}
	other := users.User{GitHubID: 43, Login: "other", AvatarURL: "b"}

	runs := []struct {
		user   users.User
		result Result
	}{
		{octo, Result{Door: 1, Passed: false, Checks: []CheckResult{}, At: at}},
		{octo, Result{Door: 1, Passed: true, Checks: []CheckResult{}, At: at.Add(time.Hour)}},
		{octo, Result{Door: 1, Passed: true, Checks: []CheckResult{}, At: at.Add(2 * time.Hour)}},
		{octo, Result{Door: 2, Passed: false, Checks: []CheckResult{}, At: at}},
		{other, Result{Door: 2, Passed: true, Checks: []CheckResult{}, At: at}},
	}
	for _, r := range runs {
		if err := store.Record(ctx, r.user, r.result); err != nil {
			t.Fatal(err)
		}
	}

	passes, err := store.FirstPasses(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(passes) != 1 || !passes[1].Equal(at.Add(time.Hour)) {
		t.Errorf("got %v", passes)
	}
}

func TestOpenHintOpensInOrderAndOnce(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)
	if err := users.Upsert(ctx, pool, users.User{GitHubID: 42, Login: "octo", AvatarURL: "a"}, at); err != nil {
		t.Fatal(err)
	}

	steps := []struct {
		door, index int
		want        error
	}{
		{1, 1, ErrHintOutOfOrder}, // not the first
		{1, 0, nil},
		{1, 0, nil}, // again: nothing changes
		{1, 2, ErrHintOutOfOrder},
		{1, 1, nil},
		{3, 0, nil}, // each door on its own
	}
	for i, s := range steps {
		if err := store.OpenHint(ctx, 42, s.door, s.index, at.Add(time.Duration(i)*time.Minute)); !errors.Is(err, s.want) {
			t.Errorf("step %d (door %d, hint %d): got %v, want %v", i, s.door, s.index, err, s.want)
		}
	}

	progress, err := store.Progress(ctx, []int64{42})
	if err != nil {
		t.Fatal(err)
	}
	hints := progress[42].Hints
	// Opening hint 0 again kept the first time.
	if len(hints[1]) != 2 || hints[1][0].Index != 0 || !hints[1][0].At.Equal(at.Add(time.Minute)) || hints[1][1].Index != 1 {
		t.Errorf("door 1: %+v", hints[1])
	}
	if len(hints[3]) != 1 {
		t.Errorf("door 3: %+v", hints[3])
	}
}

func TestOpenHintNeedsTheUser(t *testing.T) {
	store, _ := testStore(t)
	if err := store.OpenHint(context.Background(), 404, 1, 0, time.Now()); err == nil {
		t.Error("recorded a hint for nobody")
	}
}

func TestProgress(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)
	octo := users.User{GitHubID: 42, Login: "octo", AvatarURL: "a"}
	other := users.User{GitHubID: 43, Login: "other", AvatarURL: "b"}
	for _, r := range []struct {
		user   users.User
		result Result
	}{
		{octo, Result{Door: 1, NotReady: true, Checks: []CheckResult{}, At: at}},
		{octo, Result{Door: 1, Checks: []CheckResult{}, At: at.Add(time.Minute)}},
		// Never kept as not ready: it passed.
		{octo, Result{Door: 1, Passed: true, NotReady: true, Checks: []CheckResult{}, At: at.Add(2 * time.Minute)}},
		{other, Result{Door: 3, Passed: true, Checks: []CheckResult{}, At: at}},
	} {
		if err := store.Record(ctx, r.user, r.result); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.OpenHint(ctx, 43, 3, 0, at); err != nil {
		t.Fatal(err)
	}

	progress, err := store.Progress(ctx, []int64{42, 43, 44})
	if err != nil {
		t.Fatal(err)
	}
	runs := progress[42].Runs[1]
	want := []scoring.Run{
		{At: at, NotReady: true},
		{At: at.Add(time.Minute)},
		{At: at.Add(2 * time.Minute), Passed: true},
	}
	if len(runs) != len(want) {
		t.Fatalf("octo's runs: %+v", runs)
	}
	for i := range want {
		if !runs[i].At.Equal(want[i].At) || runs[i].Passed != want[i].Passed || runs[i].NotReady != want[i].NotReady {
			t.Errorf("run %d: got %+v, want %+v", i, runs[i], want[i])
		}
	}
	if len(progress[42].Hints) != 0 || len(progress[43].Runs[3]) != 1 || len(progress[43].Hints[3]) != 1 {
		t.Errorf("got %+v", progress)
	}
	// Someone with nothing recorded is there, empty.
	if p, ok := progress[44]; !ok || len(p.Runs) != 0 || p.Hints == nil {
		t.Errorf("nobody: %+v, %v", p, ok)
	}
}
