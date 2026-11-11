// Package github finds out whose GitHub token a request carries. The CLI signs in with
// GitHub's device flow and sends the token it got; the API asks GitHub who that is.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// ErrBadToken means GitHub doesn't accept the token: revoked, expired or made up.
var ErrBadToken = errors.New("GitHub doesn't accept this token")

// cacheFor is how long a token's user is kept. Only logging in asks, once per login, so this
// mostly saves asking twice when a login is retried; a revoked token keeps working this long.
const cacheFor = 5 * time.Minute

// maxCached bounds the cache. Past it, expired entries are dropped, and if that isn't
// enough, all of them: asking GitHub again is cheap, running out of memory isn't.
const maxCached = 10_000

// Users asks GitHub whose token it is and keeps the answer for a few minutes. Tokens are
// kept only as their SHA-256, so the cache holds nothing a token could be rebuilt from.
type Users struct {
	// BaseURL is GitHub's API, https://api.github.com. Tests point it elsewhere.
	BaseURL string
	Client  *http.Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	entries map[[32]byte]entry
}

type entry struct {
	user    User
	expires time.Time
}

// NewUsers asks api.github.com.
func NewUsers() *Users {
	return &Users{BaseURL: "https://api.github.com", Client: &http.Client{Timeout: 10 * time.Second}}
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

func (u *Users) ask(ctx context.Context, token string) (User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.BaseURL+"/user", nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := u.Client.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("asking GitHub who this is: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return User{}, ErrBadToken
	case resp.StatusCode != http.StatusOK:
		return User{}, fmt.Errorf("asking GitHub who this is: %s", resp.Status)
	}
	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return User{}, fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if user.ID <= 0 || user.Login == "" {
		return User{}, errors.New("GitHub answered without a user")
	}
	return user, nil
}
