package migrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/PouryDev/server-infrastructure/gokit/orm/schema"
)

// DiffOptions describe a model-to-migration comparison.
type DiffOptions struct {
	// URL of a development database server. A temporary "shadow" database is created on it, the existing
	// migrations are applied to that, and the result is compared with the models: so the diff is against
	// exactly what the migration files produce, never against whatever state your dev database drifted into.
	// The role needs CREATE DATABASE (a superuser in the development compose file).
	URL    string
	FS     fs.FS // existing migrations
	Dir    string
	Models []any

	OutDir    string // where Diff writes the new migration file
	Name      string // migration name (a-z, 0-9, _)
	AllowDrop bool   // also drop what the models no longer mention
	// ConcurrentIndexes creates new indexes on existing tables with CREATE INDEX CONCURRENTLY (no write
	// lock) in a second, non-transactional migration file.
	ConcurrentIndexes bool
	Now               time.Time
}

// DiffResult is what Diff found.
type DiffResult struct {
	Plan           *schema.Plan
	Path           string // the written file; "" when there was nothing transactional to write
	ConcurrentPath string // the CREATE INDEX CONCURRENTLY file, when requested and needed
}

// Plan compares the models with the schema the existing migrations produce, without writing anything.
// An empty plan means no drift (use it as a CI check).
func Plan(ctx context.Context, o DiffOptions) (*schema.Plan, error) {
	desired, err := schema.FromModels(o.Models...)
	if err != nil {
		return nil, err
	}
	current, cleanup, err := shadowSchema(ctx, o)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	return schema.Diff(current, desired, schema.Options{AllowDrop: o.AllowDrop, ConcurrentIndexes: o.ConcurrentIndexes}), nil
}

// Diff writes a new goose migration with the changes needed to bring the migrations in line with the models.
func Diff(ctx context.Context, o DiffOptions) (DiffResult, error) {
	name := strings.ToLower(strings.TrimSpace(o.Name))
	if !nameRe.MatchString(name) {
		return DiffResult{}, fmt.Errorf("migration name %q must match %s", name, nameRe)
	}
	plan, err := Plan(ctx, o)
	if err != nil {
		return DiffResult{}, err
	}
	if plan.Empty() {
		return DiffResult{Plan: plan}, nil
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	res := DiffResult{Plan: plan}
	if plan.HasTransactional() {
		path := filepath.Join(o.OutDir, now.UTC().Format("20060102150405")+"_"+name+".sql")
		if err := writeNew(path, plan.Render()); err != nil {
			return DiffResult{}, err
		}
		res.Path = path
	}
	if conc := plan.RenderConcurrent(); conc != "" {
		// One second later, so it sorts (and is applied) after the transactional file.
		path := filepath.Join(o.OutDir, now.Add(time.Second).UTC().Format("20060102150405")+"_"+name+"_indexes.sql")
		if err := writeNew(path, conc); err != nil {
			return DiffResult{}, err
		}
		res.ConcurrentPath = path
	}
	return res, nil
}

func writeNew(path, body string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // source file, not a secret
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("migrate: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// shadowSchema creates a throw-away database, applies the migrations and introspects it.
func shadowSchema(ctx context.Context, o DiffOptions) (*schema.Schema, func(), error) {
	admin, err := pgx.Connect(ctx, o.URL)
	if err != nil {
		return nil, nil, errors.New("migrate: cannot connect to the database server (is DATABASE_URL right and the database up?)")
	}
	defer func() { _ = admin.Close(ctx) }()

	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	shadow := "gokit_shadow_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{shadow}.Sanitize()); err != nil {
		return nil, nil, fmt.Errorf("migrate: cannot create the shadow database (the role needs CREATE DATABASE): %w", err)
	}
	cleanup := func() {
		c, err := pgx.Connect(context.WithoutCancel(ctx), o.URL)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
		_, _ = c.Exec(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+pgx.Identifier{shadow}.Sanitize()+" WITH (FORCE)")
	}

	u, err := url.Parse(o.URL)
	if err != nil {
		cleanup()
		return nil, nil, errors.New("migrate: DATABASE_URL is not a URL")
	}
	u.Path = "/" + shadow
	shadowURL := u.String()

	// A project without migrations yet is fine: the shadow database simply stays empty.
	if _, err := (Runner{URL: shadowURL, FS: o.FS, Dir: o.Dir}).Up(ctx); err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		cleanup()
		return nil, nil, fmt.Errorf("migrate: applying the existing migrations to the shadow database failed: %w", err)
	}
	conn, err := pgx.Connect(ctx, shadowURL)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("migrate: connect to the shadow database: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	current, err := schema.Introspect(ctx, conn)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return current, cleanup, nil
}
