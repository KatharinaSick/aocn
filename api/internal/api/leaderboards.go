package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/leaderboards"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/scoring"
	"github.com/katharinasick/aocn/api/internal/users"
)

// Leaderboards players make for their own groups. There's no global one, and no list of
// public ones, so nothing becomes a global one either.
//
// Everything here decides itself who may see or change what; the website only shows it:
//
//   - A private board is only shown to its members. To everyone else it's the same 404 as an
//     ID nobody has, so it doesn't even tell that it exists. A public one is shown to anyone.
//   - Only the owner changes a board's settings, makes a new invite link, removes members and
//     deletes it. A member who isn't the owner gets 403 for those; anyone else the 404.
//   - Joining needs the invite link's code, which isn't the board's ID: seeing a public board
//     doesn't let anyone in, unless its owner made it open.
//   - Someone the owner removed can't join again, whichever way, until the owner lets them.
//   - A public board shows anyone outside it the members whose card is private as "private":
//     they may have joined while it was private, and their card says they want to stay
//     unnamed. Their points still count, so the ranks are right. The owner, who made it
//     public, is always named.
//   - Every change comes from the website's session cookie, which is SameSite=Lax, and only
//     as JSON, which a form on another site can't send.

// nameMaxRunes is how long a leaderboard's name may be.
const nameMaxRunes = 40

// Limits per IP, for creating and joining: generous, since a team or a workshop joins
// together from one address, and well below what a script would need to make a mess.
const (
	createLimit  = 30
	createWindow = time.Hour
	joinLimit    = 300
	joinWindow   = 10 * time.Minute
	// Each look at a board works out everyone's points: plenty for a team refreshing during
	// a door, not for a script hammering it.
	viewLimit  = 600
	viewWindow = 10 * time.Minute
)

// LeaderboardStore keeps the boards. *leaderboards.Store asks the database.
type LeaderboardStore interface {
	Create(ctx context.Context, ownerID int64, name string, now time.Time) (leaderboards.Leaderboard, error)
	Get(ctx context.Context, id string) (leaderboards.Leaderboard, error)
	ByJoinCode(ctx context.Context, code string) (leaderboards.Leaderboard, error)
	OfMember(ctx context.Context, githubUserID int64) ([]leaderboards.Leaderboard, error)
	Members(ctx context.Context, id string) ([]leaderboards.Member, error)
	IsMember(ctx context.Context, id string, githubUserID int64) (bool, error)
	Join(ctx context.Context, id, code string, githubUserID int64, now time.Time) error
	Leave(ctx context.Context, id string, githubUserID int64) error
	Remove(ctx context.Context, id string, githubUserID int64, now time.Time) error
	Banned(ctx context.Context, id string) ([]leaderboards.Banned, error)
	Unban(ctx context.Context, id string, githubUserID int64) error
	Update(ctx context.Context, id string, c leaderboards.Changes) (leaderboards.Leaderboard, error)
	NewJoinCode(ctx context.Context, id string) (string, error)
	Delete(ctx context.Context, id string) error
}

// ProgressStore hands out what's recorded for players, to work their points out.
// *results.Store asks the database.
type ProgressStore interface {
	Progress(ctx context.Context, githubUserIDs []int64) (map[int64]scoring.Progress, error)
}

