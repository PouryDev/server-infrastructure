package payment_test

import (
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
)

func TestStateMachineOnlyAllowsDocumentedTransitions(t *testing.T) {
	all := []payment.Status{payment.Created, payment.Pending, payment.Paid, payment.Failed, payment.Expired, payment.PartiallyRefunded, payment.Refunded}
	allowed := map[[2]payment.Status]bool{
		{payment.Created, payment.Pending}: true, {payment.Created, payment.Failed}: true, {payment.Created, payment.Expired}: true,
		{payment.Pending, payment.Paid}: true, {payment.Pending, payment.Failed}: true, {payment.Pending, payment.Expired}: true,
		{payment.Paid, payment.PartiallyRefunded}: true, {payment.Paid, payment.Refunded}: true,
		{payment.PartiallyRefunded, payment.PartiallyRefunded}: true, {payment.PartiallyRefunded, payment.Refunded}: true,
	}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransitionTo(to); got != allowed[[2]payment.Status{from, to}] {
				t.Errorf("%s -> %s: got %v", from, to, got)
			}
		}
	}
	for _, s := range []payment.Status{payment.Failed, payment.Expired, payment.Refunded} {
		if !s.Terminal() {
			t.Errorf("%s must be terminal", s)
		}
	}
}

func TestRefundAccounting(t *testing.T) {
	p := payment.Payment{Status: payment.Paid, Amount: money.Must(1000, money.IRR), Refunded: money.Zero(money.IRR)}
	if err := p.ApplyRefund(money.Must(400, money.IRR)); err != nil || p.Status != payment.PartiallyRefunded {
		t.Fatalf("partial: %v %s", err, p.Status)
	}
	if err := p.ApplyRefund(money.Must(700, money.IRR)); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("over-refund must conflict: %v", err)
	}
	if err := p.ApplyRefund(money.Must(600, money.IRR)); err != nil || p.Status != payment.Refunded {
		t.Fatalf("full: %v %s", err, p.Status)
	}
	if err := p.ApplyRefund(money.Must(1, money.IRR)); err == nil {
		t.Fatal("a refunded payment accepts nothing more")
	}
}

func TestIllegalTransitionsAreConflicts(t *testing.T) {
	p := payment.Payment{Status: payment.Failed}
	if err := p.MarkPaid("x", "", timeNow()); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("failed -> paid must conflict: %v", err)
	}
	q := payment.Payment{Status: payment.Created}
	if err := q.MarkPaid("x", "", timeNow()); err == nil {
		t.Fatal("created -> paid must be refused: a payment must pass through pending")
	}
}
