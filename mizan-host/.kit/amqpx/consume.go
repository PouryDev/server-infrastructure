package amqpx

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Delivery is one received message.
type Delivery struct {
	Body        []byte
	ContentType string
	MessageID   string
	Headers     map[string]any
	Redelivered bool
	RoutingKey  string
}

// Handler processes one message. Returning nil acknowledges it. Any other error rejects it WITHOUT
// requeueing (it goes to the queue's dead-letter exchange if one is configured, otherwise it is dropped),
// so a poison message cannot loop forever. Wrap an error with Requeue to put the message back instead.
type Handler func(ctx context.Context, d Delivery) error

type requeueError struct{ err error }

func (e requeueError) Error() string { return e.err.Error() }
func (e requeueError) Unwrap() error { return e.err }

// Requeue marks err as transient: the message is returned to the queue.
func Requeue(err error) error {
	if err == nil {
		return nil
	}
	return requeueError{err}
}

// ConsumerConfig configures Consume.
type ConsumerConfig struct {
	Queue       string
	Prefetch    int // unacknowledged messages in flight per consumer (default 20)
	Concurrency int // parallel handlers (default 1); keep it <= Prefetch
	Tag         string
}

// Consumer is a lifecycle.Runner: Run blocks, consuming until ctx is cancelled, then finishes the
// messages in flight before returning.
type Consumer struct {
	c       *Client
	cfg     ConsumerConfig
	handler Handler
}

// Consume prepares a consumer of cfg.Queue.
func (c *Client) Consume(cfg ConsumerConfig, h Handler) *Consumer {
	if cfg.Prefetch <= 0 {
		cfg.Prefetch = 20
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	return &Consumer{c: c, cfg: cfg, handler: h}
}

// Run consumes until ctx ends, reconnecting after connection loss.
func (k *Consumer) Run(ctx context.Context) error {
	for {
		err := k.session(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // stopping on request is not a failure
		}
		k.c.log.Warn("consumer stopped, restarting", "queue", k.cfg.Queue, "error", fmt.Sprint(err))
		select { // brief pause so a permanent failure (missing queue) does not spin
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

// session runs one channel's lifetime.
func (k *Consumer) session(ctx context.Context) error {
	ch, err := k.c.openChannel(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	if err := ch.Qos(k.cfg.Prefetch, 0, false); err != nil {
		return err
	}
	// An explicit tag is needed to cancel the consumer on shutdown (cancelling "" cancels nothing and the
	// delivery channel would never close).
	tag := k.cfg.Tag
	if tag == "" {
		tag = fmt.Sprintf("%s-%d", k.cfg.Queue, time.Now().UnixNano())
	}
	closed := ch.NotifyClose(make(chan *amqp.Error, 1))
	deliveries, err := ch.Consume(k.cfg.Queue, tag, false, false, false, false, nil)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	for range k.cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range deliveries {
				k.handle(ctx, d)
			}
		}()
	}

	select {
	case <-ctx.Done():
		// Stop the flow, let handlers finish what they hold, then the deferred Close ends the session.
		_ = ch.Cancel(tag, false)
		wg.Wait()
		return ctx.Err()
	case cerr := <-closed:
		wg.Wait()
		if cerr != nil {
			return cerr
		}
		return errors.New("channel closed")
	}
}

func (k *Consumer) handle(ctx context.Context, d amqp.Delivery) {
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				k.c.log.Error("panic in message handler", "queue", k.cfg.Queue, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				err = fmt.Errorf("panic: %v", p)
			}
		}()
		err = k.handler(context.WithoutCancel(ctx), Delivery{
			Body: d.Body, ContentType: d.ContentType, MessageID: d.MessageId, Headers: d.Headers,
			Redelivered: d.Redelivered, RoutingKey: d.RoutingKey,
		})
	}()
	if err == nil {
		_ = d.Ack(false)
		return
	}
	var rq requeueError
	requeue := errors.As(err, &rq)
	k.c.log.Warn("message handler failed", "queue", k.cfg.Queue, "requeue", requeue, "error", err.Error())
	_ = d.Nack(false, requeue)
}
