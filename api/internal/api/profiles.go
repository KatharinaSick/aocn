package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/users"
)

// Profile cards: who someone is and which doors they fixed. Private until their owner makes
// them public: until then, everyone but the owner gets the same 404 as for a login nobody
// has, so a private card doesn't even tell that someone plays. A public card is the same
// for everyone but its owner, so the website can cache it; only the owner's own view says
// "own", and the website keeps that one out of every cache.

// githubLogin is what GitHub allows in a login: letters, digits and hyphens, at most 39,
// not starting with a hyphen. Anything else can't be anyone, so it's a 404 without asking
// the database.
var githubLogin = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// ProfileUsers finds someone by login, and keeps whether their card is public.
// *users.Store asks the database.
type ProfileUsers interface {
	ByLogin(ctx context.Context, login string) (users.Profile, error)
	SetPublic(ctx context.Context, githubUserID int64, public bool) error
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
	// SessionKey checks the website's session cookie, which tells the owner of a card.
	SessionKey []byte
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
	// Public is whether everyone may see the card. Only the owner ever sees false.
	Public bool `json:"public"`
	// Own is whether the card is the one of who asks.
	Own bool `json:"own"`
}

// mayShow says whether a card may be shown to who asks: everyone if it's public, and its
// owner always. viewer is who is logged in, if loggedIn.
func mayShow(p users.Profile, viewer int64, loggedIn bool) bool {
	return p.Public || isOwner(p, viewer, loggedIn)
}

func isOwner(p users.Profile, viewer int64, loggedIn bool) bool {
	return loggedIn && viewer == p.GitHubID
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

// handleProfile answers with someone's card, by login, ignoring case. A private card is
// only shown to its owner, who the website's session cookie tells.
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
		profile, err := p.Users.ByLogin(ctx, login)
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "nobody here by that name")
			return
		}
		if err != nil {
			log.Error("Looking up a profile failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		now := p.now()
		viewer, loggedIn := sessionUser(r, p.SessionKey, now)
		if !mayShow(profile, viewer, loggedIn) {
			// The same answer as for nobody: a private card doesn't tell that it exists.
			writeJSONError(w, http.StatusNotFound, "nobody here by that name")
			return
		}
		passes, err := p.Results.FirstPasses(ctx, profile.GitHubID)
		if err != nil {
			log.Error("Looking up a profile's doors failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		writeJSON(w, http.StatusOK, profileResponse{
			Login:     profile.Login,
			AvatarURL: profile.AvatarURL,
			Since:     profile.Since,
			Doors:     solvedDoors(p.Schedule, passes, now),
			Public:    profile.Public,
			Own:       isOwner(profile, viewer, loggedIn),
		})
	})
}

type profileVisibility struct {
	// A pointer, so a body without it is refused instead of read as false.
	Public *bool `json:"public" validate:"required"`
}

// handleSetProfileVisibility makes the card of who is logged in on the website public or
// private. Only the website's session counts: it's a choice made on the card. The session
// cookie is SameSite=Lax, so another site can't send this on their behalf; nor can it send
// a JSON PUT without a preflight, which nothing here answers.
func handleSetProfileVisibility(log app.Logger, p Profiles) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		id, ok := sessionUser(r, p.SessionKey, p.now())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		var req profileVisibility
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "send {\"public\": true} or {\"public\": false}")
			return
		}
		if err := validate.Struct(req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "send {\"public\": true} or {\"public\": false}")
			return
		}
		err := p.Users.SetPublic(r.Context(), id, *req.Public)
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if err != nil {
			log.Error("Saving whether a profile is public failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		log.Info("Profile visibility changed", "github_user_id", id, "public", *req.Public)
		writeJSON(w, http.StatusOK, profileVisibility{Public: req.Public})
	})
}
