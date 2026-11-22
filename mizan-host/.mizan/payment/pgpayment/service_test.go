package pgpayment_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/gatewaytest"
	"github.com/PouryDev/server-infrastructure/mizan/payment/pgpayment"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

type settlement struct {
	paid, refunded atomic.Int32
	onPaidErr      error
	canRefundErr   error
}

func (s *settlement) OnPaid(context.Context, payment.Payment) (*uuid.UUID, error) {
	s.paid.Add(1)
	return nil, s.onPaidErr
}
func (s *settlement) CanRefund(context.Context, payment.Payment, int64) error { return s.canRefundErr }
func (s *settlement) OnRefunded(context.Context, payment.Payment, payment.Refund) (*uuid.UUID, error) {
	s.refunded.Add(1)
	return nil, nil
}

type fixture struct {
	svc *payment.Service
	gw  *gatewaytest.Gateway
	st  *settlement
	now *time.Time
}

func setup(t *testing.T) fixture {
	t.Helper()
	db := testdb.New(t)
	repo, err := pgpayment.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gw, st := gatewaytest.New(), &settlement{}
	now := time.Now()
	svc := payment.NewService(repo, db, trail.Discard{}, trail.Discard{},
		payment.Config{DefaultGateway: "fake", CallbackBaseURL: "https://api.example.com/cb", AllowedReturnHosts: []string{"shop.example.com"}},
		payment.WithGateway(gw), payment.WithSettlement("order", st), payment.WithClock(func() time.Time { return now }))
	return fixture{svc, gw, st, &now}
}

func (f fixture) initiate(t *testing.T, key string, amount int64) payment.Payment {
	t.Helper()
	p, err := f.svc.Initiate(context.Background(), payment.InitiateInput{
		IdemKey: key, Purpose: payment.Purpose{Type: "order", ID: "o-" + key}, Amount: money.Must(amount, money.IRR), PayerRef: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f fixture) callback(ref, status string) (payment.CallbackResult, error) {
	return f.svc.HandleCallback(context.Background(), "fake", map[string]string{"ref": ref, "status": status}, "raw:"+ref+status, "1.2.3.4")
}

func TestInitiateIsIdempotent(t *testing.T) {
	f := setup(t)
	a := f.initiate(t, "k1", 1000)
	b := f.initiate(t, "k1", 1000)
	if a.ID != b.ID || a.Status != payment.Pending || b.GatewayRef != a.GatewayRef || a.PayURL == "" {
		t.Fatalf("retry must return the same pending payment: %+v %+v", a, b)
	}
	if a.CallbackURL != "https://api.example.com/cb/fake" {
		t.Fatalf("callback url %q", a.CallbackURL)
	}
	_, err := f.svc.Initiate(context.Background(), payment.InitiateInput{IdemKey: "k1", Purpose: payment.Purpose{Type: "order", ID: "o-k1"}, Amount: money.Must(999, money.IRR)})
	if apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("same key, different amount must conflict: %v", err)
	}
	_, err = f.svc.Initiate(context.Background(), payment.InitiateInput{IdemKey: "k2", Purpose: payment.Purpose{Type: "order", ID: "x"},
		Amount: money.Must(5, money.IRR), ReturnURL: "https://evil.example.org/x"})
	if apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("return url on a foreign host must be refused: %v", err)
	}
}

