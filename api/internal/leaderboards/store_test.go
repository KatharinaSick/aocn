package leaderboards

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/db/dbtest"
	"github.com/katharinasick/aocn/api/internal/users"
)

var now = time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)

// testStore gives each test an empty database of its own, with players 1 to n.
func testStore(t *testing.T, n int) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t, "leaderboards")
	for id := 1; id <= n; id++ {
		u := users.User{GitHubID: int64(id), Login: fmt.Sprintf("player%d", id), AvatarURL: "a"}
		if err := users.Upsert(context.Background(), pool, u, now); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(pool), pool
}

func TestCreate(t *testing.T) {
	store, _ := testStore(t, 1)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "Platform Team", now)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidToken(l.ID) || !ValidToken(l.JoinCode) || l.ID == l.JoinCode {
		t.Errorf("ID %q, join code %q", l.ID, l.JoinCode)
	}
	got, err := store.Get(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Private, invite only and without a deadline until the owner says otherwise.
	if got.Name != "Platform Team" || got.OwnerID != 1 || got.Public || got.OpenJoin || !got.CountsUntil.IsZero() || got.Members != 1 || !got.CreatedAt.Equal(now) {
		t.Errorf("got %+v", got)
	}
	if member, err := store.IsMember(ctx, l.ID, 1); err != nil || !member {
		t.Errorf("owner is a member: %v, %v", member, err)
	}
	if byCode, err := store.ByJoinCode(ctx, l.JoinCode); err != nil || byCode.ID != l.ID {
		t.Errorf("by join code: %+v, %v", byCode, err)
	}
	if _, err := store.Get(ctx, "AAAAAAAAAAAAAAAA"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ID: %v", err)
	}
	if _, err := store.ByJoinCode(ctx, l.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("the ID as a join code: %v", err)
	}
}

// Nobody owns more than MaxOwned, not even with every create at once.
func TestCreateLimit(t *testing.T) {
	store, _ := testStore(t, 2)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, MaxOwned+3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = store.Create(ctx, 1, "board", now)
		}()
	}
	wg.Wait()
	created := 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, ErrTooMany):
			t.Errorf("unexpected: %v", err)
		}
	}
	if created != MaxOwned {
		t.Errorf("created %d, want %d", created, MaxOwned)
	}
	// Someone else still can.
	if _, err := store.Create(ctx, 2, "board", now); err != nil {
		t.Errorf("player 2: %v", err)
	}
	// Owning fewer makes room again.
	boards, err := store.OfMember(ctx, 1)
	if err != nil || len(boards) != MaxOwned {
		t.Fatalf("boards: %d, %v", len(boards), err)
	}
	if err := store.Delete(ctx, boards[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ctx, 1, "board", now); err != nil {
		t.Errorf("after deleting one: %v", err)
	}
}

func TestJoinAndRemove(t *testing.T) {
	store, _ := testStore(t, 3)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Again: nothing changes, not even when they joined.
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	members, err := store.Members(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || members[0].GitHubID != 1 || members[1].GitHubID != 2 || members[1].Login != "player2" || !members[1].JoinedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("members: %+v", members)
	}
	if got, _ := store.Get(ctx, l.ID); got.Members != 2 {
		t.Errorf("member count %d", got.Members)
	}
	boards, err := store.OfMember(ctx, 2)
	if err != nil || len(boards) != 1 || boards[0].ID != l.ID {
		t.Errorf("player 2's boards: %+v, %v", boards, err)
	}
	if boards, _ := store.OfMember(ctx, 3); len(boards) != 0 {
		t.Errorf("player 3's boards: %+v", boards)
	}

	if err := store.Leave(ctx, l.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.Leave(ctx, l.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("leaving again: %v", err)
	}
	if member, _ := store.IsMember(ctx, l.ID, 2); member {
		t.Error("still a member")
	}
	// Leaving on your own keeps the way back open.
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now); err != nil {
		t.Errorf("joining after leaving: %v", err)
	}
	if err := store.Join(ctx, "AAAAAAAAAAAAAAAA", "x", 2, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown board: %v", err)
	}
}

