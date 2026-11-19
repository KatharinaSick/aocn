// Package leaderboards keeps the leaderboards players make for their own groups: who owns
// each, who's in it, and its settings. It never decides who may see or change one; the API
// does. Standings aren't kept here: they're worked out from the members' results on every read.
package leaderboards

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// MaxOwned is how many leaderboards one player may own.
	MaxOwned = 3
	// MaxMembers is how many players fit on one, owner included, the same as Advent of Code.
	MaxMembers = 200
	// MaxJoined is how many leaderboards one player may be on, owned ones included. Plenty
	// for anyone's teams and meetups, and it bounds the work of their card and their list,
	// which rank them on every one.
	MaxJoined = 50
)

var (
	// ErrNotFound means there's no leaderboard with that ID or join code.
	ErrNotFound = errors.New("no such leaderboard")
	// ErrTooMany means the player owns MaxOwned already.
	ErrTooMany = errors.New("too many leaderboards")
	// ErrTooManyJoined means the player is on MaxJoined leaderboards already.
	ErrTooManyJoined = errors.New("on too many leaderboards")
	// ErrFull means the leaderboard has MaxMembers already.
	ErrFull = errors.New("leaderboard is full")
	// ErrClosed means the way in no longer works: the invite link was replaced, or the board
	// isn't open to anyone anymore.
	ErrClosed = errors.New("leaderboard can't be joined this way")
	// ErrBanned means the owner removed the player, and hasn't let them back in.
	ErrBanned = errors.New("removed from this leaderboard")
)

// Leaderboard is one leaderboard and its settings.
type Leaderboard struct {
	ID      string
	OwnerID int64
	// OwnerLogin is the owner's GitHub login.
	OwnerLogin string
	Name       string
	Public     bool
	OpenJoin   bool
	// JoinCode is in the invite link. It's not the ID, so seeing a public board doesn't let
	// anyone join it.
	JoinCode string
	// CountsUntil is the deadline: only fixes up to it count. Zero for none.
	CountsUntil time.Time
	CreatedAt   time.Time
	// Members is how many players are in it, owner included.
	Members int
}

// Member is a player on a leaderboard.
type Member struct {
	GitHubID int64
	Login    string
	// ProfilePublic is whether their card may be linked to.
	ProfilePublic bool
	JoinedAt      time.Time
}

// tokenBytes is how much randomness an ID or a join code has: 96 bits, far past guessing.
const tokenBytes = 12

// tokenPattern is what an ID or a join code looks like: 16 characters of URL-safe base64.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)

// ValidToken says whether text could be an ID or a join code at all, so the API can answer
// anything else without asking the database.
func ValidToken(text string) bool { return tokenPattern.MatchString(text) }

func newToken() string {
	b := make([]byte, tokenBytes)
	rand.Read(b) // never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// columns are what scan reads, with the member count.
const columns = `l.id, l.owner_id, (SELECT u.login FROM users u WHERE u.github_user_id = l.owner_id), l.name, l.public, l.open_join, l.join_code, l.counts_until, l.created_at,
	(SELECT count(*) FROM leaderboard_members m WHERE m.leaderboard_id = l.id)`

func scan(row pgx.Row) (Leaderboard, error) {
	var l Leaderboard
	var until *time.Time
	err := row.Scan(&l.ID, &l.OwnerID, &l.OwnerLogin, &l.Name, &l.Public, &l.OpenJoin, &l.JoinCode, &until, &l.CreatedAt, &l.Members)
	if errors.Is(err, pgx.ErrNoRows) {
		return Leaderboard{}, ErrNotFound
	}
	if until != nil {
		l.CountsUntil = *until
	}
	return l, err
}

// Create makes a private leaderboard owned by ownerID, with them as its first member. It's
// ErrTooMany once they own MaxOwned. The owner must exist.
func (s *Store) Create(ctx context.Context, ownerID int64, name string, now time.Time) (Leaderboard, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Leaderboard{}, err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	// Locking the owner makes two creates at once count one after the other, so nobody ends
	// up with one too many.
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT true FROM users WHERE github_user_id = $1 FOR UPDATE", ownerID).Scan(&exists); err != nil {
		return Leaderboard{}, err
	}
	var owned int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM leaderboards WHERE owner_id = $1", ownerID).Scan(&owned); err != nil {
		return Leaderboard{}, err
	}
	if owned >= MaxOwned {
		return Leaderboard{}, ErrTooMany
	}
	if err := checkJoined(ctx, tx, ownerID); err != nil {
		return Leaderboard{}, err
	}
	l := Leaderboard{ID: newToken(), OwnerID: ownerID, Name: name, JoinCode: newToken(), CreatedAt: now, Members: 1}
	if _, err := tx.Exec(ctx,
		"INSERT INTO leaderboards (id, owner_id, name, join_code, created_at) VALUES ($1, $2, $3, $4, $5)",
		l.ID, l.OwnerID, l.Name, l.JoinCode, l.CreatedAt,
	); err != nil {
		return Leaderboard{}, err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO leaderboard_members (leaderboard_id, github_user_id, joined_at) VALUES ($1, $2, $3)",
		l.ID, ownerID, now,
	); err != nil {
		return Leaderboard{}, err
	}
	return l, tx.Commit(ctx)
}

