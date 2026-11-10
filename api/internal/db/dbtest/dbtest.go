// Package dbtest gives a store's tests a database of their own.
package dbtest

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/katharinasick/aocn/api/internal/db"
)

// Pool connects to TEST_DATABASE_URL in a fresh schema named after the package, migrated to
// the latest version. go test runs packages in parallel, and two that share tables would
// empty them under each other; one schema each keeps them apart. Skipped without
// TEST_DATABASE_URL.
func Pool(t *testing.T, schema string) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	name := pgx.Identifier{"test_" + schema}.Sanitize()
	_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+name+" CASCADE; CREATE SCHEMA "+name)
	conn.Close(ctx)
	if err != nil {
		t.Fatalf("creating schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	q := u.Query()
	// pgx sends parameters it doesn't know to the server, so every connection starts here.
	q.Set("search_path", "test_"+schema)
	u.RawQuery = q.Encode()

	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
