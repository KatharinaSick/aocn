package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/github"
	"github.com/katharinasick/aocn/api/internal/session"
	"github.com/katharinasick/aocn/api/internal/users"
)

// Logging in on the website: GitHub's OAuth flow, then a signed session cookie that holds
// only the GitHub user ID. The GitHub token is used once, to ask who it belongs to, and
// then dropped.
//
// The browser reaches these endpoints through the website's /api/* proxy, so every cookie
// here belongs to the website's own origin and never has to be cross-site. Paths in
// cookies and redirects are the browser's, with /api in front.

const (
	sessionCookie = "aocn_session"
	// stateCookie holds the random state sent to GitHub, which has to come back unchanged,
	// so only a login this browser started can end in it (OAuth's CSRF protection).
	stateCookie = "aocn_login_state"
	// returnCookie holds where to go after logging in.
	returnCookie = "aocn_login_return"
	// loginTakesAtMost is how long the state and return cookies last.
	loginTakesAtMost = 10 * time.Minute

	// Logins finished per IP: enough for a workshop behind one address, not for a script.
	loginIPLimit  = 30
	loginIPWindow = 10 * time.Minute
	// Logins finished by everyone together, per instance: far more than real people do, and
	// few enough that GitHub never sees a flood from us.
	githubCallLimit  = 120
	githubCallWindow = time.Minute
)

// tooManyLogins answers a client past a login limit. JSON, which aocn login reads; the
// browser shows it as it is, which is rare enough.
func tooManyLogins(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Retry-After", "60")
	writeJSONError(w, http.StatusTooManyRequests, "too many logins right now, try again in a few minutes")
}

// OAuthApp is the GitHub OAuth app. *github.OAuth talks to github.com.
type OAuthApp interface {
	AuthorizeURL(redirectURI, state string) string
	Exchange(ctx context.Context, code, redirectURI string) (string, error)
}

// GitHubUsers asks GitHub whose token it is. *github.Users does. Only logging in asks: once
// on the website, once in aocn login.
type GitHubUsers interface {
	User(ctx context.Context, token string) (github.User, error)
}

// UserStore keeps who logged in.
type UserStore interface {
	Save(ctx context.Context, user users.User, at time.Time) error
	Get(ctx context.Context, githubUserID int64) (users.User, error)
}

// Auth is what the login endpoints need.
type Auth struct {
	// OAuth is nil when the API has no client secret, which only happens in dev mode.
	OAuth OAuthApp
	// Users finds out whose GitHub token it is.
	Users      GitHubUsers
	Store      UserStore
	SessionKey []byte
	// SiteURL is the website, without a trailing slash: GitHub sends people back to its
	// /api/auth/github/callback, which has to match the OAuth app's callback URL.
	SiteURL string
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (a Auth) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a Auth) redirectURI() string {
	return a.SiteURL + "/api/auth/github/callback"
}

// cookie is one of ours. Secure whenever the site is served over HTTPS; locally it's
// plain http://localhost. Lax, so the cookies come along when GitHub sends the browser
// back, and when someone follows a link to the site, but not on another site's POST.
func (a Auth) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(a.SiteURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	}
	if maxAge < 0 {
		c.MaxAge = -1
	}
	return c
}

// returnPath is a path on the website to go back to after logging in. Nothing that could
// leave the site: no scheme, no host, no "//" (another host to a browser), no backslash
// (which some browsers read as a slash) and no query string.
var returnPath = regexp.MustCompile(`^/[A-Za-z0-9/_.~%-]*$`)

// safeReturn is p if it's a path on the website, and the home page otherwise.
func safeReturn(p string) string {
	if len(p) <= 200 && returnPath.MatchString(p) && !strings.HasPrefix(p, "//") {
		return p
	}
	return "/"
}

// handleLogin sends the browser to GitHub. ?return= is where to come back to.
func handleLogin(a Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.OAuth == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "GitHub login isn't set up here: set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET")
			return
		}
		b := make([]byte, 32)
		rand.Read(b) // never returns an error
		state := base64.RawURLEncoding.EncodeToString(b)

		http.SetCookie(w, a.cookie(stateCookie, state, loginTakesAtMost))
		http.SetCookie(w, a.cookie(returnCookie, safeReturn(r.URL.Query().Get("return")), loginTakesAtMost))
		http.Redirect(w, r, a.OAuth.AuthorizeURL(a.redirectURI(), state), http.StatusFound)
	})
}