func TestPaidThroughCallbackSettlesOnce(t *testing.T) {
	f := setup(t)
	p := f.initiate(t, "k", 5000)
	f.gw.Pay(p.GatewayRef)

	var wg sync.WaitGroup
	for range 10 { // the payer's redirect and gateway retries arrive together
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.callback(p.GatewayRef, "OK"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := f.svc.Get(context.Background(), p.ID)
	if got.Status != payment.Paid || got.GatewayTxnRef == "" || got.PaidAt == nil || got.CardPAN == "" {
		t.Fatalf("payment %+v", got)
	}
	if f.st.paid.Load() != 1 {
		t.Fatalf("settlement ran %d times, want exactly once", f.st.paid.Load())
	}
	hist, _ := f.svc.History(context.Background(), p.ID)
	if len(hist) != 2 || hist[0].To != payment.Pending || hist[1].To != payment.Paid {
		t.Fatalf("history %+v", hist)
	}
}

func TestForgedCallbackIsNotTrusted(t *testing.T) {
	f := setup(t)
	p := f.initiate(t, "k", 5000)
	// The payer's browser says OK, but the gateway has no payment.
	res, err := f.callback(p.GatewayRef, "OK")
	if err != nil || res.Payment.Status != payment.Failed || f.st.paid.Load() != 0 {
		t.Fatalf("forged success must fail the payment: %+v err=%v settled=%d", res.Payment, err, f.st.paid.Load())
	}
}

func TestCancelledByPayer(t *testing.T) {
	f := setup(t)
	p := f.initiate(t, "k", 5000)
	res, err := f.callback(p.GatewayRef, "NOK")
	if err != nil || res.Payment.Status != payment.Failed || f.gw.VerifyCalls != 0 {
		t.Fatalf("%+v %v verifyCalls=%d", res.Payment, err, f.gw.VerifyCalls)
	}
}

func TestVerifyOutageKeepsPendingAndSweeperRecovers(t *testing.T) {
	f := setup(t)
	p := f.initiate(t, "k", 5000)
	f.gw.Pay(p.GatewayRef)
	f.gw.VerifyErr = errors.New("gateway timeout")
	if _, err := f.callback(p.GatewayRef, "OK"); apperr.KindOf(err) != apperr.KindUnavailable {
		t.Fatalf("outage must surface as unavailable: %v", err)
	}
	if got, _ := f.svc.Get(context.Background(), p.ID); got.Status != payment.Pending {
		t.Fatalf("must stay pending, got %s", got.Status)
	}
	f.gw.VerifyErr = nil
	*f.now = f.now.Add(10 * time.Minute)
	res, err := f.svc.Sweep(context.Background(), 5*time.Minute, 100)
	if err != nil || res.Paid != 1 || f.st.paid.Load() != 1 {
		t.Fatalf("sweep %+v %v settled=%d", res, err, f.st.paid.Load())
	}
}

func TestLostCallbackIsRecoveredAndUnpaidExpires(t *testing.T) {
	f := setup(t)
	paid := f.initiate(t, "paid", 1000)
	abandoned := f.initiate(t, "abandoned", 2000)
	f.gw.Pay(paid.GatewayRef) // the payer paid but the callback never reached us

	*f.now = f.now.Add(10 * time.Minute) // older than minAge, not yet expired
	if r, _ := f.svc.Sweep(context.Background(), 5*time.Minute, 100); r.Paid != 1 || r.Expired != 0 {
		t.Fatalf("first sweep %+v", r)
	}
	if a, _ := f.svc.Get(context.Background(), abandoned.ID); a.Status != payment.Pending {
		t.Fatalf("an unexpired unpaid payment stays pending, got %s", a.Status)
	}
	*f.now = f.now.Add(time.Hour)
	if r, _ := f.svc.Sweep(context.Background(), 5*time.Minute, 100); r.Expired != 1 {
		t.Fatalf("second sweep %+v", r)
	}
	if a, _ := f.svc.Get(context.Background(), abandoned.ID); a.Status != payment.Expired {
		t.Fatalf("got %s", a.Status)
	}
	// A payment that was paid after we expired it cannot come back: the machine has no such edge.
	f.gw.Pay(abandoned.GatewayRef)
	res, err := f.callback(abandoned.GatewayRef, "OK")
	if err != nil || res.Payment.Status != payment.Expired || !res.Replay {
		t.Fatalf("late callback: %+v %v", res, err)
	}
}

func TestAmountMismatchIsNotApplied(t *testing.T) {
	f := setup(t)
	p := f.initiate(t, "k", 5000)
	f.gw.Pay(p.GatewayRef)
	less := money.Must(4000, money.IRR)
	f.gw.Collected = &less
	res, err := f.callback(p.GatewayRef, "OK")
	if err != nil || res.Payment.Status != payment.Failed || res.Payment.FailureCode != "amount_mismatch" || f.st.paid.Load() != 0 {
		t.Fatalf("%+v %v settled=%d", res.Payment, err, f.st.paid.Load())
	}
}

func TestSettlementFailureHandling(t *testing.T) {
	f := setup(t)
	// A business problem (e.g. the invoice was voided meanwhile) must not lose the captured money.
	f.st.onPaidErr = apperr.Conflict("invoice is void")
	p := f.initiate(t, "biz", 5000)
	f.gw.Pay(p.GatewayRef)
	res, err := f.callback(p.GatewayRef, "OK")
	if err != nil || res.Payment.Status != payment.Paid || res.Payment.Metadata["settlement_error"] == "" {
		t.Fatalf("%+v %v", res.Payment, err)
	}
	if list, _ := f.svc.UnsettledPaid(context.Background(), 10); len(list) != 1 || list[0].ID != p.ID {
		t.Fatalf("unsettled list %+v", list)
	}

	// An infrastructure problem rolls everything back so the retry can succeed.
	f.st.onPaidErr = apperr.Unavailable("db down")
	q := f.initiate(t, "infra", 7000)
	f.gw.Pay(q.GatewayRef)
	if _, err := f.callback(q.GatewayRef, "OK"); err == nil {
		t.Fatal("expected an error")
	}
	if got, _ := f.svc.Get(context.Background(), q.ID); got.Status != payment.Pending {
		t.Fatalf("must stay pending, got %s", got.Status)
	}
	f.st.onPaidErr = nil
	if res, err := f.callback(q.GatewayRef, "OK"); err != nil || res.Payment.Status != payment.Paid {
		t.Fatalf("retry: %+v %v", res.Payment, err)
	}
}

func TestRefunds(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p := f.initiate(t, "k", 1000)
	f.gw.Pay(p.GatewayRef)
	if _, err := f.callback(p.GatewayRef, "OK"); err != nil {
		t.Fatal(err)
	}
	r1, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r1", Amount: money.Must(300, money.IRR), Reason: "partial"})
	if err != nil || r1.Status != payment.RefundDone {
		t.Fatalf("%+v %v", r1, err)
	}
	again, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r1", Amount: money.Must(300, money.IRR)})
	if err != nil || again.ID != r1.ID || f.st.refunded.Load() != 1 {
		t.Fatalf("a retried refund must not refund twice: %+v %v refunded=%d", again, err, f.st.refunded.Load())
	}
	if _, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r2", Amount: money.Must(701, money.IRR)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("over-refund must conflict: %v", err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != payment.PartiallyRefunded || got.Refunded.Amount() != 300 {
		t.Fatalf("%+v", got)
	}

	// A gateway without a refund API: the refund waits for a manual payout.
	f.gw.NoRefunds = true
	r2, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r3", Reason: "rest"})
	if err != nil || r2.Status != payment.RefundPending || r2.Amount.Amount() != 700 {
		t.Fatalf("%+v %v", r2, err)
	}
	if _, err := f.svc.Refund(ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r4", Amount: money.Must(1, money.IRR)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a pending refund reserves its amount: %v", err)
	}
	if r2.Method != payment.RefundManualCardToCard {
		t.Fatalf("ErrNotSupported must turn the refund into a manual one: %+v", r2)
	}
}

