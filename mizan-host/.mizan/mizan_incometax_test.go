package mizan_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/incometax"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

func TestIncomeTaxOfAFiscalYear(t *testing.T) {
	e := newClockEnv(t)
	e.post(t, "it:capital", day1405, ledger.Dr(mizan.AccBank, irr(100_000_000)), ledger.Cr(mizan.AccCapital, irr(100_000_000)))
	e.post(t, "it:sale", day1405, ledger.Dr(mizan.AccBank, irr(10_000_000)), ledger.Cr(mizan.AccRevenue, irr(10_000_000)))
	e.post(t, "it:cost", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(4_000_000)), ledger.Cr(mizan.AccBank, irr(4_000_000)))
	fy := e.fiscalYear(t, "1405")

	f, err := e.m.IncomeTaxFacts(e.ctx, fy.ID)
	must(t, err)
	if f.Profit != 6_000_000 || f.FiscalYear != "1405" {
		t.Fatalf("facts: %+v", f)
	}
	adj := incometax.Adjustments{AddBacks: []incometax.Item{{Label: "fines", Amount: 1_000_000}}, Deductions: []incometax.Item{{Label: "exempt income", Amount: 500_000}}, DepreciationAdj: 500_000, Credits: 200_000}
	res, err := e.m.IncomeTax.Preview(e.ctx, f, adj)
	must(t, err)
	if res.Taxable != 7_000_000 || res.RateBP != 2500 || res.Tax != 1_750_000 {
		t.Fatalf("preview: %+v", res)
	}
	// The seeded rule is the one of art. 105; an earlier year than any rule has none.
	if _, err := e.m.IncomeTax.RuleFor(e.ctx, time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no rule before 2002: %v", err)
	}
	bad := adj
	bad.Credits = 2_000_000
	if _, err := e.m.DeclareIncomeTax(e.ctx, fy.ID, bad, "bad", "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("credits above the tax: %v", err)
	}

	// Concurrent declarations with different keys: one assessment per fiscal year.
	var wg sync.WaitGroup
	errs := make([]error, 4)
	got := make([]incometax.Assessment, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = e.m.DeclareIncomeTax(e.ctx, fy.ID, adj, fmt.Sprintf("d%d", i), "tester")
		}()
	}
	wg.Wait()
	n := 0
	var a incometax.Assessment
	for i, err := range errs {
		switch {
		case err == nil:
			n++
			a = got[i]
		case !apperr.Is(err, apperr.KindConflict):
			t.Fatalf("unexpected: %v", err)
		}
	}
	if n != 1 || a.Result.Tax != 1_750_000 || a.Payable != 1_550_000 || a.Journal == nil || a.DueOn.Format("2006-01-02") != "2027-07-22" {
		t.Fatalf("%d declared: %+v", n, a)
	}
	if e.bal(t, mizan.AccIncomeTaxExpense) != 1_750_000 || e.bal(t, mizan.AccIncomeTaxPayable) != 1_550_000 || e.bal(t, mizan.AccIncomeTaxPrepaid) != -200_000 {
		t.Fatalf("books: expense %d payable %d prepaid %d", e.bal(t, mizan.AccIncomeTaxExpense), e.bal(t, mizan.AccIncomeTaxPayable), e.bal(t, mizan.AccIncomeTaxPrepaid))
	}
	// The tax is now in the books: the profit the next preview reads is lower, the second declaration is refused either way.
	if _, err := e.m.DeclareIncomeTax(e.ctx, fy.ID, adj, "again", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("second declaration: %v", err)
	}

	// Payments: idempotent, never above the amount owed, concurrent safe.
	if _, err := e.m.IncomeTax.Pay(e.ctx, a.ID, incometax.PayInput{IdemKey: "p-big", Amount: irr(1_550_001)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("above owed: %v", err)
	}
	perr := make([]error, 4)
	for i := range perr {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, perr[i] = e.m.IncomeTax.Pay(e.ctx, a.ID, incometax.PayInput{IdemKey: fmt.Sprintf("p%d", i), Amount: irr(500_000), Actor: "tester"})
		}()
	}
	wg.Wait()
	paid := 0
	for _, err := range perr {
		if err == nil {
			paid++
		} else if !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if paid != 3 { // 3 x 500,000 fit in 1,550,000, the fourth would exceed it
		t.Fatalf("%d payments went through", paid)
	}
	if again, err := e.m.IncomeTax.Pay(e.ctx, a.ID, incometax.PayInput{IdemKey: "p-retry", Amount: irr(50_000)}); err != nil || again.Outstanding() != 0 {
		t.Fatalf("last part: %+v %v", again, err)
	}
	if e.bal(t, mizan.AccIncomeTaxPayable) != 0 {
		t.Fatalf("payable after paying: %d", e.bal(t, mizan.AccIncomeTaxPayable))
	}
}

func TestIncomeTaxLossCarriesForward(t *testing.T) {
	e := newClockEnv(t)
	e.post(t, "l:cost", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(3_000_000)), ledger.Cr(mizan.AccBank, irr(3_000_000)))
	fy := e.fiscalYear(t, "1405")
	a, err := e.m.DeclareIncomeTax(e.ctx, fy.ID, incometax.Adjustments{}, "loss1", "tester")
	must(t, err)
	if a.Result.NewLoss != 3_000_000 || a.Result.Tax != 0 || a.Journal != nil {
		t.Fatalf("a loss year: %+v", a.Result)
	}
	ls, err := e.m.IncomeTax.OpenLosses(e.ctx)
	must(t, err)
	if len(ls) != 1 || ls[0].Remaining != 3_000_000 {
		t.Fatalf("losses: %+v", ls)
	}
	// 1406: a profit of 5,000,000 meets the loss.
	later := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	e.set(later)
	if _, err := e.m.Ledger.OpenFiscalYear(e.ctx, 1406); err != nil {
		t.Fatal(err)
	}
	e.post(t, "l:sale", later, ledger.Dr(mizan.AccBank, irr(5_000_000)), ledger.Cr(mizan.AccRevenue, irr(5_000_000)))
	fy6 := e.fiscalYear(t, "1406")
	b, err := e.m.DeclareIncomeTax(e.ctx, fy6.ID, incometax.Adjustments{}, "profit1", "tester")
	must(t, err)
	if b.Result.LossUsedTotal != 3_000_000 || b.Result.Taxable != 2_000_000 || b.Result.Tax != 500_000 {
		t.Fatalf("1406: %+v", b.Result)
	}
	if ls, _ = e.m.IncomeTax.OpenLosses(e.ctx); len(ls) != 0 {
		t.Fatalf("the loss is used up: %+v", ls)
	}
}
