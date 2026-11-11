package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/session"
	"github.com/katharinasick/aocn/api/internal/users"
)

var octo = users.User{GitHubID: 42, Login: "Octo", AvatarURL: "https://example.com/42.png"}

// fakeProfiles knows octo, who passed door 1 an hour after it opened and door 3 the day
// before it opened, as a previewer. octo's card is public only if private is false.
type fakeProfiles struct {
	down    bool
	private bool
}

var octoSince = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func (f *fakeProfiles) ByLogin(_ context.Context, login string) (users.Profile, error) {
	switch {
	case f.down:
		return users.Profile{}, errors.New("database is down")
	case strings.EqualFold(login, octo.Login):
		return users.Profile{User: octo, Since: octoSince, Public: !f.private}, nil
	}
	return users.Profile{}, users.ErrNotFound
}

func (f *fakeProfiles) SetPublic(_ context.Context, id int64, public bool) error {
	switch {
	case f.down:
		return errors.New("database is down")
	case id != octo.GitHubID:
		return users.ErrNotFound
	}
	f.private = !public
	return nil
}

func (f *fakeProfiles) FirstPasses(_ context.Context, id int64) (map[int]time.Time, error) {
	if id != octo.GitHubID {
		return nil, nil
	}
	return map[int]time.Time{
		1: door1OpensAt.Add(time.Hour),
		3: door3OpensAt.Add(-24 * time.Hour),
	}, nil
}

func profilesAt(t *testing.T, now time.Time, f *fakeProfiles) http.Handler {
	t.Helper()
	sched, err := schedule.Parse([]byte(testSchedule))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(nopLogger{}, Subscriptions{}, Doors{}, Auth{}, Profiles{
		Schedule:   sched,
		Users:      f,
		Results:    f,
		SessionKey: sessionKey,
		Now:        func() time.Time { return now },
	})
}

func profileAt(t *testing.T, now time.Time) profileResponse {
	t.Helper()
	rec := get(profilesAt(t, now, &fakeProfiles{}), "/users/octo")
	var body profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	return body
}

func TestProfile(t *testing.T) {
	body := profileAt(t, door1OpensAt.Add(2*time.Hour))
	if body.Login != "Octo" || body.AvatarURL != octo.AvatarURL || !body.Since.Equal(octoSince) || !body.Public || body.Own {
		t.Errorf("got %+v", body)
	}
	if len(body.Doors) != 1 || body.Doors[0].Door != 1 || !body.Doors[0].SolvedAt.Equal(door1OpensAt.Add(time.Hour)) {
		t.Errorf("doors: %+v", body.Doors)
	}
}

// A previewer's pass shows only once the door is open, not a second earlier.
func TestProfileShowsADoorOnlyOnceItsOpen(t *testing.T) {
	if got := profileAt(t, door3OpensAt.Add(-time.Second)).Doors; len(got) != 1 {
		t.Errorf("before door 3 opens: %+v", got)
	}
	got := profileAt(t, door3OpensAt).Doors
	if len(got) != 2 || got[0].Door != 1 || got[1].Door != 3 {
		t.Errorf("once door 3 is open: %+v", got)
	}
	// Solved the day before, but the public card can't say so: that would tell who previews.
	if len(got) == 2 && !got[1].SolvedAt.Equal(door3OpensAt) {
		t.Errorf("door 3 solved at %s, want its opening %s", got[1].SolvedAt, door3OpensAt)
	}
}

func TestProfileWithoutDoorsIsAnEmptyList(t *testing.T) {
	rec := get(profilesAt(t, door1OpensAt.Add(-time.Hour), &fakeProfiles{}), "/users/octo")
	if !strings.Contains(rec.Body.String(), `"doors":[]`) {
		t.Errorf("got %s", rec.Body.String())
	}
}

func TestProfileNotFound(t *testing.T) {
	h := profilesAt(t, door1OpensAt, &fakeProfiles{})
	for _, login := range []string{"nobody", "-octo", "octo_", "a" + strings.Repeat("b", 39), "%2e%2e"} {
		if rec := get(h, "/users/"+login); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", login, rec.Code)
		}
	}
}

func TestProfileWhenTheDatabaseIsDown(t *testing.T) {
	rec := get(profilesAt(t, door1OpensAt, &fakeProfiles{down: true}), "/users/octo")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// websiteSession is the session cookie of someone logged in on the website at now.
func websiteSession(t *testing.T, githubUserID int64, now time.Time) *http.Cookie {
	t.Helper()
	signed, err := session.Sign(sessionKey, githubUserID, now, session.Website)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: signed}
}