// handleCallback is where GitHub sends the browser back, with a code to swap for a token.
// It sets the session cookie and goes back to where the login started.
//
// The browser shows what this answers, so failures are short plain sentences.
func handleCallback(log app.Logger, a Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if a.OAuth == nil {
			http.Error(w, "GitHub login isn't set up here.", http.StatusServiceUnavailable)
			return
		}
		back := "/"
		if c, err := r.Cookie(returnCookie); err == nil {
			back = safeReturn(c.Value)
		}
		var state string
		if c, err := r.Cookie(stateCookie); err == nil {
			state = c.Value
		}
		// Each state is good for one try.
		http.SetCookie(w, a.cookie(stateCookie, "", -1))
		http.SetCookie(w, a.cookie(returnCookie, "", -1))

		q := r.URL.Query()
		if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(q.Get("state"))) != 1 {
			http.Error(w, "This login took too long or didn't start here. Please log in again.", http.StatusBadRequest)
			return
		}
		if q.Get("error") != "" {
			// Most likely "access_denied": they changed their mind on GitHub's page.
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}

		ctx := r.Context()
		token, err := a.OAuth.Exchange(ctx, q.Get("code"), a.redirectURI())
		if errors.Is(err, github.ErrBadCode) {
			http.Error(w, "GitHub didn't accept this login. Please log in again.", http.StatusBadRequest)
			return
		}
		if err != nil {
			log.Error("Swapping a login code with GitHub failed", "error", err)
			http.Error(w, "GitHub couldn't log you in right now. Try again in a few minutes.", http.StatusBadGateway)
			return
		}
		gh, err := a.Users.User(ctx, token)
		if err != nil {
			log.Error("Asking GitHub who just logged in failed", "error", err)
			http.Error(w, "GitHub couldn't log you in right now. Try again in a few minutes.", http.StatusBadGateway)
			return
		}
		now := a.now()
		if err := a.Store.Save(ctx, users.User{GitHubID: gh.ID, Login: gh.Login, AvatarURL: gh.AvatarURL}, now); err != nil {
			log.Error("Saving who logged in failed", "error", err)
			http.Error(w, "Logging in failed on our side. Try again in a few minutes.", http.StatusInternalServerError)
			return
		}
		signed, err := session.Sign(a.SessionKey, gh.ID, now, session.Website)
		if err != nil {
			log.Error("Signing a session failed", "error", err)
			http.Error(w, "Logging in failed on our side. Try again in a few minutes.", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, a.cookie(sessionCookie, signed, session.Website.LastsFor))
		log.Info("Logged in", "github_user_id", gh.ID, "login", gh.Login)
		http.Redirect(w, r, back, http.StatusSeeOther)
	})
}

// handleLogout forgets the session in this browser. A POST, so a link or an image on
// another page can't log anyone out.
func handleLogout(a Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		http.SetCookie(w, a.cookie(sessionCookie, "", -1))
		w.WriteHeader(http.StatusNoContent)
	})
}

type meResponse struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

// handleMe says who is logged in, or 401. The website's pages are the same for everyone;
// this is the one request that differs, so it's never cached anywhere.
func handleMe(log app.Logger, a Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		id, ok := a.loggedIn(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		user, err := a.Store.Get(r.Context(), id)
		if errors.Is(err, users.ErrNotFound) {
			writeJSONError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if err != nil {
			log.Error("Looking up who is logged in failed", "error", err)
			writeJSONError(w, http.StatusServiceUnavailable, "try again in a few minutes")
			return
		}
		writeJSON(w, http.StatusOK, meResponse{Login: user.Login, AvatarURL: user.AvatarURL})
	})
}

// loggedIn returns the GitHub user ID from a valid session cookie.
func (a Auth) loggedIn(r *http.Request) (int64, bool) {
	return sessionUser(r, a.SessionKey, a.now())
}

// sessionUser returns the GitHub user ID from a valid session cookie. Without a key, nobody
// is logged in.
func sessionUser(r *http.Request, key []byte, now time.Time) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	id, err := session.Verify(key, c.Value, now, session.Website)
	return id, err == nil
}

type cliLoginResponse struct {
	Token string `json:"token"`
	Login string `json:"login"`
}

// handleCLILogin swaps the GitHub token from aocn login's device flow for this API's own,
// once. The CLI keeps only this API's token, which carries the GitHub user ID: no request
// after this asks GitHub who it is.
func handleCLILogin(log app.Logger, a Auth) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			writeJSONError(w, http.StatusUnauthorized, "sign in with: aocn login")
			return
		}
		ctx := r.Context()
		gh, err := a.Users.User(ctx, token)
		if errors.Is(err, github.ErrBadToken) {
			writeJSONError(w, http.StatusUnauthorized, "GitHub doesn't accept this sign-in. Run aocn login again")
			return
		}
		if err != nil {
			log.Error("Asking GitHub who is signing in failed", "error", err)
			writeJSONError(w, http.StatusBadGateway, "GitHub couldn't tell us who you are right now, try again in a few minutes")
			return
		}
		now := a.now()
		if err := a.Store.Save(ctx, users.User{GitHubID: gh.ID, Login: gh.Login, AvatarURL: gh.AvatarURL}, now); err != nil {
			log.Error("Saving who signed in failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "signing in failed on our side, try again in a few minutes")
			return
		}
		signed, err := session.Sign(a.SessionKey, gh.ID, now, session.CLI)
		if err != nil {
			log.Error("Signing a CLI token failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "signing in failed on our side, try again in a few minutes")
			return
		}
		log.Info("Signed in from the CLI", "github_user_id", gh.ID, "login", gh.Login)
		writeJSON(w, http.StatusOK, cliLoginResponse{Token: signed, Login: gh.Login})
	})
}
