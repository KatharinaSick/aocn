package subscribe

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/db"
)

// testStore connects to TEST_DATABASE_URL, migrates it and empties the tables. The
// tests are skipped without it; CI runs them against a Postgres service, and locally
// `devbox services up` provides one.
func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	pool, err := db.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(context.Background(), "TRUNCATE subscribe_requests, subscribe_confirmations"); err != nil {
		t.Fatalf("emptying tables: %v", err)
	}
	return NewStore(pool), pool
}

func TestRecordRequestAllowsUpToTheLimitPerWindow(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	hash := AddressHash(key, addr)
	window := 24 * time.Hour

	for i := range 3 {
		allowed, err := store.RecordRequest(ctx, hash, now.Add(time.Duration(i)*time.Minute), 3, window)
		if err != nil || !allowed {
			t.Fatalf("request %d: allowed = %v, err = %v", i+1, allowed, err)
		}
	}

	if allowed, err := store.RecordRequest(ctx, hash, now.Add(time.Hour), 3, window); err != nil || allowed {
		t.Errorf("fourth request in the window: allowed = %v, err = %v", allowed, err)
	}

	other := AddressHash(key, "other@example.com")
	if allowed, err := store.RecordRequest(ctx, other, now.Add(time.Hour), 3, window); err != nil || !allowed {
		t.Errorf("another address: allowed = %v, err = %v", allowed, err)
	}

	// The first request leaves the window a day after it was made.
	if allowed, err := store.RecordRequest(ctx, hash, now.Add(window+time.Second), 3, window); err != nil || !allowed {
		t.Errorf("after the first request left the window: allowed = %v, err = %v", allowed, err)
	}
}

func TestRecordRequestForgetsRequestsOutsideTheWindow(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	store.RecordRequest(ctx, AddressHash(key, addr), now, 3, time.Hour)
	store.RecordRequest(ctx, AddressHash(key, "other@example.com"), now.Add(2*time.Hour), 3, time.Hour)

	var rows int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM subscribe_requests").Scan(&rows)
	if rows != 1 {
		t.Errorf("rows = %d, want 1: the request outside the window should be gone", rows)
	}
}

func TestRecordRequestStoresNoAddress(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	store.RecordRequest(ctx, AddressHash(key, addr), now, 3, time.Hour)

	var hash []byte
	pool.QueryRow(ctx, "SELECT address_hash FROM subscribe_requests").Scan(&hash)
	if string(hash) != string(AddressHash(key, addr)) {
		t.Errorf("stored %x, want the address hash", hash)
	}
}

// Requests for one address arriving together must not all see room for one more.
func TestRecordRequestHoldsTheLimitUnderConcurrency(t *testing.T) {
	store, _ := testStore(t)
	hash := AddressHash(key, addr)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for range 20 {
		wg.Go(func() {
			ok, err := store.RecordRequest(context.Background(), hash, now, 3, time.Hour)
			if err != nil {
				t.Errorf("RecordRequest: %v", err)
			}
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if allowed != 3 {
		t.Errorf("allowed %d of 20 concurrent requests, want 3", allowed)
	}
}

func TestRecordConfirmationKeepsEveryConfirmation(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()

	for _, confirmedAt := range []time.Time{now, now.Add(time.Hour)} {
		if err := store.RecordConfirmation(ctx, req, confirmedAt); err != nil {
			t.Fatalf("RecordConfirmation: %v", err)
		}
	}

	rows, err := pool.Query(ctx, "SELECT email, requested_at, confirmed_at FROM subscribe_confirmations ORDER BY confirmed_at")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var got []time.Time
	for rows.Next() {
		var (
			email                    string
			requestedAt, confirmedAt time.Time
		)
		if err := rows.Scan(&email, &requestedAt, &confirmedAt); err != nil {
			t.Fatal(err)
		}
		if email != addr || !requestedAt.Equal(req.RequestedAt) {
			t.Errorf("row = %s %v, want %s %v", email, requestedAt, addr, req.RequestedAt)
		}
		got = append(got, confirmedAt)
	}
	if len(got) != 2 || !got[0].Equal(now) || !got[1].Equal(now.Add(time.Hour)) {
		t.Errorf("confirmed at %v, want both confirmations", got)
	}
}
