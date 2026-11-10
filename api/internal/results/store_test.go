package results

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/db"
)

// testStore connects to TEST_DATABASE_URL, migrates it and empties the tables. Skipped
// without it, like the subscribe store's tests.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), "TRUNCATE verify_results, users"); err != nil {
		t.Fatalf("emptying tables: %v", err)
	}
	return NewStore(pool), pool
}

func TestRecordKeepsEveryRunAndTheNewestLogin(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	at := time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

	runs := []struct {
		user   User
		result Result
	}{
		{User{42, "octo", "https://example.com/a.png"}, Result{Door: 1, Checks: []CheckResult{{ID: "a", Got: "403 Forbidden"}}, At: at}},
		// Renamed in between.
		{User{42, "octo-renamed", "https://example.com/b.png"}, Result{Door: 1, Passed: true, Checks: []CheckResult{{ID: "a", Passed: true}}, At: at.Add(time.Hour)}},
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
