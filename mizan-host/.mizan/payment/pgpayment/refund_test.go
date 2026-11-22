package pgpayment_test

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/gatewaytest"
	"github.com/PouryDev/server-infrastructure/mizan/payment/pgpayment"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// recorder keeps audit entries and events in memory.
type recorder struct {
	mu      sync.Mutex
	topics  []string
	audited []string
	actors  map[string]string // action -> actor of its last record
}

func (r *recorder) Emit(_ context.Context, e trail.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topics = append(r.topics, e.Topic)
	return nil
}

func (r *recorder) Record(_ context.Context, e trail.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audited = append(r.audited, e.Action)
	if r.actors == nil {
		r.actors = map[string]string{}
	}
	r.actors[e.Action] = e.Actor
	return nil
}

func (r *recorder) count(topic string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, t := range r.topics {
		if t == topic {
			n++
		}
	}
	return n
}

// noRefundAPI hides the Refunder of the fake: a gateway like ZarinPal/IDPay here.
type noRefundAPI struct{ payment.Gateway }

type manualFixture struct {
	svc *payment.Service
	gw  *gatewaytest.Gateway
	st  *settlement
	rec *recorder
	db  *postgres.DB
}

func setupManual(t *testing.T, withRefundAPI bool) manualFixture {
	t.Helper()
	db := testdb.New(t)
	repo, err := pgpayment.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gw, st, rec := gatewaytest.New(), &settlement{}, &recorder{}
	var g payment.Gateway = gw
	if !withRefundAPI {
		g = noRefundAPI{gw}
	}
	svc := payment.NewService(repo, db, rec, rec, payment.Config{DefaultGateway: "fake", CallbackBaseURL: "https://api.example.com/cb"},
		payment.WithGateway(g), payment.WithSettlement("order", st))
	return manualFixture{svc, gw, st, rec, db}
}

