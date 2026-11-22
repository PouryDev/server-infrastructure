// Command api runs the HTTP API. `api healthcheck` probes the local /healthz and exits 0/1; the
// production image is built from scratch (no curl), so the compose healthcheck uses it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"

	"github.com/PouryDev/mizan-host/internal/bootstrap"
	"github.com/PouryDev/mizan-host/internal/config"
)

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return healthcheck()
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:\n"+err.Error())
		return 2
	}
	if err := bootstrap.Run(context.Background(), cfg); err != nil {
		slog.Error("service stopped with error", "error", err.Error())
		return 1
	}
	return 0
}

func healthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid HTTP_ADDR:", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if err := httpx.Probe(context.Background(), "http://"+net.JoinHostPort(host, port)+"/healthz"); err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	return 0
}
