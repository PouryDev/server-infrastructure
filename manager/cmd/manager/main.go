package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/builder"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/delivery"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/docker"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/gateway"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/metrics"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/resources"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/storefront"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/vpn"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/transport/httpapi"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 2 * time.Second}
		res, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("manager stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	token := os.Getenv("MANAGER_GATEWAY_TOKEN")
	if len(token) < 32 {
		return &configError{"MANAGER_GATEWAY_TOKEN must contain at least 32 characters"}
	}
	var handler http.Handler
	writeTimeout := 3 * time.Minute
	if len(os.Args) > 1 && os.Args[1] == "gateway" {
		handler = gateway.Handler(docker.New("/var/run/docker.sock", "infra", "infra"), token)
	} else if len(os.Args) > 1 && os.Args[1] == "vpn" {
		engine, err := vpn.Open(ctx, env("MANAGER_XRAY_BINARY", "/usr/local/bin/xray"), env("MANAGER_VPN_DATA", "/data/vpn"))
		if err != nil {
			return err
		}
		done := make(chan struct{})
		go func() { defer close(done); engine.Run(ctx) }()
		defer func() { stop(); <-done }()
		handler = vpn.Handler(engine, token)
	} else if len(os.Args) > 1 && os.Args[1] == "resources" {
		sources := map[string]resources.Source{}
		if raw := os.Getenv("MANAGER_RESOURCE_SOURCES"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &sources); err != nil {
				return &configError{"invalid MANAGER_RESOURCE_SOURCES"}
			}
		}
		handler = resources.Handler(&resources.Engine{InfraRoot: env("MANAGER_SOURCE_INFRA", "/source/infra"), AppsRoot: env("MANAGER_SOURCE_APPS", "/source/apps"), Sources: sources, PushEnabled: env("MANAGER_RESOURCE_PUSH_ENABLED", "false") == "true"}, token)
		writeTimeout = 3 * time.Minute
	} else if len(os.Args) > 1 && os.Args[1] == "builder" {
		registry, err := buildsrc.Load(os.Getenv("MANAGER_BUILD_SOURCES"))
		if err != nil {
			return &configError{err.Error()}
		}
		handler = builder.Handler(&builder.Engine{Registry: registry, Proxy: os.Getenv("MANAGER_BUILD_PROXY")}, token)
		writeTimeout = 31 * time.Minute
	} else if len(os.Args) > 1 && os.Args[1] == "shipper" {
		registry, err := buildsrc.Load(os.Getenv("MANAGER_BUILD_SOURCES"))
		if err != nil {
			return &configError{err.Error()}
		}
		handler = delivery.Handler(&delivery.Engine{Sources: registry, InfraRoot: env("MANAGER_SOURCE_INFRA", "/source/infra"), AppsRoot: env("MANAGER_SOURCE_APPS", "/source/apps"), KeysRoot: env("MANAGER_SSH_DIR", "/keys"), HostInfraRoot: os.Getenv("MANAGER_HOST_INFRA"), HostAppsRoot: os.Getenv("MANAGER_HOST_APPS"), HostDataRoot: os.Getenv("MANAGER_HOST_DATA")}, token)
		writeTimeout = 31 * time.Minute
	} else {
		auth, err := httpapi.NewAuth(os.Getenv("MANAGER_USERNAME"), os.Getenv("MANAGER_PASSWORD_HASH"), env("MANAGER_SECURE_COOKIE", "false") == "true")
		if err != nil {
			return err
		}
		repo, err := sqlite.Open(env("MANAGER_DB", "/data/manager.db"))
		if err != nil {
			return err
		}
		defer repo.Close()
		manager := application.New(gateway.New(env("MANAGER_GATEWAY_URL", "http://manager-runtime:8080"), token), repo, metrics.New(env("MANAGER_PROMETHEUS_URL", "http://prometheus:9090")))
		shipper := delivery.NewClient(env("MANAGER_SHIPPER_URL", "http://manager-shipper:8080"), token)
		shipping := application.NewShipping(shipper, repo, repo).WithEnv(shipper)
		if builderURL := os.Getenv("MANAGER_BUILDER_URL"); builderURL != "" {
			shipping.WithBuilder(builder.NewClient(builderURL, token))
		}
		shippingDone := make(chan struct{})
		go func() { defer close(shippingDone); shipping.Run(ctx) }()
		defer func() { stop(); <-shippingDone }()
		wake := make(chan struct{}, 1)
		projectRunner := storefront.New(env("MANAGER_STOREFRONT_URL", "http://manager-storefront:8080"), token)
		projectRunner.HTTP.Timeout = 31 * time.Minute
		handler = (&httpapi.Server{Manager: manager, VPN: &application.VPN{Runner: vpn.New(env("MANAGER_VPN_URL", "http://xray:8080"), token), Repo: repo}, Shipping: shipping, Deployments: &application.Deployments{Runner: projectRunner, Repo: repo}, Envs: application.NewEnvs(shipper, repo, repo), Resources: &application.Resources{Runner: resources.New(env("MANAGER_RESOURCES_URL", "http://manager-resources:8080"), token), Repo: repo}, Storefront: &application.Storefront{Runner: storefront.New(env("MANAGER_STOREFRONT_URL", "http://manager-storefront:8080"), token), Repo: repo}, Auth: auth, Origin: env("MANAGER_ORIGIN", "http://manager.localhost"), WebDir: env("MANAGER_WEB_DIR", "/web"), Wake: func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		}}).Handler()
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				err := manager.Reconcile(syncCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					slog.Error("reconciliation failed", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-wake:
				}
			}
		}()
		metricsDone := make(chan struct{})
		go func() {
			defer close(metricsDone)
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					sampleCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
					err := manager.CollectMetrics(sampleCtx)
					cancel()
					if err != nil && ctx.Err() == nil {
						slog.Warn("monitoring sample incomplete", "error", err)
					}
				}
			}
		}()
		defer func() { stop(); <-done; <-metricsDone }()
	}
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: writeTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	finished := make(chan error, 1)
	go func() { slog.Info("manager listening"); finished <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-finished:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

type configError struct{ message string }

func (e *configError) Error() string { return e.message }
