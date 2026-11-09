package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceIDIsLogged(t *testing.T) {
	var buf bytes.Buffer
	log := New(Config{Output: &buf}, "service", "demo")

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))

	log.InfoContext(ctx, "hello")
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["trace_id"] != tid.String() || m["service"] != "demo" {
		t.Fatalf("unexpected record %v", m)
	}

	buf.Reset()
	log.Info("no span")
	if bytes.Contains(buf.Bytes(), []byte("trace_id")) {
		t.Fatal("trace_id must be absent without a span")
	}
}

func TestParseLevel(t *testing.T) {
	if ParseLevel("DEBUG") != slog.LevelDebug || ParseLevel("nonsense") != slog.LevelInfo {
		t.Fatal("level parsing")
	}
}
