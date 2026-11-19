package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/db/dbtest"
	"github.com/katharinasick/aocn/api/internal/leaderboards"
	"github.com/katharinasick/aocn/api/internal/results"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/session"
	"github.com/katharinasick/aocn/api/internal/users"
)

// The leaderboard endpoints decide who may see and change what, so these tests run them
// against the real stores, in a database of their own: what's tested is what runs.

// Who's in these tests: alice owns boards, bob and carol join them, mallory never gets in.
const (
	alice   int64 = 101
	bob     int64 = 102
	carol   int64 = 103
	mallory int64 = 104
)

var boardNow = door1OpensAt.Add(48 * time.Hour)

type boardAPI struct {
	http.Handler
	boards  *leaderboards.Store
	results *results.Store
	users   *users.Store
	clock   *time.Time
}

func boardsAt(t *testing.T) boardAPI {
	t.Helper()
	pool := dbtest.Pool(t, "api_leaderboards")
	ctx := context.Background()
	accounts := users.NewStore(pool)
	for id, login := range map[int64]string{alice: "alice", bob: "Bob", carol: "carol", mallory: "mallory"} {
		if err := accounts.Save(ctx, users.User{GitHubID: id, Login: login, AvatarURL: "https://example.com/" + login}, door1OpensAt); err != nil {
			t.Fatal(err)
		}
	}
	sched, err := schedule.Parse([]byte(testSchedule))
	if err != nil {
		t.Fatal(err)
	}
	now := boardNow
	a := boardAPI{boards: leaderboards.NewStore(pool), results: results.NewStore(pool), clock: &now}
	boards := Leaderboards{
		Schedule:   sched,
		Store:      a.boards,
		Results:    a.results,
		Accounts:   accounts,
		SessionKey: sessionKey,
		Now:        func() time.Time { return *a.clock },
	}
	a.Handler = NewHandler(nopLogger{}, Subscriptions{}, Doors{}, Auth{}, Profiles{
		Schedule:   sched,
		Users:      accounts,
		Results:    a.results,
		Boards:     boards,
		SessionKey: sessionKey,
		Now:        func() time.Time { return *a.clock },
	}, boards)
	a.users = accounts
	return a
}

