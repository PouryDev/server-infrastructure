package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/outbox"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

type fakePub struct {
	got  []outbox.Message
	fail bool
}

func (f *fakePub) Publish(_ context.Context, m outbox.Message) error {
	if f.fail {
		return errors.New("broker down")
	}
	f.got = append(f.got, m)
	return nil
}

func TestEventsAreAtomicWithTheChangeAndPublishedInOrder(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	w := outbox.NewWriter(db)

	// A rolled-back change must not leave an event behind.
	_ = db.WithTx(ctx, func(ctx context.Context) error {
		_ = w.Emit(ctx, trail.Event{Topic: "payment.paid", Payload: map[string]string{"id": "ghost"}})
		return errors.New("boom")
	})
	for _, id := range []string{"1", "2", "3"} {
		if err := w.Emit(ctx, trail.Event{Topic: "payment.paid", AggregateType: "payment", AggregateID: id, Payload: map[string]string{"id": id}}); err != nil {
			t.Fatal(err)
		}
	}

	pub := &fakePub{fail: true}
	relay := outbox.NewRelay(db, pub, outbox.WithBatch(10))
	if n, err := relay.Drain(ctx); err != nil || n != 0 {
		t.Fatalf("failing broker: n=%d err=%v", n, err)
	}
	attempts, _ := postgres.Value[int](ctx, db.Querier(ctx), `SELECT max(attempts) FROM mz_outbox`)
	if attempts != 1 {
		t.Fatalf("failure not recorded: %d", attempts)
	}
	// The retry is scheduled in the future, so an immediate drain does nothing...
	pub.fail = false
	if n, _ := relay.Drain(ctx); n != 0 {
		t.Fatalf("a later event overtook the one waiting for its retry: %d", n)
	}
	// ...until it is due.
	if _, err := db.Pool().Exec(ctx, `UPDATE mz_outbox SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	n, err := relay.Drain(ctx)
	if err != nil || n != 3 || len(pub.got) != 3 {
		t.Fatalf("n=%d err=%v got=%d", n, err, len(pub.got))
	}
	for i, want := range []string{"1", "2", "3"} {
		if pub.got[i].Topic != "payment.paid" || string(pub.got[i].Body) != `{"id": "`+want+`"}` {
			t.Fatalf("order or payload wrong at %d: %s", i, pub.got[i].Body)
		}
	}
	if n, _ := relay.Drain(ctx); n != 0 {
		t.Fatal("events published twice")
	}
	if purged, _ := relay.Purge(ctx, -time.Hour); purged != 3 {
		t.Fatalf("purge removed %d", purged)
	}
}