// Leaderboards is what the leaderboard endpoints need.
type Leaderboards struct {
	Schedule *schedule.Schedule
	Store    LeaderboardStore
	Results  ProgressStore
	// Accounts makes sure who is logged in still exists before they're added anywhere.
	Accounts Accounts
	// SessionKey checks the website's session cookie.
	SessionKey []byte
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (l Leaderboards) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// viewer is who asks: logged in on the website, or not.
type viewer struct {
	id       int64
	loggedIn bool
}

func (l Leaderboards) viewer(r *http.Request) viewer {
	id, ok := sessionUser(r, l.SessionKey, l.now())
	return viewer{id: id, loggedIn: ok}
}

// writeNoLeaderboard is the one answer for a board that doesn't exist and one the caller may
// not see, so a private board doesn't tell that it exists.
func writeNoLeaderboard(w http.ResponseWriter) {
	writeJSONError(w, http.StatusNotFound, "no such leaderboard")
}

func writeTryAgain(log app.Logger, w http.ResponseWriter, what string, err error) {
	log.Error(what, "error", err)
	writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
}

// requireJSON refuses a body that isn't JSON: a form on another site can send a POST, but
// never one with this content type, so this stops anything the cookie alone wouldn't.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "send JSON, with Content-Type: application/json")
		return false
	}
	return true
}

// decodeJSON reads a small JSON body into v, refusing unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "the request can't be read")
		return false
	}
	return true
}

// cleanName is a leaderboard's name as it's kept: trimmed, 1 to nameMaxRunes characters, no
// control characters.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > nameMaxRunes {
		return "", fmt.Errorf("a name has 1 to %d characters", nameMaxRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return "", errors.New("a name is one line of text")
		}
	}
	return name, nil
}

// deadlineRange bounds a deadline, so it's a date a person meant.
var (
	earliestDeadline = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	latestDeadline   = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// isFinal says whether a board's standings are final: its deadline has passed.
func isFinal(b leaderboards.Leaderboard, now time.Time) bool {
	return !b.CountsUntil.IsZero() && now.After(b.CountsUntil)
}

// mayView says whether a board may be shown: to anyone if it's public, to members otherwise.
func (l Leaderboards) mayView(ctx context.Context, b leaderboards.Leaderboard, v viewer) (member bool, ok bool, err error) {
	if v.loggedIn {
		member, err = l.Store.IsMember(ctx, b.ID, v.id)
		if err != nil {
			return false, false, err
		}
	}
	return member, b.Public || member, nil
}

// find looks up the board in the path and decides whether the viewer may see it. It answers
// the request itself unless ok.
func (l Leaderboards) find(log app.Logger, w http.ResponseWriter, r *http.Request, v viewer) (b leaderboards.Leaderboard, member bool, ok bool) {
	id := r.PathValue("id")
	if !leaderboards.ValidToken(id) {
		writeNoLeaderboard(w)
		return b, false, false
	}
	ctx := r.Context()
	b, err := l.Store.Get(ctx, id)
	if errors.Is(err, leaderboards.ErrNotFound) {
		writeNoLeaderboard(w)
		return b, false, false
	}
	if err != nil {
		writeTryAgain(log, w, "Looking up a leaderboard failed", err)
		return b, false, false
	}
	member, may, err := l.mayView(ctx, b, v)
	if err != nil {
		writeTryAgain(log, w, "Looking up a leaderboard's members failed", err)
		return b, false, false
	}
	if !may {
		writeNoLeaderboard(w)
		return b, false, false
	}
	return b, member, true
}

// findOwned is find for what only the owner may do. Members get 403, everyone else 404.
func (l Leaderboards) findOwned(log app.Logger, w http.ResponseWriter, r *http.Request) (leaderboards.Leaderboard, bool) {
	v := l.viewer(r)
	if !v.loggedIn {
		writeJSONError(w, http.StatusUnauthorized, "not logged in")
		return leaderboards.Leaderboard{}, false
	}
	b, _, ok := l.find(log, w, r, v)
	if !ok {
		return b, false
	}
	if b.OwnerID != v.id {
		writeJSONError(w, http.StatusForbidden, "only its owner can do that")
		return b, false
	}
	return b, true
}

// loggedInUser is who is logged in, for what needs it: 401 otherwise, and 401 if their
// account is gone. It answers the request itself unless ok.
func (l Leaderboards) loggedInUser(log app.Logger, w http.ResponseWriter, r *http.Request) (int64, bool) {
	v := l.viewer(r)
	if !v.loggedIn {
		writeJSONError(w, http.StatusUnauthorized, "not logged in")
		return 0, false
	}
	if _, err := l.Accounts.Get(r.Context(), v.id); err != nil {
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return 0, false
		}
		writeTryAgain(log, w, "Looking up who is logged in failed", err)
		return 0, false
	}
	return v.id, true
}

