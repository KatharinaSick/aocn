package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/session"
)

// A previewer of every door gets them all early, from every endpoint, marked as a preview.
func TestPreviewerGetsClosedDoors(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-30*24*time.Hour), uploaded)

	rec := getAs(h, "/doors/3", previewerToken)
	var page pageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("page: status %d, %v", rec.Code, err)
	}
	if !page.Preview || page.Title != "Second" || !page.OpensAt.Equal(door3OpensAt) {
		t.Errorf("page: got %+v", page)
	}

	rec = getAs(h, "/doors/3/bundle", previewerToken)
	opensAt, err := time.Parse(time.RFC3339, rec.Header().Get(previewHeader))
	if rec.Code != http.StatusOK || rec.Body.String() != "bundle 3" || err != nil || !opensAt.Equal(door3OpensAt) {
		t.Errorf("bundle: status %d, body %q, preview header %q", rec.Code, rec.Body.String(), rec.Header().Get(previewHeader))
	}

	rec = getAs(h, "/doors/3/checks", previewerToken)
	var checks checksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &checks); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("checks: status %d, %v", rec.Code, err)
	}
	if len(checks.Checks) != 2 || checks.Checks[1].Job != "secret-ns/cronjob/secret-job" || checks.Checks[1].ID == "" {
		t.Errorf("checks: got %+v", checks)
	}
}

// A previewer of one door gets that door early, and no other: not even the one before it.
func TestPreviewerOfOneDoorGetsOnlyThatDoor(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)

	for _, path := range []string{"/doors/3", "/doors/3/bundle", "/doors/3/checks"} {
		if rec := getAs(h, path, door3PreviewerToken); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rec.Code)
		}
	}
	for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/1/checks"} {
		rec := getAs(h, path, door3PreviewerToken)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
		assertNoSecrets(t, path, rec)
	}
}

// An open door isn't a preview for anyone, previewers included.
func TestOpenDoorIsNoPreview(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	rec := getAs(h, "/doors/1", previewerToken)
	var page pageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Preview {
		t.Errorf("page: preview on an open door")
	}
	if got := getAs(h, "/doors/1/bundle", previewerToken).Header().Get(previewHeader); got != "" {
		t.Errorf("bundle: preview header %q on an open door", got)
	}
}

// At 06:00 nothing may depend on GitHub: an open door never asks it, whatever token comes
// along, even a broken one.
func TestOpenDoorNeverAsksGitHub(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	h.users.down = true
	for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/1/checks"} {
		for _, token := range []string{"", playerToken, "made-up"} {
			if rec := getAs(h, path, token); rec.Code != http.StatusOK {
				t.Errorf("%s as %q: status %d, want 200", path, token, rec.Code)
			}
		}
	}
	if h.users.asked != 0 {
		t.Errorf("GitHub was asked %d times", h.users.asked)
	}
}

// Someone signed out gets the closed door without anyone asking GitHub.
func TestClosedDoorSignedOutNeverAsksGitHub(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	for _, path := range closedPaths {
		get(h, path)
	}
	if h.users.asked != 0 {
		t.Errorf("GitHub was asked %d times", h.users.asked)
	}
}

// A token GitHub doesn't accept gets a 401, so the CLI can say to sign in again, and nothing
// about the door.
func TestClosedDoorWithBadToken(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	for _, path := range closedPaths {
		rec := getAs(h, path, "made-up")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", path, rec.Code)
		}
		assertNoSecrets(t, path, rec)
	}

	// Not a bearer token at all.
	rec := withAuthorization(h, http.MethodGet, "/doors/1", "Basic dXNlcjpwYXNz", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("basic auth: status %d, want 401", rec.Code)
	}
	assertNoSecrets(t, "basic auth", rec)
}

// When GitHub can't say who's asking, a previewer gets an error, never the door by mistake.
func TestClosedDoorWhileGitHubIsDown(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	h.users.down = true
	for _, path := range closedPaths {
		rec := getAs(h, path, previewerToken)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, rec.Code)
		}
		assertNoSecrets(t, path, rec)
	}
}

// getWithSession sends a session cookie the way the website's function passes it on, signed
// for the user with key.
func getWithSession(t *testing.T, h http.Handler, path string, key []byte, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	signed, err := session.Sign(key, userID, door1OpensAt.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return request(h, http.MethodGet, path, &http.Cookie{Name: sessionCookie, Value: signed})
}

// On the website a previewer is known by the session cookie, and gets the door's page early,
// marked as a preview, without asking GitHub.
func TestPreviewerGetsTheClosedDoorPageWithTheSessionCookie(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)

	rec := getWithSession(t, h, "/doors/3", sessionKey, door3Previewer.ID)
	var page pageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	if !page.Preview || page.Title != "Second" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("got %+v, Cache-Control %q", page, rec.Header().Get("Cache-Control"))
	}
	if h.users.asked != 0 {
		t.Errorf("asked GitHub %d times", h.users.asked)
	}
}

// The cookie gets nothing it shouldn't: not someone else's door, and not with a cookie that
// isn't ours. Either way the page answers like for anyone: closed, never 401.
func TestSessionCookieGetsNoMoreThanItsDoors(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	cases := []struct {
		name string
		path string
		key  []byte
		user int64
	}{
		{"a previewer of another door", "/doors/1", sessionKey, door3Previewer.ID},
		{"a player", "/doors/3", sessionKey, player.ID},
		{"signed with another key", "/doors/3", []byte("another key, another key, another"), previewer.ID},
	}
	for _, c := range cases {
		rec := getWithSession(t, h, c.path, c.key, c.user)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", c.name, rec.Code)
		}
		assertNoSecrets(t, c.name, rec)
	}
}

// The cookie is for the website's door page only. Bundles, checks and results stay with the
// CLI's token, so a page on another site can't use someone's cookie to fetch or send them.
func TestSessionCookieIsOnlyForThePage(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), uploaded)
	for _, path := range []string{"/doors/3/bundle", "/doors/3/checks"} {
		rec := getWithSession(t, h, path, sessionKey, previewer.ID)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
		assertNoSecrets(t, path, rec)
	}
}
