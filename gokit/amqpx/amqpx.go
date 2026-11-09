// Package amqpx is a RabbitMQ client that survives broker restarts: it reconnects with backoff,
// re-declares the topology your code registered, publishes with publisher confirms (a nil error means
// the broker took responsibility for the message), and consumes with explicit acknowledgements, bounded
// concurrency and a graceful stop.
//
// The shared broker contract applies: use the project's own vhost (AMQP_URL), keep messages small, and
// make sure queues are consumed, because publishers block when the broker hits its memory watermark.
package amqpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Config configures Connect.
type Config struct {
	URL            string // AMQP_URL (contains the vhost)
	Heartbeat      time.Duration
	StartupTimeout time.Duration // how long Connect keeps retrying (default 60s)
	Logger         *slog.Logger
}

// Client owns one connection and a confirm-mode publishing channel, and keeps both alive.
type Client struct {
	cfg Config
	log *slog.Logger

	mu       sync.RWMutex
	conn     *amqp.Connection
	pub      *amqp.Channel
	confirms chan amqp.Confirmation
	ready    chan struct{} // closed while connected; replaced while reconnecting
	pubMu    sync.Mutex    // one publish at a time on the confirm channel

	topology []func(*amqp.Channel) error

	closed chan struct{}
	done   sync.WaitGroup
	once   sync.Once
}

// Connect dials the broker (retrying, the infrastructure may start later) and starts the supervisor
// that reconnects after failures.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 10 * time.Second
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 60 * time.Second
	}
	c := &Client{cfg: cfg, log: cfg.Logger, ready: make(chan struct{}), closed: make(chan struct{})}

	wctx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	err := retry.Do(wctx, retry.Policy{
		Initial: 500 * time.Millisecond, Max: 5 * time.Second,
		OnRetry: func(attempt int, err error, delay time.Duration) {
			c.log.Warn("rabbitmq not ready, retrying", "attempt", attempt, "retry_in", delay.String(), "error", sanitize(err))
		},
	}, c.dial)
	if err != nil {
		return nil, fmt.Errorf("amqpx: not reachable after %s: %w", cfg.StartupTimeout, err)
	}
	c.done.Add(1)
	go c.supervise() //nolint:gosec // the supervisor outlives the connect context by design; Close stops it
	return c, nil
}

// sanitize keeps the broker URL (it contains the password) out of logs.
func sanitize(err error) string {
	var ae *amqp.Error
	if errors.As(err, &ae) {
		return ae.Error()
	}
	return "connection failed"
}

// dial opens a connection, re-runs the topology and publishes the new state.
func (c *Client) dial(context.Context) error {
	conn, err := amqp.DialConfig(c.cfg.URL, amqp.Config{Heartbeat: c.cfg.Heartbeat, Locale: "en_US"})
	if err != nil {
		return errors.New(sanitize(err))
	}
	pub, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := pub.Confirm(false); err != nil {
		_ = conn.Close()
		return err
	}
	c.mu.RLock()
	topology := append([]func(*amqp.Channel) error(nil), c.topology...)
	c.mu.RUnlock()
	for _, declare := range topology {
		if err := declare(pub); err != nil {
			_ = conn.Close()
			return fmt.Errorf("declare topology: %w", err)
		}
	}

	c.mu.Lock()
	c.conn, c.pub = conn, pub
	c.confirms = pub.NotifyPublish(make(chan amqp.Confirmation, 1))
	select {
	case <-c.ready: // already closed (first connect after a failed attempt cannot happen, reconnects replace it)
	default:
		close(c.ready)
	}
	c.mu.Unlock()
	return nil
}

// supervise waits for the connection to drop and reconnects with backoff until Close.
func (c *Client) supervise() {
	defer c.done.Done()
	for {
		c.mu.RLock()
		lost := c.conn.NotifyClose(make(chan *amqp.Error, 1))
		c.mu.RUnlock()
		select {
		case <-c.closed:
			return
		case err := <-lost:
			if err == nil { // closed on purpose
				return
			}
			c.log.Warn("rabbitmq connection lost, reconnecting", "error", err.Error())
		}
		c.mu.Lock()
		c.ready = make(chan struct{})
		c.mu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			select {
			case <-c.closed:
				cancel()
			case <-ctx.Done():
			}
		}()
		err := retry.Do(ctx, retry.Policy{
			Initial: 500 * time.Millisecond, Max: 10 * time.Second,
			OnRetry: func(attempt int, err error, delay time.Duration) {
				c.log.Warn("rabbitmq reconnect failed", "attempt", attempt, "retry_in", delay.String(), "error", sanitize(err))
			},
		}, c.dial)
		cancel()
		if err != nil {
			return // Close was called
		}
		c.log.Info("rabbitmq reconnected")
	}
}

// Declare registers topology (exchanges, queues, bindings) and applies it now and after every reconnect.
func (c *Client) Declare(fn func(ch *amqp.Channel) error) error {
	c.mu.Lock()
	c.topology = append(c.topology, fn)
	pub := c.pub
	c.mu.Unlock()
	return fn(pub)
}

// Ping reports whether the connection is up (readiness probe).
func (c *Client) Ping(context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil || c.conn.IsClosed() {
		return apperr.Unavailable("rabbitmq not connected")
	}
	return nil
}

// Close stops reconnecting and closes the connection.
func (c *Client) Close() error {
	var err error
	c.once.Do(func() {
		close(c.closed)
		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()
		if conn != nil {
			err = conn.Close()
		}
		c.done.Wait()
	})
	return err
}

// waitReady blocks until connected or ctx ends.
func (c *Client) waitReady(ctx context.Context) error {
	for {
		c.mu.RLock()
		ready := c.ready
		c.mu.RUnlock()
		select {
		case <-ready:
			return nil
		case <-ctx.Done():
			return apperr.Wrap(apperr.KindUnavailable, "rabbitmq_unavailable", "message broker not reachable", ctx.Err())
		case <-c.closed:
			return errors.New("amqpx: client closed")
		}
	}
}

// openChannel gives a consumer its own channel on the current connection.
func (c *Client) openChannel(ctx context.Context) (*amqp.Channel, error) {
	if err := c.waitReady(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()
	return conn.Channel()
}
