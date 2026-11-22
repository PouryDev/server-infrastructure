// Package amqpout publishes outbox messages to a RabbitMQ topic exchange through gokit's amqpx.
package amqpout

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/PouryDev/server-infrastructure/gokit/amqpx"
	"github.com/PouryDev/server-infrastructure/mizan/outbox"
)

// Publisher implements outbox.Publisher.
type Publisher struct {
	client   *amqpx.Client
	exchange string
}

var _ outbox.Publisher = (*Publisher)(nil)

// New declares the durable topic exchange (re-declared after every reconnect) and returns the publisher.
func New(client *amqpx.Client, exchange string) (*Publisher, error) {
	err := client.Declare(func(ch *amqp.Channel) error {
		return ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil)
	})
	if err != nil {
		return nil, err
	}
	return &Publisher{client: client, exchange: exchange}, nil
}

// Publish sends the event with its id as MessageID so consumers can deduplicate.
func (p *Publisher) Publish(ctx context.Context, m outbox.Message) error {
	return p.client.Publish(ctx, p.exchange, m.Topic, amqpx.Message{
		Body: m.Body, ContentType: "application/json", MessageID: m.ID,
		Headers: map[string]any{"created_at": m.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")},
	})
}