// A private card is a 404 to everyone but its owner, the same as a login nobody has.
func TestPrivateProfileIsOnlyForItsOwner(t *testing.T) {
	now := door1OpensAt.Add(2 * time.Hour)
	h := profilesAt(t, now, &fakeProfiles{private: true})
	nobody := request(h, http.MethodGet, "/users/nobody").Body.String()

	cliToken, err := session.Sign(sessionKey, octo.GitHubID, now, session.CLI)
	if err != nil {
		t.Fatal(err)
	}
	strangers := map[string]*httptest.ResponseRecorder{
		"logged out":             request(h, http.MethodGet, "/users/octo"),
		"someone else":           request(h, http.MethodGet, "/users/octo", websiteSession(t, 7, now)),
		"octo's expired session": request(h, http.MethodGet, "/users/octo", websiteSession(t, octo.GitHubID, now.Add(-session.Website.LastsFor-time.Second))),
		"octo's CLI token":       request(h, http.MethodGet, "/users/octo", &http.Cookie{Name: sessionCookie, Value: cliToken}),
	}
	for who, rec := range strangers {
		if rec.Code != http.StatusNotFound || rec.Body.String() != nobody {
			t.Errorf("%s: status %d, %s", who, rec.Code, rec.Body.String())
		}
	}

	rec := request(h, http.MethodGet, "/users/OCTO", websiteSession(t, octo.GitHubID, now))
	var body profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("octo: status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	if body.Public || !body.Own || len(body.Doors) != 1 {
		t.Errorf("octo: %+v", body)
	}
}

func TestPublicProfileSaysWhoseItIs(t *testing.T) {
	now := door1OpensAt.Add(2 * time.Hour)
	h := profilesAt(t, now, &fakeProfiles{})
	for id, own := range map[int64]bool{octo.GitHubID: true, 7: false} {
		var body profileResponse
		rec := request(h, http.MethodGet, "/users/octo", websiteSession(t, id, now))
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Public || body.Own != own {
			t.Errorf("as %d: %+v, %v", id, body, err)
		}
	}
}

// putVisibility sends PUT /me/profile with this body and cookies.
func putVisibility(h http.Handler, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/me/profile", strings.NewReader(body))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSetProfileVisibility(t *testing.T) {
	now := door1OpensAt.Add(2 * time.Hour)
	f := &fakeProfiles{private: true}
	h := profilesAt(t, now, f)
	me := websiteSession(t, octo.GitHubID, now)

	rec := putVisibility(h, `{"public": true}`, me)
	if rec.Code != http.StatusOK || f.private || strings.TrimSpace(rec.Body.String()) != `{"public":true}` {
		t.Fatalf("going public: status %d, private %v, %s", rec.Code, f.private, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	if rec := request(h, http.MethodGet, "/users/octo"); rec.Code != http.StatusOK {
		t.Errorf("public card, logged out: status %d", rec.Code)
	}

	if rec := putVisibility(h, `{"public": false}`, me); rec.Code != http.StatusOK || !f.private {
		t.Fatalf("going private: status %d, private %v", rec.Code, f.private)
	}
	if rec := request(h, http.MethodGet, "/users/octo"); rec.Code != http.StatusNotFound {
		t.Errorf("private card, logged out: status %d", rec.Code)
	}
}

func TestSetProfileVisibilityRefuses(t *testing.T) {
	now := door1OpensAt.Add(2 * time.Hour)
	cliToken, err := session.Sign(sessionKey, octo.GitHubID, now, session.CLI)
	if err != nil {
		t.Fatal(err)
	}
	me := websiteSession(t, octo.GitHubID, now)
	for name, c := range map[string]struct {
		body    string
		cookies []*http.Cookie
		status  int
	}{
		"logged out":      {`{"public": true}`, nil, http.StatusUnauthorized},
		"a CLI token":     {`{"public": true}`, []*http.Cookie{{Name: sessionCookie, Value: cliToken}}, http.StatusUnauthorized},
		"an unknown user": {`{"public": true}`, []*http.Cookie{websiteSession(t, 404, now)}, http.StatusUnauthorized},
		"no body":         {``, []*http.Cookie{me}, http.StatusBadRequest},
		"public missing":  {`{}`, []*http.Cookie{me}, http.StatusBadRequest},
		"public not bool": {`{"public": "yes"}`, []*http.Cookie{me}, http.StatusBadRequest},
		"public null":     {`{"public": null}`, []*http.Cookie{me}, http.StatusBadRequest},
	} {
		f := &fakeProfiles{private: true}
		rec := putVisibility(profilesAt(t, now, f), c.body, c.cookies...)
		if rec.Code != c.status || !f.private {
			t.Errorf("%s: status %d, private %v", name, rec.Code, f.private)
		}
	}
	rec := putVisibility(profilesAt(t, now, &fakeProfiles{down: true}), `{"public": true}`, me)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("database down: status %d", rec.Code)
	}
}
