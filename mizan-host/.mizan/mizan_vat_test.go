package mizan_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/vat"
)

func TestVATReturnLifecycle(t *testing.T) {
	e := newClockEnv(t)
	// 1405 quarter 3 = Mehr, Aban, Azar (ends 2026-12-21). A sale with 100,000 of tax and a purchase with 50,000 of deductible tax in Aban.
	e.post(t, "v:sale", day1405, ledger.Dr(mizan.AccBank, irr(1_100_000)), ledger.Cr(mizan.AccRevenue, irr(1_000_000)), ledger.Cr(mizan.AccVATPayable, irr(100_000)))
	e.post(t, "v:buy", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(500_000)), ledger.Dr(mizan.AccVATInput, irr(50_000)), ledger.Cr(mizan.AccBank, irr(550_000)))

	f, err := e.m.VAT.Preview(e.ctx, 1405, 0, 3)
	must(t, err)
	if f.Output != 100_000 || f.Input != 50_000 || f.Net != 50_000 || f.Months != 3 || f.DueOn == nil || f.DueOn.Format("2006-01-02") != "2027-01-20" { // the rule seeded by migration 00017 (law of 1400): end of the month after the season
		t.Fatalf("preview: %+v", f)
	}
	if _, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "f0", Year: 1405, Period: 3, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a period that has not ended: %v", err)
	}
	if _, err := e.m.VAT.Preview(e.ctx, 1405, 0, 5); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("period 5 of a quarterly year: %v", err)
	}

	// The legal parameters are versioned data: a newer rule version replaces the seeded one.
	if _, err := e.m.VAT.AddRule(e.ctx, vat.RuleInput{EffectiveFrom: time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), PeriodMonths: 3, Source: "x"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a rule needs a deadline: %v", err)
	}
	_, err = e.m.VAT.AddRule(e.ctx, vat.RuleInput{EffectiveFrom: time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), PeriodMonths: 3, FilingMonthEnd: true, LatePenaltyBP: 200,
		Source: "test rule (to be replaced by the accountant's confirmed text)", Actor: "tester"})
	must(t, err)
	f, err = e.m.VAT.Preview(e.ctx, 1405, 0, 3)
	must(t, err)
	if f.DueOn == nil || f.DueOn.Format("2006-01-02") != "2027-01-20" { // the end of Dey, the month after the quarter
		t.Fatalf("due: %v", f.DueOn)
	}

	e.set(dec25)
	// Two concurrent filers of the same period: exactly one return, one journal.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	rets := make([]vat.Return, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rets[i], errs[i] = e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: fmt.Sprintf("f%d", i+1), Year: 1405, Period: 3, Actor: "tester"})
		}()
	}
	wg.Wait()
	filed := 0
	var ret vat.Return
	for i, err := range errs {
		switch {
		case err == nil:
			filed++
			ret = rets[i]
		case !apperr.Is(err, apperr.KindConflict):
			t.Fatalf("unexpected: %v", err)
		}
	}
	if filed != 1 || ret.Net != 50_000 || ret.Journal == nil {
		t.Fatalf("%d filed: %+v", filed, ret)
	}
	if e.bal(t, mizan.AccVATPayable) != 0 || e.bal(t, mizan.AccVATInput) != 0 || e.bal(t, mizan.AccVATSettlement) != 50_000 {
		t.Fatalf("books after filing: output %d input %d settlement %d", e.bal(t, mizan.AccVATPayable), e.bal(t, mizan.AccVATInput), e.bal(t, mizan.AccVATSettlement))
	}
	e.balanced(t)
	if _, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "other", Year: 1405, Period: 3}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a period is filed once: %v", err)
	}
	if again, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "f1", Year: 1405, Period: 3}); err != nil && !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a retry: %v %v", again, err)
	}

	// Paying the authority.
	if pos, _ := e.m.VAT.Position(e.ctx); pos != 50_000 {
		t.Fatalf("position %d", pos)
	}
	if _, err := e.m.VAT.Pay(e.ctx, vat.PayInput{IdemKey: "p1", Amount: irr(60_000)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("more than owed: %v", err)
	}
	if _, err := e.m.VAT.Pay(e.ctx, vat.PayInput{IdemKey: "p2", Amount: irr(50_000), Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	if pos, _ := e.m.VAT.Position(e.ctx); pos != 0 {
		t.Fatalf("position after payment %d", pos)
	}

	// Next quarter: only a purchase, so an excess credit (art. 17 note 1) that can be refunded on request.
	e.post(t, "v:buy2", time.Date(2027, 1, 5, 0, 0, 0, 0, time.UTC), ledger.Dr(mizan.AccSalaryExpense, irr(300_000)), ledger.Dr(mizan.AccVATInput, irr(30_000)), ledger.Cr(mizan.AccBank, irr(330_000)))
	e.set(time.Date(2027, 4, 5, 10, 0, 0, 0, time.UTC))               // 1406/01/16: the quarter (Dey..Esfand) is over
	if _, err := e.m.Ledger.OpenFiscalYear(e.ctx, 1406); err != nil { // payments and refunds are dated today, which is in the new year
		t.Fatal(err)
	}
	r4, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "q4", Year: 1405, Period: 4, Actor: "tester"})
	must(t, err)
	if r4.Net != -30_000 || r4.OpeningBalance != 50_000 { // the 50,000 was still owed when the quarter began (paid on 25 Dec)
		t.Fatalf("credit quarter: %+v", r4.Figures)
	}
	if pos, _ := e.m.VAT.Position(e.ctx); pos != -30_000 {
		t.Fatalf("credit position %d", pos)
	}
	if _, err := e.m.VAT.ReceiveRefund(e.ctx, vat.PayInput{IdemKey: "r1", Amount: irr(40_000)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a refund above the credit: %v", err)
	}
	if _, err := e.m.VAT.ReceiveRefund(e.ctx, vat.PayInput{IdemKey: "r2", Amount: irr(30_000)}); err != nil {
		t.Fatal(err)
	}
	if pos, _ := e.m.VAT.Position(e.ctx); pos != 0 {
		t.Fatalf("position after the refund %d", pos)
	}
	e.balanced(t)
}
