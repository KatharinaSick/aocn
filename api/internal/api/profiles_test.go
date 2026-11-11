package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/users"
)

var octo = users.User{GitHubID: 42, Login: "Octo", AvatarURL: "https://example.com/42.png"}

// fakeProfiles knows octo, who passed door 1 an hour after it opened and door 3 the day
// before it opened, as a previewer.
type fakeProfiles struct{ down bool }

var octoSince = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func (f fakeProfiles) ByLogin(_ context.Context, login string) (users.User, time.Time, error) {
	switch {
	case f.down:
		return users.User{}, time.Time{}, errors.New("database is down")
	case strings.EqualFold(login, octo.Login):
		return octo, octoSince, nil
	}
	return users.User{}, time.Time{}, users.ErrNotFound
}

func (f fakeProfiles) FirstPasses(_ context.Context, id int64) (map[int]time.Time, error) {
	if id != octo.GitHubID {
		return nil, nil
	}
	return map[int]time.Time{
		1: door1OpensAt.Add(time.Hour),
		3: door3OpensAt.Add(-24 * time.Hour),
	}, nil
}

func profilesAt(t *testing.T, now time.Time, f fakeProfiles) http.Handler {
	t.Helper()
	sched, err := schedule.Parse([]byte(testSchedule))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(nopLogger{}, Subscriptions{}, Doors{}, Auth{}, Profiles{
		Schedule: sched,
		Users:    f,
		Results:  f,
		Now:      func() time.Time { return now },
	})
}

func profileAt(t *testing.T, now time.Time) profileResponse {
	t.Helper()
	rec := get(profilesAt(t, now, fakeProfiles{}), "/users/octo")
	var body profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	return body
}

func TestProfile(t *testing.T) {
	body := profileAt(t, door1OpensAt.Add(2*time.Hour))
	if body.Login != "Octo" || body.AvatarURL != octo.AvatarURL || !body.Since.Equal(octoSince) {
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
	rec := get(profilesAt(t, door1OpensAt.Add(-time.Hour), fakeProfiles{}), "/users/octo")
	if !strings.Contains(rec.Body.String(), `"doors":[]`) {
		t.Errorf("got %s", rec.Body.String())
	}
}

func TestProfileNotFound(t *testing.T) {
	h := profilesAt(t, door1OpensAt, fakeProfiles{})
	for _, login := range []string{"nobody", "-octo", "octo_", "a" + strings.Repeat("b", 39), "%2e%2e"} {
		if rec := get(h, "/users/"+login); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", login, rec.Code)
		}
	}
}

func TestProfileWhenTheDatabaseIsDown(t *testing.T) {
	rec := get(profilesAt(t, door1OpensAt, fakeProfiles{down: true}), "/users/octo")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("status %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}
