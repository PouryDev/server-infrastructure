package amqpqueue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/amqpx"
	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/notify/amqpqueue"
	"github.com/PouryDev/server-infrastructure/gokit/notify/notifytest"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Needs a broker: GOKIT_TEST_AMQP_URL (make gokit-integration starts one).
func client(t *testing.T) *amqpx.Client {
	t.Helper()
	u := os.Getenv("GOKIT_TEST_AMQP_URL")
	if u == "" {
		t.Skip("GOKIT_TEST_AMQP_URL not set")
	}
	mq, err := amqpx.Connect(context.Background(), amqpx.Config{URL: u, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mq.Close() })
	return mq
}

func TestEndToEndAndDeadLetter(t *testing.T) {
	mq := client(t)
	name := "notify-test-" + time.Now().Format("150405.000000")
	q, err := amqpqueue.New(mq, amqpqueue.Config{Queue: name, Concurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	rec := &notifytest.Recorder{}
	rec.FailWith(nil, retry.Permanent(errors.New("rejected"))) // first job delivered, second rejected -> DLQ
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender, _ := notify.NewSender(notify.Config{Email: rec, SMS: rec, Logger: quiet})
	worker := notify.NewWorker(q, sender, quiet)
	async := notify.NewAsync(q)

	ctx := context.Background()
	if err := async.SendSMS(ctx, notify.SMS{To: "09123456789", Body: "one"}); err != nil {
		t.Fatal(err)
	}
	wctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(wctx) }()

	waitFor(t, func() bool { return len(rec.SMS()) == 1 })
	if err := async.SendEmail(ctx, notify.Email{To: []string{"a@example.com"}, Subject: "s", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	// The rejected job must land in the dead-letter queue.
	waitFor(t, func() bool {
		conn, err := amqp.Dial(os.Getenv("GOKIT_TEST_AMQP_URL"))
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close() }()
		ch, err := conn.Channel()
		if err != nil {
			return false
		}
		qi, err := ch.QueueDeclarePassive(name+".dlq", true, false, false, false, nil)
		return err == nil && qi.Messages == 1
	})
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}
