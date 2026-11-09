package notify_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/notify/notifytest"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func goodEmail() notify.Email {
	return notify.Email{To: []string{"Ada <ada@example.com>"}, Subject: "Hi", Text: "hello"}
}

var fast = retry.Policy{MaxAttempts: 3, Initial: time.Millisecond, Max: 2 * time.Millisecond}

func newSender(t *testing.T, rec *notifytest.Recorder) *notify.Sender {
	t.Helper()
	s, err := notify.NewSender(notify.Config{Email: rec, SMS: rec, Retry: fast, Logger: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidate(t *testing.T) {
	cases := map[string]notify.Email{
		"no recipient":     {Subject: "s", Text: "t"},
		"bad address":      {To: []string{"nope"}, Subject: "s", Text: "t"},
		"header injection": {To: []string{"a@b.co"}, Subject: "s\r\nBcc: x@y.z", Text: "t"},
		"no body":          {To: []string{"a@b.co"}, Subject: "s"},
		"no subject":       {To: []string{"a@b.co"}, Text: "t"},
		"bad header":       {To: []string{"a@b.co"}, Subject: "s", Text: "t", Headers: map[string]string{"X-A": "v\nw"}},
	}
	for name, e := range cases {
		if err := e.Validate(); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s: want KindInvalid, got %v", name, err)
		}
	}
	if err := goodEmail().Validate(); err != nil {
		t.Errorf("valid email rejected: %v", err)
	}
	for _, p := range []string{"09123456789", "+989123456789"} {
		if err := (notify.SMS{To: p, Body: "x"}).Validate(); err != nil {
			t.Errorf("%s rejected: %v", p, err)
		}
	}
	for _, p := range []string{"", "0912 345", "abc", "+1", "0912345678901234"} {
		if err := (notify.SMS{To: p, Body: "x"}).Validate(); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestMasking(t *testing.T) {
	if got := notify.MaskPhone("09123456789"); got != "0912***6789" {
		t.Error(got)
	}
	if got := notify.MaskEmail("Ada <ada@example.com>"); got != "a***@example.com" {
		t.Error(got)
	}
}

func TestSenderRetriesTransientThenSucceeds(t *testing.T) {
	rec := &notifytest.Recorder{}
	rec.FailWith(errors.New("boom"), errors.New("boom"))
	if err := newSender(t, rec).SendEmail(context.Background(), goodEmail()); err != nil {
		t.Fatal(err)
	}
	if len(rec.Emails()) != 1 {
		t.Fatal("want 1 delivered email")
	}
	if rec.Emails()[0].ID == "" {
		t.Error("an id should have been generated")
	}
}

func TestSenderPermanentIsNotRetried(t *testing.T) {
	rec := &notifytest.Recorder{}
	rec.FailWith(retry.Permanent(errors.New("mailbox does not exist")), nil)
	err := newSender(t, rec).SendEmail(context.Background(), goodEmail())
	if !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("want KindInvalid (rejected), got %v", err)
	}
	if len(rec.Emails()) != 0 {
		t.Error("a permanent failure must not be retried")
	}
}

func TestSenderGivesUpAfterMaxAttempts(t *testing.T) {
	rec := &notifytest.Recorder{}
	rec.FailWith(errors.New("1"), errors.New("2"), errors.New("3"), nil)
	err := newSender(t, rec).SendSMS(context.Background(), notify.SMS{To: "09123456789", Body: "x"})
	if !apperr.Is(err, apperr.KindUnavailable) {
		t.Fatalf("want KindUnavailable, got %v", err)
	}
	if len(rec.SMS()) != 0 {
		t.Error("the fourth (successful) attempt must not happen")
	}
}

func TestSenderUnconfiguredChannel(t *testing.T) {
	s, _ := notify.NewSender(notify.Config{SMS: &notifytest.Recorder{}, Logger: quiet()})
	err := s.SendEmail(context.Background(), goodEmail())
	if ae, ok := apperr.As(err); !ok || ae.Code != "channel_not_configured" {
		t.Fatalf("got %v", err)
	}
	if _, err := notify.NewSender(notify.Config{}); err == nil {
		t.Error("no provider must be a configuration error")
	}
}

func TestSenderAttemptTimeout(t *testing.T) {
	slow := slowProvider{}
	s, _ := notify.NewSender(notify.Config{Email: slow, AttemptTimeout: 20 * time.Millisecond,
		Retry: retry.Policy{MaxAttempts: 2, Initial: time.Millisecond}, Logger: quiet()})
	start := time.Now()
	if err := s.SendEmail(context.Background(), goodEmail()); !apperr.Is(err, apperr.KindUnavailable) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("attempt timeout not applied")
	}
}

type slowProvider struct{}

func (slowProvider) Name() string { return "slow" }
func (slowProvider) SendEmail(ctx context.Context, _ notify.Email) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestSenderCallerCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, _ := notify.NewSender(notify.Config{Email: slowProvider{}, Logger: quiet()})
	if err := s.SendEmail(ctx, goodEmail()); err == nil {
		t.Fatal("want an error")
	}
}

// tracing installs an in-memory exporter for one test.
func tracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	oldTP, oldProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(oldTP); otel.SetTextMapPropagator(oldProp) })
	return sr
}