func TestGatewayRejectionFailsThePayment(t *testing.T) {
	f := setup(t)
	f.gw.RequestErr = &payment.RejectedError{Code: "-9", Message: "bad amount"}
	_, err := f.svc.Initiate(context.Background(), payment.InitiateInput{IdemKey: "k", Purpose: payment.Purpose{Type: "order", ID: "1"}, Amount: money.Must(1, money.IRR)})
	if err == nil {
		t.Fatal("expected an error")
	}
	f.gw.RequestErr = errors.New("timeout")
	p, err := f.svc.Initiate(context.Background(), payment.InitiateInput{IdemKey: "k2", Purpose: payment.Purpose{Type: "order", ID: "2"}, Amount: money.Must(1, money.IRR)})
	if apperr.KindOf(err) != apperr.KindUnavailable || p.Status != payment.Created {
		t.Fatalf("a transient failure leaves the payment created for a retry: %+v %v", p, err)
	}
	f.gw.RequestErr = nil
	p2, err := f.svc.Initiate(context.Background(), payment.InitiateInput{IdemKey: "k2", Purpose: payment.Purpose{Type: "order", ID: "2"}, Amount: money.Must(1, money.IRR)})
	if err != nil || p2.ID != p.ID || p2.Status != payment.Pending {
		t.Fatalf("the retry must resume the same payment: %+v %v", p2, err)
	}
}
