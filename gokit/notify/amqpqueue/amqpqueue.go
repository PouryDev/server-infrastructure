// Package amqpqueue is a durable notify.Queue on RabbitMQ (via amqpx): publisher confirms on enqueue, explicit
// acknowledgements and a dead-letter queue on the consuming side. Delivery is at-least-once, so a
// message can in rare cases (a worker crash between the provider call and the ack) be sent twice.
package amqpqueue

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/amqpx"
	"github.com/PouryDev/server-infrastructure/gokit/notify"
)

// Config configures New.
type Config struct {
	Queue       string // default "notifications"; the dead-letter queue is Queue + ".dlq"
	Prefetch    int    // default 20
	Concurrency int    // parallel deliveries per worker process (default 4)
}

// Queue implements notify.Queue.
type Queue struct {
	mq  *amqpx.Client
	cfg Config
}

var _ notify.Queue = (*Queue)(nil)

// New declares the durable queue and its dead-letter queue (the declaration is repeated automatically after
// every reconnect). A job that the worker rejects, or that cannot be decoded, ends up in <queue>.dlq for
// inspection or replay instead of being retried forever. Declaring an existing queue with different
// arguments fails, so do not declare it elsewhere with other settings.
func New(mq *amqpx.Client, cfg Config) (*Queue, error) {
	if mq == nil {
		return nil, errors.New("amqpqueue: client is required")
	}
	if cfg.Queue == "" {
		cfg.Queue = "notifications"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Prefetch <= 0 {
		cfg.Prefetch = 20
	}
	err := mq.Declare(func(ch *amqp.Channel) error {
		if _, err := ch.QueueDeclare(cfg.Queue+".dlq", true, false, false, false, nil); err != nil {
			return err
		}
		_, err := ch.QueueDeclare(cfg.Queue, true, false, false, false, amqp.Table{
			"x-dead-letter-exchange":    "", // the default exchange routes by queue name
			"x-dead-letter-routing-key": cfg.Queue + ".dlq",
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("amqpqueue: declare %s: %w", cfg.Queue, err)
	}
	return &Queue{mq: mq, cfg: cfg}, nil
}

// Enqueue publishes the job and returns after the broker's confirmation.
func (q *Queue) Enqueue(ctx context.Context, j notify.Job) error {
	b, err := j.Encode()
	if err != nil {
		return err
	}
	return q.mq.Publish(ctx, "", q.cfg.Queue, amqpx.Message{Body: b, ContentType: "application/json", MessageID: j.ID, Mandatory: true})
}

// Run consumes until ctx ends.
func (q *Queue) Run(ctx context.Context, h notify.JobHandler) error {
	return q.mq.Consume(amqpx.ConsumerConfig{Queue: q.cfg.Queue, Prefetch: q.cfg.Prefetch, Concurrency: q.cfg.Concurrency},
		func(ctx context.Context, d amqpx.Delivery) error {
			j, err := notify.DecodeJob(d.Body)
			if err != nil {
				return err // poison message: rejected without requeue, so it goes to the dead-letter queue
			}
			return h(ctx, j)
		}).Run(ctx)
}
