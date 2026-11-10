package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGitHub knows one token and counts how often it's asked.
func fakeGitHub(t *testing.T, status *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if s := status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}
		if r.URL.Path != "/user" || r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"id": 42, "login": "octo", "avatar_url": "https://example.com/a.png"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestUser(t *testing.T) {
	var status atomic.Int32
	srv, asked := fakeGitHub(t, &status)
	now := time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)
	u := &Users{BaseURL: srv.URL, Client: srv.Client(), Now: func() time.Time { return now }}

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
	u := &Users{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := u.User(context.Background(), "made-up"); !errors.Is(err, ErrBadToken) {
		t.Errorf("got %v, want ErrBadToken", err)
	}
}