// as sends a request from the website, logged in as id (0: logged out), with a JSON body
// unless body is empty.
func (a boardAPI) as(id int64, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if id != 0 {
		signed, err := session.Sign(sessionKey, id, *a.clock, session.Website)
		if err != nil {
			panic(err)
		}
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: signed})
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, want int) T {
	t.Helper()
	var v T
	if rec.Code != want {
		t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return v
}

// create makes a board owned by owner and returns it, join code included.
func (a boardAPI) create(t *testing.T, owner int64, name string) leaderboards.Leaderboard {
	t.Helper()
	created := decode[createdResponse](t, a.as(owner, http.MethodPost, "/me/leaderboards", fmt.Sprintf(`{"name": %q}`, name)), http.StatusCreated)
	b, err := a.boards.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (a boardAPI) join(t *testing.T, b leaderboards.Leaderboard, id int64) {
	t.Helper()
	decode[createdResponse](t, a.as(id, http.MethodPost, "/join/"+b.JoinCode, `{}`), http.StatusOK)
}

func (a boardAPI) makePublic(t *testing.T, b leaderboards.Leaderboard, openJoin bool) {
	t.Helper()
	body := fmt.Sprintf(`{"public": true, "openJoin": %t}`, openJoin)
	if rec := a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, body); rec.Code != http.StatusOK {
		t.Fatalf("making it public: %d %s", rec.Code, rec.Body.String())
	}
}

// A private board is the same 404 as nothing, to everyone but its members.
func TestPrivateLeaderboardIsOnlyForMembers(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "Platform Team")
	a.join(t, b, bob)

	nothing := a.as(0, http.MethodGet, "/leaderboards/AAAAAAAAAAAAAAAA", "")
	for name, id := range map[string]int64{"logged out": 0, "not a member": mallory} {
		rec := a.as(id, http.MethodGet, "/leaderboards/"+b.ID, "")
		if rec.Code != http.StatusNotFound || rec.Body.String() != nothing.Body.String() {
			t.Errorf("%s: status %d, %s", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "Platform") || strings.Contains(rec.Body.String(), b.JoinCode) {
			t.Errorf("%s: gave something away: %s", name, rec.Body.String())
		}
	}
	for _, id := range []int64{alice, bob} {
		got := decode[boardResponse](t, a.as(id, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
		if got.Name != "Platform Team" || got.Members != 2 || !got.You.Member || got.You.Owner != (id == alice) || got.JoinCode != b.JoinCode || got.Owner.Login != "alice" {
			t.Errorf("member %d: %+v", id, got)
		}
	}
	if got := a.as(bob, http.MethodGet, "/leaderboards/"+b.ID, "").Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control %q", got)
	}
	// A CLI token isn't a website session.
	req := httptest.NewRequest(http.MethodGet, "/leaderboards/"+b.ID, nil)
	req.Header.Set("Authorization", "Bearer "+cliToken(bob))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("with a CLI token: status %d", rec.Code)
	}
	// Nonsense IDs don't reach the database, and look the same.
	for _, id := range []string{"x", "..%2F..%2Fusers", strings.Repeat("A", 17)} {
		if rec := a.as(bob, http.MethodGet, "/leaderboards/"+id, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%q: status %d", id, rec.Code)
		}
	}
}

// A public board is shown to anyone, but its invite link only to members.
func TestPublicLeaderboard(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "KubeCon Crew")
	a.makePublic(t, b, false)
	for name, id := range map[string]int64{"logged out": 0, "not a member": mallory} {
		got := decode[boardResponse](t, a.as(id, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
		if got.Name != "KubeCon Crew" || got.JoinCode != "" || got.You.Member || got.You.Owner || got.You.LoggedIn != (id != 0) {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestCreateLeaderboard(t *testing.T) {
	a := boardsAt(t)
	if rec := a.as(0, http.MethodPost, "/me/leaderboards", `{"name": "x"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("logged out: status %d", rec.Code)
	}
	// Only JSON: a form on another site can't send it.
	req := httptest.NewRequest(http.MethodPost, "/me/leaderboards", strings.NewReader("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	signed, _ := session.Sign(sessionKey, alice, boardNow, session.Website)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: signed})
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a form: status %d", rec.Code)
	}

	for _, body := range []string{
		`{"name": ""}`, `{"name": "   "}`, `{"name": "` + strings.Repeat("x", 41) + `"}`,
		`{"name": "two\nlines"}`, `{"name": "tab\there"}`, `{}`, `{"name": "x", "public": true}`, `not json`,
	} {
		if rec := a.as(alice, http.MethodPost, "/me/leaderboards", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, rec.Code)
		}
	}

	// Trimmed, 40 characters at most, counted as characters, not bytes.
	b := a.create(t, alice, "  "+strings.Repeat("ö", 40)+" ")
	if b.Name != strings.Repeat("ö", 40) || b.Public || b.OpenJoin {
		t.Errorf("created %+v", b)
	}
	a.create(t, alice, "two")
	a.create(t, alice, "three")
	if rec := a.as(alice, http.MethodPost, "/me/leaderboards", `{"name": "four"}`); rec.Code != http.StatusConflict {
		t.Errorf("a fourth: status %d", rec.Code)
	}

	mine := decode[myLeaderboardsResponse](t, a.as(alice, http.MethodGet, "/me/leaderboards", ""), http.StatusOK)
	if len(mine.Leaderboards) != 3 || mine.Owned != 3 || mine.MaxOwned != 3 || !mine.Leaderboards[0].Own || mine.Leaderboards[0].Rank != 1 {
		t.Errorf("mine: %+v", mine)
	}
	// Bob's list doesn't have them, until he joins one.
	if theirs := decode[myLeaderboardsResponse](t, a.as(bob, http.MethodGet, "/me/leaderboards", ""), http.StatusOK); len(theirs.Leaderboards) != 0 {
		t.Errorf("bob's: %+v", theirs)
	}
	a.join(t, b, bob)
	theirs := decode[myLeaderboardsResponse](t, a.as(bob, http.MethodGet, "/me/leaderboards", ""), http.StatusOK)
	if len(theirs.Leaderboards) != 1 || theirs.Leaderboards[0].Own || theirs.Owned != 0 || theirs.Leaderboards[0].Members != 2 {
		t.Errorf("bob's: %+v", theirs)
	}
	if rec := a.as(0, http.MethodGet, "/me/leaderboards", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("logged out: status %d", rec.Code)
	}
}

// Only the owner changes a board. Members get 403; everyone else can't tell it's there.
func TestOnlyTheOwnerChangesALeaderboard(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	a.join(t, b, bob)

	owned := []struct{ method, path, body string }{
		{http.MethodPatch, "/leaderboards/" + b.ID, `{"public": true}`},
		{http.MethodPost, "/leaderboards/" + b.ID + "/invite", `{}`},
		{http.MethodDelete, "/leaderboards/" + b.ID, ""},
		{http.MethodDelete, fmt.Sprintf("/leaderboards/%s/members/%d", b.ID, carol), ""},
	}
	a.join(t, b, carol)
	for _, r := range owned {
		if rec := a.as(0, r.method, r.path, r.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s logged out: status %d", r.method, r.path, rec.Code)
		}
		if rec := a.as(mallory, r.method, r.path, r.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s by a stranger: status %d", r.method, r.path, rec.Code)
		}
		if rec := a.as(bob, r.method, r.path, r.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s by a member: status %d", r.method, r.path, rec.Code)
		}
	}
	got, err := a.boards.Get(context.Background(), b.ID)
	if err != nil || got.Public || got.JoinCode != b.JoinCode || got.Members != 3 {
		t.Fatalf("changed by someone else: %+v, %v", got, err)
	}

	// Public now: strangers see it, and still can't change it.
	a.makePublic(t, b, false)
	for _, r := range owned {
		if rec := a.as(mallory, r.method, r.path, r.body); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s by a stranger, public: status %d", r.method, r.path, rec.Code)
		}
	}
}

func TestUpdateLeaderboard(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	type settings struct {
		Name        string     `json:"name"`
		Public      bool       `json:"public"`
		OpenJoin    bool       `json:"openJoin"`
		CountsUntil *time.Time `json:"countsUntil"`
	}
	got := decode[settings](t, a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, `{"name": " Renamed ", "public": true, "openJoin": true, "countsUntil": "2026-12-31T23:59:00+01:00"}`), http.StatusOK)
	if got.Name != "Renamed" || !got.Public || !got.OpenJoin || got.CountsUntil == nil || !got.CountsUntil.Equal(time.Date(2026, 12, 31, 22, 59, 0, 0, time.UTC)) {
		t.Errorf("got %+v", got)
	}
	// Left out stays; null removes the deadline.
	got = decode[settings](t, a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, `{"countsUntil": null}`), http.StatusOK)
	if got.Name != "Renamed" || !got.Public || got.CountsUntil != nil {
		t.Errorf("no deadline: %+v", got)
	}
	for _, body := range []string{`{"name": ""}`, `{"countsUntil": "tomorrow"}`, `{"countsUntil": "1970-01-01T00:00:00Z"}`, `{"public": "yes"}`, `{"owner": 104}`} {
		if rec := a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPatch, "/leaderboards/"+b.ID, strings.NewReader(`{"public": false}`))
	req.Header.Set("Content-Type", "text/plain")
	signed, _ := session.Sign(sessionKey, alice, boardNow, session.Website)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: signed})
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("not JSON: status %d", rec.Code)
	}
}

func TestJoinWithTheInviteLink(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")

	// What the link is for, to anyone who has it. Not the ID of a private board.
	preview := decode[joinPreview](t, a.as(0, http.MethodGet, "/join/"+b.JoinCode, ""), http.StatusOK)
	if preview.Name != "board" || preview.ID != "" || preview.Members != 1 || preview.Owner.Login != "alice" || preview.Member || preview.Full {
		t.Errorf("preview: %+v", preview)
	}
	if rec := a.as(0, http.MethodPost, "/join/"+b.JoinCode, `{}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("logged out: status %d", rec.Code)
	}
	if rec := a.as(bob, http.MethodPost, "/join/"+b.JoinCode, ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("not JSON: status %d", rec.Code)
	}
	// The ID is no join code.
	if rec := a.as(bob, http.MethodPost, "/join/"+b.ID, `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("with the ID: status %d", rec.Code)
	}
	a.join(t, b, bob)
	a.join(t, b, bob) // again: nothing changes
	preview = decode[joinPreview](t, a.as(bob, http.MethodGet, "/join/"+b.JoinCode, ""), http.StatusOK)
	if !preview.Member || preview.ID != b.ID || preview.Members != 2 {
		t.Errorf("preview as a member: %+v", preview)
	}

	// A new invite link: the old one stops working, the new one works.
	invite := decode[struct {
		JoinCode string `json:"joinCode"`
	}](t, a.as(alice, http.MethodPost, "/leaderboards/"+b.ID+"/invite", `{}`), http.StatusOK)
	if invite.JoinCode == b.JoinCode || !leaderboards.ValidToken(invite.JoinCode) {
		t.Fatalf("new code %q", invite.JoinCode)
	}
	for _, rec := range []*httptest.ResponseRecorder{
		a.as(carol, http.MethodGet, "/join/"+b.JoinCode, ""),
		a.as(carol, http.MethodPost, "/join/"+b.JoinCode, `{}`),
	} {
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "isn't valid") {
			t.Errorf("old link: status %d, %s", rec.Code, rec.Body.String())
		}
	}
	decode[createdResponse](t, a.as(carol, http.MethodPost, "/join/"+invite.JoinCode, `{}`), http.StatusOK)
}

func TestJoinOpenLeaderboard(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	join := func(id int64) int { return a.as(id, http.MethodPost, "/leaderboards/"+b.ID+"/join", `{}`).Code }

	// Private: as if it weren't there.
	if code := join(bob); code != http.StatusNotFound {
		t.Errorf("private: status %d", code)
	}
	// Open join means nothing while it's private.
	if rec := a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, `{"openJoin": true}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if code := join(bob); code != http.StatusNotFound {
		t.Errorf("private, open join: status %d", code)
	}
	// Public, invite only: watching is fine, joining needs the link.
	a.makePublic(t, b, false)
	if code := join(bob); code != http.StatusForbidden {
		t.Errorf("invite only: status %d", code)
	}
	a.makePublic(t, b, true)
	if code := join(0); code != http.StatusUnauthorized {
		t.Errorf("open, logged out: status %d", code)
	}
	if code := join(bob); code != http.StatusOK {
		t.Errorf("open: status %d", code)
	}
	if member, _ := a.boards.IsMember(context.Background(), b.ID, bob); !member {
		t.Error("bob didn't join")
	}
}

func TestLeaderboardIsFull(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	ctx := context.Background()
	// Fill it up to the limit straight in the store, with made-up players.
	for id := int64(1000); id < 1000+leaderboards.MaxMembers-1; id++ {
		if err := a.saveUser(ctx, id); err != nil {
			t.Fatal(err)
		}
		if err := a.boards.Join(ctx, b.ID, b.JoinCode, id, boardNow); err != nil {
			t.Fatal(err)
		}
	}
	if rec := a.as(bob, http.MethodPost, "/join/"+b.JoinCode, `{}`); rec.Code != http.StatusConflict {
		t.Errorf("full: status %d", rec.Code)
	}
	if preview := decode[joinPreview](t, a.as(bob, http.MethodGet, "/join/"+b.JoinCode, ""), http.StatusOK); !preview.Full {
		t.Errorf("preview: %+v", preview)
	}
}

func TestLeaveAndRemove(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	a.join(t, b, bob)
	a.join(t, b, carol)
	remove := func(by, who int64) int {
		return a.as(by, http.MethodDelete, fmt.Sprintf("/leaderboards/%s/members/%d", b.ID, who), "").Code
	}

	if code := remove(bob, carol); code != http.StatusForbidden {
		t.Errorf("a member removing another: status %d", code)
	}
	if code := remove(alice, alice); code != http.StatusConflict {
		t.Errorf("the owner leaving: status %d", code)
	}
	if code := remove(bob, alice); code != http.StatusConflict {
		t.Errorf("removing the owner: status %d", code)
	}
	if code := remove(bob, bob); code != http.StatusNoContent {
		t.Errorf("leaving: status %d", code)
	}
	// Gone, so the board is a 404 for him now.
	if rec := a.as(bob, http.MethodGet, "/leaderboards/"+b.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("after leaving: status %d", rec.Code)
	}
	if code := remove(alice, carol); code != http.StatusNoContent {
		t.Errorf("removing: status %d", code)
	}
	if code := remove(alice, carol); code != http.StatusNotFound {
		t.Errorf("removing again: status %d", code)
	}
	if code := remove(alice, 0); code != http.StatusNotFound {
		t.Errorf("removing nobody: status %d", code)
	}
	// Bob left on his own, so the link lets him back in.
	a.join(t, b, bob)
	// Carol was removed, so it doesn't, until alice lets her back in.
	if rec := a.as(carol, http.MethodPost, "/join/"+b.JoinCode, `{}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "removed you") {
		t.Errorf("rejoining after being removed: %d %s", rec.Code, rec.Body.String())
	}
	got := decode[boardResponse](t, a.as(alice, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
	if got.Members != 2 || len(got.Removed) != 1 || got.Removed[0].ID != carol || got.Removed[0].Login != "carol" {
		t.Errorf("owner's view: members %d, removed %+v", got.Members, got.Removed)
	}
	// Only the owner sees who was removed.
	if got := decode[boardResponse](t, a.as(bob, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK); got.Removed != nil {
		t.Errorf("a member sees who was removed: %+v", got.Removed)
	}
	letBackIn := func(by, who int64) int {
		return a.as(by, http.MethodDelete, fmt.Sprintf("/leaderboards/%s/removed/%d", b.ID, who), "").Code
	}
	if code := letBackIn(bob, carol); code != http.StatusForbidden {
		t.Errorf("a member letting someone back in: status %d", code)
	}
	if code := letBackIn(mallory, carol); code != http.StatusNotFound {
		t.Errorf("a stranger letting someone back in: status %d", code)
	}
	if code := letBackIn(alice, carol); code != http.StatusNoContent {
		t.Errorf("letting back in: status %d", code)
	}
	if code := letBackIn(alice, carol); code != http.StatusNotFound {
		t.Errorf("letting back in again: status %d", code)
	}
	a.join(t, b, carol)
	got = decode[boardResponse](t, a.as(alice, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
	if got.Members != 3 || len(got.Removed) != 0 {
		t.Errorf("after letting back in: members %d, removed %+v", got.Members, got.Removed)
	}
}

func TestDeleteLeaderboard(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	a.join(t, b, bob)
	if rec := a.as(alice, http.MethodDelete, "/leaderboards/"+b.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	for _, id := range []int64{alice, bob} {
		if rec := a.as(id, http.MethodGet, "/leaderboards/"+b.ID, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%d after deleting: status %d", id, rec.Code)
		}
	}
	if rec := a.as(bob, http.MethodGet, "/join/"+b.JoinCode, ""); rec.Code != http.StatusNotFound {
		t.Errorf("link after deleting: status %d", rec.Code)
	}
}

// record saves a run for id at the time.
func (a boardAPI) record(t *testing.T, id int64, door int, passed bool, at time.Time) {
	t.Helper()
	u := users.User{GitHubID: id, Login: map[int64]string{alice: "alice", bob: "Bob", carol: "carol", mallory: "mallory"}[id], AvatarURL: "a"}
	if err := a.results.Record(context.Background(), u, results.Result{Door: door, Passed: passed, Checks: []results.CheckResult{}, At: at}); err != nil {
		t.Fatal(err)
	}
}

func (a boardAPI) saveUser(ctx context.Context, id int64) error {
	return a.results.Record(ctx, users.User{GitHubID: id, Login: fmt.Sprintf("p%d", id), AvatarURL: "a"}, results.Result{Door: 1, Checks: []results.CheckResult{}, At: boardNow})
}

func TestStandings(t *testing.T) {
	a := boardsAt(t)
	b := a.create(t, alice, "board")
	a.join(t, b, bob)
	a.join(t, b, carol)
	ctx := context.Background()
	h := time.Hour

	// alice: door 1 after one failed verify. bob: door 1 with the first hint, and a previewer's
	// pass of door 3, which isn't open yet. carol: nothing fixed, one failed verify.
	a.record(t, alice, 1, false, door1OpensAt.Add(h))
	a.record(t, alice, 1, true, door1OpensAt.Add(2*h))
	if _, err := a.results.OpenHint(ctx, bob, 1, 0, door1OpensAt.Add(h)); err != nil {
		t.Fatal(err)
	}
	a.record(t, bob, 1, true, door1OpensAt.Add(3*h))
	a.record(t, bob, 3, true, door1OpensAt.Add(4*h))
	a.record(t, carol, 1, false, door1OpensAt.Add(h))

	*a.clock = door1OpensAt.Add(5 * h)
	got := decode[boardResponse](t, a.as(carol, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
	want := []struct {
		login                       string
		rank, points, failed, fixed int
		you                         bool
	}{
		{"alice", 1, 100, 1, 1, false},
		{"Bob", 2, 95, 0, 1, false},
		{"carol", 3, 0, 0, 0, true},
	}
	if len(got.Standings) != len(want) {
		t.Fatalf("standings: %+v", got.Standings)
	}
	for i, w := range want {
		s := got.Standings[i]
		if s.Login != w.login || s.Rank != w.rank || s.Points != w.points || s.FailedVerifies != w.failed || s.Fixed != w.fixed || s.You != w.you {
			t.Errorf("%d: got %+v, want %+v", i, s, w)
		}
	}
	if d := got.Standings[1].Doors; len(d) != 1 || d[0] != (standingDoor{Door: 1, Points: 95, Hints: 1}) {
		t.Errorf("bob's doors: %+v", d)
	}
	if got.Together != 2 || len(got.Doors) != 2 || !got.Doors[0].Counts || got.Doors[1].Counts || got.Final {
		t.Errorf("together %d, doors %+v, final %v", got.Together, got.Doors, got.Final)
	}

	// Door 3 opens: bob's early pass counts, clean.
	*a.clock = door3OpensAt
	got = decode[boardResponse](t, a.as(carol, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
	if got.Standings[0].Login != "Bob" || got.Standings[0].Points != 195 || got.Together != 3 {
		t.Errorf("after door 3 opens: %+v", got.Standings)
	}

	// A deadline before door 3 opened: final, and door 3 doesn't count here.
	until := door3OpensAt.Add(-h)
	if rec := a.as(alice, http.MethodPatch, "/leaderboards/"+b.ID, fmt.Sprintf(`{"countsUntil": %q}`, until.Format(time.RFC3339))); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	got = decode[boardResponse](t, a.as(carol, http.MethodGet, "/leaderboards/"+b.ID, ""), http.StatusOK)
	if !got.Final || got.CountsUntil == nil || got.Standings[0].Login != "alice" || got.Standings[1].Points != 95 || got.Doors[1].Counts {
		t.Errorf("with a deadline: final %v, %+v, doors %+v", got.Final, got.Standings, got.Doors)
	}
	// Bob's rank on his list says the same.
	mine := decode[myLeaderboardsResponse](t, a.as(bob, http.MethodGet, "/me/leaderboards", ""), http.StatusOK)
	if len(mine.Leaderboards) != 1 || mine.Leaderboards[0].Rank != 2 || !mine.Leaderboards[0].Final {
		t.Errorf("bob's list: %+v", mine)
	}
}

// A card lists the player's leaderboards that who looks may see: public ones, and private
// ones they're in too. A private board never shows outside it, not even its name.
func TestCardListsTheLeaderboardsTheViewerMaySee(t *testing.T) {
	a := boardsAt(t)
	team := a.create(t, alice, "Secret Team")
	*a.clock = a.clock.Add(time.Minute)
	crew := a.create(t, alice, "Public Crew")
	a.makePublic(t, crew, false)
	a.join(t, team, bob)
	a.join(t, crew, carol)
	a.record(t, carol, 1, true, door1OpensAt.Add(time.Hour))
	if err := a.users.SetPublic(context.Background(), alice, true); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		viewer int64
		want   []cardLeaderboard
	}{
		{"logged out", 0, []cardLeaderboard{{ID: crew.ID, Name: "Public Crew", Rank: 2, Members: 2}}},
		{"in neither", mallory, []cardLeaderboard{{ID: crew.ID, Name: "Public Crew", Rank: 2, Members: 2}}},
		{"in the public one", carol, []cardLeaderboard{{ID: crew.ID, Name: "Public Crew", Rank: 2, Members: 2}}},
		{"in the private one", bob, []cardLeaderboard{{ID: team.ID, Name: "Secret Team", Rank: 1, Members: 2}, {ID: crew.ID, Name: "Public Crew", Rank: 2, Members: 2}}},
		{"the owner of the card", alice, []cardLeaderboard{{ID: team.ID, Name: "Secret Team", Rank: 1, Members: 2}, {ID: crew.ID, Name: "Public Crew", Rank: 2, Members: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := a.as(tt.viewer, http.MethodGet, "/users/alice", "")
			got := decode[profileResponse](t, rec, http.StatusOK)
			if fmt.Sprint(got.Leaderboards) != fmt.Sprint(tt.want) {
				t.Errorf("got %+v, want %+v", got.Leaderboards, tt.want)
			}
			if len(tt.want) == 1 && strings.Contains(rec.Body.String(), "Secret") {
				t.Errorf("gave the private board away: %s", rec.Body.String())
			}
		})
	}

	// A private card stays a 404, leaderboards or not.
	if err := a.users.SetPublic(context.Background(), alice, false); err != nil {
		t.Fatal(err)
	}
	if rec := a.as(bob, http.MethodGet, "/users/alice", ""); rec.Code != http.StatusNotFound {
		t.Errorf("private card: status %d", rec.Code)
	}
}