// summary is a board in a list: yours, or on someone's card.
type summary struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Public      bool       `json:"public"`
	Own         bool       `json:"own"`
	Members     int        `json:"members"`
	CountsUntil *time.Time `json:"countsUntil"`
	Final       bool       `json:"final"`
	// Rank is the player's place on it.
	Rank int `json:"rank"`
}

type standingDoor struct {
	Door           int `json:"door"`
	Points         int `json:"points"`
	Hints          int `json:"hints"`
	FailedVerifies int `json:"failedVerifies"`
}

type standing struct {
	Rank int `json:"rank"`
	// ID is the player's GitHub user ID, which removing them takes. GitHub shows it to
	// anyone, so it tells nothing new.
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
	// ProfilePublic says whether their card may be linked to.
	ProfilePublic bool `json:"profilePublic"`
	// Private means who they are is left out: their card is private, and who asks sees a
	// public board without being on it. Login and ID are empty then.
	Private        bool `json:"private,omitempty"`
	You            bool `json:"you"`
	Owner          bool `json:"owner"`
	Points         int  `json:"points"`
	FailedVerifies int  `json:"failedVerifies"`
	Fixed          int  `json:"fixed"`
	// Doors are the doors they fixed, by number, with what each is worth.
	Doors []standingDoor `json:"doors"`
}

type boardDoor struct {
	Door    int       `json:"door"`
	OpensAt time.Time `json:"opensAt"`
	// Counts says whether fixing it counts on this board now: it's open, and opened before
	// the deadline.
	Counts bool `json:"counts"`
}

// removedMember is someone the owner removed: they can't join again until let back in.
type removedMember struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	RemovedAt time.Time `json:"removedAt"`
}

