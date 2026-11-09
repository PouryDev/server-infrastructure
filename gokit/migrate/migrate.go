// Package migrate applies SQL migrations with goose from an embedded filesystem.
//
// It is built for the shared PostgreSQL setup: no session-level advisory locks (unsafe behind
// PgBouncer's transaction pooling) and the simple query protocol, so a migration file may contain
// several statements. Run it as a separate one-shot step (a "migrate" command or container), not on
// every replica boot, so two instances never migrate at once.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Runner applies migrations found in FS (under Dir, if set) to the database at URL.
type Runner struct {
	URL string
	FS  fs.FS
	Dir string // sub-directory of FS holding the .sql files; "" = the root
}

// Status describes one migration.
type Status struct {
	Version   int64
	Name      string
	Applied   bool
	AppliedAt time.Time
}

// Up applies all pending migrations.
func (r Runner) Up(ctx context.Context) ([]string, error) {
	var applied []string
	err := r.with(func(p *goose.Provider) error {
		res, err := p.Up(ctx)
		for _, m := range res {
			applied = append(applied, m.Source.Path)
		}
		return err
	})
	return applied, err
}

// Down rolls back the most recent migration.
func (r Runner) Down(ctx context.Context) (string, error) {
	var name string
	err := r.with(func(p *goose.Provider) error {
		res, err := p.Down(ctx)
		if res != nil {
			name = res.Source.Path
		}
		return err
	})
	return name, err
}

// Status lists every known migration and whether it has been applied.
func (r Runner) Status(ctx context.Context) ([]Status, error) {
	var out []Status
	err := r.with(func(p *goose.Provider) error {
		list, err := p.Status(ctx)
		for _, s := range list {
			out = append(out, Status{
				Version: s.Source.Version, Name: filepath.Base(s.Source.Path),
				Applied: s.State == goose.StateApplied, AppliedAt: s.AppliedAt,
			})
		}
		return err
	})
	return out, err
}

func (r Runner) with(fn func(*goose.Provider) error) error {
	cc, err := pgx.ParseConfig(r.URL)
	if err != nil {
		return errors.New("migrate: DATABASE_URL is not a valid connection string")
	}
	cc.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*cc)
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1) // migrations are sequential; one connection avoids surprises

	fsys := r.FS
	if r.Dir != "" {
		if fsys, err = fs.Sub(r.FS, r.Dir); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	p, err := newProvider(db, fsys)
	if err != nil {
		return err
	}
	return fn(p)
}

func newProvider(db *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	// No session locker on purpose, see the package comment.
	p, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return p, nil
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,60}$`)

const template = `-- +goose Up
-- +goose StatementBegin

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- +goose StatementEnd
`

// Create writes an empty, timestamp-named migration file into dir and returns its path.
func Create(dir, name string, now time.Time) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("migration name %q must match %s", name, nameRe)
	}
	path := filepath.Join(dir, now.UTC().Format("20060102150405")+"_"+name+".sql")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // source file, not a secret
	if err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	if _, err := f.WriteString(template); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("migrate: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	return path, nil
}
