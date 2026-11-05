package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/katharinasick/aocn/api/internal/app"
	"github.com/katharinasick/aocn/api/internal/email"
	"github.com/katharinasick/aocn/api/internal/subscribe"
)

// The signup is double opt-in: an address is only added once someone who can read
// its inbox confirms. POST /subscribe sends the confirmation email; the link in it
// opens a page on the website, whose button calls POST /subscribe/confirm.
//
// Confirming is a POST behind a button rather than the link itself, because mail
// scanners open every link in an email. A link that subscribed on GET would subscribe
// people whose mail server merely looked at it.

const (
	confirmationValidFor = 24 * time.Hour
	maxRequestBytes      = 4 << 10

	// Generous per IP, because a conference wifi puts a whole room behind one address.
	ipLimit  = 20
	ipWindow = time.Hour

	// Strict per address, because this is what stops the form being used to send
	// confirmation emails to someone who did not ask for them. Counted in the
	// database, so restarts and parallel instances do not reset it.
	addressLimit  = 3
	addressWindow = 24 * time.Hour
)

// Mailer sends the confirmation email and keeps the subscriber list.
type Mailer interface {
	SendSubscribeConfirmation(ctx context.Context, to, confirmURL string) error
	AddSubscriber(ctx context.Context, address string) error
}

// Store counts confirmation emails per address and keeps the proof of each confirmation.
type Store interface {
	RecordRequest(ctx context.Context, addressHash []byte, now time.Time, limit int, window time.Duration) (bool, error)
	RecordConfirmation(ctx context.Context, req subscribe.Request, confirmedAt time.Time) error
}

// Subscriptions is what the signup endpoints need.
type Subscriptions struct {
	Mailer     Mailer
	Store      Store
	SigningKey []byte
	// SiteURL is where the link in the confirmation email points, without a trailing
	// slash: https://aocn.dev, or the dev server locally.
	SiteURL string
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
}

func (s Subscriptions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var validate = validator.New(validator.WithRequiredStructEnabled())

type subscribeRequest struct {
	Email string `json:"email" validate:"required,email,max=254"`
	// Website is a honeypot: the form hides it from people, and bots fill it in.
	Website string `json:"website"`
}

// handleSubscribe answers 202 whenever the request was well-formed and not refused
// outright, whether or not an email went out. Answering differently for an address
// that hit its limit would tell anyone who asks which addresses were tried.
//
// The per-IP limit is applied around this handler, in addRoutes.
func handleSubscribe(log app.Logger, s Subscriptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req subscribeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		if req.Website != "" {
			log.Info("Subscribe honeypot filled in, ignoring")
			w.WriteHeader(http.StatusAccepted)
			return
		}

		req.Email = strings.ToLower(strings.TrimSpace(req.Email))
		if err := validate.Struct(req); err != nil {
			http.Error(w, "not an email address", http.StatusBadRequest)
			return
		}

		now := s.now()
		allowed, err := s.Store.RecordRequest(r.Context(), subscribe.AddressHash(s.SigningKey, req.Email), now, addressLimit, addressWindow)
		if err != nil {
			log.Error("Recording subscribe request failed", "error", err)
			http.Error(w, "something went wrong, try again later", http.StatusInternalServerError)
			return
		}
		if !allowed {
			log.Info("Subscribe limit reached for an address, not sending")
			w.WriteHeader(http.StatusAccepted)
			return
		}

		token := subscribe.Sign(s.SigningKey, subscribe.Request{Email: req.Email, RequestedAt: now}, now.Add(confirmationValidFor))
		confirmURL := s.SiteURL + "/subscribe/confirm?token=" + url.QueryEscape(token)

		if err := s.Mailer.SendSubscribeConfirmation(r.Context(), req.Email, confirmURL); err != nil {
			writeMailerError(log, w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	})
}

type confirmRequest struct {
	Token string `json:"token" validate:"required"`
}

func handleConfirmSubscription(log app.Logger, s Subscriptions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req confirmRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		now := s.now()
		request, err := subscribe.Verify(s.SigningKey, req.Token, now)
		switch {
		case errors.Is(err, subscribe.ErrExpiredToken):
			// Distinct from invalid, so the page can say "sign up again" rather than
			// "this link is broken".
			http.Error(w, "confirmation link expired", http.StatusGone)
			return
		case err != nil:
			http.Error(w, "invalid confirmation link", http.StatusBadRequest)
			return
		}

		// The proof is written before the contact exists, so there is never a
		// subscriber without one. Confirming again just adds another record.
		if err := s.Store.RecordConfirmation(r.Context(), request, now); err != nil {
			log.Error("Recording subscribe confirmation failed", "error", err)
			http.Error(w, "something went wrong, try again later", http.StatusInternalServerError)
			return
		}

		if err := s.Mailer.AddSubscriber(r.Context(), request.Email); err != nil {
			writeMailerError(log, w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// writeMailerError logs what Resend said and keeps it out of the response, except for
// the one case the visitor can act on.
func writeMailerError(log app.Logger, w http.ResponseWriter, err error) {
	log.Error("Mail service call failed", "error", err)
	if errors.Is(err, email.ErrQuotaExceeded) {
		http.Error(w, "too many emails today, try again tomorrow", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "could not reach the mail service", http.StatusBadGateway)
}
