// Package results keeps what aocn verify sends: who ran it, for which door, and what each
// check got.
package results

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/users"
)

// CheckResult is one check of a run: its ID from the door's checks.yaml, whether it passed,
// and what it got.
type CheckResult struct {
	ID     string `json:"id"`
	Passed bool   `json:"passed"`
	Got    string `json:"got,omitempty"`
}

// Result is one aocn verify.
type Result struct {
	Door   int
	Passed bool
	Checks []CheckResult
	At     time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Record keeps a result and refreshes the user's login and avatar, in one transaction.
func (s *Store) Record(ctx context.Context, user users.User, result Result) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	if err := users.Upsert(ctx, tx, user, result.At); err != nil {
		return err
	}
	// pgx sends a Go value as JSON for a JSONB column.
	if _, err := tx.Exec(ctx,
		"INSERT INTO verify_results (github_user_id, door, passed, checks, created_at) VALUES ($1, $2, $3, $4, $5)",
		user.GitHubID, result.Door, result.Passed, result.Checks, result.At,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FirstPasses returns, for each door the user ever passed, when they first did. Runs from
// before a door opened (previewers) are in it too; which doors to show is up to the caller.
func (s *Store) FirstPasses(ctx context.Context, githubUserID int64) (map[int]time.Time, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT door, min(created_at) FROM verify_results
		WHERE github_user_id = $1 AND passed
		GROUP BY door`, githubUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passes := map[int]time.Time{}
	for rows.Next() {
		var door int
		var at time.Time
		if err := rows.Scan(&door, &at); err != nil {
			return nil, err
		}
		passes[door] = at
	}
	return passes, rows.Err()
}
