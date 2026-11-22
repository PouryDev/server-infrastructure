package mizan_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/outbox"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/gatewaytest"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

var now = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

func irr(n int64) money.Money { return money.Must(n, money.IRR) }

type env struct {
	m   *mizan.Mizan
	gw  *gatewaytest.Gateway
	db  *postgres.DB
	ctx context.Context
}

func setup(t *testing.T, coding bool) env {
	t.Helper()
	db := testdb.New(t)
	gw := gatewaytest.New()
	m, err := mizan.New(context.Background(), db, mizan.Config{IranianCoding: coding,
		Payment: payment.Config{CallbackBaseURL: "https://api.example.com/v1/gateway-callbacks"}},
		mizan.WithGateway(gw), mizan.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	return env{m, gw, db, context.Background()}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e env) issuedInvoice(t *testing.T, key string, items ...invoice.ItemInput) invoice.Invoice {
	t.Helper()
	inv, err := e.m.Invoices.Create(e.ctx, invoice.CreateInput{IdemKey: key, CustomerRef: "user-1", Currency: money.IRR, Items: items})
	must(t, err)
	inv, err = e.m.Invoices.Issue(e.ctx, inv.ID, "tester")
	must(t, err)
	return inv
}

func (e env) bal(t *testing.T, code string) int64 {
	t.Helper()
	b, err := e.m.Ledger.Balance(e.ctx, code, nil)
	must(t, err)
	return b.Amount()
}

func (e env) balanced(t *testing.T) {
	t.Helper()
	tb, err := e.m.Ledger.TrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if !tb.Balanced {
		t.Fatalf("the books do not balance: %+v", tb)
	}
}

func (e env) payThroughGateway(t *testing.T, p payment.Payment) payment.Payment {
	t.Helper()
	e.gw.Pay(p.GatewayRef)
	res, err := e.m.Payments.HandleCallback(e.ctx, "fake", map[string]string{"ref": p.GatewayRef, "status": "OK"}, "raw", "1.1.1.1")
	must(t, err)
	return res.Payment
}

func TestInvoiceToPaymentToRefund(t *testing.T) {
	e := setup(t, true)
	// 3 x 1,000 + 1 x 10,000 with 15% off, VAT 10%: total 3,300 + 9,350 = 12,650 (VAT 1,150).
	inv := e.issuedInvoice(t, "inv-1",
		invoice.ItemInput{Description: "a", Quantity: 3, UnitPrice: 1000}, invoice.ItemInput{Description: "b", Quantity: 1, UnitPrice: 10000, DiscountBP: 1500})
	if inv.Status != invoice.Issued || inv.Number == nil || *inv.Number != 1 || inv.Total.Amount() != 12650 {
		t.Fatalf("%+v", inv)
	}
	if e.bal(t, mizan.AccReceivable) != 12650 || e.bal(t, mizan.AccRevenue) != 11500 || e.bal(t, mizan.AccVATPayable) != 1150 {
		t.Fatalf("issue entry wrong: ar=%d rev=%d vat=%d", e.bal(t, mizan.AccReceivable), e.bal(t, mizan.AccRevenue), e.bal(t, mizan.AccVATPayable))
	}
	again, err := e.m.Invoices.Issue(e.ctx, inv.ID, "tester")
	if err != nil || *again.Number != 1 || e.bal(t, mizan.AccReceivable) != 12650 {
		t.Fatal("issuing twice must change nothing")
	}

	p, err := e.m.StartInvoicePayment(e.ctx, mizan.StartInvoicePaymentInput{InvoiceID: inv.ID, IdemKey: "attempt-1", Actor: "u1"})
	must(t, err)
	if p.Status != payment.Pending || p.Amount.Amount() != 12650 {
		t.Fatalf("%+v", p)
	}
	paid := e.payThroughGateway(t, p)
	if paid.Status != payment.Paid || paid.SettledJournal == nil {
		t.Fatalf("%+v", paid)
	}
	// A repeated callback must not book the money again.
	e.payThroughGateway(t, p)
	got, _ := e.m.Invoices.Get(e.ctx, inv.ID)
	if got.Status != invoice.Paid || got.Paid.Amount() != 12650 {
		t.Fatalf("%+v", got)
	}
	if e.bal(t, mizan.AccReceivable) != 0 || e.bal(t, mizan.AccGatewayClearing) != 12650 {
		t.Fatalf("payment entry wrong: ar=%d clearing=%d", e.bal(t, mizan.AccReceivable), e.bal(t, mizan.AccGatewayClearing))
	}
	e.balanced(t)

	// Partial refund: the invoice owes again by the refunded amount.
	r, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "ref-1", Amount: irr(2650), Reason: "returned item"})
	must(t, err)
	if r.Status != payment.RefundDone {
		t.Fatalf("%+v", r)
	}
	got, _ = e.m.Invoices.Get(e.ctx, inv.ID)
	if got.Status != invoice.PartiallyPaid || got.Paid.Amount() != 10000 || e.bal(t, mizan.AccReceivable) != 2650 || e.bal(t, mizan.AccGatewayClearing) != 10000 {
		t.Fatalf("after refund: %+v ar=%d", got, e.bal(t, mizan.AccReceivable))
	}
	e.balanced(t)

	// Every domain event reached the outbox in the same transaction as its change.
	n, err := postgres.Value[int](e.ctx, e.db.Querier(e.ctx), `SELECT count(*) FROM mz_outbox WHERE topic IN ('payment.paid', 'invoice.paid', 'payment.refunded', 'invoice.issued')`)
	must(t, err)
	if n != 4 {
		t.Fatalf("outbox has %d of the 4 expected events", n)
	}
	// Every posted journal is traceable to its invoice.
	js, err := e.m.Ledger.JournalsFor(e.ctx, ledger.Ref{Type: "invoice", ID: inv.ID.String()})
	must(t, err)
	if len(js) != 3 { // issue, payment, refund
		t.Fatalf("%d journals for the invoice", len(js))
	}
}

