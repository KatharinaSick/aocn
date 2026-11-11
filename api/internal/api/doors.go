package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/github"
	"github.com/katharinasick/aocn/api/internal/results"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/users"
)

// Everything was uploaded weeks before. These handlers decide on each request whether a
// door is open, so at 06:00 nothing runs: requests just get a different answer. The website
// and the CLI both ask here, and nowhere else decides. Previewers get their doors early.

// doorNumber is how a door is named in a path: 1, 2, … 24, never 01 or +1. One spelling per
// door, so there's one URL to cache, log and link to.
var doorNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// Doors is what the door endpoints need.
type Doors struct {
	Schedule *schedule.Schedule
	Storage  doors.Storage
	Pages    *doors.Pages
	Checks   *doors.Checks
	// Users finds out whose GitHub token a request carries.
	Users Users
	// Previewers says who gets which doors early.
	Previewers app.Previewers
	Results    ResultStore
	// SessionKey checks the website's session cookie, which the door page takes instead of a
	// token: the website's function passes it on, so a previewer sees their door early there.
	SessionKey []byte
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

// Users finds out whose GitHub token it is. *github.Users asks GitHub.
type Users interface {
	User(ctx context.Context, token string) (github.User, error)
}

// ResultStore keeps what aocn verify sends.
type ResultStore interface {
	Record(ctx context.Context, user users.User, result results.Result) error
}

func (d Doors) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// closedResponse answers for a door that isn't open yet, with when it opens, so the website
// can count down and the CLI can say when to come back.
type closedResponse struct {
	Door    int       `json:"door"`
	OpensAt time.Time `json:"opensAt"`
}

type pageResponse struct {
	Door    int       `json:"door"`
	OpensAt time.Time `json:"opensAt"`
	// Preview means the door isn't open: the caller only gets it as a previewer.
	Preview bool `json:"preview,omitempty"`
	doors.Page
}

type checksResponse struct {
	Door   int           `json:"door"`
	Checks []doors.Check `json:"checks"`
}

// previewHeader marks a bundle handed out only because the caller is a previewer, with when
// the door opens (RFC 3339), so the CLI can say so.
const previewHeader = "Aocn-Preview"

// access is what the gate let through.
type access struct {
	door    int
	opensAt time.Time
	// preview means the door isn't open and the caller is a previewer of it.
	preview bool
	// caller is who sent the request. Only known when it had to be: when the endpoint needs
	// a signed-in caller, or the door is closed and the request carries a token.
	caller *github.User
}

// gate parses the door number and decides whether it may be served: when it's open, or to a
// previewer of this door. With signIn, the caller must be signed in whatever the door. With
// website, a closed door also takes the website's session cookie, for the door page. It
// answers the request itself when the door may not be served, and returns ok only when it may.
//
// An open door never asks GitHub unless signIn is set, so 06:00 doesn't depend on GitHub, and
// never looks at the cookie.
func (d Doors) gate(log app.Logger, w http.ResponseWriter, r *http.Request, signIn, website bool) (a access, ok bool) {
	// The answer changes at the opening, and depends on who asks, so nothing between here
	// and the caller may keep a copy. The website caches what it renders.
	w.Header().Set("Cache-Control", "no-store")

	text := r.PathValue("door")
	if !doorNumber.MatchString(text) {
		writeJSONError(w, http.StatusNotFound, "no such door")
		return access{}, false
	}
	door, err := strconv.Atoi(text)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no such door")
		return access{}, false
	}
	opensAt, err := d.Schedule.OpensAt(door)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "no such door")
		return access{}, false
	}
	a = access{door: door, opensAt: opensAt}
	open, _ := d.Schedule.IsOpen(door, d.now())

	header := r.Header.Get("Authorization")
	if signIn || (!open && header != "") {
		if header == "" {
			writeJSONError(w, http.StatusUnauthorized, "sign in first, with: aocn login")
			return access{}, false
		}
		user, ok := d.caller(log, w, r, header)
		if !ok {
			return access{}, false
		}
		a.caller = &user
	}
	// A cookie that's missing, expired or signed with another key is nobody: the door page
	// works for everyone, so it never answers 401.
	via := "cli"
	if !open && a.caller == nil && website {
		if id, ok := sessionUser(r, d.SessionKey, d.now()); ok {
			a.caller = &github.User{ID: id}
			via = "website"
		}
	}

	if !open {
		if a.caller == nil || !d.Previewers.May(a.caller.ID, door) {
			writeJSON(w, http.StatusNotFound, closedResponse{Door: door, OpensAt: opensAt})
			return access{}, false
		}
		a.preview = true
		log.Info("Preview", "door", door, "github_user_id", a.caller.ID, "login", a.caller.Login, "via", via)
	}
	return a, true
}

// caller finds out who sent the Authorization header. It answers the request itself when it
// can't.
func (d Doors) caller(log app.Logger, w http.ResponseWriter, r *http.Request, header string) (github.User, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		writeJSONError(w, http.StatusUnauthorized, "sign in again, with: aocn login")
		return github.User{}, false
	}
	user, err := d.Users.User(r.Context(), token)
	if errors.Is(err, github.ErrBadToken) {
		writeJSONError(w, http.StatusUnauthorized, "your GitHub sign-in isn't valid anymore. Sign in again, with: aocn login")
		return github.User{}, false
	}
	if err != nil {
		log.Error("Asking GitHub who a token belongs to failed", "error", err)
		writeJSONError(w, http.StatusServiceUnavailable, "GitHub couldn't tell us who you are right now, try again in a few minutes")
		return github.User{}, false
	}
	return user, true
}

