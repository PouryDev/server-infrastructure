// Package logging builds the process logger: one JSON object per line on stdout, with the
// OpenTelemetry trace and span ids attached so a log line can be matched to its trace in Grafana.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Config selects level and format.
type Config struct {
	Level  string // debug, info, warn, error (default info)
	Format string // json (default) or text
	Output io.Writer
}

// New returns a logger and does not touch slog's default; call slog.SetDefault if you want that.
func New(cfg Config, attrs ...any) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}
	opts := &slog.HandlerOptions{Level: ParseLevel(cfg.Level)}
	var h slog.Handler
	if strings.EqualFold(cfg.Format, "text") {
		h = slog.NewTextHandler(out, opts)
	} else {
		h = slog.NewJSONHandler(out, opts)
	}
	return slog.New(&traceHandler{Handler: h}).With(attrs...)
}

// ParseLevel maps a name to a slog level; unknown names mean info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// traceHandler adds trace_id/span_id from the record's context.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(a)}
}

func (h *traceHandler) WithGroup(n string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(n)}
}
