package mizan_test

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
)

func paidOut(agent, tracking string) payment.CompleteManualInput {
	return payment.CompleteManualInput{Agent: agent, TrackingNumber: tracking, PaidAt: now.Add(-time.Hour), DestinationCardMasked: "603799******1234"}
}

// A manual refund leaves OUR bank account, not the gateway's clearing account, and only when it is completed.
func TestManualRefundCreditsTheBank(t *testing.T) {
	e := setup(t, true)
	e.gw.NoRefunds = true
	inv := e.issuedInvoice(t, "inv", invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: 10000, TaxBP: ptr(0)})
	p, err := e.m.StartInvoicePayment(e.ctx, mizan.StartInvoicePaymentInput{InvoiceID: inv.ID, IdemKey: "a1"})
	must(t, err)
	e.payThroughGateway(t, p)
	clearing := e.bal(t, mizan.AccGatewayClearing)

	r, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r1", Amount: irr(4000), Reason: "returned"})
	must(t, err)
	if r.Method != payment.RefundManualCardToCard || r.Status != payment.RefundPending {
		t.Fatalf("%+v", r)
	}
	_, err = e.m.Payments.ClaimRefund(e.ctx, r.ID, "ann")
	must(t, err)
	if e.bal(t, mizan.AccBank) != 0 || e.bal(t, mizan.AccReceivable) != 0 {
		t.Fatal("the books must not move before the refund is completed")
	}
	done, err := e.m.Payments.CompleteManualRefund(e.ctx, r.ID, paidOut("ann", "TRK-1"))
	must(t, err)
	if done.JournalID == nil {
		t.Fatal("a completed refund points at its journal")
	}
	if e.bal(t, mizan.AccBank) != -4000 || e.bal(t, mizan.AccGatewayClearing) != clearing || e.bal(t, mizan.AccReceivable) != 4000 {
		t.Fatalf("manual refund must credit bank.main and leave the gateway clearing alone: bank=%d clearing=%d ar=%d",
			e.bal(t, mizan.AccBank), e.bal(t, mizan.AccGatewayClearing), e.bal(t, mizan.AccReceivable))
	}
	// A retry books nothing more.
	_, err = e.m.Payments.CompleteManualRefund(e.ctx, r.ID, paidOut("ann", "TRK-1"))
	must(t, err)
	if e.bal(t, mizan.AccBank) != -4000 {
		t.Fatal("a retried completion must not book twice")
	}

	// The rest of the payment through the manual path too.
	r2, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: p.ID, IdemKey: "r2"})
	must(t, err)
	if r2.Amount.Amount() != 6000 {
		t.Fatalf("%+v", r2)
	}
	_, err = e.m.Payments.ClaimRefund(e.ctx, r2.ID, "bob")
	must(t, err)
	_, err = e.m.Payments.CompleteManualRefund(e.ctx, r2.ID, paidOut("bob", "TRK-2"))
	must(t, err)
	if got, _ := e.m.Payments.Get(e.ctx, p.ID); got.Status != payment.Refunded {
		t.Fatalf("%+v", got)
	}
	if e.bal(t, mizan.AccBank) != -10000 || e.bal(t, mizan.AccReceivable) != 10000 {
		t.Fatalf("bank=%d ar=%d", e.bal(t, mizan.AccBank), e.bal(t, mizan.AccReceivable))
	}
	e.balanced(t)

	// A gateway refund still pays out of the clearing account.
	e.gw.NoRefunds = false
	inv2 := e.issuedInvoice(t, "inv2", invoice.ItemInput{Description: "b", Quantity: 1, UnitPrice: 5000, TaxBP: ptr(0)})
	p2, err := e.m.StartInvoicePayment(e.ctx, mizan.StartInvoicePaymentInput{InvoiceID: inv2.ID, IdemKey: "a2"})
	must(t, err)
	e.payThroughGateway(t, p2)
	before := e.bal(t, mizan.AccGatewayClearing)
	g, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: p2.ID, IdemKey: "g1", Amount: irr(1000)})
	must(t, err)
	if g.Method != payment.RefundViaGateway || g.Status != payment.RefundDone {
		t.Fatalf("%+v", g)
	}
	if e.bal(t, mizan.AccGatewayClearing) != before-1000 || e.bal(t, mizan.AccBank) != -10000 {
		t.Fatalf("a gateway refund credits the clearing account: clearing=%d bank=%d", e.bal(t, mizan.AccGatewayClearing), e.bal(t, mizan.AccBank))
	}
	e.balanced(t)
}

// The wallet must still hold the money when the agent is about to pay it out, not only when it was requested.
func TestManualRefundOfWalletTopUp(t *testing.T) {
	e := setup(t, false)
	e.gw.NoRefunds = true
	w, err := e.m.Wallets.Open(e.ctx, "user-1", "IRR")
	must(t, err)
	topup, err := e.m.StartWalletTopUp(e.ctx, mizan.TopUpInput{WalletID: w.ID, Amount: irr(5000), IdemKey: "t1"})
	must(t, err)
	e.payThroughGateway(t, topup)
	inv := e.issuedInvoice(t, "i", invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: 3500, TaxBP: ptr(0)})

	if _, err := e.m.PayInvoiceFromWallet(e.ctx, inv.ID, w.ID, "p1", "user-1"); err != nil { // wallet: 1,500 left
		t.Fatal(err)
	}
	if _, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "big", Amount: irr(2000)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a manual refund of money the wallet no longer holds is blocked at request: %v", err)
	}
	r1, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "r1", Amount: irr(1000)})
	must(t, err)
	if _, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "r2", Amount: irr(600)}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("pending refunds count together against the balance (1,000 + 600 > 1,500): %v", err)
	}
	// The customer spends the money after the request: the agent must not be sent to transfer it.
	inv2 := e.issuedInvoice(t, "i2", invoice.ItemInput{Description: "b", Quantity: 1, UnitPrice: 1200, TaxBP: ptr(0)})
	if _, err := e.m.PayInvoiceFromWallet(e.ctx, inv2.ID, w.ID, "p2", "user-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Payments.ClaimRefund(e.ctx, r1.ID, "ann"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("claiming must be refused when the wallet no longer holds the amount: %v", err)
	}
	if got, _ := e.m.Payments.RefundByID(e.ctx, r1.ID); got.Status != payment.RefundPending || got.ClaimedBy != "" {
		t.Fatalf("a refused claim leaves the refund pending: %+v", got)
	}
	_, err = e.m.Payments.CancelRefund(e.ctx, r1.ID, "support", "wallet was spent", false)
	must(t, err)

	// With enough money the whole path works and debits the wallet against the bank account.
	r3, err := e.m.Payments.Refund(e.ctx, payment.RefundInput{PaymentID: topup.ID, IdemKey: "r3", Amount: irr(300)})
	must(t, err)
	_, err = e.m.Payments.ClaimRefund(e.ctx, r3.ID, "ann")
	must(t, err)
	_, err = e.m.Payments.CompleteManualRefund(e.ctx, r3.ID, paidOut("ann", "TRK-W1"))
	must(t, err)
	if bal, _ := e.m.Wallets.Balance(e.ctx, w.ID); bal.Amount() != 0 {
		t.Fatalf("balance %v", bal)
	}
	if e.bal(t, mizan.AccBank) != -300 {
		t.Fatalf("bank=%d", e.bal(t, mizan.AccBank))
	}
	e.balanced(t)
}
