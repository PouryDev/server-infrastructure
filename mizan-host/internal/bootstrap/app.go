// Package bootstrap is the composition root: the only place that knows every layer. It creates the
// concrete adapters, injects them into the use cases, and runs the components until shutdown.
package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/gokit/lifecycle"
	"github.com/PouryDev/server-infrastructure/gokit/logging"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/redisx"
	"github.com/PouryDev/server-infrastructure/gokit/telemetry"

	"github.com/PouryDev/mizan-host/internal/adapter/httpapi"
	"github.com/PouryDev/mizan-host/internal/adapter/mizanmod"
	"github.com/PouryDev/mizan-host/internal/adapter/postgres"
	"github.com/PouryDev/mizan-host/internal/config"
	"github.com/PouryDev/mizan-host/internal/usecase/account"
)

// Run wires the service and blocks until it is stopped (SIGINT/SIGTERM) or a component fails.
func Run(ctx context.Context, cfg config.Config) error {
	log := logging.New(logging.Config{Level: cfg.LogLevel, Format: cfg.LogFormat}, "service", cfg.ServiceName)
	slog.SetDefault(log)

	group := lifecycle.New(log, cfg.HTTP.ShutdownTimeout)
	fail := func(err error) error { return errors.Join(err, group.Cleanup(ctx)) }

	// Cleanup runs in reverse order: the database closes first, telemetry flushes last.
	flush, err := telemetry.Setup(ctx, telemetry.Config{ServiceName: cfg.ServiceName, Project: "mizan-host"})
	if err != nil {
		return fail(err)
	}
	group.OnShutdown("telemetry", flush)

	db, err := kitpg.Connect(ctx, kitpg.Config{
		URL: cfg.Database.URL, MaxConns: cfg.Database.MaxConns, Logger: log,
		PreparedStatements: cfg.Database.PreparedStatements, SlowQueryThreshold: cfg.Database.SlowQuery,
	})
	if err != nil {
		return fail(err)
	}
	group.OnShutdown("postgres", func(context.Context) error { db.Close(); return nil })

	keys, err := cfg.JWT.Keys.KeySet()
	if err != nil {
		return fail(err)
	}
	checks := []httpx.Check{{Name: "postgres", Fn: db.Ping}}
	jwtCfg := kitauth.JWTConfig{
		Issuer: cfg.JWT.Issuer, Audience: cfg.JWT.Audience, AccessTTL: cfg.JWT.AccessTTL, RefreshTTL: cfg.JWT.RefreshTTL,
	}
	var accountOpts []account.Option
	if cfg.Redis.URL != "" { // optional: makes logout revoke the tokens
		rc, err := redisx.Connect(ctx, redisx.Config{URL: cfg.Redis.URL, KeyPrefix: cfg.Redis.KeyPrefix, Logger: log})
		if err != nil {
			return fail(err)
		}
		group.OnShutdown("redis", func(context.Context) error { return rc.Close() })
		rev := redisx.NewRevocations(rc)
		rev.MaxTokenLifetime = cfg.JWT.RefreshTTL
		jwtCfg.Revocations = rev
		accountOpts = append(accountOpts, account.WithRevoker(rev))
		checks = append(checks, httpx.Check{Name: "redis", Fn: rc.Ping})
	}
	tokens, err := kitauth.NewJWT(jwtCfg, keys)
	if err != nil {
		return fail(err)
	}

	userRepo, err := postgres.NewUserRepository(db)
	if err != nil {
		return fail(err)
	}
	accounts, err := account.New(userRepo, kitauth.NewArgon2id(), tokens, accountOpts...)
	if err != nil {
		return fail(err)
	}
	mod, err := mizanmod.New(ctx, db, log)
	if err != nil {
		return fail(err)
	}

	api := httpapi.New(httpapi.Deps{
		Accounts: accounts, Verifier: tokens, Checks: checks,
		Company: cfg.App.Company, WebDir: cfg.App.WebDir, SecureCookies: cfg.App.SecureCookies,
		Mount: mod.Mount,
		FiscalYears: func(ctx context.Context) ([]httpapi.FiscalYear, error) {
			ys, err := mod.FiscalYears(ctx)
			out := make([]httpapi.FiscalYear, len(ys))
			for i, y := range ys {
				out[i] = httpapi.FiscalYear{ID: y.ID, Name: y.Name, Status: y.Status}
			}
			return out, err
		},
	})
	handler := httpx.Chain(api, httpx.DefaultStack(log, httpx.StackConfig{
		CORSOrigins: cfg.HTTP.CORSOrigins, MaxBodyBytes: cfg.HTTP.MaxBodyBytes,
	})...)

	group.Go("http", httpx.NewServer(httpx.ServerConfig{Addr: cfg.HTTP.Addr, ShutdownTimeout: cfg.HTTP.ShutdownTimeout}, handler, log))
	return group.Run(ctx)
}
