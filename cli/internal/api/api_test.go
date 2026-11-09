package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/doors/2/bundle" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AOCN_API_URL", srv.URL+"/")
}

func TestDownloadBundle(t *testing.T) {
	serve(t, http.StatusOK, "bundle")
	var buf bytes.Buffer
	if err := DownloadBundle(context.Background(), 2, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "bundle" {
		t.Errorf("got %q", buf.String())
	}
}

func TestDownloadClosedDoor(t *testing.T) {
	serve(t, http.StatusNotFound, `{"door":2,"opensAt":"2026-12-03T05:00:00Z"}`)
	var closed *ClosedError
	err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
	if !errors.As(err, &closed) {
		t.Fatalf("got %v, want a ClosedError", err)
	}
	if !closed.OpensAt.Equal(time.Date(2026, 12, 3, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("opensAt %v", closed.OpensAt)
	}
	// Station time, whatever the player's time zone.
	if got, want := err.Error(), "door 2 opens Dec 3, 06:00 CET"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDownloadUnknownDoor(t *testing.T) {
	serve(t, http.StatusNotFound, `{"error":"no such door"}`)
	if err := DownloadBundle(context.Background(), 2, &bytes.Buffer{}); !errors.Is(err, ErrNoDoor) {
		t.Errorf("got %v, want ErrNoDoor", err)
	}
}

// Errors name the API that answered, so a local one isn't mistaken for aocn.dev.
func TestDownloadServerError(t *testing.T) {
	serve(t, http.StatusServiceUnavailable, `{"error":"try again"}`)
	err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
	if want := host() + ": try again"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if strings.Contains(host(), "aocn.dev") {
		t.Errorf("host() = %q for a local API", host())
	}
}

func TestDownloadUnreachable(t *testing.T) {
	t.Setenv("AOCN_API_URL", "http://127.0.0.1:1")
	err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
	if err == nil || !strings.HasPrefix(err.Error(), "couldn't reach 127.0.0.1:1: ") {
		t.Errorf("got %v", err)
	}
}

// A download that ends before the length the API announced is an error, not a bundle.
func TestDownloadCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("half"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AOCN_API_URL", srv.URL)
	if err := DownloadBundle(context.Background(), 2, &bytes.Buffer{}); err == nil {
		t.Error("a cut-off download was accepted")
	}
}

func TestBaseURL(t *testing.T) {
	t.Setenv("AOCN_API_URL", "")
	if got := BaseURL(); got != "https://aocn.dev/api" {
		t.Errorf("got %q", got)
	}
}
