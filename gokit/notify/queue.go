package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Job is the unit that travels through a Queue: one message plus the trace context of whoever enqueued it,
// so the worker's spans continue the request's trace.
type Job struct {
	ID         string            `json:"id"`
	Channel    Channel           `json:"channel"`
	Email      *Email            `json:"email,omitempty"`
	SMS        *SMS              `json:"sms,omitempty"`
	EnqueuedAt time.Time         `json:"enqueued_at"`
	Trace      map[string]string `json:"trace,omitempty"` // W3C trace context (traceparent, tracestate, baggage)
}

// Encode serialises the job for a queue that stores bytes.
func (j Job) Encode() ([]byte, error) { return json.Marshal(j) }

// DecodeJob parses and sanity-checks a job.
func DecodeJob(b []byte) (Job, error) {
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return Job{}, fmt.Errorf("notify: decode job: %w", err)
	}
	switch {
	case j.Channel == ChannelEmail && j.Email != nil:
	case j.Channel == ChannelSMS && j.SMS != nil:
	default:
		return Job{}, errors.New("notify: job has an unknown channel or no payload")
	}
	return j, nil
}

// JobHandler processes one job. A nil return means done; an error means the job failed, and a queue with
// dead-lettering sends it there instead of retrying (Sender already retried transient failures).
type JobHandler func(ctx context.Context, j Job) error

// Queue moves jobs from the code that sends notifications to the worker that delivers them. Implement it
// over any broker; amqpqueue (RabbitMQ, durable) and MemoryQueue (in-process) are included.
type Queue interface {
	// Enqueue returns once the queue took responsibility for the job.
	Enqueue(ctx context.Context, j Job) error
	// Run feeds jobs to h until ctx is cancelled, finishes the jobs in flight, and returns nil.
	Run(ctx context.Context, h JobHandler) error
}

// MemoryQueueConfig configures NewMemoryQueue.
type MemoryQueueConfig struct {
	Size        int // buffered jobs (default 1024); Enqueue fails with KindUnavailable when full
	Concurrency int // parallel deliveries (default 4)
	Logger      *slog.Logger
}

// MemoryQueue is an in-process Queue for local runs, tests and services where losing queued messages on a
// crash is acceptable. It does not survive a restart and does not retry or dead-letter failed jobs (the
// Worker logs and counts them). On shutdown it delivers what is still buffered before Run returns.
type MemoryQueue struct {
	ch          chan Job
	concurrency int
	log         *slog.Logger
	meter       metric.Meter
}

// NewMemoryQueue builds a MemoryQueue.
func NewMemoryQueue(cfg MemoryQueueConfig) *MemoryQueue {
	if cfg.Size <= 0 {
		cfg.Size = 1024
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &MemoryQueue{ch: make(chan Job, cfg.Size), concurrency: cfg.Concurrency, log: cfg.Logger, meter: newInstruments().meter}
}

// Enqueue never blocks: a full queue is back-pressure the caller should see, not a stalled request.
func (q *MemoryQueue) Enqueue(ctx context.Context, j Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case q.ch <- j:
		return nil
	default:
		return apperr.New(apperr.KindUnavailable, "queue_full", "the notification queue is full")
	}
}

// Run implements Queue.
func (q *MemoryQueue) Run(ctx context.Context, h JobHandler) error {
	if g, err := q.meter.Int64ObservableGauge("notify.queue.depth", metric.WithDescription("Jobs waiting in the in-memory queue"), metric.WithUnit("{job}")); err == nil {
		if reg, err := q.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(g, int64(len(q.ch)))
			return nil
		}, g); err == nil {
			defer func() { _ = reg.Unregister() }()
		}
	}

	var wg sync.WaitGroup
	for range q.concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case j := <-q.ch:
					q.handle(ctx, h, j)
				case <-ctx.Done():
					for { // drain what is buffered, then stop
						select {
						case j := <-q.ch:
							q.handle(ctx, h, j)
						default:
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	return nil
}

func (q *MemoryQueue) handle(ctx context.Context, h JobHandler, j Job) {
	defer func() {
		if p := recover(); p != nil {
			q.log.Error("panic in notification handler", "message_id", j.ID, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	_ = h(context.WithoutCancel(ctx), j) // the Worker already logged and counted a failure
}