type boardResponse struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Public      bool       `json:"public"`
	OpenJoin    bool       `json:"openJoin"`
	CountsUntil *time.Time `json:"countsUntil"`
	Final       bool       `json:"final"`
	Owner       struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"owner"`
	Members    int `json:"members"`
	MaxMembers int `json:"maxMembers"`
	// You says who asks: whether they're logged in, a member, the owner.
	You struct {
		LoggedIn bool `json:"loggedIn"`
		Member   bool `json:"member"`
		Owner    bool `json:"owner"`
	} `json:"you"`
	// JoinCode is for the invite link, only for members.
	JoinCode string `json:"joinCode,omitempty"`
	// Removed are who the owner removed and hasn't let back in, only for the owner.
	Removed   []removedMember `json:"removed,omitempty"`
	Doors     []boardDoor     `json:"doors"`
	Standings []standing      `json:"standings"`
	// Together is how many doors everyone fixed, added up.
	Together int `json:"together"`
}

func untilPtr(b leaderboards.Leaderboard) *time.Time {
	if b.CountsUntil.IsZero() {
		return nil
	}
	t := b.CountsUntil
	return &t
}

// openings are the schedule's doors, for scoring.
func openings(s *schedule.Schedule) []scoring.Opening {
	var list []scoring.Opening
	for _, n := range s.Numbers() {
		at, err := s.OpensAt(n)
		if err != nil {
			continue
		}
		list = append(list, scoring.Opening{Door: n, OpensAt: at})
	}
	return list
}

// standings ranks the board's members at now.
func (l Leaderboards) standings(ctx context.Context, b leaderboards.Leaderboard, members []leaderboards.Member, now time.Time) ([]scoring.Ranked, error) {
	ids := make([]int64, len(members))
	for i, m := range members {
		ids[i] = m.GitHubID
	}
	progress, err := l.Results.Progress(ctx, ids)
	if err != nil {
		return nil, err
	}
	doors := openings(l.Schedule)
	entries := make([]scoring.Entry, len(members))
	for i, m := range members {
		entries[i] = scoring.Entry{ID: m.GitHubID, Login: m.Login, Total: scoring.Season(doors, now, b.CountsUntil, progress[m.GitHubID])}
	}
	return scoring.Rank(entries), nil
}

// ranksOf is the player's place on each of the boards, by board ID. Everyone's progress is
// loaded at once, however many boards there are.
func (l Leaderboards) ranksOf(ctx context.Context, boards []leaderboards.Leaderboard, githubUserID int64, now time.Time) (map[string]int, error) {
	membersOf := make(map[string][]leaderboards.Member, len(boards))
	seen := map[int64]bool{}
	var ids []int64
	for _, b := range boards {
		members, err := l.Store.Members(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		membersOf[b.ID] = members
		for _, m := range members {
			if !seen[m.GitHubID] {
				seen[m.GitHubID] = true
				ids = append(ids, m.GitHubID)
			}
		}
	}
	progress, err := l.Results.Progress(ctx, ids)
	if err != nil {
		return nil, err
	}
	doors := openings(l.Schedule)
	ranks := make(map[string]int, len(boards))
	for _, b := range boards {
		members := membersOf[b.ID]
		entries := make([]scoring.Entry, len(members))
		for i, m := range members {
			entries[i] = scoring.Entry{ID: m.GitHubID, Login: m.Login, Total: scoring.Season(doors, now, b.CountsUntil, progress[m.GitHubID])}
		}
		for _, r := range scoring.Rank(entries) {
			if r.ID == githubUserID {
				ranks[b.ID] = r.Rank
			}
		}
	}
	return ranks, nil
}

func summarize(b leaderboards.Leaderboard, rank int, viewer int64, now time.Time) summary {
	return summary{
		ID:          b.ID,
		Name:        b.Name,
		Public:      b.Public,
		Own:         b.OwnerID == viewer,
		Members:     b.Members,
		CountsUntil: untilPtr(b),
		Final:       isFinal(b, now),
		Rank:        rank,
	}
}

type myLeaderboardsResponse struct {
	Leaderboards []summary `json:"leaderboards"`
	// Owned is how many of them are the player's own, MaxOwned how many they may have.
	Owned    int `json:"owned"`
	MaxOwned int `json:"maxOwned"`
}

// handleMyLeaderboards lists the boards of who is logged in: the ones they own and the ones
// they're in.
func handleMyLeaderboards(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		v := l.viewer(r)
		if !v.loggedIn {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		ctx := r.Context()
		boards, err := l.Store.OfMember(ctx, v.id)
		if err != nil {
			writeTryAgain(log, w, "Listing someone's leaderboards failed", err)
			return
		}
		now := l.now()
		ranks, err := l.ranksOf(ctx, boards, v.id, now)
		if err != nil {
			writeTryAgain(log, w, "Ranking someone on their leaderboards failed", err)
			return
		}
		resp := myLeaderboardsResponse{Leaderboards: []summary{}, MaxOwned: leaderboards.MaxOwned}
		for _, b := range boards {
			s := summarize(b, ranks[b.ID], v.id, now)
			if s.Own {
				resp.Owned++
			}
			resp.Leaderboards = append(resp.Leaderboards, s)
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

type createRequest struct {
	Name string `json:"name"`
}

type createdResponse struct {
	ID string `json:"id"`
}

// handleCreateLeaderboard makes a private board for who is logged in, at most MaxOwned.
func handleCreateLeaderboard(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !requireJSON(w, r) {
			return
		}
		id, ok := l.loggedInUser(log, w, r)
		if !ok {
			return
		}
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		name, err := cleanName(req.Name)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		b, err := l.Store.Create(r.Context(), id, name, l.now())
		if errors.Is(err, leaderboards.ErrTooMany) {
			writeJSONError(w, http.StatusConflict, fmt.Sprintf("you can have %d leaderboards of your own. Delete one to make another", leaderboards.MaxOwned))
			return
		}
		if err != nil {
			writeTryAgain(log, w, "Creating a leaderboard failed", err)
			return
		}
		log.Info("Leaderboard created", "leaderboard", b.ID, "github_user_id", id)
		writeJSON(w, http.StatusCreated, createdResponse{ID: b.ID})
	})
}

// handleLeaderboard answers with a board and its standings: to its members, and to anyone if
// it's public. The same 404 as for nothing otherwise.
func handleLeaderboard(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Different for every viewer, and changing with every verify.
		w.Header().Set("Cache-Control", "private, no-store")
		v := l.viewer(r)
		b, member, ok := l.find(log, w, r, v)
		if !ok {
			return
		}
		ctx := r.Context()
		members, err := l.Store.Members(ctx, b.ID)
		if err != nil {
			writeTryAgain(log, w, "Listing a leaderboard's members failed", err)
			return
		}
		now := l.now()
		ranked, err := l.standings(ctx, b, members, now)
		if err != nil {
			writeTryAgain(log, w, "Working out a leaderboard's standings failed", err)
			return
		}
		resp := l.boardResponse(b, members, ranked, v, member, now)
		if resp.You.Owner {
			banned, err := l.Store.Banned(ctx, b.ID)
			if err != nil {
				writeTryAgain(log, w, "Listing who was removed from a leaderboard failed", err)
				return
			}
			resp.Removed = []removedMember{}
			for _, m := range banned {
				resp.Removed = append(resp.Removed, removedMember{ID: m.GitHubID, Login: m.Login, RemovedAt: m.RemovedAt})
			}
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

func (l Leaderboards) boardResponse(b leaderboards.Leaderboard, members []leaderboards.Member, ranked []scoring.Ranked, v viewer, member bool, now time.Time) boardResponse {
	resp := boardResponse{
		ID:          b.ID,
		Name:        b.Name,
		Public:      b.Public,
		OpenJoin:    b.OpenJoin,
		CountsUntil: untilPtr(b),
		Final:       isFinal(b, now),
		Members:     len(members),
		MaxMembers:  leaderboards.MaxMembers,
		Doors:       []boardDoor{},
		Standings:   []standing{},
	}
	resp.You.LoggedIn = v.loggedIn
	resp.You.Member = member
	resp.You.Owner = v.loggedIn && b.OwnerID == v.id
	if member {
		resp.JoinCode = b.JoinCode
	}
	byID := make(map[int64]leaderboards.Member, len(members))
	for _, m := range members {
		byID[m.GitHubID] = m
		if m.GitHubID == b.OwnerID {
			resp.Owner.Login = m.Login
			resp.Owner.AvatarURL = m.AvatarURL
		}
	}
	for _, o := range openings(l.Schedule) {
		counts := !now.Before(o.OpensAt) && (b.CountsUntil.IsZero() || !o.OpensAt.After(b.CountsUntil))
		resp.Doors = append(resp.Doors, boardDoor{Door: o.Door, OpensAt: o.OpensAt, Counts: counts})
	}
	for _, r := range ranked {
		m := byID[r.ID]
		hidden := !member && !m.ProfilePublic && r.ID != b.OwnerID
		s := standing{
			Rank:           r.Rank,
			ID:             r.ID,
			Login:          m.Login,
			AvatarURL:      m.AvatarURL,
			ProfilePublic:  m.ProfilePublic,
			You:            v.loggedIn && r.ID == v.id,
			Owner:          r.ID == b.OwnerID,
			Points:         r.Total.Points,
			FailedVerifies: r.Total.FailedVerifies,
			Fixed:          r.Total.Fixed,
			Doors:          []standingDoor{},
		}
		for _, o := range resp.Doors {
			if d := r.Total.Doors[o.Door]; d.Fixed {
				s.Doors = append(s.Doors, standingDoor{Door: o.Door, Points: d.Points, Hints: d.Hints, FailedVerifies: d.FailedVerifies})
			}
		}
		if hidden {
			s.ID, s.Login, s.AvatarURL, s.Private = 0, "", "", true
		}
		resp.Together += r.Total.Fixed
		resp.Standings = append(resp.Standings, s)
	}
	return resp
}

// optionalTime tells a deadline that's left out apart from one that's null, which removes it.
type optionalTime struct {
	set   bool
	value *time.Time
}

func (o *optionalTime) UnmarshalJSON(data []byte) error {
	o.set = true
	return json.Unmarshal(data, &o.value)
}

type updateRequest struct {
	Name     *string `json:"name"`
	Public   *bool   `json:"public"`
	OpenJoin *bool   `json:"openJoin"`
	// CountsUntil is the deadline, RFC 3339, or null for none. Left out, it stays.
	CountsUntil optionalTime `json:"countsUntil"`
}

// handleUpdateLeaderboard changes a board's settings, for its owner.
func handleUpdateLeaderboard(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !requireJSON(w, r) {
			return
		}
		b, ok := l.findOwned(log, w, r)
		if !ok {
			return
		}
		var req updateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		var c leaderboards.Changes
		if req.Name != nil {
			name, err := cleanName(*req.Name)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			c.Name = &name
		}
		c.Public, c.OpenJoin = req.Public, req.OpenJoin
		if req.CountsUntil.set {
			until := time.Time{}
			if req.CountsUntil.value != nil {
				until = req.CountsUntil.value.UTC()
				if until.Before(earliestDeadline) || !until.Before(latestDeadline) {
					writeJSONError(w, http.StatusBadRequest, "that deadline isn't a date anyone means")
					return
				}
			}
			c.CountsUntil = &until
		}
		updated, err := l.Store.Update(r.Context(), b.ID, c)
		if errors.Is(err, leaderboards.ErrNotFound) {
			writeNoLeaderboard(w)
			return
		}
		if err != nil {
			writeTryAgain(log, w, "Changing a leaderboard failed", err)
			return
		}
		log.Info("Leaderboard changed", "leaderboard", b.ID)
		writeJSON(w, http.StatusOK, struct {
			ID          string     `json:"id"`
			Name        string     `json:"name"`
			Public      bool       `json:"public"`
			OpenJoin    bool       `json:"openJoin"`
			CountsUntil *time.Time `json:"countsUntil"`
		}{updated.ID, updated.Name, updated.Public, updated.OpenJoin, untilPtr(updated)})
	})
}

// handleNewInvite replaces a board's invite link, for its owner: the old one stops working.
func handleNewInvite(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !requireJSON(w, r) {
			return
		}
		b, ok := l.findOwned(log, w, r)
		if !ok {
			return
		}
		code, err := l.Store.NewJoinCode(r.Context(), b.ID)
		if errors.Is(err, leaderboards.ErrNotFound) {
			writeNoLeaderboard(w)
			return
		}
		if err != nil {
			writeTryAgain(log, w, "Making a new invite link failed", err)
			return
		}
		log.Info("Leaderboard invite replaced", "leaderboard", b.ID)
		writeJSON(w, http.StatusOK, struct {
			JoinCode string `json:"joinCode"`
		}{code})
	})
}

// handleDeleteLeaderboard deletes a board for everyone, for its owner.
func handleDeleteLeaderboard(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		b, ok := l.findOwned(log, w, r)
		if !ok {
			return
		}
		if err := l.Store.Delete(r.Context(), b.ID); err != nil && !errors.Is(err, leaderboards.ErrNotFound) {
			writeTryAgain(log, w, "Deleting a leaderboard failed", err)
			return
		}
		log.Info("Leaderboard deleted", "leaderboard", b.ID)
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleRemoveMember takes a member off a board: the owner removes anyone but themselves,
// who then can't join again until the owner lets them, and every other member can leave,
// and join again whenever they like. The owner can only delete the board.
func handleRemoveMember(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		v := l.viewer(r)
		if !v.loggedIn {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		b, member, ok := l.find(log, w, r, v)
		if !ok {
			return
		}
		user, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
		if err != nil || user <= 0 {
			writeJSONError(w, http.StatusNotFound, "no such member")
			return
		}
		switch {
		case user == b.OwnerID:
			writeJSONError(w, http.StatusConflict, "the owner can't leave. Delete the leaderboard instead")
			return
		case v.id == b.OwnerID, v.id == user && member:
		default:
			writeJSONError(w, http.StatusForbidden, "only its owner can remove someone else")
			return
		}
		byOwner := v.id != user
		if byOwner {
			err = l.Store.Remove(r.Context(), b.ID, user, l.now())
		} else {
			err = l.Store.Leave(r.Context(), b.ID, user)
		}
		if errors.Is(err, leaderboards.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "no such member")
			return
		}
		if err != nil {
			writeTryAgain(log, w, "Removing a member failed", err)
			return
		}
		log.Info("Leaderboard member removed", "leaderboard", b.ID, "github_user_id", user, "by_owner", byOwner)
		w.WriteHeader(http.StatusNoContent)
	})
}

// handleLetBackIn takes someone off the board's list of removed players, for its owner: they
// may join again, with the invite link or, if the board is open, without. It doesn't add them.
func handleLetBackIn(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		b, ok := l.findOwned(log, w, r)
		if !ok {
			return
		}
		user, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
		if err != nil || user <= 0 {
			writeJSONError(w, http.StatusNotFound, "they weren't removed")
			return
		}
		err = l.Store.Unban(r.Context(), b.ID, user)
		if errors.Is(err, leaderboards.ErrNotFound) {
			writeJSONError(w, http.StatusNotFound, "they weren't removed")
			return
		}
		if err != nil {
			writeTryAgain(log, w, "Letting someone back into a leaderboard failed", err)
			return
		}
		log.Info("Leaderboard member let back in", "leaderboard", b.ID, "github_user_id", user)
		w.WriteHeader(http.StatusNoContent)
	})
}

// joinPreview is what the invite link shows before joining.
type joinPreview struct {
	// ID is there once the viewer may see the board: it's public, or they're in it.
	ID          string     `json:"id,omitempty"`
	Name        string     `json:"name"`
	Public      bool       `json:"public"`
	CountsUntil *time.Time `json:"countsUntil"`
	Final       bool       `json:"final"`
	Owner       struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatarUrl"`
	} `json:"owner"`
	Members    int  `json:"members"`
	MaxMembers int  `json:"maxMembers"`
	Full       bool `json:"full"`
	// Member says whether the viewer is in it already.
	Member bool `json:"member"`
}

