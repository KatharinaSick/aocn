// Package results keeps what players do on a door: every aocn verify, with what each check
// got, and every hint they opened. Points are worked out from these on every read (package
// scoring), never stored.
package results

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/scoring"
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
	// NotReady means the run failed at a ready check: the workloads weren't up yet. It never
	// counts as a failed verify.
	NotReady bool
	Checks   []CheckResult
	At       time.Time
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
		"INSERT INTO verify_results (github_user_id, door, passed, not_ready, checks, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		user.GitHubID, result.Door, result.Passed, result.NotReady && !result.Passed, result.Checks, result.At,
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

// ErrHintOutOfOrder means a hint was asked for before the one before it: hints open in order.
var ErrHintOutOfOrder = errors.New("hints open in order")

// OpenHint records that the user opened the door's hint at index (0-based), at. Opening one
// again changes nothing. Hints open in order: index must be one already opened, or the next
// one, otherwise it's ErrHintOutOfOrder. The user must exist.
//
// Checking and recording are one statement, so two requests at once can't skip a hint or
// record one twice: the second either finds it there or doesn't find the one before it yet.
func (s *Store) OpenHint(ctx context.Context, githubUserID int64, door, index int, at time.Time) error {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO hint_views (github_user_id, door, hint, created_at)
		SELECT $1::bigint, $2::integer, $3::integer, $4::timestamptz
		WHERE (SELECT count(*) FROM hint_views WHERE github_user_id = $1 AND door = $2) = $3::integer
		ON CONFLICT DO NOTHING`,
		githubUserID, door, index, at,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var opened bool
	if err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM hint_views WHERE github_user_id = $1 AND door = $2 AND hint = $3)",
		githubUserID, door, index,
	).Scan(&opened); err != nil {
		return err
	}
	if !opened {
		return ErrHintOutOfOrder
	}
	return nil
}

// Progress returns everything recorded for each of the users, by door: their runs and the
// hints they opened. A user with nothing recorded has an empty Progress.
func (s *Store) Progress(ctx context.Context, githubUserIDs []int64) (map[int64]scoring.Progress, error) {
	progress := make(map[int64]scoring.Progress, len(githubUserIDs))
	for _, id := range githubUserIDs {
		progress[id] = scoring.Progress{Runs: map[int][]scoring.Run{}, Hints: map[int][]scoring.Hint{}}
	}
	if len(githubUserIDs) == 0 {
		return progress, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT github_user_id, door, passed, not_ready, created_at FROM verify_results
		WHERE github_user_id = ANY($1)
		ORDER BY created_at`, githubUserIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var door int
		var r scoring.Run
		if err := rows.Scan(&id, &door, &r.Passed, &r.NotReady, &r.At); err != nil {
			rows.Close()
			return nil, err
		}
		progress[id].Runs[door] = append(progress[id].Runs[door], r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT github_user_id, door, hint, created_at FROM hint_views
		WHERE github_user_id = ANY($1)
		ORDER BY hint`, githubUserIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var door int
		var h scoring.Hint
		if err := rows.Scan(&id, &door, &h.Index, &h.At); err != nil {
			return nil, err
		}
		progress[id].Hints[door] = append(progress[id].Hints[door], h)
	}
	return progress, rows.Err()
}
