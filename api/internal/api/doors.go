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

	"github.com/go-chi/httprate"
	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/results"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/scoring"
	"github.com/katharinasick/aocn/api/internal/session"
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
	Hints    *doors.Hints
	// Accounts finds a signed-in caller's login, for saving their results.
	Accounts Accounts
	// Previewers says who gets which doors early.
	Previewers app.Previewers
	Results    ResultStore
	// SessionKey checks the CLI's token, and the website's session cookie, which the door page
	// takes instead: the website's function passes it on, so a previewer sees their door
	// early there. Both carry only the GitHub user ID, so nobody asks GitHub on a request.
	SessionKey []byte
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

// Accounts finds a user by GitHub ID. *users.Store asks the database.
type Accounts interface {
	Get(ctx context.Context, githubUserID int64) (users.User, error)
}

// ResultStore keeps what aocn verify sends and which hints a player opened, and hands back
// everything recorded for working out points. *results.Store asks the database.
type ResultStore interface {
	Record(ctx context.Context, user users.User, result results.Result) error
	OpenHint(ctx context.Context, githubUserID int64, door, index int, at time.Time) (bool, error)
	Progress(ctx context.Context, githubUserIDs []int64) (map[int64]scoring.Progress, error)
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

// hintsResponse is what a player has of a door's hints: the ones they opened, and what the
// next one costs.
type hintsResponse struct {
	Door int `json:"door"`
	// Total is how many hints the door has.
	Total int `json:"total"`
	// Hints are the ones this player opened, in order. Showing them again is free.
	Hints []string `json:"hints"`
	// Next is the hint to open next, none once all are open.
	Next *nextHint `json:"next,omitempty"`
	// Fixed is whether they fixed the door: hints cost nothing from then on.
	Fixed bool `json:"fixed"`
	// Preview means the door isn't open yet. Hints cost the same.
	Preview bool `json:"preview,omitempty"`
}

type nextHint struct {
	// Hint is its number, from 1.
	Hint int `json:"hint"`
	// Cost is what opening it costs now, in points.
	Cost int `json:"cost"`
}

// openedHintResponse answers opening a hint: the player's hints, and what this one cost.
type openedHintResponse struct {
	hintsResponse
	// Cost is what opening it cost: nothing if it was open already.
	Cost int `json:"cost"`
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
	// callerID is the GitHub user ID of who sent the request, 0 if unknown. Only known when
	// it had to be: when the endpoint needs a signed-in caller, or the door is closed and the
	// request carries a token or, on the door page, a session cookie.
	callerID int64
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
		id, ok := d.caller(w, header)
		if !ok {
			return access{}, false
		}
		a.callerID = id
	}
	// A cookie that's missing, expired or signed with another key is nobody: the door page
	// works for everyone, so it never answers 401.
	via := "cli"
	if !open && a.callerID == 0 && website {
		if id, ok := sessionUser(r, d.SessionKey, d.now()); ok {
			a.callerID = id
			via = "website"
		}
	}

	if !open {
		if a.callerID == 0 || !d.Previewers.May(a.callerID, door) {
			writeJSON(w, http.StatusNotFound, closedResponse{Door: door, OpensAt: opensAt})
			return access{}, false
		}
		a.preview = true
		log.Info("Preview", "door", door, "github_user_id", a.callerID, "via", via)
	}
	return a, true
}

// caller finds out who sent the Authorization header: the GitHub user ID in the token that
// aocn login got from this API. Nothing is asked, neither GitHub nor the database. It answers
// the request itself when the token isn't valid.
func (d Doors) caller(w http.ResponseWriter, header string) (int64, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		writeJSONError(w, http.StatusUnauthorized, "sign in again, with: aocn login")
		return 0, false
	}
	id, err := session.Verify(d.SessionKey, token, d.now(), session.CLI)
	if err != nil {
		// Expired, signed with an old key, or a GitHub token from aocn before 0.4.0.
		writeJSONError(w, http.StatusUnauthorized, "your sign-in isn't valid anymore. Sign in again, with: aocn login (and update aocn if it's older than 0.4.0)")
		return 0, false
	}
	return id, true
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

