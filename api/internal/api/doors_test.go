package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/github"
	"github.com/katharinasick/aocn/api/internal/results"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/users"
)

var (
	door1OpensAt = time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)
	door3OpensAt = time.Date(2026, 12, 3, 5, 0, 0, 0, time.UTC)
)

const testSchedule = `doors:
  1: 2026-12-01T06:00:00+01:00
  3: 2026-12-03T06:00:00+01:00
christmasEve:
  beforeSeason: 2025-12-24T06:00:00+01:00
  finale: 2026-12-24T06:00:00+01:00
`

type memoryStorage map[string]string

func (s memoryStorage) Open(_ context.Context, name string) (doors.File, error) {
	data, ok := s[name]
	if !ok {
		return doors.File{}, doors.ErrNotFound
	}
	return doors.File{ReadCloser: io.NopCloser(strings.NewReader(data)), Size: int64(len(data))}, nil
}

// brokenStorage hands out a bundle that fails halfway through.
type brokenStorage struct{}

func (brokenStorage) Open(context.Context, string) (doors.File, error) {
	return doors.File{ReadCloser: io.NopCloser(io.MultiReader(strings.NewReader("half"), failingReader{})), Size: 8}, nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// Both doors are uploaded, as they will be weeks before December.
var uploaded = memoryStorage{
	"doors/01/page.yaml":     "title: First\nstory: The story.\n",
	"doors/01/bundle.tar.gz": "bundle 1",
	"doors/01/checks.yaml":   "- ready: [first-ns]\n- job: first-ns/cronjob/first-job\n",
	"doors/03/page.yaml":     "title: Second\nstory: Secret.\n",
	"doors/03/bundle.tar.gz": "bundle 3",
	"doors/03/checks.yaml":   "- ready: [secret-ns]\n- job: secret-ns/cronjob/secret-job\n",
}

// Everything a closed door must never give away, from uploaded.
var secrets = []string{"First", "The story", "Second", "Secret", "bundle", "first-ns", "secret-ns"}

// Tokens the fake GitHub knows. The previewer gets every door early, the door 3 previewer
// only door 3, the player none.
const (
	previewerToken      = "previewer-token"
	door3PreviewerToken = "door3-previewer-token"
	playerToken         = "player-token"
)

var (
	previewer      = github.User{ID: 7, Login: "previewer", AvatarURL: "https://example.com/7.png"}
	door3Previewer = github.User{ID: 9, Login: "door3-previewer", AvatarURL: "https://example.com/9.png"}
	player         = github.User{ID: 8, Login: "player", AvatarURL: "https://example.com/8.png"}
)

// fakeUsers is GitHub: it knows two tokens, counts how often it's asked, and can be down.
type fakeUsers struct {
	asked int
	down  bool
}

func (f *fakeUsers) User(_ context.Context, token string) (github.User, error) {
	f.asked++
	switch {
	case f.down:
		return github.User{}, errors.New("GitHub is down")
	case token == previewerToken:
		return previewer, nil
	case token == door3PreviewerToken:
		return door3Previewer, nil
	case token == playerToken:
		return player, nil
	}
	return github.User{}, github.ErrBadToken
}

// fakeResults keeps what was recorded.
type fakeResults struct {
	recorded []recorded
	err      error
}

type recorded struct {
	user   users.User
	result results.Result
}

func (f *fakeResults) Record(_ context.Context, user users.User, result results.Result) error {
	if f.err != nil {
		return f.err
	}
	f.recorded = append(f.recorded, recorded{user, result})
	return nil
}

type testAPI struct {
	http.Handler
	users   *fakeUsers
	results *fakeResults
}

func doorsAt(t *testing.T, now time.Time, storage doors.Storage) testAPI {
	t.Helper()
	sched, err := schedule.Parse([]byte(testSchedule))
	if err != nil {
		t.Fatal(err)
	}
	a := testAPI{users: &fakeUsers{}, results: &fakeResults{}}
	a.Handler = NewHandler(nopLogger{}, Subscriptions{}, Doors{
		Schedule: sched,
		Storage:  storage,
		Pages:    &doors.Pages{Storage: storage},
		Checks:   &doors.Checks{Storage: storage},
		Users:    a.users,
		Previewers: app.Previewers{
			previewer.ID:      {All: true},
			door3Previewer.ID: {Doors: map[int]bool{3: true}},
		},
		Results:    a.results,
		SessionKey: sessionKey,
		Now:        func() time.Time { return now },
	}, Auth{}, Profiles{})
	return a
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	return getAs(h, path, "")
}

// getAs sends token the way the CLI does. An empty token sends no Authorization header.
func getAs(h http.Handler, path, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func assertNoSecrets(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	body := rec.Body.String()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("%s: body %q contains %q", what, body, secret)
		}
	}
	if rec.Header().Get(previewHeader) != "" {
		t.Errorf("%s: has the preview header", what)
	}
}

// Nothing about a door that is not open may be served, by either endpoint. A mistake here
// cannot be undone once it is public.
func TestClosedDoorsServeNothing(t *testing.T) {
	moments := map[string]time.Time{
		"weeks before":        door1OpensAt.Add(-30 * 24 * time.Hour),
		"a second before":     door1OpensAt.Add(-time.Second),
		"a nanosecond before": door1OpensAt.Add(-time.Nanosecond),
	}
	for name, now := range moments {
		t.Run(name, func(t *testing.T) {
			h := doorsAt(t, now, uploaded)
			for _, path := range closedPaths {
				// Signed out, and signed in as someone who isn't a previewer.
				for _, token := range []string{"", playerToken} {
					rec := getAs(h, path, token)
					if rec.Code != http.StatusNotFound {
						t.Errorf("%s as %q: status %d, want 404", path, token, rec.Code)
					}
					assertNoSecrets(t, path, rec)
				}
			}
		})
	}
}

