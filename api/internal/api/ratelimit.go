package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/httprate"
)

// limitByIP allows each client IP limit requests per window, counted in memory with
// a sliding window, and answers with tooMany past it. Every Cloud Run instance counts on
// its own and a restart forgets, which is fine for what this is for: slowing down careless
// scripts. The limit that protects inboxes is the per-address one, which is kept in the
// database.
func limitByIP(limit int, window time.Duration, tooMany http.HandlerFunc) func(http.Handler) http.Handler {
	return httprate.LimitBy(limit, window,
		func(r *http.Request) (string, error) { return clientIP(r), nil },
		httprate.WithLimitHandler(tooMany),
	)
}

// limitAll allows limit requests per window from everyone together, per instance. Unlike
// limitByIP it can't be dodged with a made-up X-Forwarded-For, so it's what bounds work
// that costs something outside the API. One limiter shared by several routes counts them
// together.
func limitAll(limit int, window time.Duration, tooMany http.HandlerFunc) func(http.Handler) http.Handler {
	return httprate.LimitBy(limit, window,
		func(*http.Request) (string, error) { return "all", nil },
		httprate.WithLimitHandler(tooMany),
	)
}

// tooManySignups answers a client past the signup limit.
func tooManySignups(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "too many signups from here, try again later", http.StatusTooManyRequests)
}

// clientIP is the address the request came from, as the proxies in front report it:
// the first X-Forwarded-For entry, which Netlify sets to the visitor.
//
// The service is publicly reachable, so a caller going around Netlify can send any
// X-Forwarded-For it likes. That is why httprate deprecated keying on it by default,
// and why only the generous per-IP limit uses it.
func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		return strings.TrimSpace(first)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
