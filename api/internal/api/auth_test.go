package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/github"
	"github.com/katharinasick/aocn/api/internal/session"
	"github.com/katharinasick/aocn/api/internal/users"
)

var (
	sessionKey = []byte("session key, session key, session")
	loginNow   = time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)
)

// fakeOAuth is GitHub's OAuth app: the code "good" swaps for the player's token.
type fakeOAuth struct{ down bool }

func (fakeOAuth) AuthorizeURL(redirectURI, state string) string {
	return "https://github.example/authorize?" + url.Values{"redirect_uri": {redirectURI}, "state": {state}}.Encode()
}

func (f fakeOAuth) Exchange(_ context.Context, code, _ string) (string, error) {
	switch {
	case f.down:
		return "", errors.New("GitHub is down")
	case code != "good":
		return "", github.ErrBadCode
	}
	return githubPlayerToken, nil
}

// fakeUserStore keeps users in memory.
type fakeUserStore struct {
	saved map[int64]users.User
	down  bool
}

func (f *fakeUserStore) Save(_ context.Context, u users.User, _ time.Time) error {
	if f.down {
		return errors.New("database is down")
	}
	f.saved[u.GitHubID] = u
	return nil
}

func (f *fakeUserStore) Get(_ context.Context, id int64) (users.User, error) {
	if f.down {
		return users.User{}, errors.New("database is down")
	}
	u, ok := f.saved[id]
	if !ok {
		return users.User{}, users.ErrNotFound
	}
	return u, nil
}

type authAPI struct {
	http.Handler
	store  *fakeUserStore
	github *fakeGitHub
}

func authAt(t *testing.T, oauth OAuthApp) authAPI {
	t.Helper()
	a := authAPI{store: &fakeUserStore{saved: map[int64]users.User{}}, github: &fakeGitHub{}}
	a.Handler = NewHandler(nopLogger{}, Subscriptions{}, Doors{}, Auth{
		OAuth:      oauth,
		Users:      a.github,
		Store:      a.store,
		SessionKey: sessionKey,
		SiteURL:    "https://aocn.dev",
		Now:        func() time.Time { return loginNow },
	}, Profiles{})
	return a
}

