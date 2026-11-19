package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/katharinasick/aocn/api/internal/app"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewHandler builds the API's http.Handler.
//
// There is no CORS layer: the website reaches the API through Netlify's /api/*
// rewrite and Astro's dev-server proxy, so the browser only ever talks to its own
// origin. Adding a direct cross-origin caller would mean revisiting that.
func NewHandler(log app.Logger, subscriptions Subscriptions, doors Doors, auth Auth, profiles Profiles, boards Leaderboards) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, log, subscriptions, doors, auth, profiles, boards)

	// otelhttp names the span after the matched route and continues an incoming
	// traceparent, so requests arriving from the website or the CLI stay on one trace.
	//
	// /health is traced like everything else for now, because it is the only endpoint
	// there is to observe. Once real traffic exists it should be dropped with an
	// otelhttp.WithFilter: Cloud Run probes it continuously, and those spans say
	// nothing while crowding out the ones that do.
	handler := otelhttp.NewHandler(mux, "", otelhttp.WithSpanNameFormatter(spanName))

	return requestLogger(log, handler)
}

func addRoutes(mux *http.ServeMux, log app.Logger, subscriptions Subscriptions, doors Doors, auth Auth, profiles Profiles, boards Leaderboards) {
	mux.Handle("GET /health", handleHealth())
	mux.Handle("POST /subscribe", limitByIP(ipLimit, ipWindow, tooManySignups)(handleSubscribe(log, subscriptions)))
	// Confirming is limited too: a valid token can be replayed until it expires, and
	// every replay costs a database row and two Resend calls.
	mux.Handle("POST /subscribe/confirm", limitByIP(ipLimit, ipWindow, tooManySignups)(handleConfirmSubscription(log, subscriptions)))
	// Neither door endpoint is limited. The page is asked for by the website, from Netlify's
	// few addresses. Bundles are small, and many players may share an address (a workshop,
	// Codespaces), all opening the door at 06:00; a limit by IP would turn them away first.
	mux.Handle("GET /doors/{door}", handleDoorPage(log, doors))
	mux.Handle("GET /doors/{door}/bundle", handleDoorBundle(log, doors))
	mux.Handle("GET /doors/{door}/checks", handleDoorChecks(log, doors))
	// Both need a signed-in player, and each opened hint is one row, at most three per door.
	mux.Handle("GET /doors/{door}/hints", handleDoorHints(log, doors))
	mux.Handle("POST /doors/{door}/hints/next", handleOpenHint(log, doors))
	// Not limited by IP, for the same reason: limited per player inside (verifyLimit), since
	// each run is one row and leaderboards read them.
	mux.Handle("POST /doors/{door}/verify", handleVerify(log, doors))
	// Finishing a login asks GitHub, whatever the request carries: a made-up code or token
	// still costs a call, and enough bad ones from our address could get it blocked there,
	// turning everyone's login away. So both are limited per IP, generously, since a workshop
	// logs in together from one address, and all together, since a caller going around
	// Netlify can make up any IP. Starting a login and logging out ask nobody.
	githubCalls := limitAll(githubCallLimit, githubCallWindow, tooManyLogins)
	byIP := limitByIP(loginIPLimit, loginIPWindow, tooManyLogins)
	mux.Handle("GET /auth/github/login", handleLogin(auth))
	mux.Handle("GET /auth/github/callback", byIP(githubCalls(handleCallback(log, auth))))
	mux.Handle("POST /auth/logout", handleLogout(auth))
	mux.Handle("POST /auth/cli", byIP(githubCalls(handleCLILogin(log, auth))))
	mux.Handle("GET /me", handleMe(log, auth))
	// Asked by the website's function, from Netlify's few addresses, so not limited by IP.
	mux.Handle("GET /users/{login}", handleProfile(log, profiles))
	mux.Handle("PUT /me/profile", handleSetProfileVisibility(log, profiles))

	// Leaderboards, from the website. Creating and joining are limited per IP; a team or a
	// workshop joins together from one address, so joining is generous. Everything else
	// needs the owner or a member, who can't do much harm to anyone but their own board.
	creates := limitByIP(createLimit, createWindow, tooManyLeaderboardRequests)
	joins := limitByIP(joinLimit, joinWindow, tooManyLeaderboardRequests)
	views := limitByIP(viewLimit, viewWindow, tooManyLeaderboardRequests)
	mux.Handle("GET /me/leaderboards", views(handleMyLeaderboards(log, boards)))
	mux.Handle("POST /me/leaderboards", creates(handleCreateLeaderboard(log, boards)))
	mux.Handle("GET /leaderboards/{id}", views(handleLeaderboard(log, boards)))
	mux.Handle("PATCH /leaderboards/{id}", handleUpdateLeaderboard(log, boards))
	mux.Handle("DELETE /leaderboards/{id}", handleDeleteLeaderboard(log, boards))
	mux.Handle("POST /leaderboards/{id}/invite", handleNewInvite(log, boards))
	mux.Handle("POST /leaderboards/{id}/join", joins(handleJoinOpen(log, boards)))
	mux.Handle("DELETE /leaderboards/{id}/members/{user}", handleRemoveMember(log, boards))
	mux.Handle("DELETE /leaderboards/{id}/removed/{user}", handleLetBackIn(log, boards))
	mux.Handle("GET /join/{code}", joins(handleJoinPreview(log, boards)))
	mux.Handle("POST /join/{code}", joins(handleJoinWithCode(log, boards)))
}

// spanName labels spans by the matched route pattern (e.g. "GET /health"). When no
// route matched there is no low-cardinality name available, so it falls back to the
// bare method as the HTTP semantic conventions require — using the raw path would let
// anything scanning for URLs create unbounded distinct span names.
func spanName(_ string, r *http.Request) string {
	if pattern := r.Pattern; pattern != "" {
		return pattern
	}
	return r.Method
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	errMsg strings.Builder
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status >= 400 {
		r.errMsg.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

func requestLogger(log app.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		args := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		if rec.status >= 400 {
			args = append(args, "error", strings.TrimSpace(rec.errMsg.String()))
		}

		switch {
		case rec.status >= 500:
			log.Error("request", args...)
		case rec.status >= 400:
			log.Warn("request", args...)
		default:
			log.Info("request", args...)
		}
	})
}