// handleDoorPage answers with what the website shows for an open door, and 404 with the
// opening time before that.
func handleDoorPage(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, false, true)
		if !ok {
			return
		}
		page, err := d.Pages.Get(r.Context(), a.door, d.now())
		if err != nil {
			writeMissingDoor(log, w, a.door, "page", err)
			return
		}
		writeJSON(w, http.StatusOK, pageResponse{Door: a.door, OpensAt: a.opensAt, Preview: a.preview, Page: page})
	})
}

// handleDoorBundle streams an open door's bundle, for aocn open. Bundles are small, so the
// API streams them itself rather than handing out a link to the bucket.
func handleDoorBundle(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, false, false)
		if !ok {
			return
		}
		bundle, err := d.Storage.Open(r.Context(), doors.BundleName(a.door))
		if err != nil {
			writeMissingDoor(log, w, a.door, "bundle", err)
			return
		}
		defer bundle.Close()

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="door-%02d.tar.gz"`, a.door))
		if a.preview {
			w.Header().Set(previewHeader, a.opensAt.Format(time.RFC3339))
		}
		// With the length known, a download that's cut off is an error for the CLI, even
		// through a proxy that would otherwise end the response cleanly.
		if bundle.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(bundle.Size, 10))
		}
		if _, err := io.Copy(w, bundle); err != nil {
			log.Error("Streaming a bundle failed", "door", a.door, "error", err)
			// Returning normally would end the response as if it were complete. Aborting
			// drops the connection, so the CLI sees a broken download, not a broken bundle.
			panic(http.ErrAbortHandler)
		}
	})
}

// handleDoorChecks hands out an open door's checks, for aocn verify. They only ever come from
// here, never from the player's folder, so editing them can't make a door pass.
func handleDoorChecks(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, false, false)
		if !ok {
			return
		}
		checks, err := d.Checks.Get(r.Context(), a.door, d.now())
		if err != nil {
			writeMissingDoor(log, w, a.door, "checks", err)
			return
		}
		writeJSON(w, http.StatusOK, checksResponse{Door: a.door, Checks: checks})
	})
}

// maxVerifyBytes bounds a verify request: a door has a few dozen checks with a short answer
// each.
const maxVerifyBytes = 64 << 10

type verifyRequest struct {
	Results []results.CheckResult `json:"results"`
}

type verifyResponse struct {
	Door   int    `json:"door"`
	Passed bool   `json:"passed"`
	Login  string `json:"login"`
}

// handleVerify keeps the result of an aocn verify under the caller's GitHub ID. It needs a
// signed-in caller, and a door they can get: open, or they're a previewer of it.
//
// It trusts what each check got, since it can't look into the player's cluster. It doesn't
// trust which checks ran: the results must be exactly the door's checks, by ID, and it
// decides itself whether the door passed.
func handleVerify(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, true, false)
		if !ok {
			return
		}

		var req verifyRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxVerifyBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "the results can't be read")
			return
		}

		checks, err := d.Checks.Get(r.Context(), a.door, d.now())
		if err != nil {
			writeMissingDoor(log, w, a.door, "checks", err)
			return
		}
		ordered, passed, err := matchResults(checks, req.Results)
		if err != nil {
			// A new upload while the checks ran, or results that weren't made by running them.
			writeJSONError(w, http.StatusConflict, "the door's checks changed. Run aocn verify again")
			return
		}

		user := users.User{GitHubID: a.caller.ID, Login: a.caller.Login, AvatarURL: a.caller.AvatarURL}
		result := results.Result{Door: a.door, Passed: passed, Checks: ordered, At: d.now()}
		if err := d.Results.Record(r.Context(), user, result); err != nil {
			log.Error("Saving a result failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "your result couldn't be saved, try again in a few minutes")
			return
		}
		writeJSON(w, http.StatusOK, verifyResponse{Door: a.door, Passed: passed, Login: a.caller.Login})
	})
}

// maxGotRunes is how much of what a check got is kept.
const maxGotRunes = 300

// errResultsMismatch means the results aren't exactly the door's checks.
var errResultsMismatch = errors.New("the results don't match the door's checks")

// matchResults checks that sent has exactly one result for each of checks, by ID, and
// returns them in the checks' order. The door passed if every check did.
func matchResults(checks []doors.Check, sent []results.CheckResult) ([]results.CheckResult, bool, error) {
	if len(sent) != len(checks) {
		return nil, false, errResultsMismatch
	}
	byID := make(map[string]results.CheckResult, len(sent))
	for _, r := range sent {
		if _, dup := byID[r.ID]; dup {
			return nil, false, errResultsMismatch
		}
		byID[r.ID] = r
	}

	ordered := make([]results.CheckResult, 0, len(checks))
	passed := len(checks) > 0
	for _, c := range checks {
		r, ok := byID[c.ID]
		if !ok {
			return nil, false, errResultsMismatch
		}
		if got := []rune(r.Got); len(got) > maxGotRunes {
			r.Got = string(got[:maxGotRunes])
		}
		passed = passed && r.Passed
		ordered = append(ordered, r)
	}
	return ordered, passed, nil
}

// writeMissingDoor answers for an open door whose files can't be read. That's our mistake,
// never the caller's, so it's logged as an error.
func writeMissingDoor(log app.Logger, w http.ResponseWriter, door int, what string, err error) {
	if errors.Is(err, doors.ErrNotFound) {
		log.Error("An open door was never uploaded", "door", door, "file", what)
	} else {
		log.Error("Reading an open door failed", "door", door, "file", what, "error", err)
	}
	writeJSONError(w, http.StatusServiceUnavailable, "this door can't be served right now, try again in a few minutes")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{message})
}
