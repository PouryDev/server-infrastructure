package notify

import (
	"context"
	"errors"
	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// scope is the instrumentation scope name of every span and metric produced by this package.
const scope = "github.com/PouryDev/server-infrastructure/gokit/notify"

// Metric names. Labels are low cardinality on purpose: channel, provider, outcome. Recipients, message
// ids and error texts only ever appear in logs and span attributes, never as labels.
//
//	notify.deliveries          counter    one per message handed to a provider layer, by outcome
//	notify.delivery.duration   histogram  seconds, whole delivery including retries
//	notify.delivery.attempts   counter    one per provider call, by result
//	notify.enqueued            counter    async driver: messages offered to the queue, by outcome
//	notify.jobs.processed      counter    async worker: queue jobs finished, by outcome
//	notify.queue.wait          histogram  seconds between enqueue and the worker picking the job up
//	notify.queue.depth         gauge      in-memory queue only: jobs waiting
const (
	attrChannel  = attribute.Key("notify.channel")
	attrProvider = attribute.Key("notify.provider")
	attrOutcome  = attribute.Key("notify.outcome")
	attrResult   = attribute.Key("notify.result")
	attrMsgID    = attribute.Key("notify.message.id")
	attrCount    = attribute.Key("notify.recipients.count")
	attrAttempts = attribute.Key("notify.attempts")
	attrAttempt  = attribute.Key("notify.attempt")
)

// Outcomes of a delivery.
const (
	outcomeSent     = "sent"
	outcomeRejected = "rejected" // the provider refused it for good; retrying would not help
	outcomeFailed   = "failed"   // transient failures until the attempts ran out
	outcomeCanceled = "canceled" // the caller's context ended
)

type instruments struct {
	tracer     trace.Tracer
	deliveries metric.Int64Counter
	duration   metric.Float64Histogram
	attempts   metric.Int64Counter
	enqueued   metric.Int64Counter
	processed  metric.Int64Counter
	queueWait  metric.Float64Histogram
	meter      metric.Meter
}

// newInstruments binds to the global providers. Without telemetry.Setup they are no-ops, and because the
// OTel globals delegate, instruments created before Setup start exporting once it ran.
func newInstruments() *instruments {
	m := otel.Meter(scope)
	i := &instruments{tracer: otel.Tracer(scope), meter: m}
	// The metric API only errs on invalid names or options (a programming error caught by the tests); the
	// returned instrument is a working no-op in that case.
	i.deliveries, _ = m.Int64Counter("notify.deliveries", metric.WithDescription("Messages handed to a provider, by outcome"), metric.WithUnit("{message}"))
	i.duration, _ = m.Float64Histogram("notify.delivery.duration", metric.WithDescription("Delivery time including retries"), metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30))
	i.attempts, _ = m.Int64Counter("notify.delivery.attempts", metric.WithDescription("Provider calls, by result"), metric.WithUnit("{attempt}"))
	i.enqueued, _ = m.Int64Counter("notify.enqueued", metric.WithDescription("Messages offered to the queue, by outcome"), metric.WithUnit("{message}"))
	i.processed, _ = m.Int64Counter("notify.jobs.processed", metric.WithDescription("Queue jobs finished by the worker, by outcome"), metric.WithUnit("{job}"))
	i.queueWait, _ = m.Float64Histogram("notify.queue.wait", metric.WithDescription("Time a job waited in the queue"), metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.025, 0.1, 0.5, 1, 5, 15, 60, 300))
	return i
}

// finish ends span with the right status. Provider errors can contain addresses, so only the error text
// the provider layer chose is recorded, never message content.
func finish(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, errorType(err))
	}
	span.End()
}

// errorType is a short, low-cardinality description of why something failed.
func errorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	if ae, ok := asAppErr(err); ok {
		return ae.Code
	}
	return "error"
}

func channelAttr(ch Channel) attribute.KeyValue { return attrChannel.String(string(ch)) }

func spanName(op string, ch Channel) string { return strings.Join([]string{op, string(ch)}, " ") }

func asAppErr(err error) (*apperr.Error, bool) { return apperr.As(err) }