// Get returns the leaderboard with this ID.
func (s *Store) Get(ctx context.Context, id string) (Leaderboard, error) {
	return scan(s.pool.QueryRow(ctx, "SELECT "+columns+" FROM leaderboards l WHERE l.id = $1", id))
}

// ByJoinCode returns the leaderboard whose invite link has this code.
func (s *Store) ByJoinCode(ctx context.Context, code string) (Leaderboard, error) {
	return scan(s.pool.QueryRow(ctx, "SELECT "+columns+" FROM leaderboards l WHERE l.join_code = $1", code))
}

// OfMember returns the leaderboards the player is in, owned ones included, oldest first.
func (s *Store) OfMember(ctx context.Context, githubUserID int64) ([]Leaderboard, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+columns+` FROM leaderboards l
		JOIN leaderboard_members me ON me.leaderboard_id = l.id AND me.github_user_id = $1
		ORDER BY l.created_at, l.id`, githubUserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []Leaderboard{}
	for rows.Next() {
		l, err := scan(rows)
		if err != nil {
			return nil, err
		}
		boards = append(boards, l)
	}
	return boards, rows.Err()
}

// checkJoined is ErrTooManyJoined if the player is on MaxJoined boards already. The caller
// holds the lock on the player.
func checkJoined(ctx context.Context, tx pgx.Tx, githubUserID int64) error {
	var joined int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM leaderboard_members WHERE github_user_id = $1", githubUserID).Scan(&joined); err != nil {
		return err
	}
	if joined >= MaxJoined {
		return ErrTooManyJoined
	}
	return nil
}

// MembersOf returns who's on each of the leaderboards, by ID, in the order they joined: one
// query for all of them. A board nobody's on is missing.
func (s *Store) MembersOf(ctx context.Context, ids []string) (map[string][]Member, error) {
	members := make(map[string][]Member, len(ids))
	if len(ids) == 0 {
		return members, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT m.leaderboard_id, u.github_user_id, u.login, u.profile_public, m.joined_at
		FROM leaderboard_members m JOIN users u ON u.github_user_id = m.github_user_id
		WHERE m.leaderboard_id = ANY($1)
		ORDER BY m.joined_at, u.github_user_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var m Member
		if err := rows.Scan(&id, &m.GitHubID, &m.Login, &m.ProfilePublic, &m.JoinedAt); err != nil {
			return nil, err
		}
		members[id] = append(members[id], m)
	}
	return members, rows.Err()
}

// Members returns who's on the leaderboard, in the order they joined.
func (s *Store) Members(ctx context.Context, id string) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.github_user_id, u.login, u.profile_public, m.joined_at
		FROM leaderboard_members m JOIN users u ON u.github_user_id = m.github_user_id
		WHERE m.leaderboard_id = $1
		ORDER BY m.joined_at, u.github_user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.GitHubID, &m.Login, &m.ProfilePublic, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// IsMember says whether the player is on the leaderboard.
func (s *Store) IsMember(ctx context.Context, id string, githubUserID int64) (bool, error) {
	var member bool
	err := s.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM leaderboard_members WHERE leaderboard_id = $1 AND github_user_id = $2)",
		id, githubUserID,
	).Scan(&member)
	return member, err
}

