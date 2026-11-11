package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/cli/internal/auth"
	"github.com/katharinasick/aocn/cli/internal/check"
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
	signedOut(t)
}

// signedOut points the config folder somewhere empty, so no real token is read or sent.
func signedOut(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

func TestDownloadBundle(t *testing.T) {
	serve(t, http.StatusOK, "bundle")
	var buf bytes.Buffer
	if _, err := DownloadBundle(context.Background(), 2, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "bundle" {
		t.Errorf("got %q", buf.String())
	}
}

func TestDownloadClosedDoor(t *testing.T) {
	serve(t, http.StatusNotFound, `{"door":2,"opensAt":"2026-12-03T05:00:00Z"}`)
	var closed *ClosedError
	_, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
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
	if _, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{}); !errors.Is(err, ErrNoDoor) {
		t.Errorf("got %v, want ErrNoDoor", err)
	}
}

// Errors name the API that answered, so a local one isn't mistaken for aocn.dev.
func TestDownloadServerError(t *testing.T) {
	serve(t, http.StatusServiceUnavailable, `{"error":"try again"}`)
	_, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
	if want := host() + ": try again"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if strings.Contains(host(), "aocn.dev") {
		t.Errorf("host() = %q for a local API", host())
	}
}

func TestDownloadUnreachable(t *testing.T) {
	t.Setenv("AOCN_API_URL", "http://127.0.0.1:1")
	signedOut(t)
	_, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
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
	if _, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{}); err == nil {
		t.Error("a cut-off download was accepted")
	}
}

func TestBaseURL(t *testing.T) {
	t.Setenv("AOCN_API_URL", "")
	if got := BaseURL(); got != "https://aocn.dev/api" {
		t.Errorf("got %q", got)
	}
}

// fakeAPI answers like the API and remembers the last request.
type fakeAPI struct {
	authorization string
	body          string
}

func serveAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		f.body = string(body)
		switch r.Method + " " + r.URL.Path {
		case "GET /doors/2/bundle":
			w.Header().Set("Aocn-Preview", "2026-12-03T05:00:00Z")
			w.Write([]byte("bundle"))
		case "GET /doors/2/checks":
			w.Write([]byte(`{"door":2,"checks":[{"id":"a","ready":["apps"]},{"id":"b","http":{"method":"GET","to":"apps/api","from":"apps/new-pod","expect":"2xx"}},{"id":"c","job":"apps/cronjob/x"}]}`))
		case "POST /doors/2/verify":
			w.Write([]byte(`{"door":2,"passed":false,"login":"octo"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AOCN_API_URL", srv.URL)
	signedOut(t)
	return f
}

func TestTokenIsSentOnceSignedIn(t *testing.T) {
	f := serveAPI(t)
	if _, err := Checks(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if f.authorization != "" {
		t.Errorf("signed out: sent Authorization %q", f.authorization)
	}

	if err := auth.SaveToken("gho_test"); err != nil {
		t.Fatal(err)
	}
	if _, err := Checks(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if f.authorization != "Bearer gho_test" {
		t.Errorf("signed in: sent Authorization %q", f.authorization)
	}
}

func TestDownloadPreview(t *testing.T) {
	serveAPI(t)
	preview, err := DownloadBundle(context.Background(), 2, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.OpensAt.Equal(time.Date(2026, 12, 3, 5, 0, 0, 0, time.UTC)) {
		t.Errorf("preview opens at %v", preview.OpensAt)
	}
}

func TestChecks(t *testing.T) {
	serveAPI(t)
	checks, err := Checks(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 3 || checks[0].Ready[0] != "apps" || checks[1].HTTP.Expect != "2xx" || checks[2].Job != "apps/cronjob/x" || checks[2].ID != "c" {
		t.Errorf("got %+v", checks)
	}
}

func TestSendResults(t *testing.T) {
	f := serveAPI(t)
	saved, err := SendResults(context.Background(), 2, []check.Outcome{{ID: "a", Passed: true, Got: "ready"}, {ID: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Login != "octo" || saved.Passed {
		t.Errorf("got %+v", saved)
	}
	if want := `{"results":[{"id":"a","passed":true,"got":"ready"},{"id":"b","passed":false}]}`; f.body != want {
		t.Errorf("sent %s, want %s", f.body, want)
	}
}

// Signing in hands the GitHub token to the API once and returns the API's own.
func TestSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodPost || r.URL.Path != "/auth/cli":
			http.NotFound(w, r)
		case r.Header.Get("Authorization") != "Bearer gho_good":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"GitHub doesn't accept this sign-in. Run aocn login again"}`))
		default:
			_, _ = w.Write([]byte(`{"token":"api-token","login":"octo"}`))
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AOCN_API_URL", srv.URL)
	signedOut(t)

	s, err := SignIn(context.Background(), "gho_good")
	if err != nil || s != (Session{Token: "api-token", Login: "octo"}) {
		t.Fatalf("got %+v, %v", s, err)
	}
	if _, err := SignIn(context.Background(), "gho_bad"); err == nil || !strings.Contains(err.Error(), "Run aocn login again") {
		t.Errorf("bad token: %v", err)
	}
}