func TestVoidReversesTheSale(t *testing.T) {
	e := setup(t, false)
	inv := e.issuedInvoice(t, "v1", invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: 1000})
	if _, err := e.m.Invoices.Void(e.ctx, inv.ID, "customer cancelled", "tester"); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccReceivable) != 0 || e.bal(t, mizan.AccRevenue) != 0 || e.bal(t, mizan.AccVATPayable) != 0 {
		t.Fatal("void must cancel the sale in the books")
	}
	if _, err := e.m.StartInvoicePayment(e.ctx, mizan.StartInvoicePaymentInput{InvoiceID: inv.ID, IdemKey: "k"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a void invoice cannot be paid: %v", err)
	}
	e.balanced(t)
}

func TestWalletTopUpAndPayInvoice(t *testing.T) {
	e := setup(t, true)
	w, err := e.m.Wallets.Open(e.ctx, "user-1", money.IRR)
	must(t, err)
	if w2, _ := e.m.Wallets.Open(e.ctx, "user-1", money.IRR); w2.ID != w.ID {
		t.Fatal("opening twice must return the same wallet")
	}
	topup, err := e.m.StartWalletTopUp(e.ctx, mizan.TopUpInput{WalletID: w.ID, Amount: irr(5000), IdemKey: "t1"})
	must(t, err)
	e.payThroughGateway(t, topup)
	if bal, _ := e.m.Wallets.Balance(e.ctx, w.ID); bal.Amount() != 5000 {
		t.Fatalf("balance %v", bal)
	}

	cheap := e.issuedInvoice(t, "i-cheap", invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: 4000, TaxBP: ptr(0)})
	dear := e.issuedInvoice(t, "i-dear", invoice.ItemInput{Description: "b", Quantity: 1, UnitPrice: 9000, TaxBP: ptr(0)})

	if _, err := e.m.PayInvoiceFromWallet(e.ctx, dear.ID, w.ID, "p1", "user-1"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("an insufficient wallet must be refused: %v", err)
	}
	if got, _ := e.m.Invoices.Get(e.ctx, dear.ID); got.Status != invoice.Issued || got.Paid.Amount() != 0 {
		t.Fatal("a failed wallet payment must change nothing")
	}
	got, err := e.m.PayInvoiceFromWallet(e.ctx, cheap.ID, w.ID, "p2", "user-1")
	must(t, err)
	again, err := e.m.PayInvoiceFromWallet(e.ctx, cheap.ID, w.ID, "p2", "user-1")
	if err != nil || got.Status != invoice.Paid || again.Status != invoice.Paid {
		t.Fatalf("%+v %v", got, err)
	}
	if bal, _ := e.m.Wallets.Balance(e.ctx, w.ID); bal.Amount() != 1000 {
		t.Fatalf("the wallet was debited %d times", (5000-bal.Amount())/4000)
	}

	// The top-up cannot be refunded once the money was spent.
	if _, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "rf", Amount: irr(5000)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("refund of spent top-up: %v", err)
	}
	if _, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "rf2", Amount: irr(1000)}); err != nil {
		t.Fatalf("refunding the unspent part must work: %v", err)
	}
	if bal, _ := e.m.Wallets.Balance(e.ctx, w.ID); bal.Amount() != 0 {
		t.Fatalf("balance %v", bal)
	}
	e.balanced(t)

	// Wallet accounts are mapped to the chart when it is enabled, so reports classify them.
	rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Unclassified) != 0 {
		t.Fatalf("unclassified: %+v", rep.Unclassified)
	}
}