// Join adds the player to the leaderboard, with the invite link's code, or without one if
// the board is public and open to anyone. Both are checked again while the board is locked,
// so a link replaced a moment ago doesn't let anyone in anymore. Joining one they're in
// changes nothing. It's ErrClosed if the way in doesn't work, ErrFull at MaxMembers and
// ErrNotFound if the board is gone, and ErrBanned if its owner removed them. The player must
// exist.
func (s *Store) Join(ctx context.Context, id, code string, githubUserID int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	// Locking the board makes joins at once count one after the other, so it never ends up
	// with one too many.
	var joinCode string
	var open bool
	err = tx.QueryRow(ctx, "SELECT join_code, public AND open_join FROM leaderboards WHERE id = $1 FOR UPDATE", id).Scan(&joinCode, &open)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if (code != "" && code != joinCode) || (code == "" && !open) {
		return ErrClosed
	}
	var member bool
	var members int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(bool_or(github_user_id = $2), false), count(*) FROM leaderboard_members WHERE leaderboard_id = $1`,
		id, githubUserID,
	).Scan(&member, &members); err != nil {
		return err
	}
	if member {
		return nil
	}
	// Under the board's lock too, so removing someone and their join at the same moment
	// can't let them back in.
	var banned bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM leaderboard_bans WHERE leaderboard_id = $1 AND github_user_id = $2)",
		id, githubUserID,
	).Scan(&banned); err != nil {
		return err
	}
	if banned {
		return ErrBanned
	}
	if members >= MaxMembers {
		return ErrFull
	}
	// Locking the player too makes their joins at once count one after the other. The board
	// is always locked first, so two joins can't wait on each other.
	if _, err := tx.Exec(ctx, "SELECT 1 FROM users WHERE github_user_id = $1 FOR UPDATE", githubUserID); err != nil {
		return err
	}
	if err := checkJoined(ctx, tx, githubUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO leaderboard_members (leaderboard_id, github_user_id, joined_at) VALUES ($1, $2, $3)",
		id, githubUserID, now,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Leave takes the player off the leaderboard, on their own: they may join again. It's
// ErrNotFound if they weren't on it. Whether they may leave (the owner can't) is the API's to
// decide.
func (s *Store) Leave(ctx context.Context, id string, githubUserID int64) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM leaderboard_members WHERE leaderboard_id = $1 AND github_user_id = $2", id, githubUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Remove takes the player off the leaderboard for its owner, and keeps them off: they can't
// join again until Unban. It's ErrNotFound if they weren't on it. Whether they may be removed
// (the owner can't) is the API's to decide.
func (s *Store) Remove(ctx context.Context, id string, githubUserID int64, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) // a no-op once committed

	// The same lock as Join's, so a join at the same moment comes either before (and is
	// removed) or after (and is refused).
	if _, err := tx.Exec(ctx, "SELECT 1 FROM leaderboards WHERE id = $1 FOR UPDATE", id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		"DELETE FROM leaderboard_members WHERE leaderboard_id = $1 AND github_user_id = $2", id, githubUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO leaderboard_bans (leaderboard_id, github_user_id, created_at) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
		id, githubUserID, now,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Banned is a player the owner removed from a leaderboard.
type Banned struct {
	GitHubID  int64
	Login     string
	RemovedAt time.Time
}

// Banned returns who the owner removed and hasn't let back in, most recent first.
func (s *Store) Banned(ctx context.Context, id string) ([]Banned, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.github_user_id, u.login, b.created_at
		FROM leaderboard_bans b JOIN users u ON u.github_user_id = b.github_user_id
		WHERE b.leaderboard_id = $1
		ORDER BY b.created_at DESC, u.github_user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	banned := []Banned{}
	for rows.Next() {
		var b Banned
		if err := rows.Scan(&b.GitHubID, &b.Login, &b.RemovedAt); err != nil {
			return nil, err
		}
		banned = append(banned, b)
	}
	return banned, rows.Err()
}

// Unban lets a removed player join the leaderboard again: it doesn't add them. It's
// ErrNotFound if they weren't removed.
func (s *Store) Unban(ctx context.Context, id string, githubUserID int64) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM leaderboard_bans WHERE leaderboard_id = $1 AND github_user_id = $2", id, githubUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Changes are the settings to change; nil ones stay as they are.
type Changes struct {
	Name     *string
	Public   *bool
	OpenJoin *bool
	// CountsUntil sets the deadline; a pointer to the zero time removes it.
	CountsUntil *time.Time
}

// Update changes the leaderboard's settings and returns it as it is now.
func (s *Store) Update(ctx context.Context, id string, c Changes) (Leaderboard, error) {
	setUntil := c.CountsUntil != nil
	var until *time.Time
	if setUntil && !c.CountsUntil.IsZero() {
		until = c.CountsUntil
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE leaderboards SET
			name = COALESCE($2, name),
			public = COALESCE($3, public),
			open_join = COALESCE($4, open_join),
			counts_until = CASE WHEN $5 THEN $6 ELSE counts_until END
		WHERE id = $1`,
		id, c.Name, c.Public, c.OpenJoin, setUntil, until,
	)
	if err != nil {
		return Leaderboard{}, err
	}
	if tag.RowsAffected() == 0 {
		return Leaderboard{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// NewJoinCode replaces the leaderboard's join code, so the old invite link stops working,
// and returns the new one.
func (s *Store) NewJoinCode(ctx context.Context, id string) (string, error) {
	code := newToken()
	tag, err := s.pool.Exec(ctx, "UPDATE leaderboards SET join_code = $2 WHERE id = $1", id, code)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return code, nil
}

// Delete removes the leaderboard, for everyone.
func (s *Store) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM leaderboards WHERE id = $1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