func TestSenderSpans(t *testing.T) {
	sr := tracing(t)
	rec := &notifytest.Recorder{}
	rec.FailWith(errors.New("transient"))
	if err := newSender(t, rec).SendEmail(context.Background(), goodEmail()); err != nil {
		t.Fatal(err)
	}
	var send, attempts int
	for _, s := range sr.Ended() {
		switch s.Name() {
		case "notify.send email":
			send++
			if s.SpanKind() != trace.SpanKindClient {
				t.Error("delivery span must be a client span")
			}
			for _, a := range s.Attributes() {
				if strings.Contains(a.Value.String(), "ada@") {
					t.Errorf("span attribute leaks the recipient: %v", a)
				}
			}
		case "notify.attempt email":
			attempts++
		}
	}
	if send != 1 || attempts != 2 {
		t.Fatalf("want 1 delivery span and 2 attempt spans, got %d and %d", send, attempts)
	}
}

func TestAsyncRoundTripKeepsTrace(t *testing.T) {
	sr := tracing(t)
	rec := &notifytest.Recorder{}
	q := notify.NewMemoryQueue(notify.MemoryQueueConfig{Size: 8, Concurrency: 2, Logger: quiet()})
	async := notify.NewAsync(q)
	worker := notify.NewWorker(q, newSender(t, rec), quiet())

	ctx, root := otel.Tracer("test").Start(context.Background(), "request")
	if err := async.SendEmail(ctx, goodEmail()); err != nil {
		t.Fatal(err)
	}
	if err := async.SendSMS(ctx, notify.SMS{To: "09123456789", Body: "code"}); err != nil {
		t.Fatal(err)
	}
	root.End()

	// Run, then stop immediately: the shutdown drain must still deliver everything buffered.
	rctx, stop := context.WithCancel(context.Background())
	stop()
	if err := worker.Run(rctx); err != nil {
		t.Fatal(err)
	}
	if len(rec.Emails()) != 1 || len(rec.SMS()) != 1 {
		t.Fatalf("delivered %d emails, %d sms", len(rec.Emails()), len(rec.SMS()))
	}

	for _, s := range sr.Ended() {
		if s.Name() == "notify.process email" {
			if s.Parent().TraceID() != root.SpanContext().TraceID() {
				t.Fatal("the worker span must continue the enqueuer's trace")
			}
			if s.SpanKind() != trace.SpanKindConsumer {
				t.Error("worker span must be a consumer span")
			}
			return
		}
	}
	t.Fatal("no worker span recorded")
}

func TestAsyncRejectsInvalidBeforeQueueing(t *testing.T) {
	q := notify.NewMemoryQueue(notify.MemoryQueueConfig{Size: 1})
	err := notify.NewAsync(q).SendEmail(context.Background(), notify.Email{})
	if !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestMemoryQueueBackpressure(t *testing.T) {
	q := notify.NewMemoryQueue(notify.MemoryQueueConfig{Size: 1})
	a := notify.NewAsync(q)
	if err := a.SendSMS(context.Background(), notify.SMS{To: "09123456789", Body: "a"}); err != nil {
		t.Fatal(err)
	}
	err := a.SendSMS(context.Background(), notify.SMS{To: "09123456789", Body: "b"})
	if ae, ok := apperr.As(err); !ok || ae.Code != "queue_full" {
		t.Fatalf("want queue_full, got %v", err)
	}
}

func TestMemoryQueueSurvivesPanic(t *testing.T) {
	q := notify.NewMemoryQueue(notify.MemoryQueueConfig{Size: 4, Concurrency: 1, Logger: quiet()})
	var n atomic.Int32
	for range 2 {
		_ = q.Enqueue(context.Background(), notify.Job{ID: "x"})
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	_ = q.Run(ctx, func(context.Context, notify.Job) error {
		if n.Add(1) == 1 {
			panic("handler bug")
		}
		return nil
	})
	if n.Load() != 2 {
		t.Fatalf("handled %d jobs, want 2", n.Load())
	}
}

func TestDecodeJobRejectsGarbage(t *testing.T) {
	for _, in := range []string{`not json`, `{"channel":"email"}`, `{"channel":"pigeon","email":{}}`} {
		if _, err := notify.DecodeJob([]byte(in)); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	j := notify.Job{ID: "1", Channel: notify.ChannelSMS, SMS: &notify.SMS{To: "09123456789", Body: "b"}}
	b, _ := j.Encode()
	got, err := notify.DecodeJob(b)
	if err != nil || got.SMS.Body != "b" {
		t.Fatalf("round trip: %v %+v", err, got)
	}
}

func BenchmarkSenderOverhead(b *testing.B) {
	rec := &benchProvider{}
	s, _ := notify.NewSender(notify.Config{Email: rec, SMS: rec, Logger: quiet()})
	m := notify.SMS{To: "09123456789", Body: "your code is 123456"}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_ = s.SendSMS(ctx, m)
	}
}

type benchProvider struct{}

func (benchProvider) Name() string                                  { return "bench" }
func (benchProvider) SendEmail(context.Context, notify.Email) error { return nil }
func (benchProvider) SendSMS(context.Context, notify.SMS) error     { return nil }
