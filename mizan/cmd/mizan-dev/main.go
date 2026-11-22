// Command mizan-dev runs mizan's HTTP API for LOCAL DEVELOPMENT of the accounting UI: PostgreSQL migrations,
// the Iranian chart of accounts, the finance API and a small DEV-ONLY sign-in (see devauth.go).
//
// It is not a production service. A real deployment mounts api.Mount in its own service behind the project's
// identity provider; nothing here (the built-in user, the cookie gateway, the seed data) should be shipped.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/gokit/logging"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mizan-dev:", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return healthcheck(env("HTTP_ADDR", ":8080"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := logging.New(logging.Config{Level: env("LOG_LEVEL", "info"), Format: "text"})
	dsn := env("DATABASE_URL", "")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	if _, err := mizan.Migrate(ctx, dsn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	db, err := postgres.Connect(ctx, postgres.Config{URL: dsn, Logger: log})
	if err != nil {
		return err
	}
	defer db.Close()

	m, err := mizan.New(ctx, db, mizan.Config{IranianCodingFull: true, Logger: log})
	if err != nil {
		return err
	}

	keys, err := auth.KeyConfig{Secret: env("JWT_SECRET", "")}.KeySet()
	if err != nil {
		return fmt.Errorf("JWT_SECRET (32+ characters): %w", err)
	}
	jwt, err := auth.NewJWT(auth.JWTConfig{Issuer: "mizan-dev", Audience: "mizan-ui", AccessTTL: 15 * time.Minute, RefreshTTL: 30 * time.Minute}, keys)
	if err != nil {
		return err
	}

	dev, err := newDevAuth(jwt, m, devUser{
		Username: env("DEV_USER", "m.ahmadi"),
		Password: env("DEV_PASSWORD", ""),
		Name:     env("DEV_NAME", "مریم احمدی"),
		Title:    env("DEV_TITLE", "حسابدار ارشد"),
		Company:  env("DEV_COMPANY", "شرکت نمونه"),
	}, env("COOKIE_SECURE", "") == "true")
	if err != nil {
		return err
	}

	router := httpx.NewRouter()
	httpx.RegisterHealth(router)
	dev.mount(router)
	api.Mount(router, m, api.Options{Authenticate: httpx.Authenticate(jwt)})

	if env("SEED", "") == "true" {
		if err := seed(ctx, router, jwt); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}

	handler := httpx.Chain(dev.cookieGateway(router), httpx.DefaultStack(log, httpx.StackConfig{MaxBodyBytes: 8 << 20})...)
	srv := httpx.NewServer(httpx.ServerConfig{Addr: env("HTTP_ADDR", ":8080")}, handler, log)
	slog.SetDefault(log)
	return srv.Run(ctx)
}

func healthcheck(addr string) error {
	if addr != "" && addr[0] == ':' {
		addr = "127.0.0.1" + addr
	}
	return httpx.Probe(context.Background(), "http://"+addr+"/healthz")
}

var _ http.Handler = (*httpx.Router)(nil)
