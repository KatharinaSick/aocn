package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/katharinasick/aocn/api/internal/email"
	"github.com/katharinasick/aocn/api/internal/subscribe"
)

var (
	testKey = []byte("test-signing-key")
	testNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
)

type sentConfirmation struct{ to, confirmURL string }

type fakeMailer struct {
	sent  []sentConfirmation
	added []string
	err   error
}

func (m *fakeMailer) SendSubscribeConfirmation(_ context.Context, to, confirmURL string) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, sentConfirmation{to, confirmURL})
	return nil
}

func (m *fakeMailer) AddSubscriber(_ context.Context, address string) error {
	if m.err != nil {
		return m.err
	}
	m.added = append(m.added, address)
	return nil
}

// fakeStore keeps the same rules as subscribe.Store, in memory. The store's own tests
// check the SQL against a real database.
type fakeStore struct {
	requests      map[string][]time.Time
	confirmations []subscribe.Request
	err           error
}

func (s *fakeStore) RecordRequest(_ context.Context, addressHash []byte, now time.Time, limit int, window time.Duration) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	if s.requests == nil {
		s.requests = map[string][]time.Time{}
	}
	sent := 0
	for _, at := range s.requests[string(addressHash)] {
		if at.After(now.Add(-window)) {
			sent++
		}
	}
	if sent >= limit {
		return false, nil
	}
	s.requests[string(addressHash)] = append(s.requests[string(addressHash)], now)
	return true, nil
}

func (s *fakeStore) RecordConfirmation(_ context.Context, req subscribe.Request, _ time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.confirmations = append(s.confirmations, req)
	return nil
}

func newTestHandler(m *fakeMailer) http.Handler {
	return newTestHandlerWithStore(m, &fakeStore{})
}

func newTestHandlerWithStore(m *fakeMailer, store *fakeStore) http.Handler {
	return NewHandler(nopLogger{}, Subscriptions{
		Mailer:     m,
		Store:      store,
		SigningKey: testKey,
		SiteURL:    "https://aocn.dev",
		Now:        func() time.Time { return testNow },
	}, Doors{}, Auth{}, Profiles{})
}

func post(h http.Handler, path, body, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", ip)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSubscribeSendsASignedConfirmationLink(t *testing.T) {
	m := &fakeMailer{}
	rec := post(newTestHandler(m), "/subscribe", `{"email":"  Player@Example.com "}`, "203.0.113.1")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(m.sent) != 1 {
		t.Fatalf("sent %d confirmations, want 1", len(m.sent))
	}
	if m.sent[0].to != "player@example.com" {
		t.Errorf("sent to %q, want the trimmed, lower-cased address", m.sent[0].to)
	}

	link, err := url.Parse(m.sent[0].confirmURL)
	if err != nil {
		t.Fatalf("parsing link: %v", err)
	}
	if got := link.Scheme + "://" + link.Host + link.Path; got != "https://aocn.dev/subscribe/confirm" {
		t.Errorf("link points to %q", got)
	}
	request, err := subscribe.Verify(testKey, link.Query().Get("token"), testNow)
	if err != nil || request.Email != "player@example.com" || !request.RequestedAt.Equal(testNow) {
		t.Errorf("token verifies to %+v, %v; want the address, requested now", request, err)
	}
	if _, err := subscribe.Verify(testKey, link.Query().Get("token"), testNow.Add(confirmationValidFor)); err == nil {
		t.Errorf("token still verifies after %s", confirmationValidFor)
	}
}

func TestSubscribeIgnoresTheHoneypot(t *testing.T) {
	m := &fakeMailer{}
	rec := post(newTestHandler(m), "/subscribe", `{"email":"player@example.com","website":"spam.example"}`, "203.0.113.1")

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d, so a bot cannot tell it was caught", rec.Code, http.StatusAccepted)
	}
	if len(m.sent) != 0 {
		t.Errorf("sent %d confirmations, want none", len(m.sent))
	}
}

func TestSubscribeRejectsInvalidInput(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":       `email=player@example.com`,
		"no email":       `{}`,
		"not an email":   `{"email":"player"}`,
		"too long":       fmt.Sprintf(`{"email":"%s@example.com"}`, strings.Repeat("a", 250)),
		"body too large": `{"email":"player@example.com","website":"` + strings.Repeat("a", maxRequestBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := &fakeMailer{}
			rec := post(newTestHandler(m), "/subscribe", body, "203.0.113.1")

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
			if len(m.sent) != 0 {
				t.Errorf("sent %d confirmations, want none", len(m.sent))
			}
		})
	}
}

// The limit that protects inboxes: however many times an address is entered, from
// however many places, it gets only a few emails a day. The answer stays the same, so
// it does not reveal that the address was tried before.
func TestSubscribeLimitsEmailsPerAddress(t *testing.T) {
	m := &fakeMailer{}
	h := newTestHandler(m)

	for i := range addressLimit + 2 {
		rec := post(h, "/subscribe", `{"email":"victim@example.com"}`, fmt.Sprintf("203.0.113.%d", i))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusAccepted)
		}
	}
	if len(m.sent) != addressLimit {
		t.Errorf("sent %d confirmations, want %d", len(m.sent), addressLimit)
	}
}

