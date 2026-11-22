// Package bootstrap wires adapters into application services and manages process lifetime.
package bootstrap

import (
	"context"
	"errors"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/gokit/logging"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/telemetry"
	"github.com/PouryDev/storefront/internal/adapter/payments"
	"github.com/PouryDev/storefront/internal/adapter/sms"
	"github.com/PouryDev/storefront/internal/adapter/store"
	"github.com/PouryDev/storefront/internal/adapter/torob"
	"github.com/PouryDev/storefront/internal/adapter/web"
	"github.com/PouryDev/storefront/internal/config"
	"github.com/PouryDev/storefront/internal/usecase/care"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"github.com/PouryDev/storefront/internal/usecase/identity"
	"github.com/PouryDev/storefront/internal/usecase/syndication"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Run(parent context.Context, c config.Config) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	log := logging.New(logging.Config{Level: "info", Format: "json"}, "service", c.Instance)
	slog.SetDefault(log)
	flush, e := telemetry.Setup(ctx, telemetry.Config{ServiceName: c.Instance + "-api", Project: c.Instance})
	if e != nil {
		return e
	}
	defer func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = flush(end)
	}()
	db, e := kitpg.Connect(ctx, kitpg.Config{URL: c.DatabaseURL, MaxConns: 8, Logger: log, PreparedStatements: true})
	if e != nil {
		return e
	}
	defer db.Close()
	repo := &store.Store{DB: db, Secret: []byte(c.Secret)}
	if e = seed(ctx, repo, c.Demo); e != nil {
		return e
	}
	auth, e := identity.New(repo, sms.Kavenegar{Key: c.SMSKey, Template: c.SMSTemplate}, c.Secret, c.Demo)
	if e != nil {
		return e
	}
	if c.OwnerPhone != "" {
		if e = auth.Bootstrap(ctx, c.OwnerPhone, c.OwnerPassword, "seller"); e != nil {
			return e
		}
	}
	if c.AdminPhone != "" {
		if e = auth.Bootstrap(ctx, c.AdminPhone, c.AdminPassword, "admin"); e != nil {
			return e
		}
	}
	if e = os.MkdirAll(c.Uploads, 0700); e != nil {
		return e
	}
	app := commerce.New(repo, payments.New(c.Demo, c.Origin), c.Origin)
	originURL, _ := url.Parse(c.Origin)
	verifier, e := torob.NewVerifier(torob.PublicKey, originURL.Host)
	if e != nil {
		return e
	}
	feed := syndication.New(repo, repo, torob.NewClient(c.Origin), c.Origin)
	delivery := web.New(web.Server{Care: care.New(repo), Feed: feed, TorobVerify: verifier.Verify, Ready: db.Ping, App: app, Auth: auth, Origin: c.Origin, Uploads: c.Uploads, GapchiSecret: c.GapchiSecret, Demo: c.Demo})
	handler := httpx.Chain(delivery.Handler(), httpx.DefaultStack(log, httpx.StackConfig{MaxBodyBytes: 21 << 20})...)
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(5 * time.Second)
		expiryTick := 0
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				work, cancel := context.WithTimeout(ctx, 15*time.Second)
				expiryTick++
				if err := feed.Tick(work); err != nil {
					slog.Warn("torob sync deferred", "error", err)
				}
				if expiryTick%12 == 0 {
					if err := app.Expire(work); err != nil {
						slog.Error("order expiration failed", "error", err)
					}
				}
				cancel()
			}
		}
	}()
	defer func() { stop(); <-workerDone }()
	slog.Info("storefront listening", "instance", c.Instance, "demo", c.Demo)
	select {
	case <-ctx.Done():
		end, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(end)
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	}
}