// request sends a request with these cookies.
func request(h http.Handler, method, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login starts a login and returns the state GitHub would send back, and the cookies.
func login(t *testing.T, h http.Handler, returnTo string) (string, []*http.Cookie) {
	t.Helper()
	rec := request(h, http.MethodGet, "/auth/github/login?return="+url.QueryEscape(returnTo))
	if rec.Code != http.StatusFound {
		t.Fatalf("login: status %d", rec.Code)
	}
	to, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if got := to.Query().Get("redirect_uri"); got != "https://aocn.dev/api/auth/github/callback" {
		t.Errorf("redirect_uri: %q", got)
	}
	return to.Query().Get("state"), rec.Result().Cookies()
}

func TestLoginSetsTheSessionAndGoesBack(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	state, cookies := login(t, a, "/doors/3")
	for _, c := range cookies {
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("cookie %s: %+v", c.Name, c)
		}
	}

	rec := request(a, http.MethodGet, "/auth/github/callback?code=good&state="+url.QueryEscape(state), cookies...)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/doors/3" {
		t.Fatalf("callback: status %d, to %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	c := cookieNamed(rec, sessionCookie)
	if c == nil || !c.HttpOnly || !c.Secure || c.MaxAge != int(session.Website.LastsFor.Seconds()) {
		t.Fatalf("session cookie: %+v", c)
	}
	if id, err := session.Verify(sessionKey, c.Value, loginNow, session.Website); err != nil || id != player.ID {
		t.Errorf("session: %d, %v", id, err)
	}
	if got := a.store.saved[player.ID]; got.Login != player.Login {
		t.Errorf("saved: %+v", got)
	}
	// The state is good for one try.
	if s := cookieNamed(rec, stateCookie); s == nil || s.MaxAge >= 0 {
		t.Errorf("state cookie not cleared: %+v", s)
	}
}

func TestCallbackNeedsTheStateItStartedWith(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	state, cookies := login(t, a, "/")

	for name, query := range map[string]string{
		"other state": "code=good&state=other",
		"no state":    "code=good",
	} {
		rec := request(a, http.MethodGet, "/auth/github/callback?"+query, cookies...)
		if rec.Code != http.StatusBadRequest || cookieNamed(rec, sessionCookie) != nil {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	// Someone else's browser, which never started this login.
	rec := request(a, http.MethodGet, "/auth/github/callback?code=good&state="+url.QueryEscape(state))
	if rec.Code != http.StatusBadRequest || cookieNamed(rec, sessionCookie) != nil {
		t.Errorf("without the cookie: status %d", rec.Code)
	}
}

func TestCallbackFailures(t *testing.T) {
	tests := []struct {
		name   string
		oauth  fakeOAuth
		code   string
		dbDown bool
		want   int
	}{
		{"bad code", fakeOAuth{}, "used", false, http.StatusBadRequest},
		{"GitHub down", fakeOAuth{down: true}, "good", false, http.StatusBadGateway},
		{"database down", fakeOAuth{}, "good", true, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		a := authAt(t, tt.oauth)
		a.store.down = tt.dbDown
		state, cookies := login(t, a, "/")
		rec := request(a, http.MethodGet, "/auth/github/callback?code="+tt.code+"&state="+url.QueryEscape(state), cookies...)
		if rec.Code != tt.want || cookieNamed(rec, sessionCookie) != nil {
			t.Errorf("%s: status %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
}

func TestCallbackWhenTheyChangedTheirMind(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	state, cookies := login(t, a, "/how-it-works")
	rec := request(a, http.MethodGet, "/auth/github/callback?error=access_denied&state="+url.QueryEscape(state), cookies...)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/how-it-works" || cookieNamed(rec, sessionCookie) != nil {
		t.Fatalf("status %d, to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLoginWithoutOAuthApp(t *testing.T) {
	a := authAt(t, nil)
	if rec := request(a, http.MethodGet, "/auth/github/login"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("login: status %d", rec.Code)
	}
	if rec := request(a, http.MethodGet, "/auth/github/callback?code=good&state=x"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("callback: status %d", rec.Code)
	}
}

func TestSafeReturn(t *testing.T) {
	tests := map[string]string{
		"/doors/3":                      "/doors/3",
		"/":                             "/",
		"/u/octo-cat":                   "/u/octo-cat",
		"":                              "/",
		"//evil.example":                "/",
		"/\\evil.example":               "/",
		"https://evil.example":          "/",
		"evil.example":                  "/",
		"/doors/1?x=1":                  "/",
		"/a\nb":                         "/",
		"javascript:alert(1)":           "/",
		"/" + string(make([]byte, 300)): "/",
	}
	for in, want := range tests {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMe(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	a.store.saved[player.ID] = users.User{GitHubID: player.ID, Login: "player", AvatarURL: "https://example.com/8.png"}
	signed, _ := session.Sign(sessionKey, player.ID, loginNow, session.Website)

	rec := request(a, http.MethodGet, "/me", &http.Cookie{Name: sessionCookie, Value: signed})
	var body meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || body.Login != "player" {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control: %q", got)
	}
}

func TestMeWhenNotLoggedIn(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	other, _ := session.Sign([]byte("another key, another key, another"), player.ID, loginNow, session.Website)
	unknown, _ := session.Sign(sessionKey, 404, loginNow, session.Website)

	for name, cookies := range map[string][]*http.Cookie{
		"no cookie":    nil,
		"other key":    {{Name: sessionCookie, Value: other}},
		"unknown user": {{Name: sessionCookie, Value: unknown}},
	} {
		rec := request(a, http.MethodGet, "/me", cookies...)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: status %d, Cache-Control %q", name, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestLogout(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	if rec := request(a, http.MethodGet, "/auth/logout"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", rec.Code)
	}
	rec := request(a, http.MethodPost, "/auth/logout")
	if c := cookieNamed(rec, sessionCookie); rec.Code != http.StatusNoContent || c == nil || c.MaxAge >= 0 {
		t.Fatalf("status %d, cookie %+v", rec.Code, c)
	}
}

// postAs sends a POST with this Authorization header (none if empty).
func postAs(h http.Handler, path, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// aocn login hands over the device flow's GitHub token once and gets this API's own token,
// which the door endpoints then take without asking GitHub.
func TestCLILoginSwapsTheGitHubToken(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	rec := postAs(a, "/auth/cli", "Bearer "+githubPlayerToken)
	var body cliLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	if body.Login != player.Login || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("got %+v", body)
	}
	if id, err := session.Verify(sessionKey, body.Token, loginNow.Add(session.CLI.LastsFor-time.Second), session.CLI); err != nil || id != player.ID {
		t.Errorf("token: %d, %v", id, err)
	}
	if _, err := session.Verify(sessionKey, body.Token, loginNow, session.Website); err == nil {
		t.Error("the CLI's token works as a website cookie")
	}
	if got := a.store.saved[player.ID]; got.Login != player.Login {
		t.Errorf("saved: %+v", got)
	}
}

func TestCLILoginFailures(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	for name, authorization := range map[string]string{
		"no token":     "",
		"not GitHub's": "Bearer made-up",
		"not a bearer": "Basic dXNlcjpwYXNz",
		"empty bearer": "Bearer ",
	} {
		if rec := postAs(a, "/auth/cli", authorization); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	if rec := request(a, http.MethodGet, "/auth/cli"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d", rec.Code)
	}

	down := authAt(t, fakeOAuth{})
	down.store.down = true
	if rec := postAs(down, "/auth/cli", "Bearer "+githubPlayerToken); rec.Code != http.StatusInternalServerError {
		t.Errorf("database down: status %d", rec.Code)
	}
}

// postFrom is postAs from an IP, as Netlify reports it.
func postFrom(h http.Handler, path, authorization, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Every CLI login asks GitHub, even with a made-up token, so one address gets only so many.
func TestCLILoginLimitsRequestsPerIP(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	for i := range loginIPLimit {
		if rec := postFrom(a, "/auth/cli", "Bearer made-up", "203.0.113.1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status %d, want 401", i+1, rec.Code)
		}
	}
	rec := postFrom(a, "/auth/cli", "Bearer "+githubPlayerToken, "203.0.113.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("past the limit: status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if a.github.asked != loginIPLimit {
		t.Errorf("asked GitHub %d times, want %d", a.github.asked, loginIPLimit)
	}
	if rec := postFrom(a, "/auth/cli", "Bearer "+githubPlayerToken, "203.0.113.2"); rec.Code != http.StatusOK {
		t.Errorf("another address: status %d, want 200", rec.Code)
	}
}

// A caller going around Netlify can make up a new IP for every request, so logins are also
// limited all together, the website's and the CLI's counted as one.
func TestLoginsAreLimitedAllTogether(t *testing.T) {
	a := authAt(t, fakeOAuth{})
	for i := range githubCallLimit - 1 {
		ip := fmt.Sprintf("198.51.%d.%d", i/256, i%256)
		if rec := postFrom(a, "/auth/cli", "Bearer made-up", ip); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status %d, want 401", i+1, rec.Code)
		}
	}
	// The last one allowed is a website login.
	state, cookies := login(t, a, "/")
	if rec := request(a, http.MethodGet, "/auth/github/callback?code=good&state="+state, cookies...); rec.Code != http.StatusSeeOther {
		t.Fatalf("website login: status %d", rec.Code)
	}

	rec := postFrom(a, "/auth/cli", "Bearer "+githubPlayerToken, "192.0.2.200")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("CLI past the limit: status %d, want 429", rec.Code)
	}
	state, cookies = login(t, a, "/")
	if rec := request(a, http.MethodGet, "/auth/github/callback?code=good&state="+state, cookies...); rec.Code != http.StatusTooManyRequests {
		t.Errorf("website past the limit: status %d, want 429", rec.Code)
	}
	if a.github.asked != githubCallLimit {
		t.Errorf("asked GitHub %d times, want %d", a.github.asked, githubCallLimit)
	}
}

// Without an OAuth app (dev mode only) there's nothing to check a token as, so nobody signs in.
func TestCLILoginWithoutAnOAuthApp(t *testing.T) {
	h := NewHandler(nopLogger{}, Subscriptions{}, Doors{}, Auth{SessionKey: sessionKey}, Profiles{})
	if rec := postAs(h, "/auth/cli", "Bearer "+githubPlayerToken); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}
