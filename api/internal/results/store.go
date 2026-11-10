// Package results keeps what aocn verify sends: who ran it, for which door, and what each
// check got.
package results

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// User is who sent a result, as GitHub knows them.
type User struct {
	GitHubID  int64
	Login     string
	AvatarURL string
}

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
func (s *Store) Record(ctx context.Context, user User, result Result) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	if _, err := tx.Exec(ctx, `
		INSERT INTO users (github_user_id, login, avatar_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (github_user_id) DO UPDATE
		SET login = EXCLUDED.login, avatar_url = EXCLUDED.avatar_url, updated_at = EXCLUDED.updated_at`,
		user.GitHubID, user.Login, user.AvatarURL, result.At,
	); err != nil {
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
