package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub is GitHub's check-token endpoint for the app "app-id" with the secret
// "app-secret". It knows the token "good", issued to this app, and "other-app", issued to
// another one, and counts how often it's asked.
func fakeGitHub(t *testing.T, status *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if s := status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "app-id" || secret != "app-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/applications/app-id/token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AccessToken == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		user := `"user": {"id": 42, "login": "octo", "avatar_url": "https://example.com/a.png"}`
		switch body.AccessToken {
		case "good":
			fmt.Fprintf(w, `{"app": {"client_id": "app-id"}, %s}`, user)
		case "other-app":
			// GitHub wouldn't answer this; the client ID is checked all the same.
			fmt.Fprintf(w, `{"app": {"client_id": "someone-else"}, %s}`, user)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func usersAt(srv *httptest.Server) *Users {
	return &Users{BaseURL: srv.URL, ClientID: "app-id", ClientSecret: "app-secret", Client: srv.Client()}
}

func TestUser(t *testing.T) {
	var status atomic.Int32
	srv, asked := fakeGitHub(t, &status)
	now := time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)
	u := usersAt(srv)
	u.Now = func() time.Time { return now }

	user, err := u.User(context.Background(), "good")
	if err != nil || user != (User{ID: 42, Login: "octo", AvatarURL: "https://example.com/a.png"}) {
		t.Fatalf("got %+v, %v", user, err)
	}

	// Kept for a few minutes: GitHub is asked once, even while it's down.
	status.Store(http.StatusBadGateway)
	now = now.Add(cacheFor - time.Second)
	if user, err := u.User(context.Background(), "good"); err != nil || user.ID != 42 {
		t.Errorf("cached: got %+v, %v", user, err)
	}
	if asked.Load() != 1 {
		t.Errorf("GitHub was asked %d times, want 1", asked.Load())
	}

	// Then asked again.
	now = now.Add(time.Second)
	if _, err := u.User(context.Background(), "good"); err == nil || errors.Is(err, ErrBadToken) {
		t.Errorf("after the cache, with GitHub down: got %v, want an error that isn't ErrBadToken", err)
	}
	status.Store(0)
	if user, err := u.User(context.Background(), "good"); err != nil || user.ID != 42 {
		t.Errorf("errors aren't kept: got %+v, %v", user, err)
	}
}

func TestBadToken(t *testing.T) {
	var status atomic.Int32
	srv, _ := fakeGitHub(t, &status)
	u := usersAt(srv)
	for _, token := range []string{"made-up", "other-app"} {
		if _, err := u.User(context.Background(), token); !errors.Is(err, ErrBadToken) {
			t.Errorf("%s: got %v, want ErrBadToken", token, err)
		}
	}
}

// A wrong client secret is our mistake, so it's an error, but never says the caller's token
// is bad.
func TestWrongClientSecret(t *testing.T) {
	var status atomic.Int32
	srv, _ := fakeGitHub(t, &status)
	u := usersAt(srv)
	u.ClientSecret = "wrong"
	if _, err := u.User(context.Background(), "good"); err == nil || errors.Is(err, ErrBadToken) {
		t.Errorf("got %v, want an error that isn't ErrBadToken", err)
	}
}

func TestNoOAuthApp(t *testing.T) {
	var status atomic.Int32
	srv, asked := fakeGitHub(t, &status)
	u := &Users{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := u.User(context.Background(), "good"); err == nil || asked.Load() != 0 {
		t.Errorf("got %v after asking GitHub %d times, want an error without asking", err, asked.Load())
	}
}
