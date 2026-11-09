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

	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/schedule"
)

var (
	door1OpensAt = time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)
	door2OpensAt = time.Date(2026, 12, 3, 5, 0, 0, 0, time.UTC)
)

const testSchedule = `doors:
  - 2026-12-01T06:00:00+01:00
  - 2026-12-03T06:00:00+01:00
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
	"doors/02/page.yaml":     "title: Second\nstory: Secret.\n",
	"doors/02/bundle.tar.gz": "bundle 2",
}

func doorsAt(t *testing.T, now time.Time, storage doors.Storage) http.Handler {
	t.Helper()
	sched, err := schedule.Parse([]byte(testSchedule))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(nopLogger{}, Subscriptions{}, Doors{
		Schedule: sched,
		Storage:  storage,
		Pages:    &doors.Pages{Storage: storage},
		Now:      func() time.Time { return now },
	})
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
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
			for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/2", "/doors/2/bundle"} {
				rec := get(h, path)
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s: status %d, want 404", path, rec.Code)
				}
				body := rec.Body.String()
				for _, secret := range []string{"First", "The story", "Second", "Secret", "bundle"} {
					if strings.Contains(body, secret) {
						t.Errorf("%s: body %q contains %q", path, body, secret)
					}
				}
			}
		})
	}
}

func TestClosedDoorSaysWhenItOpens(t *testing.T) {
	for _, path := range []string{"/doors/2", "/doors/2/bundle"} {
		rec := get(doorsAt(t, door1OpensAt, uploaded), path)
		var body closedResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if body.Door != 2 || !body.OpensAt.Equal(door2OpensAt) {
			t.Errorf("%s: got %+v, want door 2 opening at %v", path, body, door2OpensAt)
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
	if rec := get(h, "/doors/2"); rec.Code != http.StatusNotFound {
		t.Errorf("door 2: status %d", rec.Code)
	}
}

func TestUnknownDoors(t *testing.T) {
	h := doorsAt(t, door2OpensAt.Add(time.Hour), uploaded)
	for _, path := range []string{"/doors/0", "/doors/3", "/doors/-1", "/doors/one", "/doors/01x", "/doors/3/bundle",
		// One spelling per door: these all parse as 1, which is open.
		"/doors/01", "/doors/+1", "/doors/1.0", "/doors/%201", "/doors/01/bundle", "/doors/+1/bundle"} {
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
	for _, path := range []string{"/doors/1", "/doors/1/bundle"} {
		if rec := get(h, path); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", path, rec.Code)
		}
	}
}

// Nothing between the API and its caller may keep a copy: the answer changes at the opening.
func TestDoorResponsesAreNotCached(t *testing.T) {
	for _, now := range []time.Time{door1OpensAt.Add(-time.Second), door1OpensAt} {
		h := doorsAt(t, now, uploaded)
		for _, path := range []string{"/doors/1", "/doors/1/bundle", "/doors/9"} {
			if got := get(h, path).Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%v %s: Cache-Control %q", now, path, got)
			}
		}
	}
}
