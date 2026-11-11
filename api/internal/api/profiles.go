package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/users"
)

// Profile cards: who someone is and which doors they fixed. Public, and the same for
// everyone who asks, so the website can cache the card.

// githubLogin is what GitHub allows in a login: letters, digits and hyphens, at most 39,
// not starting with a hyphen. Anything else can't be anyone, so it's a 404 without asking
// the database.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// ProfileUsers finds someone by login. *users.Store asks the database.
type ProfileUsers interface {
	ByLogin(ctx context.Context, login string) (users.User, time.Time, error)
}

// ProfileResults says when someone first passed each door. *results.Store asks the database.
type ProfileResults interface {
	FirstPasses(ctx context.Context, githubUserID int64) (map[int]time.Time, error)
}

// Profiles is what the profile endpoint needs.
type Profiles struct {
	Schedule *schedule.Schedule
	Users    ProfileUsers
	Results  ProfileResults
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (p Profiles) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

type solvedDoor struct {
	Door     int       `json:"door"`
	SolvedAt time.Time `json:"solvedAt"`
}

type profileResponse struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
	// Since is when they first logged in or sent a result.
	Since time.Time    `json:"since"`
	Doors []solvedDoor `json:"doors"`
}

// solvedDoors are the doors to show as fixed at now: open, and passed at least once, by
// door. A previewer's pass from before the opening counts, but only once the door is open,
// so a card never shows a door early, and nobody has to verify again on the day. It counts
// as solved at the opening: the card is public, and an earlier time would tell everyone who
// previews.
func solvedDoors(sched *schedule.Schedule, passes map[int]time.Time, now time.Time) []solvedDoor {
	solved := []solvedDoor{}
	for door, at := range passes {
		opensAt, err := sched.OpensAt(door)
		if err != nil || now.Before(opensAt) {
			continue
		}
		if at.Before(opensAt) {
			at = opensAt
		}
		solved = append(solved, solvedDoor{Door: door, SolvedAt: at})
	}
	sort.Slice(solved, func(i, j int) bool { return solved[i].Door < solved[j].Door })
	return solved
}

// handleProfile answers with someone's card, by login, ignoring case.
func handleProfile(log app.Logger, p Profiles) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The website caches the card it renders; the API answers fresh every time, since
		// a door opening changes the answer.
		w.Header().Set("Cache-Control", "no-store")
		login := r.PathValue("login")
		if !githubLogin.MatchString(login) {
			writeJSONError(w, http.StatusNotFound, "nobody here by that name")
			return
		}
		ctx := r.Context()
		user, since, err := p.Users.ByLogin(ctx, login)
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "nobody here by that name")
			return
		}
		if err != nil {
			log.Error("Looking up a profile failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		passes, err := p.Results.FirstPasses(ctx, user.GitHubID)
		if err != nil {
			log.Error("Looking up a profile's doors failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		writeJSON(w, http.StatusOK, profileResponse{
			Login:     user.Login,
			AvatarURL: user.AvatarURL,
			Since:     since,
			Doors:     solvedDoors(p.Schedule, passes, p.now()),
		})
	})
}
