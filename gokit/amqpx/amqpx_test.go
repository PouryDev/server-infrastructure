package amqpx

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Needs a broker: GOKIT_TEST_AMQP_URL (make gokit-integration starts one).
func brokerURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("GOKIT_TEST_AMQP_URL")
	if u == "" {
		t.Skip("GOKIT_TEST_AMQP_URL not set")
	}
	return u
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// proxy forwards TCP to the broker and can cut every connection, which looks to the client like a network
// failure or a broker restart.
type proxy struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func newProxy(t *testing.T, target string) *proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, up)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); p.sever() })
	return p
}

func (p *proxy) sever() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *proxy) urlFor(t *testing.T, broker string) string {
	u, err := url.Parse(broker)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = p.ln.Addr().String()
	return u.String()
}

func brokerHostPort(t *testing.T, broker string) string {
	u, err := url.Parse(broker)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "5672")
	}
	return host
}

func deleteQueues(t *testing.T, c *Client, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		if ch, err := c.openChannel(context.Background()); err == nil {
			for _, n := range names {
				_, _ = ch.QueueDelete(n, false, false, false)
			}
			_ = ch.Close()
		}
	})
}

func queueName(t *testing.T) string {
	return "gokit-test-" + t.Name() + "-" + time.Now().Format("150405.000")
}

func TestPublishConsumeAckAndDeadLetter(t *testing.T) {
	broker := brokerURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Connect(ctx, Config{URL: broker, Logger: quiet(), StartupTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	q, dlq := queueName(t), queueName(t)+".dead"
	if err := c.Declare(func(ch *amqp.Channel) error {
		// RabbitMQ 4 only accepts durable (or exclusive) queues.
		if _, err := ch.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
			return err
		}
		_, err := ch.QueueDeclare(q, true, false, false, false, amqp.Table{"x-dead-letter-exchange": "", "x-dead-letter-routing-key": dlq})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	deleteQueues(t, c, q, dlq)

	got := make(chan Delivery, 10)
	attempts := map[string]int{}
	var mu sync.Mutex
	cons := c.Consume(ConsumerConfig{Queue: q, Concurrency: 2}, func(_ context.Context, d Delivery) error {
		mu.Lock()
		attempts[string(d.Body)]++
		n := attempts[string(d.Body)]
		mu.Unlock()
		switch string(d.Body) {
		case "ok":
			got <- d
			return nil
		case "transient":
			if n == 1 {
				return Requeue(io.ErrUnexpectedEOF)
			}
			got <- d
			return nil
		case "panic":
			panic("boom")
		}
		return io.EOF // permanent failure -> dead letter
	})
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- cons.Run(runCtx) }()

	for _, body := range []string{"ok", "transient", "poison", "panic"} {
		if err := c.Publish(ctx, "", q, Message{Body: []byte(body), ContentType: "text/plain", MessageID: "id-" + body, Mandatory: true}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]Delivery{}
	for range 2 {
		select {
		case d := <-got:
			seen[string(d.Body)] = d
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out, saw %v", seen)
		}
	}
	if seen["ok"].MessageID != "id-ok" || seen["ok"].ContentType != "text/plain" || seen["ok"].Redelivered {
		t.Fatalf("%+v", seen["ok"])
	}
	if !seen["transient"].Redelivered {
		t.Fatalf("a requeued message must come back redelivered: %+v", seen["transient"])
	}

	// the two failed messages end up in the dead-letter queue, exactly once each
	ch, _ := c.openChannel(ctx)
	defer ch.Close()
	dead := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(dead) < 2 {
		select {
		case <-deadline:
			t.Fatalf("dead letters: %v", dead)
		default:
		}
		m, ok, err := ch.Get(dlq, true)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		dead[string(m.Body)] = true
	}
	if !dead["poison"] || !dead["panic"] {
		t.Fatalf("%v", dead)
	}

	// Graceful stop: Run returns without error.
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("consumer did not stop")
	}
}

func TestSurvivesConnectionLoss(t *testing.T) {
	broker := brokerURL(t)
	p := newProxy(t, brokerHostPort(t, broker))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := Connect(ctx, Config{URL: p.urlFor(t, broker), Logger: quiet(), StartupTimeout: 20 * time.Second, Heartbeat: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	q := queueName(t)
	// Not auto-delete: the queue must outlive the severed connection, as it would in production.
	declare := func(ch *amqp.Channel) error {
		_, err := ch.QueueDeclare(q, true, false, false, false, nil)
		return err
	}
	if err := c.Declare(declare); err != nil {
		t.Fatal(err)
	}
	deleteQueues(t, c, q)

	received := make(chan string, 10)
	cons := c.Consume(ConsumerConfig{Queue: q}, func(_ context.Context, d Delivery) error { received <- string(d.Body); return nil })
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = cons.Run(runCtx) }()

	if err := c.Publish(ctx, "", q, Message{Body: []byte("before")}); err != nil {
		t.Fatal(err)
	}
	if got := wait(t, received); got != "before" {
		t.Fatalf("%q", got)
	}

	p.sever() // the "broker restart"
	// Publishing right away waits for the reconnect (bounded by ctx) and then succeeds.
	pctx, pcancel := context.WithTimeout(ctx, 30*time.Second)
	defer pcancel()
	var perr error
	for range 20 { // the first attempts may race with the failure being noticed
		if perr = c.Publish(pctx, "", q, Message{Body: []byte("after")}); perr == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if perr != nil {
		t.Fatalf("publish after reconnect: %v", perr)
	}
	// AMQP is at-least-once: if the connection dropped before the ack of "before" reached the broker,
	// "before" is redelivered first. What matters is that the consumer resumes and "after" arrives.
	for got := wait(t, received); got != "after"; got = wait(t, received) {
		if got != "before" {
			t.Fatalf("unexpected message %q", got)
		}
	}
	if err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func wait(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(20 * time.Second):
		t.Fatal("timed out waiting for a message")
		return ""
	}
}

func TestPublishWithoutBrokerFailsFast(t *testing.T) {
	if _, err := Connect(context.Background(), Config{URL: "amqp://u:secret-password@127.0.0.1:1/v", StartupTimeout: 1500 * time.Millisecond, Logger: quiet()}); err == nil {
		t.Fatal("expected an error")
	} else if s := err.Error(); containsAny(s, "secret-password") {
		t.Fatalf("the password leaked into the error: %s", s)
	}
	_ = apperr.KindInternal
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
