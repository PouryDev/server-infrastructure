// Package lifecycle runs long-lived components and stops them in order on SIGINT/SIGTERM.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

// Runner is a long-running component (HTTP server, consumer, scheduler).
// Run blocks until ctx is cancelled or the component fails, and must shut itself down gracefully.
type Runner interface {
	Run(ctx context.Context) error
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context) error

func (f RunnerFunc) Run(ctx context.Context) error { return f(ctx) }

type cleanup struct {
	name string
	fn   func(ctx context.Context) error
}

// Group owns runners and cleanup hooks.
type Group struct {
	log             *slog.Logger
	shutdownTimeout time.Duration
	runners         []namedRunner
	cleanups        []cleanup
}

type namedRunner struct {
	name string
	r    Runner
}

// New creates a Group. shutdownTimeout bounds the cleanup phase (keep it below the container's
// stop_grace_period).
func New(log *slog.Logger, shutdownTimeout time.Duration) *Group {
	if shutdownTimeout <= 0 {
		shutdownTimeout = 10 * time.Second
	}
	return &Group{log: log, shutdownTimeout: shutdownTimeout}
}

// Go registers a runner.
func (g *Group) Go(name string, r Runner) { g.runners = append(g.runners, namedRunner{name, r}) }

// OnShutdown registers a cleanup hook. Hooks run in reverse registration order after all runners
// have stopped, so register resources (DB pool, telemetry) in the order they were created.
func (g *Group) OnShutdown(name string, fn func(ctx context.Context) error) {
	g.cleanups = append(g.cleanups, cleanup{name, fn})
}

// Run starts every runner and blocks until a signal arrives, ctx ends, or a runner fails; then it
// stops the rest and runs the cleanup hooks.
func (g *Group) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	eg, egCtx := errgroup.WithContext(ctx)
	for _, nr := range g.runners {
		eg.Go(func() error {
			g.log.Info("component started", "component", nr.name)
			err := nr.r.Run(egCtx)
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("%s: %w", nr.name, err)
			}
			g.log.Info("component stopped", "component", nr.name)
			return nil
		})
	}
	if len(g.runners) == 0 {
		<-egCtx.Done()
	}
	runErr := eg.Wait()

	return errors.Join(runErr, g.Cleanup(ctx))
}

// Cleanup runs the OnShutdown hooks in reverse order with the group's shutdown timeout, ignoring
// cancellation of ctx. Run calls it; call it yourself when start-up fails before Run.
func (g *Group) Cleanup(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), g.shutdownTimeout)
	defer cancel()
	var errs []error
	for i := len(g.cleanups) - 1; i >= 0; i-- {
		c := g.cleanups[i]
		if err := c.fn(cctx); err != nil {
			g.log.Error("cleanup failed", "component", c.name, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", c.name, err))
		}
	}
	return errors.Join(errs...)
}