// Every endpoint that hands out a door's content, for both doors.
var closedPaths = []string{"/doors/1", "/doors/1/bundle", "/doors/1/checks", "/doors/3", "/doors/3/bundle", "/doors/3/checks"}

func TestClosedDoorSaysWhenItOpens(t *testing.T) {
	for _, path := range []string{"/doors/3", "/doors/3/bundle", "/doors/3/checks"} {
		rec := get(doorsAt(t, door1OpensAt, uploaded), path)
		var body closedResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if body.Door != 3 || !body.OpensAt.Equal(door3OpensAt) {
			t.Errorf("%s: got %+v, want door 3 opening at %v", path, body, door3OpensAt)
		}
	}
}

func TestOpenDoorPage(t *testing.T) {
	// At the exact moment, and later.
	for _, now := range []time.Time{door1OpensAt, door1OpensAt.Add(time.Hour)} {
		rec := get(doorsAt(t, now, uploaded), "/doors/1")
		if rec.Code != http.StatusOK {
			t.Fatalf("%v: status %d, want 200", now, rec.Code)
		}
		var body pageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Door != 1 || body.Title != "First" || body.Story != "The story." || !body.OpensAt.Equal(door1OpensAt) {
			t.Errorf("%v: got %+v", now, body)
		}
	}
}

func TestOpenDoorBundle(t *testing.T) {
	rec := get(doorsAt(t, door1OpensAt, uploaded), "/doors/1/bundle")
	if rec.Code != http.StatusOK || rec.Body.String() != "bundle 1" {
		t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/gzip" {
		t.Errorf("Content-Type %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "8" {
		t.Errorf("Content-Length %q, want 8", got)
	}
}

// A bundle that can't be read to the end must not look like a complete download.
func TestBrokenBundleAbortsTheResponse(t *testing.T) {
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Errorf("recovered %v, want http.ErrAbortHandler", r)
		}
	}()
	get(doorsAt(t, door1OpensAt, brokenStorage{}), "/doors/1/bundle")
}

// An open door doesn't open the next one.
func TestOnlyOpenDoorsAreServed(t *testing.T) {
	h := doorsAt(t, door1OpensAt, uploaded)
	if rec := get(h, "/doors/1"); rec.Code != http.StatusOK {
		t.Errorf("door 1: status %d", rec.Code)
	}
	if rec := get(h, "/doors/3"); rec.Code != http.StatusNotFound {
		t.Errorf("door 3: status %d", rec.Code)
	}
}

func TestUnknownDoors(t *testing.T) {
	h := doorsAt(t, door3OpensAt.Add(time.Hour), uploaded)
	for _, path := range []string{"/doors/0", "/doors/2", "/doors/4", "/doors/-1", "/doors/one", "/doors/01x", "/doors/2/bundle", "/doors/2/checks",
		// 2 and 4 are days without a door.
		// One spelling per door: these all parse as 1, which is open.
		"/doors/01", "/doors/+1", "/doors/1.0", "/doors/%201", "/doors/01/bundle", "/doors/+1/bundle", "/doors/01/checks"} {
		rec := get(h, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "opensAt") {
			t.Errorf("%s: an unknown door has no opening time, got %q", path, rec.Body.String())
		}
	}
}

func TestOpenDoorNotUploaded(t *testing.T) {
	h := doorsAt(t, door1OpensAt, memoryStorage{})
	for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/1/checks"} {
		rec := get(h, path)
		var body notReadyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status %d, %v", path, rec.Code, err)
		}
		if !body.NotReady || body.Door != 1 || body.Error != "door 1 isn't ready yet" {
			t.Errorf("%s: got %+v", path, body)
		}
	}
}

// A door that can't be read is an outage, not a door that isn't ready.
func TestOpenDoorUnreadable(t *testing.T) {
	h := doorsAt(t, door1OpensAt, brokenStorage{})
	rec := get(h, "/doors/1")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "notReady") {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
}

// A closed door says nothing about whether it's uploaded to anyone but its previewers.
func TestClosedDoorNotUploadedIsJustClosed(t *testing.T) {
	h := doorsAt(t, door1OpensAt.Add(-time.Hour), memoryStorage{})
	if rec := get(h, "/doors/1"); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "notReady") {
		t.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := getAs(h, "/doors/1", previewerToken); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "notReady") {
		t.Errorf("previewer: status %d: %s", rec.Code, rec.Body.String())
	}
}

// Nothing between the API and its caller may keep a copy: the answer changes at the opening.
func TestDoorResponsesAreNotCached(t *testing.T) {
	for _, now := range []time.Time{door1OpensAt.Add(-time.Second), door1OpensAt} {
		h := doorsAt(t, now, uploaded)
		for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/1/checks", "/doors/9"} {
			// A previewer's early door most of all: nobody else may ever get that copy.
			for _, token := range []string{"", previewerToken} {
				if got := getAs(h, path, token).Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("%v %s as %q: Cache-Control %q", now, path, token, got)
				}
			}
		}
	}
}
