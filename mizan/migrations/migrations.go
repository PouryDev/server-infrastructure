// Package migrations embeds mizan's schema. Apply it with mizan.Migrate (which keeps its own goose
// version table so it never collides with the host project's migrations).
package migrations

import (
	"context"
	"embed"

	"github.com/PouryDev/server-infrastructure/gokit/migrate"
)

// FS holds the goose SQL files.
//
//go:embed *.sql
var FS embed.FS

// Table is the goose version table. It is separate from the default one so mizan's migrations and the
// host project's never see each other's versions.
const Table = "mizan_schema_migrations"

// Up applies every pending mizan migration to the database at url. Run it as a one-shot step, not on
// every replica boot (gokit's runner: simple protocol, no session locks, safe behind PgBouncer).
func Up(ctx context.Context, url string) ([]string, error) {
	return migrate.Runner{URL: url, FS: FS, Table: Table}.Up(ctx)
}
