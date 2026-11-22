package mizan_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

// closedYearWithProfit books a share capital of 100,000,000, a profit of 6,000,000 in 1405 and closes the year.
func (e *clockEnv) closedYearWithProfit(t *testing.T) ledger.FiscalYear {
	t.Helper()
	e.post(t, "ap:capital", day1405, ledger.Dr(mizan.AccBank, irr(100_000_000)), ledger.Cr(mizan.AccCapital, irr(100_000_000)))
	e.post(t, "ap:sale", day1405, ledger.Dr(mizan.AccBank, irr(10_000_000)), ledger.Cr(mizan.AccRevenue, irr(10_000_000)))
	e.post(t, "ap:cost", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(4_000_000)), ledger.Cr(mizan.AccBank, irr(4_000_000)))
	e.set(afterYear)
	fy := e.fiscalYear(t, "1405")
	_, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)
	return fy
}

func TestProfitAppropriationFollowsTheCommercialCode(t *testing.T) {
	e := newClockEnv(t)
	// The year is still open: profit is appropriated only after the accounts are closed.
	if _, _, err := e.m.AppropriationFacts(e.ctx, e.fiscalYear(t, "1405").ID, false); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an open year: %v", err)
	}
	fy := e.closedYearWithProfit(t)

	facts, name, err := e.m.AppropriationFacts(e.ctx, fy.ID, false)
	must(t, err)
	if name != "1405" || facts.NetProfit != 6_000_000 || facts.Capital != 100_000_000 || facts.RetainedAtStart != 0 || facts.LegalReserve != 0 {
		t.Fatalf("facts: %+v", facts)
	}
	plan := e.m.Appropriate.Preview(facts)
	if plan.LegalReserve != 300_000 || plan.Distributable != 5_700_000 || plan.MinDividend != 600_000 {
		t.Fatalf("plan: %+v", plan)
	}

	d := func(div, opt int64) appropriation.Decision {
		return appropriation.Decision{Dividend: div, OptionalReserve: opt}
	}
	if _, err := e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, d(599_999, 0), "bad1", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("less than the mandatory 10%%: %v", err)
	}
	if _, err := e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, d(5_000_000, 800_000), "bad2", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("more than distributable: %v", err)
	}

	rec, err := e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, d(600_000, 1_000_000), "ap1", "tester")
	must(t, err)
	if rec.LegalReserve != 300_000 || rec.Journal == nil || rec.PayBy.Format("2006-01-02") != "2027-11-26" { // 1406/01/05 + 8 months = 1406/09/05
		t.Fatalf("record: %+v pay by %s", rec, rec.PayBy.Format("2006-01-02"))
	}
	if e.bal(t, mizan.AccLegalReserve) != 300_000 || e.bal(t, mizan.AccOptionalReserve) != 1_000_000 || e.bal(t, mizan.AccDividendsPayable) != 600_000 ||
		e.bal(t, mizan.AccRetainedEarnings) != 6_000_000-1_900_000 {
		t.Fatalf("books: reserve %d optional %d dividends %d retained %d", e.bal(t, mizan.AccLegalReserve), e.bal(t, mizan.AccOptionalReserve), e.bal(t, mizan.AccDividendsPayable), e.bal(t, mizan.AccRetainedEarnings))
	}
	e.balanced(t)
	if again, err := e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, d(600_000, 1_000_000), "ap1", "tester"); err != nil || again.ID != rec.ID {
		t.Fatalf("a retry returns the same decision: %v", err)
	}
	if _, err := e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, d(600_000, 0), "ap2", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("one decision per year: %v", err)
	}

	// Paying the dividend.
	paid, err := e.m.Appropriate.Pay(e.ctx, rec.ID, appropriation.PayInput{IdemKey: "p1", Amount: irr(400_000), Actor: "tester"})
	must(t, err)
	if paid.Outstanding() != 200_000 || e.bal(t, mizan.AccDividendsPayable) != 200_000 {
		t.Fatalf("after a payment: %+v", paid)
	}
	if again, err := e.m.Appropriate.Pay(e.ctx, rec.ID, appropriation.PayInput{IdemKey: "p1", Amount: irr(400_000)}); err != nil || again.Outstanding() != 200_000 {
		t.Fatalf("a retry must not pay twice: %v", err)
	}
	if _, err := e.m.Appropriate.Pay(e.ctx, rec.ID, appropriation.PayInput{IdemKey: "p2", Amount: irr(200_001)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("more than owed: %v", err)
	}
	if e.m.Appropriate.Overdue(paid) {
		t.Fatal("not overdue yet")
	}
	e.set(time.Date(2027, 12, 1, 0, 0, 0, 0, time.UTC)) // after the eight months
	late, _ := e.m.Appropriate.Record(e.ctx, rec.ID)
	if !e.m.Appropriate.Overdue(late) {
		t.Fatal("200,000 is still owed after the legal deadline")
	}
	e.balanced(t)
}

func TestProfitAppropriationOnlyOnePerYearUnderConcurrency(t *testing.T) {
	e := newClockEnv(t)
	fy := e.closedYearWithProfit(t)
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.m.DeclareAppropriation(e.ctx, fy.ID, false, time.Time{}, appropriation.Decision{Dividend: 600_000}, fmt.Sprintf("c%d", i), "tester")
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case !apperr.Is(err, apperr.KindConflict):
			t.Fatalf("unexpected: %v", err)
		}
	}
	if won != 1 || e.bal(t, mizan.AccLegalReserve) != 300_000 || e.bal(t, mizan.AccDividendsPayable) != 600_000 {
		t.Fatalf("%d decisions won, reserve %d, dividends %d", won, e.bal(t, mizan.AccLegalReserve), e.bal(t, mizan.AccDividendsPayable))
	}
	e.balanced(t)
}
