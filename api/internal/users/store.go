// Package users keeps everyone who logged in on the website or sent a result from aocn
// verify, by GitHub user ID.
package users

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is someone as GitHub knows them. GitHubID is what everything is kept under: a login
// can be renamed and then taken by someone else, an ID can't.
type User struct {
	GitHubID  int64
	Login     string
	AvatarURL string
}

// ErrNotFound means nobody with that ID ever logged in or sent a result.
var ErrNotFound = errors.New("no such user")

// Execer runs a statement: a pool, or a transaction that does more than this.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Upsert adds the user, or refreshes their login and avatar, which GitHub lets them change.
func Upsert(ctx context.Context, db Execer, user User, at time.Time) error {
	_, err := db.Exec(ctx, `
		INSERT INTO users (github_user_id, login, avatar_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (github_user_id) DO UPDATE
		SET login = EXCLUDED.login, avatar_url = EXCLUDED.avatar_url, updated_at = EXCLUDED.updated_at`,
		user.GitHubID, user.Login, user.AvatarURL, at,
	)
	return err
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Save adds the user or refreshes their login and avatar.
func (s *Store) Save(ctx context.Context, user User, at time.Time) error {
	return Upsert(ctx, s.pool, user, at)
}

// Get returns the user with this GitHub ID.
func (s *Store) Get(ctx context.Context, githubUserID int64) (User, error) {
	u := User{GitHubID: githubUserID}
	err := s.pool.QueryRow(ctx,
		"SELECT login, avatar_url FROM users WHERE github_user_id = $1", githubUserID,
	).Scan(&u.Login, &u.AvatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