// handleDoorHints answers with the hints the signed-in player opened for an open door, for
// aocn hint, and what the next one costs. Never the ones they didn't open: each costs points,
// so only POST /doors/{door}/hints/next hands out a new one. Hints aren't in the bundle, so a
// player can't read them in the door's folder either.
func handleDoorHints(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, true, false)
		if !ok {
			return
		}
		all, err := d.Hints.Get(r.Context(), a.door, d.now())
		if err != nil {
			writeMissingDoor(log, w, a.door, "hints", err)
			return
		}
		state, err := d.hintState(r.Context(), a, all)
		if err != nil {
			log.Error("Looking up a player's hints failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "your hints can't be looked up right now, try again in a few minutes")
			return
		}
		writeJSON(w, http.StatusOK, state)
	})
}

type openHintRequest struct {
	// Hint is the number of the hint to open, from 1: the one aocn hint said the price of,
	// so a second aocn hint at the same time can't open one more than the player agreed to.
	Hint int `json:"hint"`
	// Cost is what aocn hint said it costs, and must be there: opening it refuses another
	// price, so a door that opened or got fixed while the player was asked never costs what
	// they didn't agree to. A pointer, so a body without it is refused instead of read as 0.
	Cost *int `json:"cost"`
}

// handleOpenHint opens a hint of an open door for the signed-in player: it records it, which
// is what makes it cost points, and answers with it. Hints open in order. Opening one that's
// open already changes nothing and costs nothing.
func handleOpenHint(log app.Logger, d Doors) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, true, false)
		if !ok {
			return
		}
		var req openHintRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.Cost == nil {
			writeJSONError(w, http.StatusBadRequest, `send {"hint": N, "cost": C}: the number of the hint to open, and what it costs`)
			return
		}
		ctx := r.Context()
		all, err := d.Hints.Get(ctx, a.door, d.now())
		if err != nil {
			writeMissingDoor(log, w, a.door, "hints", err)
			return
		}
		if req.Hint < 1 || req.Hint > len(all) {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("door %d has hints 1 to %d", a.door, len(all)))
			return
		}
		before, err := d.hintState(ctx, a, all)
		if err != nil {
			log.Error("Looking up a player's hints failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "the hint can't be opened right now, try again in a few minutes")
			return
		}
		if req.Hint <= len(before.Hints) {
			writeJSON(w, http.StatusOK, openedHintResponse{hintsResponse: before})
			return
		}
		if req.Hint > len(before.Hints)+1 {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				hintsResponse
			}{fmt.Sprintf("hints open in order: open hint %d first", len(before.Hints)+1), before})
			return
		}
		if *req.Cost != before.Next.Cost {
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				hintsResponse
			}{fmt.Sprintf("hint %d costs %d points now, not %d. Run aocn hint again", req.Hint, before.Next.Cost, *req.Cost), before})
			return
		}

		// The user has to exist for their hint to be kept. They do once they signed in.
		if _, err := d.Accounts.Get(ctx, a.callerID); errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "sign in again, with: aocn login")
			return
		} else if err != nil {
			log.Error("Looking up who opened a hint failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "the hint can't be opened right now, try again in a few minutes")
			return
		}
		at := d.now()
		inserted, err := d.Results.OpenHint(ctx, a.callerID, a.door, req.Hint-1, at)
		if errors.Is(err, results.ErrHintOutOfOrder) {
			// Another aocn hint got there first.
			writeJSONError(w, http.StatusConflict, "your hints changed in the meantime. Run aocn hint again")
			return
		}
		if err != nil {
			log.Error("Saving an opened hint failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "the hint can't be opened right now, try again in a few minutes")
			return
		}
		progress, err := d.Results.Progress(ctx, []int64{a.callerID})
		if err != nil {
			log.Error("Looking up a player's hints failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "the hint was opened, but can't be shown right now. Run aocn hint again in a few minutes")
			return
		}
		p := progress[a.callerID]
		after := d.hintStateOf(a, all, p)
		// What it cost going by what's recorded, as Score will count it: a pass that came in
		// while this ran makes it free. Nothing if another aocn hint opened it a moment ago,
		// which said what it cost.
		cost := 0
		if inserted {
			cost = scoring.HintCostAt(at, p.Runs[a.door], req.Hint-1)
			log.Info("Hint opened", "door", a.door, "hint", req.Hint, "github_user_id", a.callerID, "cost", cost)
		}
		writeJSON(w, http.StatusOK, openedHintResponse{hintsResponse: after, Cost: cost})
	})
}