// findByCode looks up the board of the invite link in the path. Anyone who has the link may
// see what it's for.
func (l Leaderboards) findByCode(log app.Logger, w http.ResponseWriter, r *http.Request) (leaderboards.Leaderboard, bool) {
	code := r.PathValue("code")
	if !leaderboards.ValidToken(code) {
		writeJSONError(w, http.StatusNotFound, "this invite link isn't valid anymore")
		return leaderboards.Leaderboard{}, false
	}
	b, err := l.Store.ByJoinCode(r.Context(), code)
	if errors.Is(err, leaderboards.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "this invite link isn't valid anymore")
		return b, false
	}
	if err != nil {
		writeTryAgain(log, w, "Looking up an invite link failed", err)
		return b, false
	}
	return b, true
}

// handleJoinPreview says what an invite link is for: anyone with the link may know.
func handleJoinPreview(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		b, ok := l.findByCode(log, w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		v := l.viewer(r)
		member := false
		if v.loggedIn {
			var err error
			if member, err = l.Store.IsMember(ctx, b.ID, v.id); err != nil {
				writeTryAgain(log, w, "Looking up a leaderboard's members failed", err)
				return
			}
		}
		members, err := l.Store.Members(ctx, b.ID)
		if err != nil {
			writeTryAgain(log, w, "Listing a leaderboard's members failed", err)
			return
		}
		now := l.now()
		p := joinPreview{
			Name:        b.Name,
			Public:      b.Public,
			CountsUntil: untilPtr(b),
			Final:       isFinal(b, now),
			Members:     len(members),
			MaxMembers:  leaderboards.MaxMembers,
			Full:        len(members) >= leaderboards.MaxMembers,
			Member:      member,
		}
		if member || b.Public {
			p.ID = b.ID
		}
		for _, m := range members {
			if m.GitHubID == b.OwnerID {
				p.Owner.Login, p.Owner.AvatarURL = m.Login, m.AvatarURL
			}
		}
		writeJSON(w, http.StatusOK, p)
	})
}

