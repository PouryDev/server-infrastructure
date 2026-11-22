package postgres

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
)

// tracer is the OpenTelemetry tracer plus an optional slow-statement log. It embeds the otelpgx
// tracer so batch, copy, prepare and connect tracing keep working, and only wraps query tracing.
type tracer struct {
	*otelpgx.Tracer
	log       *slog.Logger
	threshold time.Duration
}

type queryStart struct {
	at  time.Time
	sql string
}

type queryStartKey struct{}

func newTracer(log *slog.Logger, threshold time.Duration) pgx.QueryTracer {
	t := otelpgx.NewTracer()
	if threshold <= 0 {
		return t // no wrapper, no per-query context allocation
	}
	return &tracer{Tracer: t, log: log, threshold: threshold}
}

func (t *tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx = t.Tracer.TraceQueryStart(ctx, conn, data)
	return context.WithValue(ctx, queryStartKey{}, queryStart{at: time.Now(), sql: data.SQL})
}

func (t *tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	t.Tracer.TraceQueryEnd(ctx, conn, data)
	qs, ok := ctx.Value(queryStartKey{}).(queryStart)
	if !ok {
		return
	}
	if d := time.Since(qs.at); d >= t.threshold {
		t.log.WarnContext(ctx, "slow query", "duration_ms", float64(d.Microseconds())/1000,
			"rows", data.CommandTag.RowsAffected(), "sql", squash(qs.sql, 300), "failed", data.Err != nil)
	}
}

// squash collapses whitespace and truncates, so one statement is one log line.
func squash(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
