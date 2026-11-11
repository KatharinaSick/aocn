// Package github finds out whose GitHub token a request carries. The CLI signs in with
// GitHub's device flow and sends the token it got; the API asks GitHub who that is.
package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// User is who a token belongs to. ID is what the API keeps: a login can be renamed and then
// taken by someone else, an ID can't.
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
}

// ErrBadToken means the token isn't one GitHub issued to this OAuth app: revoked, expired,
// made up, or a token of any other app or a personal access token.
var ErrBadToken = errors.New("GitHub doesn't accept this token")

// cacheFor is how long a token's user is kept. Only logging in asks, once per login, so this
// mostly saves asking twice when a login is retried; a revoked token keeps working this long.
const cacheFor = 5 * time.Minute

// maxCached bounds the cache. Past it, expired entries are dropped, and if that isn't
// enough, all of them: asking GitHub again is cheap, running out of memory isn't.
const maxCached = 10_000

// Users asks GitHub whose token it is and keeps the answer for a few minutes. Tokens are
// kept only as their SHA-256, so the cache holds nothing a token could be rebuilt from.
//
// It asks as the OAuth app, with its client secret, and GitHub answers only for tokens it
// issued to this app. Asking GET /user instead would take any token: one that another app
// holds for someone, or a personal access token, would then sign them in here too. Asking as
// the app also counts against the app's rate limit, not against our address.
type Users struct {
	// BaseURL is GitHub's API, https://api.github.com. Tests point it elsewhere.
	BaseURL      string
	ClientID     string
	ClientSecret string
	Client       *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	entries map[[32]byte]entry
}

type entry struct {
	user    User
	expires time.Time
}

// NewUsers asks api.github.com as the OAuth app.
func NewUsers(clientID, clientSecret string) *Users {
	return &Users{
		BaseURL:      "https://api.github.com",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Client:       &http.Client{Timeout: 10 * time.Second},
	}
}

func (u *Users) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

// User returns whose token it is. Errors are not kept, so a token that failed because GitHub
// had a hiccup works on the next try.
func (u *Users) User(ctx context.Context, token string) (User, error) {
	key := sha256.Sum256([]byte(token))
	now := u.now()

	u.mu.Lock()
	e, ok := u.entries[key]
	u.mu.Unlock()
	if ok && now.Before(e.expires) {
		return e.user, nil
	}

	user, err := u.ask(ctx, token)
	if err != nil {
		return User{}, err
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.entries == nil {
		u.entries = map[[32]byte]entry{}
	}
	if len(u.entries) >= maxCached {
		for k, e := range u.entries {
			if !now.Before(e.expires) {
				delete(u.entries, k)
			}
		}
		if len(u.entries) >= maxCached {
			clear(u.entries)
		}
	}
	u.entries[key] = entry{user: user, expires: now.Add(cacheFor)}
	return user, nil
}

// checkResponse is what GitHub's check-token endpoint answers, as far as it's read.
type checkResponse struct {
	User User `json:"user"`
	App  struct {
		ClientID string `json:"client_id"`
	} `json:"app"`
}

// ask checks the token with GitHub as the OAuth app: POST /applications/{client_id}/token.
func (u *Users) ask(ctx context.Context, token string) (User, error) {
	if u.ClientID == "" || u.ClientSecret == "" {
		return User{}, errors.New("asking GitHub who this is: no OAuth app to ask as")
	}
	body, err := json.Marshal(map[string]string{"access_token": token})
	if err != nil {
		return User{}, err
	}
	endpoint := u.BaseURL + "/applications/" + url.PathEscape(u.ClientID) + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return User{}, err
	}
	req.SetBasicAuth(u.ClientID, u.ClientSecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := u.Client.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("asking GitHub who this is: %w", err)
	}
	defer resp.Body.Close()

	switch {
	// 404: not a token of this app. 422: not a token at all. A 401 would be our own client
	// secret being wrong, which is our mistake, not the caller's, so it's an error below.
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusUnprocessableEntity:
		return User{}, ErrBadToken
	case resp.StatusCode != http.StatusOK:
		return User{}, fmt.Errorf("asking GitHub who this is: %s", resp.Status)
	}
	var check checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&check); err != nil {
		return User{}, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if check.App.ClientID != u.ClientID {
		return User{}, ErrBadToken
	}
	if check.User.ID <= 0 || check.User.Login == "" {
		return User{}, errors.New("GitHub answered without a user")
	}
	return check.User, nil
}
