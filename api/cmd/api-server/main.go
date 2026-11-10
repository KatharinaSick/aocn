package main

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/katharinasick/aocn/api/internal/api"
	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/db"
	"github.com/katharinasick/aocn/api/internal/doors"
	"github.com/katharinasick/aocn/api/internal/email"
	"github.com/katharinasick/aocn/api/internal/github"
	"github.com/katharinasick/aocn/api/internal/results"
	"github.com/katharinasick/aocn/api/internal/schedule"
	"github.com/katharinasick/aocn/api/internal/subscribe"
)

// shutdownTimeout bounds how long in-flight requests and the final span export get
// after SIGTERM. Cloud Run allows 10s by default before killing the container.
const shutdownTimeout = 8 * time.Second

func main() {
	log := app.NewLogger()

	// Cloud Run signals shutdown with SIGTERM. Catching it is what allows buffered
	// spans to be flushed instead of lost.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := app.ParseConfig()
	if err != nil {
		log.Error("Failed to parse config. Terminating.", "error", err)
		os.Exit(1)
	}

	// A missing or broken schedule.yaml stops the API at startup, not when the first
	// door is asked for.
	sched, err := schedule.Load(cfg.Schedule.Path)
	if err != nil {
		log.Error("Failed to load the schedule. Terminating.", "error", err)
		os.Exit(1)
	}

	// A previewer for a door that isn't in the schedule is a typo; it stops the API like one.
	for _, door := range cfg.PreviewerAccess.Doors() {
		if _, err := sched.OpensAt(door); err != nil {
			log.Error("A previewer gets a door that isn't in the schedule. Terminating.", "door", door)
			os.Exit(1)
		}
	}

	shutdownOTel, err := app.SetupOTel(ctx, log)
	if err != nil {
		log.Error("Failed to set up OpenTelemetry. Terminating.", "error", err)
		os.Exit(1)
	}

	pool, err := db.Connect(ctx, cfg.Database.URL)
	if err != nil {
		log.Error("Failed to connect to the database. Terminating.", "error", err)
		flush(log, shutdownOTel)
		os.Exit(1)
	}
	defer pool.Close()

	doorStorage, err := doorStorage(ctx, cfg)
	if err != nil {
		log.Error("Failed to connect to the doors' storage. Terminating.", "error", err)
		flush(log, shutdownOTel)
		os.Exit(1)
	}
	doorsAPI := api.Doors{
		Schedule:   sched,
		Storage:    doorStorage,
		Pages:      &doors.Pages{Storage: doorStorage},
		Checks:     &doors.Checks{Storage: doorStorage},
		Users:      github.NewUsers(),
		Previewers: cfg.PreviewerAccess,
		Results:    results.NewStore(pool),
	}
	log.Info("Previewers loaded", "count", len(cfg.PreviewerAccess))

	// WriteTimeout is also the longest a bundle download may take: past it the connection
	// drops and aocn open reports a broken download. Fine while bundles are small; bigger
	// bundles or slower players mean raising it, or handing out signed bucket URLs instead.
	httpServer := &http.Server{
		Addr:         net.JoinHostPort("", cfg.Server.Port),
		Handler:      api.NewHandler(log, subscriptions(log, cfg, subscribe.NewStore(pool)), doorsAPI),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("Starting server", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Error("Server failed. Terminating.", "error", err)
		flush(log, shutdownOTel)
		os.Exit(1)
	case <-ctx.Done():
		log.Info("Shutdown signal received")
	}

	// Stop accepting connections and let in-flight requests finish before the
	// spans they produced are exported.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("Server did not shut down cleanly", "error", err)
	}
	flush(log, shutdownOTel)
	log.Info("Shutdown complete")
}

func flush(log app.Logger, shutdownOTel func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := shutdownOTel(ctx); err != nil {
		log.Error("Failed to flush telemetry", "error", err)
	}
}

// doorStorage is the bucket on Cloud Run and a local folder in dev; ParseConfig has made
// sure exactly one is set.
func doorStorage(ctx context.Context, cfg *app.Config) (doors.Storage, error) {
	if cfg.Doors.Dir != "" {
		return doors.Dir(cfg.Doors.Dir), nil
	}
	return doors.NewBucket(ctx, cfg.Doors.Bucket)
}

// subscriptions wires the signup to Resend. In dev mode, missing secrets are replaced
// by stand-ins that let the flow be clicked through; ParseConfig has already refused
// missing secrets everywhere else.
func subscriptions(log app.Logger, cfg *app.Config, store api.Store) api.Subscriptions {
	s := api.Subscriptions{Store: store, SiteURL: cfg.Site.URL, SigningKey: []byte(cfg.Subscribe.SigningKey)}

	if cfg.Resend.APIKey != "" {
		s.Mailer = email.NewClient(cfg.Resend.APIKey, cfg.Resend.FromAddress, cfg.Resend.SegmentID)
	} else if cfg.Dev {
		log.Warn("Dev mode without a Resend API key, logging emails instead of sending them")
		s.Mailer = email.NewLogClient(log)
	}

	if len(s.SigningKey) == 0 && cfg.Dev {
		log.Warn("Dev mode without a signing key, using a random one: confirmation links stop working on restart")
		s.SigningKey = make([]byte, 32)
		rand.Read(s.SigningKey) // never returns an error
	}

	return s
}
