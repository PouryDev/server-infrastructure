package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunFailureStopsOthersAndCleansUpInReverse(t *testing.T) {
	g := New(quiet(), time.Second)
	var order []string
	stopped := make(chan struct{})

	g.Go("blocker", RunnerFunc(func(ctx context.Context) error { <-ctx.Done(); close(stopped); return ctx.Err() }))
	g.Go("failing", RunnerFunc(func(context.Context) error { return errors.New("crash") }))
	g.OnShutdown("first", func(context.Context) error { order = append(order, "first"); return nil })
	g.OnShutdown("second", func(context.Context) error { order = append(order, "second"); return nil })

	err := g.Run(context.Background())
	if err == nil || err.Error() != "failing: crash" {
		t.Fatalf("err=%v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("other runner was not cancelled")
	}
	if len(order) != 2 || order[0] != "second" || order[1] != "first" {
		t.Fatalf("cleanup order = %v", order)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	g := New(quiet(), time.Second)
	g.Go("svc", RunnerFunc(func(ctx context.Context) error { <-ctx.Done(); return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	if err := g.Run(ctx); err != nil {
		t.Fatalf("err=%v", err)
	}
}