// Someone the owner removed stays out, with the link or without, until the owner lets them
// back in. Letting them back in doesn't add them.
func TestRemoveKeepsThemOut(t *testing.T) {
	store, _ := testStore(t, 3)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err := store.Update(ctx, l.ID, Changes{Public: &yes, OpenJoin: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(ctx, l.ID, 3, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing a non-member: %v", err)
	}
	if err := store.Remove(ctx, l.ID, 2, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if member, _ := store.IsMember(ctx, l.ID, 2); member {
		t.Error("still a member")
	}
	for name, code := range map[string]string{"with the link": l.JoinCode, "open": ""} {
		if err := store.Join(ctx, l.ID, code, 2, now); !errors.Is(err, ErrBanned) {
			t.Errorf("joining %s: %v", name, err)
		}
	}
	banned, err := store.Banned(ctx, l.ID)
	if err != nil || len(banned) != 1 || banned[0].GitHubID != 2 || banned[0].Login != "player2" || !banned[0].RemovedAt.Equal(now.Add(time.Hour)) {
		t.Errorf("banned: %+v, %v", banned, err)
	}

	if err := store.Unban(ctx, l.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := store.Unban(ctx, l.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("letting back in again: %v", err)
	}
	if member, _ := store.IsMember(ctx, l.ID, 2); member {
		t.Error("letting back in added them")
	}
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now); err != nil {
		t.Errorf("joining after being let back in: %v", err)
	}
	// Deleting the board takes its list along.
	if err := store.Remove(ctx, l.ID, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, l.ID); err != nil {
		t.Errorf("deleting with someone removed: %v", err)
	}
}

// A board never gets more than MaxMembers, not even with everyone joining at once.
func TestJoinLimit(t *testing.T) {
	store, _ := testStore(t, MaxMembers+5)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	full := 0
	for id := 2; id <= MaxMembers+5; id++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Join(ctx, l.ID, l.JoinCode, int64(id), now)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrFull):
				full++
			case err != nil:
				t.Errorf("player %d: %v", id, err)
			}
		}()
	}
	wg.Wait()
	got, err := store.Get(ctx, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Members != MaxMembers || full != 5 {
		t.Errorf("%d members, %d turned away", got.Members, full)
	}
}

func TestUpdate(t *testing.T) {
	store, _ := testStore(t, 1)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	name, yes := "Renamed", true
	until := time.Date(2026, 12, 31, 22, 59, 0, 0, time.UTC)
	got, err := store.Update(ctx, l.ID, Changes{Name: &name, Public: &yes, CountsUntil: &until})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || !got.Public || got.OpenJoin || !got.CountsUntil.Equal(until) {
		t.Errorf("got %+v", got)
	}
	// Leaving everything out changes nothing.
	got, err = store.Update(ctx, l.ID, Changes{OpenJoin: &yes})
	if err != nil || got.Name != "Renamed" || !got.Public || !got.OpenJoin || !got.CountsUntil.Equal(until) {
		t.Errorf("open join: %+v, %v", got, err)
	}
	// The zero time removes the deadline.
	got, err = store.Update(ctx, l.ID, Changes{CountsUntil: &time.Time{}})
	if err != nil || !got.CountsUntil.IsZero() {
		t.Errorf("no deadline: %+v, %v", got, err)
	}
	if _, err := store.Update(ctx, "AAAAAAAAAAAAAAAA", Changes{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown board: %v", err)
	}
}

func TestNewJoinCode(t *testing.T) {
	store, _ := testStore(t, 1)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	code, err := store.NewJoinCode(ctx, l.ID)
	if err != nil || code == l.JoinCode || !ValidToken(code) {
		t.Fatalf("code %q, %v", code, err)
	}
	if _, err := store.ByJoinCode(ctx, l.JoinCode); !errors.Is(err, ErrNotFound) {
		t.Errorf("old code: %v", err)
	}
	if got, err := store.ByJoinCode(ctx, code); err != nil || got.ID != l.ID {
		t.Errorf("new code: %+v, %v", got, err)
	}
}

func TestDeleteTakesTheMembersAlong(t *testing.T) {
	store, pool := testStore(t, 2)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM leaderboard_members").Scan(&left); err != nil || left != 0 {
		t.Errorf("%d members left, %v", left, err)
	}
	if err := store.Delete(ctx, l.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting again: %v", err)
	}
}

func TestValidToken(t *testing.T) {
	for _, s := range []string{newToken(), "AAAAAAAAAAAAAAAA", "abc-_DEF12345678"} {
		if !ValidToken(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "short", "AAAAAAAAAAAAAAAAA", "AAAAAAAAAAAAAAA=", "AAAAAAAAAAAAAAA/", "../AAAAAAAAAAAAA"} {
		if ValidToken(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

// The way in is checked under the lock: a replaced code, or a board that isn't open.
func TestJoinChecksTheWayIn(t *testing.T) {
	store, _ := testStore(t, 2)
	ctx := context.Background()
	l, err := store.Create(ctx, 1, "board", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, "", 2, now); !errors.Is(err, ErrClosed) {
		t.Errorf("private, no code: %v", err)
	}
	if _, err := store.NewJoinCode(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, l.JoinCode, 2, now); !errors.Is(err, ErrClosed) {
		t.Errorf("old code: %v", err)
	}
	yes := true
	if _, err := store.Update(ctx, l.ID, Changes{Public: &yes, OpenJoin: &yes}); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, l.ID, "", 2, now); err != nil {
		t.Errorf("open: %v", err)
	}
}
