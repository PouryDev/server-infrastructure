package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Async is the async driver: SendEmail and SendSMS validate, put the message on a Queue and return, so a
// slow or failing provider never delays the request. A Worker (same Queue) delivers it. The enqueue span
// is a producer span; its trace context travels in the job, so the delivery shows up in the same trace.
type Async struct {
	q    Queue
	inst *instruments
}

// NewAsync builds the async driver over q.
func NewAsync(q Queue) *Async { return &Async{q: q, inst: newInstruments()} }

// SendEmail validates e and enqueues it.
func (a *Async) SendEmail(ctx context.Context, e Email) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if e.ID == "" {
		e.ID = newID()
	}
	return a.enqueue(ctx, Job{ID: e.ID, Channel: ChannelEmail, Email: &e}, len(e.Recipients()))
}

// SendSMS validates m and enqueues it.
func (a *Async) SendSMS(ctx context.Context, m SMS) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.ID == "" {
		m.ID = newID()
	}
	return a.enqueue(ctx, Job{ID: m.ID, Channel: ChannelSMS, SMS: &m}, 1)
}

func (a *Async) enqueue(ctx context.Context, j Job, recipients int) error {
	ctx, span := a.inst.tracer.Start(ctx, spanName("notify.enqueue", j.Channel),
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(channelAttr(j.Channel), attrMsgID.String(j.ID), attrCount.Int(recipients)))
	j.EnqueuedAt = time.Now()
	j.Trace = map[string]string{}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(j.Trace))

	err := a.q.Enqueue(ctx, j)
	outcome := "queued"
	if err != nil {
		outcome = "error"
	}
	a.inst.enqueued.Add(ctx, 1, metric.WithAttributes(channelAttr(j.Channel), attrOutcome.String(outcome)))
	span.SetAttributes(attrOutcome.String(outcome))
	finish(span, err)
	return err
}

// Worker delivers queued jobs through a Notifier (normally the sync Sender). It is a lifecycle.Runner:
// register it with group.Go and it stops gracefully with the process.
type Worker struct {
	q    Queue
	n    Notifier
	log  *slog.Logger
	inst *instruments
}

// NewWorker builds a Worker. n must be a synchronous driver; passing an Async would loop jobs forever.
func NewWorker(q Queue, n Notifier, log *slog.Logger) *Worker {
	if log == nil {
		log = slog.Default()
	}
	return &Worker{q: q, n: n, log: log, inst: newInstruments()}
}

// Run consumes the queue until ctx ends.
func (w *Worker) Run(ctx context.Context) error { return w.q.Run(ctx, w.process) }

func (w *Worker) process(ctx context.Context, j Job) error {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(j.Trace))
	ctx, span := w.inst.tracer.Start(ctx, spanName("notify.process", j.Channel),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(channelAttr(j.Channel), attrMsgID.String(j.ID)))
	if !j.EnqueuedAt.IsZero() {
		wait := time.Since(j.EnqueuedAt).Seconds()
		span.SetAttributes(attribute.Float64("notify.queue.wait_s", wait))
		w.inst.queueWait.Record(ctx, wait, metric.WithAttributes(channelAttr(j.Channel)))
	}

	var err error
	switch {
	case j.Channel == ChannelEmail && j.Email != nil:
		e := *j.Email
		e.ID = j.ID
		err = w.n.SendEmail(ctx, e)
	case j.Channel == ChannelSMS && j.SMS != nil:
		s := *j.SMS
		s.ID = j.ID
		err = w.n.SendSMS(ctx, s)
	default:
		err = fmt.Errorf("notify: job %s has an unknown channel or no payload", j.ID)
	}

	outcome := outcomeSent
	if err != nil {
		outcome = errorType(err)
		w.log.WarnContext(ctx, "notification job failed", "message_id", j.ID, "channel", string(j.Channel), "error", err.Error())
	}
	w.inst.processed.Add(ctx, 1, metric.WithAttributes(channelAttr(j.Channel), attrOutcome.String(outcome)))
	finish(span, err)
	return err
}
