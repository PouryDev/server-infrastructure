// Package mizanmod is the adapter to the financial module: it migrates mizan's schema, builds the module and
// mounts its HTTP API behind the host's authentication. The rest of the application only sees this package.
package mizanmod

import (
	"context"
	"log/slog"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/api"
)

// Prefix is where mizan's routes live. The host's gateway also answers them under /accounting.
const Prefix = "/v1"

// FinanceRoles may use the accounting API (mizan's own default, spelled out here).
var FinanceRoles = []string{"finance", "admin"}

// FiscalYear is what the sign-in page shows in its fiscal-year select.
type FiscalYear struct {
	ID     int64
	Name   string
	Status string
}

// Module is a running mizan.
type Module struct{ m *mizan.Mizan }

// Migrate applies mizan's schema (its own goose table, separate from the host's).
func Migrate(ctx context.Context, databaseURL string) ([]string, error) {
	return mizan.Migrate(ctx, databaseURL)
}

// New wires the module with the full Iranian chart of accounts. It is idempotent.
func New(ctx context.Context, db *kitpg.DB, log *slog.Logger) (*Module, error) {
	m, err := mizan.New(ctx, db, mizan.Config{IranianCodingFull: true, Logger: log})
	if err != nil {
		return nil, err
	}
	return &Module{m: m}, nil
}

// Mount registers mizan's routes; authn verifies the access token.
func (mo *Module) Mount(r *httpx.Router, authn httpx.Middleware) {
	api.Mount(r, mo.m, api.Options{Prefix: Prefix, Authenticate: authn, FinanceRoles: FinanceRoles})
}

// FiscalYears lists the books' fiscal years.
func (mo *Module) FiscalYears(ctx context.Context) ([]FiscalYear, error) {
	fys, err := mo.m.Ledger.FiscalYears(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FiscalYear, len(fys))
	for i, f := range fys {
		out[i] = FiscalYear{ID: f.ID, Name: f.Name, Status: string(f.Status)}
	}
	return out, nil
}
