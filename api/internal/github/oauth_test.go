package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// fakeOAuth swaps the code "good" for the token "token", and answers like GitHub otherwise.
func fakeOAuth(t *testing.T) *OAuth {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/login/oauth/access_token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		r.ParseForm()
		switch {
		case r.Form.Get("client_id") != "id" || r.Form.Get("client_secret") != "secret":
			w.Write([]byte(`{"error": "incorrect_client_credentials"}`))
		case r.Form.Get("code") != "good":
			w.Write([]byte(`{"error": "bad_verification_code"}`))
		default:
			w.Write([]byte(`{"access_token": "token", "token_type": "bearer", "scope": ""}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &OAuth{ClientID: "id", ClientSecret: "secret", BaseURL: srv.URL, Client: srv.Client()}
}

func TestExchange(t *testing.T) {
	o := fakeOAuth(t)
	token, err := o.Exchange(context.Background(), "good", "https://aocn.dev/api/auth/github/callback")
	if err != nil || token != "token" {
		t.Fatalf("got %q, %v", token, err)
	}
}

func TestExchangeBadCode(t *testing.T) {
	o := fakeOAuth(t)
	if _, err := o.Exchange(context.Background(), "used", ""); !errors.Is(err, ErrBadCode) {
		t.Fatalf("got %v", err)
	}
}

func TestExchangeWrongSecret(t *testing.T) {
	o := fakeOAuth(t)
	o.ClientSecret = "wrong"
	_, err := o.Exchange(context.Background(), "good", "")
	if err == nil || errors.Is(err, ErrBadCode) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	o := &OAuth{ClientID: "id", BaseURL: "https://github.com"}
	u, err := url.Parse(o.AuthorizeURL("https://aocn.dev/api/auth/github/callback", "s t"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "github.com" || u.Path != "/login/oauth/authorize" ||
		q.Get("client_id") != "id" || q.Get("state") != "s t" ||
		q.Get("redirect_uri") != "https://aocn.dev/api/auth/github/callback" || q.Has("scope") {
		t.Fatalf("got %s", u)
	}
}
