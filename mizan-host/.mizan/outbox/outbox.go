// Package outbox implements the transactional outbox: an event is inserted in the same transaction as the
// state change that caused it, and a relay publishes it afterwards. There is no dual write, so an event is
// never lost when the broker is down and never published for a rolled-back change.
//
// Delivery is at least once and ordered per relay: consumers deduplicate by Message.ID.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Writer inserts events into mz_outbox and implements trail.Emitter.
type Writer struct{ db *postgres.DB }

var _ trail.Emitter = (*Writer)(nil)

// NewWriter creates the writer.
func NewWriter(db *postgres.DB) *Writer { return &Writer{db: db} }

// Emit stores the event. Call it inside the transaction of the change it announces.
func (w *Writer) Emit(ctx context.Context, e trail.Event) error {
	body, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("outbox: payload of %s: %w", e.Topic, err)
	}
	_, err = postgres.Exec(ctx, w.db.Querier(ctx), `
		INSERT INTO mz_outbox (event_id, topic, aggregate_type, aggregate_id, payload) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), e.Topic, e.AggregateType, e.AggregateID, body)
	return err
}

// Message is what a Publisher receives.
type Message struct {
	ID        string
	Topic     string
	Body      []byte
	CreatedAt time.Time
}

// Publisher sends a message to the broker and returns only when it is safely accepted.
type Publisher interface {
	Publish(ctx context.Context, m Message) error
}

// Relay moves pending events to a Publisher. It is a lifecycle.Runner: Run blocks until ctx ends.
type Relay struct {
	db       *postgres.DB
	pub      Publisher
	log      *slog.Logger
	batch    int
	interval time.Duration
}

// RelayOption customises a Relay.
type RelayOption func(*Relay)

func WithBatch(n int) RelayOption              { return func(r *Relay) { r.batch = n } }
func WithInterval(d time.Duration) RelayOption { return func(r *Relay) { r.interval = d } }
func WithLogger(l *slog.Logger) RelayOption    { return func(r *Relay) { r.log = l } }

// NewRelay creates a relay (defaults: batch 100, poll every second).
func NewRelay(db *postgres.DB, pub Publisher, opts ...RelayOption) *Relay {
	r := &Relay{db: db, pub: pub, log: slog.Default(), batch: 100, interval: time.Second}
	for _, o := range opts {
		o(r)
	}
	return r
}

// advisoryKey makes sure only one relay publishes at a time, which keeps events in order. It is a
// transaction-level lock, so it is safe behind PgBouncer's transaction pooling.
const advisoryKey int64 = 0x6d697a616e6f6278 // "mizanobx"

type pending struct {
	ID        int64     `db:"id"`
	EventID   uuid.UUID `db:"event_id"`
	Topic     string    `db:"topic"`
	Payload   []byte    `db:"payload"`
	Attempts  int       `db:"attempts"`
	CreatedAt time.Time `db:"created_at"`
	Due       bool      `db:"due"`
}

// Drain publishes one batch and returns how many events were sent. On the first failure it records the
// error, schedules a retry with exponential backoff and stops, so later events never overtake it.
func (r *Relay) Drain(ctx context.Context) (int, error) {
	sent := 0
	err := r.db.WithTx(ctx, func(ctx context.Context) error {
		q := r.db.Querier(ctx)
		leader, err := postgres.Value[bool](ctx, q, `SELECT pg_try_advisory_xact_lock($1)`, advisoryKey)
		if err != nil || !leader {
			return err
		}
		rows, err := postgres.Select[pending](ctx, q, `
			SELECT id, event_id, topic, payload, attempts, created_at, next_attempt_at <= now() AS due FROM mz_outbox
			 WHERE published_at IS NULL ORDER BY id LIMIT $1 FOR UPDATE`, r.batch)
		if err != nil {
			return err
		}
		for _, p := range rows {
			if !p.Due { // an earlier event is waiting for its retry: nothing may overtake it
				return nil
			}
			perr := r.pub.Publish(ctx, Message{ID: p.EventID.String(), Topic: p.Topic, Body: p.Payload, CreatedAt: p.CreatedAt})
			if perr != nil {
				delay := backoff(p.Attempts + 1)
				_, err := postgres.Exec(ctx, q, `
					UPDATE mz_outbox SET attempts = attempts + 1, last_error = $2, next_attempt_at = now() + make_interval(secs => $3) WHERE id = $1`,
					p.ID, truncate(perr.Error(), 500), delay.Seconds())
				r.log.WarnContext(ctx, "outbox publish failed", "topic", p.Topic, "event", p.EventID, "attempt", p.Attempts+1, "retry_in", delay)
				return err // commit the bookkeeping, stop the batch
			}
			if _, err := postgres.Exec(ctx, q, `UPDATE mz_outbox SET published_at = now() WHERE id = $1`, p.ID); err != nil {
				return err
			}
			sent++
		}
		return nil
	})
	return sent, err
}

// Run drains until ctx ends. A full batch is followed immediately by the next one.
func (r *Relay) Run(ctx context.Context) error {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		for {
			n, err := r.Drain(ctx)
			if err != nil && ctx.Err() == nil {
				r.log.ErrorContext(ctx, "outbox drain failed", "error", err)
			}
			if err != nil || n < r.batch {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Stats describes the backlog; alert on a growing Pending, an old Oldest or a rising MaxAttempts.
type Stats struct {
	Pending     int64
	MaxAttempts int
	Oldest      time.Duration
}

type statsRow struct {
	Pending     int64   `db:"pending"`
	MaxAttempts int     `db:"max_attempts"`
	OldestSecs  float64 `db:"oldest"`
}

// Stats returns the current backlog.
func (r *Relay) Stats(ctx context.Context) (Stats, error) {
	row, err := postgres.SelectOne[statsRow](ctx, r.db.Querier(ctx), `
		SELECT count(*) AS pending, COALESCE(max(attempts), 0)::int AS max_attempts,
		       COALESCE(extract(epoch FROM now() - min(created_at)), 0)::float8 AS oldest
		  FROM mz_outbox WHERE published_at IS NULL`)
	if err != nil {
		return Stats{}, err
	}
	return Stats{Pending: row.Pending, MaxAttempts: row.MaxAttempts, Oldest: time.Duration(row.OldestSecs * float64(time.Second))}, nil
}

// Purge deletes published events older than the given age; run it from a daily job.
func (r *Relay) Purge(ctx context.Context, olderThan time.Duration) (int64, error) {
	return postgres.Exec(ctx, r.db.Querier(ctx),
		`DELETE FROM mz_outbox WHERE published_at IS NOT NULL AND published_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
}

func backoff(attempt int) time.Duration {
	d := time.Second << min(attempt, 8)
	return min(d, 5*time.Minute)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