// hintState is what the caller has of the door's hints, all of them: the ones they opened,
// and the next one's price.
func (d Doors) hintState(ctx context.Context, a access, all []string) (hintsResponse, error) {
	progress, err := d.Results.Progress(ctx, []int64{a.callerID})
	if err != nil {
		return hintsResponse{}, err
	}
	return d.hintStateOf(a, all, progress[a.callerID]), nil
}

// hintStateOf is hintState from the caller's progress.
func (d Doors) hintStateOf(a access, all []string, p scoring.Progress) hintsResponse {
	opened := min(len(p.Hints[a.door]), len(all))
	fixed := scoring.Score(a.opensAt, time.Time{}, p.Runs[a.door], p.Hints[a.door]).Fixed
	state := hintsResponse{
		Door:    a.door,
		Total:   len(all),
		Hints:   all[:opened],
		Fixed:   fixed,
		Preview: a.preview,
	}
	if opened < len(all) {
		state.Next = &nextHint{Hint: opened + 1, Cost: scoring.NextHintCost(fixed, opened)}
	}
	return state
}

// verifyLimit is how often one player may verify, per verifyWindow, counted per instance like
// every other limit. A person fixing a door verifies a few times a minute at most; an agent in
// a loop doesn't get to fill verify_results.
const (
	verifyLimit  = 20
	verifyWindow = time.Minute
)

// tooManyVerifies answers a player past verifyLimit.
func tooManyVerifies(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Retry-After", "60")
	writeJSONError(w, http.StatusTooManyRequests, "that's a lot of verifies in a minute. Wait a moment, then run aocn verify again")
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
	// Score is what the door is worth to the player, once they fixed it.
	Score *doorScore `json:"score,omitempty"`
}

// doorScore is what a fixed door is worth: set by the first pass, whatever came after.
type doorScore struct {
	Points         int `json:"points"`
	Hints          int `json:"hints"`
	FailedVerifies int `json:"failedVerifies"`
	// Preview means the door isn't open yet: this is what it will be worth once it is.
	Preview bool `json:"preview,omitempty"`
}

// handleVerify keeps the result of an aocn verify under the caller's GitHub ID. It needs a
// signed-in caller, and a door they can get: open, or they're a previewer of it.
//
// It trusts what each check got, since it can't look into the player's cluster. It doesn't
// trust which checks ran: the results must be exactly the door's checks, by ID, and it
// decides itself whether the door passed.
func handleVerify(log app.Logger, d Doors) http.Handler {
	limit := httprate.NewRateLimiter(verifyLimit, verifyWindow, httprate.WithLimitHandler(tooManyVerifies))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := d.gate(log, w, r, true, false)
		if !ok {
			return
		}
		// Per player, not per IP: a whole workshop verifies from one address.
		if limit.RespondOnLimit(w, r, strconv.FormatInt(a.callerID, 10)) {
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
		ordered, passed, notReady, err := matchResults(checks, req.Results)
		if err != nil {
			// A new upload while the checks ran, or results that weren't made by running them.
			writeJSONError(w, http.StatusConflict, "the door's checks changed. Run aocn verify again")
			return
		}

		// Saved with the login they had when they signed in; the token only carries the ID.
		user, err := d.Accounts.Get(r.Context(), a.callerID)
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "sign in again, with: aocn login")
			return
		}
		if err != nil {
			log.Error("Looking up who sent a result failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "your result couldn't be saved right now, try again in a few minutes")
			return
		}
		result := results.Result{Door: a.door, Passed: passed, NotReady: notReady, Checks: ordered, At: d.now()}
		if err := d.Results.Record(r.Context(), user, result); err != nil {
			log.Error("Saving a result failed", "door", a.door, "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "your result couldn't be saved, try again in a few minutes")
			return
		}
		response := verifyResponse{Door: a.door, Passed: passed, Login: user.Login}
		if passed {
			// The run is saved either way; without the database, the answer just has no points.
			if score, err := d.doorScore(r.Context(), a); err != nil {
				log.Error("Working out a door's points failed", "door", a.door, "error", err)
			} else {
				response.Score = score
			}
		}
		writeJSON(w, http.StatusOK, response)
	})
}

