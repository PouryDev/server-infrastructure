// Package testdb gives integration tests a private, fully migrated PostgreSQL schema.
// Tests are skipped when MIZAN_TEST_DATABASE_URL is not set (use `make mizan-integration`).
package testdb

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/migrations"
)

// New returns a connection pool bound to a fresh schema that is dropped when the test ends.
func New(t testing.TB) *postgres.DB {
	t.Helper()
	base := lookup()
	if base == "" {
		t.Skip("MIZAN_TEST_DATABASE_URL not set; run `make mizan-integration`")
	}
	ctx := context.Background()
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(ctx)
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if _, err := migrations.Up(ctx, u.String()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := postgres.Connect(ctx, postgres.Config{URL: u.String(), MaxConns: 8})
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}
