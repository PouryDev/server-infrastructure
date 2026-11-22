package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServerConfig configures Server. Zero values get defaults suited to sitting behind nginx (whose
// proxy read timeout is 60 s).
type ServerConfig struct {
	Addr              string        // default ":8080"
	ReadHeaderTimeout time.Duration // default 5s
	ReadTimeout       time.Duration // default 30s
	WriteTimeout      time.Duration // default 55s
	IdleTimeout       time.Duration // default 120s
	ShutdownTimeout   time.Duration // default 10s; keep below the container's stop_grace_period
	MaxHeaderBytes    int           // default 64 KiB
}

// Server is an HTTP server that satisfies lifecycle.Runner.
type Server struct {
	srv             *http.Server
	log             *slog.Logger
	shutdownTimeout time.Duration
}

// NewServer wraps handler (usually a Router already wrapped with Chain(..., DefaultStack(...)...)).
func NewServer(cfg ServerConfig, handler http.Handler, log *slog.Logger) *Server {
	d := func(v, def time.Duration) time.Duration {
		if v <= 0 {
			return def
		}
		return v
	}
	addr := cfg.Addr
	if addr == "" {
		addr = ":8080"
	}
	maxHeader := cfg.MaxHeaderBytes
	if maxHeader <= 0 {
		maxHeader = 64 << 10
	}
	return &Server{
		srv: &http.Server{
			Addr: addr, Handler: handler,
			ReadHeaderTimeout: d(cfg.ReadHeaderTimeout, 5*time.Second),
			ReadTimeout:       d(cfg.ReadTimeout, 30*time.Second),
			WriteTimeout:      d(cfg.WriteTimeout, 55*time.Second),
			IdleTimeout:       d(cfg.IdleTimeout, 120*time.Second),
			MaxHeaderBytes:    maxHeader,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		log:             log,
		shutdownTimeout: d(cfg.ShutdownTimeout, 10*time.Second),
	}
}

// Run listens and serves until ctx is cancelled, then drains in-flight requests.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.srv.Addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve is Run on an existing listener.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	errc := make(chan error, 1)
	go func() { errc <- s.srv.Serve(ln) }()
	s.log.Info("http server listening", "addr", ln.Addr().String())

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()
	if err := s.srv.Shutdown(sctx); err != nil {
		_ = s.srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Probe performs GET url and returns nil only for a 2xx answer. The scratch-based production image
// has no curl, so the binary offers a "healthcheck" subcommand built on this.
func Probe(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