// maxGotRunes is how much of what a check got is kept.
const maxGotRunes = 300

// errResultsMismatch means the results aren't exactly the door's checks.
var errResultsMismatch = errors.New("the results don't match the door's checks")

// doorScore is what the door is worth to the caller now that they passed it.
func (d Doors) doorScore(ctx context.Context, a access) (*doorScore, error) {
	progress, err := d.Results.Progress(ctx, []int64{a.callerID})
	if err != nil {
		return nil, err
	}
	p := progress[a.callerID]
	door := scoring.Score(a.opensAt, time.Time{}, p.Runs[a.door], p.Hints[a.door])
	if !door.Fixed {
		// Only if the run that just passed isn't among the runs: the database is behind.
		return nil, errors.New("a door that just passed isn't fixed")
	}
	return &doorScore{Points: door.Points, Hints: door.Hints, FailedVerifies: door.FailedVerifies, Preview: a.preview}, nil
}

// matchResults checks that sent has exactly one result for each of checks, by ID, and
// returns them in the checks' order. The door passed if every check did. notReady says the
// run failed at a ready check, with every check before it passed: the workloads weren't up
// yet, so it isn't a failed verify.
func matchResults(checks []doors.Check, sent []results.CheckResult) (ordered []results.CheckResult, passed, notReady bool, err error) {
	if len(sent) != len(checks) {
		return nil, false, false, errResultsMismatch
	}
	byID := make(map[string]results.CheckResult, len(sent))
	for _, r := range sent {
		if _, dup := byID[r.ID]; dup {
			return nil, false, false, errResultsMismatch
		}
		byID[r.ID] = r
	}

	ordered = make([]results.CheckResult, 0, len(checks))
	passed = len(checks) > 0
	for _, c := range checks {
		r, ok := byID[c.ID]
		if !ok {
			return nil, false, false, errResultsMismatch
		}
		if got := []rune(r.Got); len(got) > maxGotRunes {
			r.Got = string(got[:maxGotRunes])
		}
		if passed && !r.Passed && c.Ready != nil {
			// The first check that failed is a ready check.
			notReady = true
		}
		passed = passed && r.Passed
		ordered = append(ordered, r)
	}
	return ordered, passed, notReady, nil
}

// writeMissingDoor answers for an open door whose files can't be read. That's our mistake,
// never the caller's, so it's logged as an error.
func writeMissingDoor(log app.Logger, w http.ResponseWriter, door int, what string, err error) {
	if !errors.Is(err, doors.ErrNotFound) {
		log.Error("Reading an open door failed", "door", door, "file", what, "error", err)
		writeJSONError(w, http.StatusServiceUnavailable, "this door can't be served right now, try again in a few minutes")
		return
	}
	// Mostly a previewer of a door still being built. Only someone who may have the door
	// gets this far, so saying it isn't there yet tells nobody else anything.
	log.Error("A door was asked for but never uploaded", "door", door, "file", what)
	writeJSON(w, http.StatusServiceUnavailable, notReadyResponse{
		Error:    fmt.Sprintf("door %d isn't ready yet", door),
		Door:     door,
		NotReady: true,
	})
}

// notReadyResponse answers for a door that may be served but was never uploaded, so the
// website can say so instead of "try again in a few minutes".
type notReadyResponse struct {
	Error    string `json:"error"`
	Door     int    `json:"door"`
	NotReady bool   `json:"notReady"`
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
