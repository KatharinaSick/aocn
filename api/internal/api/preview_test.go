package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
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
