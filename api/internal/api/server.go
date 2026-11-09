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
func NewHandler(log app.Logger, subscriptions Subscriptions, doors Doors) http.Handler {
	mux := http.NewServeMux()
	addRoutes(mux, log, subscriptions, doors)

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

func addRoutes(mux *http.ServeMux, log app.Logger, subscriptions Subscriptions, doors Doors) {
	mux.Handle("GET /health", handleHealth())
	mux.Handle("POST /subscribe", limitByIP(ipLimit, ipWindow)(handleSubscribe(log, subscriptions)))
	// Confirming is limited too: a valid token can be replayed until it expires, and
	// every replay costs a database row and two Resend calls.
	mux.Handle("POST /subscribe/confirm", limitByIP(ipLimit, ipWindow)(handleConfirmSubscription(log, subscriptions)))
	mux.Handle("GET /doors/{door}", handleDoorPage(log, doors))
	mux.Handle("GET /doors/{door}/bundle", handleDoorBundle(log, doors))
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