func (f manualFixture) paid(t *testing.T, key string, amount int64) payment.Payment {
	t.Helper()
	ctx := context.Background()
	p, err := f.svc.Initiate(ctx, payment.InitiateInput{IdemKey: key, Purpose: payment.Purpose{Type: "order", ID: "o-" + key},
		Amount: money.Must(amount, money.IRR), PayerRef: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	f.gw.Pay(p.GatewayRef)
	if _, err := f.svc.HandleCallback(ctx, "fake", map[string]string{"ref": p.GatewayRef, "status": "OK"}, "raw:"+key, "1.1.1.1"); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f manualFixture) request(t *testing.T, p payment.Payment, key string, amount int64) payment.Refund {
	t.Helper()
	in := payment.RefundInput{PaymentID: p.ID, IdemKey: key, Reason: "customer asked", Actor: "support"}
	if amount > 0 {
		in.Amount = money.Must(amount, money.IRR)
	}
	r, err := f.svc.Refund(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func done(agent, tracking string) payment.CompleteManualInput {
	return payment.CompleteManualInput{Agent: agent, TrackingNumber: tracking, PaidAt: time.Now().Add(-time.Minute),
		DestinationCardMasked: "603799******1234", Note: "receipt ok"}
}

func TestManualRefundFullLifecycle(t *testing.T) {
	ctx := context.Background()
	for name, withAPI := range map[string]bool{"gateway without a refund API": false, "gateway answering ErrNotSupported": true} {
		t.Run(name, func(t *testing.T) {
			f := setupManual(t, withAPI)
			f.gw.NoRefunds = true
			p := f.paid(t, "k", 1000)
			r := f.request(t, p, "r1", 400)
			if r.Method != payment.RefundManualCardToCard || r.Status != payment.RefundPending {
				t.Fatalf("a gateway that cannot refund makes a manual refund: %+v", r)
			}
			if f.rec.count("refund.manual_requested") != 1 {
				t.Fatalf("refund.manual_requested events: %d", f.rec.count("refund.manual_requested"))
			}
			if again := f.request(t, p, "r1", 400); again.ID != r.ID || f.rec.count("refund.manual_requested") != 1 {
				t.Fatalf("retrying the request must return the same refund and not announce it twice: %+v", again)
			}
			// Nothing moves before completion: not at request, not at claim.
			c, err := f.svc.ClaimRefund(ctx, r.ID, "ann")
			if err != nil || c.Status != payment.RefundProcessing || c.ClaimedBy != "ann" || c.ClaimedAt == nil {
				t.Fatalf("claim: %+v %v", c, err)
			}
			if f.st.refunded.Load() != 0 {
				t.Fatal("claiming must not touch the books")
			}
			if got, _ := f.svc.Get(ctx, p.ID); got.Refunded.Amount() != 0 {
				t.Fatalf("claiming must not change the payment: %+v", got)
			}
			// The reserved amount stays reserved while processing.
			if _, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r2", Amount: money.Must(601, money.IRR)}); apperr.KindOf(err) != apperr.KindConflict {
				t.Fatalf("a processing refund reserves its amount: %v", err)
			}
			// Release and claim by someone else.
			if _, err := f.svc.ReleaseRefund(ctx, r.ID, "ann", false); err != nil {
				t.Fatal(err)
			}
			if c, err := f.svc.ClaimRefund(ctx, r.ID, "bob"); err != nil || c.ClaimedBy != "bob" {
				t.Fatalf("%+v %v", c, err)
			}
			if _, err := f.svc.CompleteManualRefund(ctx, r.ID, done("ann", "TRK-1001")); apperr.KindOf(err) != apperr.KindConflict {
				t.Fatalf("only the claiming agent completes: %v", err)
			}
			d, err := f.svc.CompleteManualRefund(ctx, r.ID, done("bob", "TRK-1001"))
			if err != nil || d.Status != payment.RefundDone || d.TrackingNumber != "TRK-1001" || d.DestinationCardMasked != "603799******1234" ||
				d.PaidAt == nil || d.Note != "receipt ok" || d.ClaimedBy != "bob" {
				t.Fatalf("complete: %+v %v", d, err)
			}
			if got, _ := f.svc.Get(ctx, p.ID); got.Status != payment.PartiallyRefunded || got.Refunded.Amount() != 400 {
				t.Fatalf("payment after the refund: %+v", got)
			}
			// A retry changes nothing and books nothing.
			if again, err := f.svc.CompleteManualRefund(ctx, r.ID, done("bob", "TRK-1001")); err != nil || again.ID != d.ID || f.st.refunded.Load() != 1 || f.rec.count("payment.refunded") != 1 {
				t.Fatalf("retry of complete: %+v %v refunded=%d events=%d", again, err, f.st.refunded.Load(), f.rec.count("payment.refunded"))
			}
			if _, err := f.svc.CompleteManualRefund(ctx, r.ID, done("bob", "TRK-OTHER")); apperr.KindOf(err) != apperr.KindConflict {
				t.Fatalf("a different tracking number on a done refund must conflict: %v", err)
			}

			// Second, final refund through the manual path.
			r2 := f.request(t, p, "r3", 0)
			if r2.Amount.Amount() != 600 || r2.Method != payment.RefundManualCardToCard {
				t.Fatalf("the rest of the payment: %+v", r2)
			}
			if _, err := f.svc.ClaimRefund(ctx, r2.ID, "ann"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.CompleteManualRefund(ctx, r2.ID, done("ann", "TRK-1002")); err != nil {
				t.Fatal(err)
			}
			if got, _ := f.svc.Get(ctx, p.ID); got.Status != payment.Refunded || got.Refunded.Amount() != 1000 {
				t.Fatalf("partial then full: %+v", got)
			}
			if f.st.refunded.Load() != 2 {
				t.Fatalf("books touched %d times, want 2", f.st.refunded.Load())
			}

			// Audit carries the agent; events cover every step.
			if f.rec.actors["refund.claimed"] != "bob" && f.rec.actors["refund.claimed"] != "ann" {
				t.Fatalf("audit actors %+v", f.rec.actors)
			}
			if f.rec.actors["refund.completed"] != "ann" {
				t.Fatalf("completion audit actor %q", f.rec.actors["refund.completed"])
			}
			for topic, n := range map[string]int{"refund.manual_requested": 2, "refund.claimed": 3, "refund.released": 1, "payment.refunded": 2} {
				if f.rec.count(topic) != n {
					t.Errorf("event %s: %d, want %d", topic, f.rec.count(topic), n)
				}
			}
		})
	}
}

func TestOnlyOneAgentClaims(t *testing.T) {
	f := setupManual(t, false)
	p := f.paid(t, "k", 1000)
	r := f.request(t, p, "r1", 0)

	const agents = 12
	var wins, lost atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range agents {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.svc.ClaimRefund(context.Background(), r.ID, fmt.Sprintf("agent-%d", i))
			switch {
			case err == nil:
				wins.Add(1)
			case apperr.KindOf(err) == apperr.KindConflict:
				lost.Add(1)
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || lost.Load() != agents-1 {
		t.Fatalf("wins=%d lost=%d: exactly one agent must win", wins.Load(), lost.Load())
	}
	if f.rec.count("refund.claimed") != 1 {
		t.Fatalf("claimed events: %d", f.rec.count("refund.claimed"))
	}
}

func TestConcurrentCompleteBooksOnce(t *testing.T) {
	f := setupManual(t, false)
	p := f.paid(t, "k", 1000)
	r := f.request(t, p, "r1", 0)
	if _, err := f.svc.ClaimRefund(context.Background(), r.ID, "ann"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 { // the agent double-clicks, the client retries
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.CompleteManualRefund(context.Background(), r.ID, done("ann", "TRK-7")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.st.refunded.Load() != 1 || f.rec.count("payment.refunded") != 1 {
		t.Fatalf("booked %d times, %d events", f.st.refunded.Load(), f.rec.count("payment.refunded"))
	}
}

func TestDuplicateTrackingNumberIsRejected(t *testing.T) {
	ctx := context.Background()
	f := setupManual(t, false)
	p1, p2 := f.paid(t, "a", 1000), f.paid(t, "b", 1000)
	r1, r2 := f.request(t, p1, "r1", 0), f.request(t, p2, "r2", 0)
	for _, r := range []payment.Refund{r1, r2} {
		if _, err := f.svc.ClaimRefund(ctx, r.ID, "ann"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.CompleteManualRefund(ctx, r1.ID, done("ann", "TRK-SAME")); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.CompleteManualRefund(ctx, r2.ID, done("ann", "TRK-SAME"))
	if apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("the same transfer cannot prove two refunds: %v", err)
	}
	// The failed attempt left no trace (the fake settlement's call counter is not transactional, the rows are):
	// the second refund is still claimed and a correct tracking number completes it.
	got, _ := f.svc.RefundByID(ctx, r2.ID)
	if got.Status != payment.RefundProcessing || got.TrackingNumber != "" || f.rec.count("payment.refunded") != 1 {
		t.Fatalf("second refund after the rejected attempt: %+v", got)
	}
	if p, _ := f.svc.Get(ctx, p2.ID); p.Refunded.Amount() != 0 {
		t.Fatalf("the payment must be untouched: %+v", p)
	}
	if _, err := f.svc.CompleteManualRefund(ctx, r2.ID, done("ann", "TRK-OTHER")); err != nil {
		t.Fatal(err)
	}
}

func TestFailAndCancelReleaseTheReservation(t *testing.T) {
	ctx := context.Background()
	f := setupManual(t, false)
	p := f.paid(t, "k", 1000)
	r := f.request(t, p, "r1", 0)
	if _, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "x", Amount: money.Must(1, money.IRR)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("reserved: %v", err)
	}
	if _, err := f.svc.FailRefund(ctx, r.ID, "ann", "bank rejected", false); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("failing needs a claim: %v", err)
	}
	if _, err := f.svc.ClaimRefund(ctx, r.ID, "ann"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.FailRefund(ctx, r.ID, "bob", "bank rejected", false); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("another agent cannot fail it: %v", err)
	}
	failed, err := f.svc.FailRefund(ctx, r.ID, "ann", "destination card is blocked", false)
	if err != nil || failed.Status != payment.RefundFailed || failed.Failure != "destination card is blocked" {
		t.Fatalf("%+v %v", failed, err)
	}
	if _, err := f.svc.CompleteManualRefund(ctx, r.ID, done("ann", "TRK-9")); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a failed refund cannot be completed: %v", err)
	}
	r2, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "again", Reason: "second try"})
	if err != nil || r2.Amount.Amount() != 1000 {
		t.Fatalf("the failed refund released its amount: %+v %v", r2, err)
	}
	c, err := f.svc.CancelRefund(ctx, r2.ID, "support", "customer withdrew", false)
	if err != nil || c.Status != payment.RefundCancelled {
		t.Fatalf("cancel a pending refund: %+v %v", c, err)
	}
	if f.rec.count("refund.failed") != 2 || f.st.refunded.Load() != 0 {
		t.Fatalf("refund.failed events=%d, books touched=%d", f.rec.count("refund.failed"), f.st.refunded.Load())
	}
}

func TestManualRefundQueueAndStorageRules(t *testing.T) {
	ctx := context.Background()
	f := setupManual(t, false)
	p1, p2, p3 := f.paid(t, "a", 100), f.paid(t, "b", 200), f.paid(t, "c", 300)
	r1, r2, r3 := f.request(t, p1, "r1", 0), f.request(t, p2, "r2", 0), f.request(t, p3, "r3", 0)
	if _, err := f.svc.ClaimRefund(ctx, r2.ID, "ann"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ClaimRefund(ctx, r3.ID, "ann"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CompleteManualRefund(ctx, r3.ID, done("ann", "TRK-3")); err != nil {
		t.Fatal(err)
	}

	list := func(raw string) []payment.Refund {
		t.Helper()
		params, err := query.ParseValues(mustValues(t, raw))
		if err != nil {
			t.Fatal(err)
		}
		page, err := f.svc.ListRefunds(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		return page.Items
	}
	queue := list("filter[method]=manual_card_to_card&filter[status][in]=pending,processing")
	if len(queue) != 2 || queue[0].ID != r1.ID || queue[1].ID != r2.ID {
		t.Fatalf("the queue is oldest first and holds pending and processing only: %+v", queue)
	}
	if mine := list("filter[claimed_by]=ann&filter[status]=processing"); len(mine) != 1 || mine[0].ID != r2.ID {
		t.Fatalf("filter by agent: %+v", mine)
	}

	// The database refuses what the service never produces.
	for name, sql := range map[string]string{
		"a full card number":                   `UPDATE mz_refunds SET destination_card_masked = '6037997012345675' WHERE id = '` + r1.ID.String() + `'`,
		"a done manual refund without proof":   `UPDATE mz_refunds SET status = 'done', claimed_by = 'x', claimed_at = now() WHERE id = '` + r1.ID.String() + `'`,
		"a processing refund without an agent": `UPDATE mz_refunds SET status = 'processing' WHERE id = '` + r1.ID.String() + `'`,
	} {
		if _, err := postgres.Exec(ctx, f.db.Querier(ctx), sql); err == nil {
			t.Errorf("the schema accepted %s", name)
		}
	}
	if _, err := postgres.Exec(ctx, f.db.Querier(ctx),
		`UPDATE mz_refunds SET tracking_number = 'TRK-3' WHERE id = $1`, r1.ID); err == nil {
		t.Error("the schema accepted a duplicate (method, tracking_number)")
	}
}

func mustValues(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