// handleJoinWithCode adds who is logged in to the board of the invite link.
func handleJoinWithCode(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !requireJSON(w, r) {
			return
		}
		id, ok := l.loggedInUser(log, w, r)
		if !ok {
			return
		}
		b, ok := l.findByCode(log, w, r)
		if !ok {
			return
		}
		l.join(log, w, r, b, b.JoinCode, id)
	})
}

// handleJoinOpen adds who is logged in to a public board its owner opened to anyone. Every
// other board needs the invite link: the same 404 as for nothing if they may not see it, 403
// if they may.
func handleJoinOpen(log app.Logger, l Leaderboards) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if !requireJSON(w, r) {
			return
		}
		id, ok := l.loggedInUser(log, w, r)
		if !ok {
			return
		}
		b, member, ok := l.find(log, w, r, viewer{id: id, loggedIn: true})
		if !ok {
			return
		}
		if member {
			writeJSON(w, http.StatusOK, createdResponse{ID: b.ID})
			return
		}
		if !(b.Public && b.OpenJoin) {
			writeJSONError(w, http.StatusForbidden, "this leaderboard is invite only: ask its owner for the link")
			return
		}
		l.join(log, w, r, b, "", id)
	})
}

// join adds the player with code, the invite link's, or "" for an open board. The store
// checks it again under its lock.
func (l Leaderboards) join(log app.Logger, w http.ResponseWriter, r *http.Request, b leaderboards.Leaderboard, code string, id int64) {
	err := l.Store.Join(r.Context(), b.ID, code, id, l.now())
	if errors.Is(err, leaderboards.ErrClosed) {
		if code != "" {
			writeJSONError(w, http.StatusNotFound, "this invite link isn't valid anymore")
		} else {
			writeJSONError(w, http.StatusForbidden, "this leaderboard is invite only: ask its owner for the link")
		}
		return
	}
	if errors.Is(err, leaderboards.ErrBanned) {
		writeJSONError(w, http.StatusForbidden, "the owner removed you from this leaderboard. Only they can let you back in")
		return
	}
	if errors.Is(err, leaderboards.ErrFull) {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("this leaderboard is full: it has %d members", leaderboards.MaxMembers))
		return
	}
	if errors.Is(err, leaderboards.ErrNotFound) {
		writeNoLeaderboard(w)
		return
	}
	if err != nil {
		writeTryAgain(log, w, "Joining a leaderboard failed", err)
		return
	}
	log.Info("Leaderboard joined", "leaderboard", b.ID, "github_user_id", id)
	writeJSON(w, http.StatusOK, createdResponse{ID: b.ID})
}

// tooManyLeaderboardRequests answers a client past a create or join limit.
func tooManyLeaderboardRequests(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "60")
	writeJSONError(w, http.StatusTooManyRequests, "too many requests from here, try again in a few minutes")
}
