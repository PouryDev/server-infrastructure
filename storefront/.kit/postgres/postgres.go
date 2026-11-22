// Package postgres connects to PostgreSQL (through PgBouncer in transaction pooling mode), runs
// transactions that repositories join implicitly, maps driver errors to apperr kinds, and offers
// generic helpers for scanning rows and for filtered, sorted, paginated listing.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Querier is what repositories use. Both the pool and a transaction satisfy it, so a repository
// method is written once and works inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Config configures Connect. Zero values get safe defaults.
type Config struct {
	URL string // DATABASE_URL

	MaxConns          int32         // default 10 (PgBouncer multiplexes; do not size for "all you may ever need")
	MinConns          int32         // default 0
	MaxConnLifetime   time.Duration // default 30m
	MaxConnIdleTime   time.Duration // default 5m
	HealthCheckPeriod time.Duration // default 30s

	// PreparedStatements switches from "describe on every execution" (CacheDescribe, works behind any
	// PgBouncer) to named server-side prepared statements (CacheStatement): each distinct statement is
	// parsed and planned once per connection. It needs PgBouncer >= 1.21 with max_prepared_statements > 0,
	// which the shared infrastructure provides. Measure with the benchmarks before relying on it.
	PreparedStatements bool
	// StatementCacheCapacity bounds the per-connection statement cache (default 128; keep it below
	// PgBouncer's max_prepared_statements).
	StatementCacheCapacity int

	// SlowQueryThreshold logs every statement slower than this (SQL text only, never arguments).
	// 0 disables it.
	SlowQueryThreshold time.Duration

	// StartupTimeout bounds how long Connect keeps retrying while the database is unreachable
	// (the infrastructure may start after the project). Default 60s.
	StartupTimeout time.Duration

	Logger *slog.Logger
}

// DB owns the connection pool.
type DB struct {
	pool *pgxpool.Pool
}

type txKey struct{}

// Connect creates the pool and waits, with backoff, until the database answers.
func Connect(ctx context.Context, cfg Config) (*DB, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Never echo the URL: it contains the password.
		return nil, errors.New("postgres: DATABASE_URL is not a valid connection string")
	}
	pc.MaxConns = orDefault(cfg.MaxConns, 10)
	pc.MinConns = cfg.MinConns
	pc.MaxConnLifetime = orDefault(cfg.MaxConnLifetime, 30*time.Minute)
	pc.MaxConnIdleTime = orDefault(cfg.MaxConnIdleTime, 5*time.Minute)
	pc.HealthCheckPeriod = orDefault(cfg.HealthCheckPeriod, 30*time.Second)

	// With transaction pooling a client does not keep its server connection between statements, so
	// server-side prepared statements cannot be cached by name. CacheDescribe prepares nothing
	// permanently and is safe behind PgBouncer.
	pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	if cfg.PreparedStatements {
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		pc.ConnConfig.StatementCacheCapacity = int(orDefault(int32(cfg.StatementCacheCapacity), 128))
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	pc.ConnConfig.Tracer = newTracer(log, cfg.SlowQueryThreshold)

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	timeout := orDefault(cfg.StartupTimeout, 60*time.Second)
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = retry.Do(wctx, retry.Policy{
		Initial: 500 * time.Millisecond, Max: 5 * time.Second,
		OnRetry: func(attempt int, err error, delay time.Duration) {
			log.Warn("postgres not ready, retrying", "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		},
	}, func(ctx context.Context) error {
		pctx, c := context.WithTimeout(ctx, 3*time.Second)
		defer c()
		return pool.Ping(pctx)
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: not reachable after %s: %w", timeout, err)
	}

	if err := otelpgx.RecordStats(pool); err != nil {
		log.Warn("postgres pool metrics disabled", "error", err.Error())
	}
	return &DB{pool: pool}, nil
}

func orDefault[T int32 | time.Duration](v, def T) T {
	if v <= 0 {
		return def
	}
	return v
}

// Pool exposes the underlying pool for code that needs pgx directly (LISTEN is not usable behind
// PgBouncer's transaction pooling).
func (d *DB) Pool() *pgxpool.Pool { return d.pool }

// Ping checks connectivity; use it for readiness, never for liveness.
func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

// Close releases the pool.
func (d *DB) Close() { d.pool.Close() }

// Querier returns the transaction carried by ctx, or the pool when there is none. Repositories call
// it for every statement.
func (d *DB) Querier(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return d.pool
}

// WithTx runs fn in a transaction and commits when it returns nil; any error or panic rolls back.
// The context passed to fn carries the transaction, so repositories called with it join it. A nested
// WithTx joins the outer transaction instead of opening a new one.
//
// WithTx satisfies the small Transactor interfaces that use cases declare for themselves.
func (d *DB) WithTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return MapError(err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// Roll back even when ctx is already cancelled, otherwise the connection stays dirty.
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return MapError(err)
	}
	return nil
}