func TestSubscribeLimitsRequestsPerIP(t *testing.T) {
	m := &fakeMailer{}
	h := newTestHandler(m)

	for i := range ipLimit {
		rec := post(h, "/subscribe", fmt.Sprintf(`{"email":"player%d@example.com"}`, i), "203.0.113.1")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusAccepted)
		}
	}

	rec := post(h, "/subscribe", `{"email":"one-more@example.com"}`, "203.0.113.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if len(m.sent) != ipLimit {
		t.Errorf("sent %d confirmations, want %d", len(m.sent), ipLimit)
	}
}

// A valid token can be replayed until it expires, and every replay costs a database row
// and Resend calls, so confirming is limited per IP like subscribing.
func TestConfirmLimitsRequestsPerIP(t *testing.T) {
	m := &fakeMailer{}
	h := newTestHandler(m)
	body := `{"token":"` + signedToken("player@example.com", testNow.Add(time.Hour)) + `"}`

	for i := range ipLimit {
		if rec := post(h, "/subscribe/confirm", body, "203.0.113.1"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusNoContent)
		}
	}

	rec := post(h, "/subscribe/confirm", body, "203.0.113.1")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if len(m.added) != ipLimit {
		t.Errorf("added %d times, want %d", len(m.added), ipLimit)
	}
}

func TestMailerErrors(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"quota reached": {email.ErrQuotaExceeded, http.StatusServiceUnavailable},
		"other failure": {errors.New("connection refused"), http.StatusBadGateway},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newTestHandler(&fakeMailer{err: tc.err})

			if rec := post(h, "/subscribe", `{"email":"player@example.com"}`, "203.0.113.1"); rec.Code != tc.want {
				t.Errorf("subscribe: status = %d, want %d", rec.Code, tc.want)
			}

			token := signedToken("player@example.com", testNow.Add(time.Hour))
			if rec := post(h, "/subscribe/confirm", `{"token":"`+token+`"}`, "203.0.113.1"); rec.Code != tc.want {
				t.Errorf("confirm: status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestConfirmAddsTheSignedAddressAndKeepsProof(t *testing.T) {
	m := &fakeMailer{}
	store := &fakeStore{}
	token := signedToken("player@example.com", testNow.Add(time.Hour))

	rec := post(newTestHandlerWithStore(m, store), "/subscribe/confirm", `{"token":"`+token+`"}`, "203.0.113.1")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if len(m.added) != 1 || m.added[0] != "player@example.com" {
		t.Errorf("added %v, want [player@example.com]", m.added)
	}
	if len(store.confirmations) != 1 || store.confirmations[0].Email != "player@example.com" {
		t.Errorf("recorded %v, want one confirmation for player@example.com", store.confirmations)
	}
}

// No subscriber without proof: if the proof cannot be written, the address is not added.
func TestConfirmAddsNobodyWhenTheProofCannotBeKept(t *testing.T) {
	m := &fakeMailer{}
	token := signedToken("player@example.com", testNow.Add(time.Hour))

	rec := post(newTestHandlerWithStore(m, &fakeStore{err: errors.New("database down")}), "/subscribe/confirm", `{"token":"`+token+`"}`, "203.0.113.1")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if len(m.added) != 0 {
		t.Errorf("added %v, want nothing", m.added)
	}
}

func TestSubscribeSendsNothingWhenTheLimitCannotBeChecked(t *testing.T) {
	m := &fakeMailer{}
	rec := post(newTestHandlerWithStore(m, &fakeStore{err: errors.New("database down")}), "/subscribe", `{"email":"player@example.com"}`, "203.0.113.1")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if len(m.sent) != 0 {
		t.Errorf("sent %d confirmations, want none", len(m.sent))
	}
}

func signedToken(email string, expires time.Time) string {
	return subscribe.Sign(testKey, subscribe.Request{Email: email, RequestedAt: testNow.Add(-time.Hour)}, expires)
}

func TestConfirmRejectsBadTokens(t *testing.T) {
	valid := signedToken("player@example.com", testNow.Add(time.Hour))

	cases := map[string]struct {
		body string
		want int
	}{
		"expired":   {`{"token":"` + signedToken("player@example.com", testNow) + `"}`, http.StatusGone},
		"wrong key": {`{"token":"` + subscribe.Sign([]byte("other"), subscribe.Request{Email: "player@example.com"}, testNow.Add(time.Hour)) + `"}`, http.StatusBadRequest},
		"tampered":  {`{"token":"x` + valid + `"}`, http.StatusBadRequest},
		"no token":  {`{}`, http.StatusBadRequest},
		"not JSON":  {`token=` + valid, http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := &fakeMailer{}
			rec := post(newTestHandler(m), "/subscribe/confirm", tc.body, "203.0.113.1")

			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
			if len(m.added) != 0 {
				t.Errorf("added %v, want nothing", m.added)
			}
		})
	}
}

// Confirming must not happen on GET: mail scanners open every link in an email.
func TestSubscribeRoutesArePOSTOnly(t *testing.T) {
	for _, path := range []string{"/subscribe", "/subscribe/confirm"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestHandler(&fakeMailer{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}