func TestWalletTransferAndFreeze(t *testing.T) {
	e := setup(t, false)
	a, _ := e.m.Wallets.Open(e.ctx, "a", money.IRR)
	b, _ := e.m.Wallets.Open(e.ctx, "b", money.IRR)
	_, err := e.m.Wallets.Adjust(e.ctx, wallet.Movement{WalletID: a.ID, Amount: irr(1000), Key: "gift"}, true)
	must(t, err)
	if _, err := e.m.Wallets.Transfer(e.ctx, a.ID, b.ID, irr(400), "tr1", "gift", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Wallets.Transfer(e.ctx, a.ID, b.ID, irr(400), "tr1", "gift", "a"); err != nil {
		t.Fatalf("a retried transfer must be a no-op: %v", err)
	}
	if _, err := e.m.Wallets.Transfer(e.ctx, a.ID, b.ID, irr(700), "tr2", "x", "a"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("overdraft: %v", err)
	}
	ba, _ := e.m.Wallets.Balance(e.ctx, a.ID)
	bb, _ := e.m.Wallets.Balance(e.ctx, b.ID)
	if ba.Amount() != 600 || bb.Amount() != 400 {
		t.Fatalf("a=%v b=%v", ba, bb)
	}
	must(t, e.m.Wallets.SetStatus(e.ctx, a.ID, wallet.Frozen, "ops"))
	if _, err := e.m.Wallets.Debit(e.ctx, wallet.Movement{WalletID: a.ID, Amount: irr(1), Key: "d", Counter: mizan.AccRevenue}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a frozen wallet cannot be debited: %v", err)
	}
	if err := e.m.Wallets.SetStatus(e.ctx, a.ID, wallet.Closed, "ops"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a wallet with money cannot be closed: %v", err)
	}
	e.balanced(t)
}

func TestCodingOffStillWorksAndOutboxPublishes(t *testing.T) {
	e := setup(t, false)
	e.issuedInvoice(t, "x", invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: 100})
	rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Nodes) != 0 || len(rep.Unclassified) == 0 {
		t.Fatalf("%+v", rep)
	}
	relay := outbox.NewRelay(e.db, collect{})
	if n, err := relay.Drain(e.ctx); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	st, _ := relay.Stats(e.ctx)
	if st.Pending != 0 {
		t.Fatalf("%+v", st)
	}
	// enabling the chart later maps what already exists
	must(t, e.m.EnableIranianCoding(e.ctx))
	rep, _ = e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	if len(rep.Unclassified) != 0 || len(rep.Nodes) == 0 {
		t.Fatalf("%+v", rep)
	}
}

type collect struct{}

func (collect) Publish(_ context.Context, m outbox.Message) error {
	if _, err := uuid.Parse(m.ID); err != nil || !strings.Contains(m.Topic, ".") {
		return err
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
