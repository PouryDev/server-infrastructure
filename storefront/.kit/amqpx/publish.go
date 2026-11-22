package amqpx

import (
	"context"
	"errors"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Message is what Publish sends. The zero value is a persistent message with no content type.
type Message struct {
	Body        []byte
	ContentType string // e.g. "application/json"
	MessageID   string
	Headers     map[string]any
	Expiration  time.Duration // per-message TTL, 0 = none
	Transient   bool          // by default messages are persistent (survive a broker restart)
	Mandatory   bool          // fail with an error when no queue is bound for the routing key
}

// Publish sends msg and returns only after the broker confirmed it (or ctx ended). While the client is
// reconnecting it waits for the connection, bounded by ctx, so give ctx a deadline.
func (c *Client) Publish(ctx context.Context, exchange, routingKey string, msg Message) error {
	if err := c.waitReady(ctx); err != nil {
		return err
	}
	c.pubMu.Lock()
	defer c.pubMu.Unlock()

	c.mu.RLock()
	pub, confirms := c.pub, c.confirms
	c.mu.RUnlock()

	mode := amqp.Persistent
	if msg.Transient {
		mode = amqp.Transient
	}
	p := amqp.Publishing{
		Body: msg.Body, ContentType: msg.ContentType, MessageId: msg.MessageID, DeliveryMode: mode,
		Headers: amqp.Table(msg.Headers), Timestamp: time.Now(),
	}
	if msg.Expiration > 0 {
		p.Expiration = itoa(msg.Expiration.Milliseconds())
	}
	if err := pub.PublishWithContext(ctx, exchange, routingKey, msg.Mandatory, false, p); err != nil {
		return apperr.Wrap(apperr.KindUnavailable, "publish_failed", "message could not be sent", err)
	}
	select {
	case conf, ok := <-confirms:
		if !ok || !conf.Ack {
			return apperr.Wrap(apperr.KindUnavailable, "publish_nacked", "the broker did not accept the message", errors.New("nack or closed channel"))
		}
		return nil
	case <-ctx.Done():
		return apperr.Wrap(apperr.KindUnavailable, "publish_timeout", "no confirmation from the broker", ctx.Err())
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
