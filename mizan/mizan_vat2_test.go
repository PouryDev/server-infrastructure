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

func TestVATSeededLawData(t *testing.T) {
	e := newClockEnv(t)
	rs, err := e.m.VAT.Rates(e.ctx)
	must(t, err)
	if len(rs) < 8 {
		t.Fatalf("seeded rates: %d", len(rs))
	}
	r, ok, err := e.m.VAT.RateAt(e.ctx, vat.CategoryGeneral, day1405)
	must(t, err)
	if !ok || r.RateBP != 1000 || r.Verified || r.LevyBP == nil {
		t.Fatalf("1405 general rate is secondary-source data (unverified): %+v", r)
	}
	if r, _, _ = e.m.VAT.RateAt(e.ctx, vat.CategoryGeneral, day1405.AddDate(-1, 0, 0)); r.RateBP != 1000 || !r.Verified {
		t.Fatalf("1404 rate: %+v", r)
	}
	sp, err := e.m.VAT.Compute(e.ctx, vat.CategoryGeneral, 1_000_000, day1405)
	must(t, err)
	if sp.Total != 100_000 || !sp.LevyKnown || sp.Levy != 30_000 || sp.State != 70_000 {
		t.Fatalf("split: %+v", sp)
	}
	if sp, err = e.m.VAT.Compute(e.ctx, "petrol_jet_fuel", 1_000, day1405); err != nil || sp.Total != 300 || sp.LevyKnown {
		t.Fatalf("petrol: %+v %v", sp, err)
	}
	if _, err = e.m.VAT.Compute(e.ctx, "nope", 1, day1405); !apperr.Is(err, apperr.KindNotFound) {
		t.Fatalf("unknown category: %v", err)
	}
	ex, err := e.m.VAT.Exemptions(e.ctx, day1405)
	must(t, err)
	if len(ex) < 25 {
		t.Fatalf("seeded exemptions: %d", len(ex))
	}
	if _, err := e.m.VAT.AddRate(e.ctx, vat.RateInput{Category: "general", EffectiveFrom: time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), RateBP: 1200, Source: "dup date"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken date: %v", err)
	}
}

func TestVATAmendedReturn(t *testing.T) {
	e := newClockEnv(t)
	e.post(t, "a:sale", day1405, ledger.Dr(mizan.AccBank, irr(1_100_000)), ledger.Cr(mizan.AccRevenue, irr(1_000_000)), ledger.Cr(mizan.AccVATPayable, irr(100_000)))
	e.post(t, "a:buy", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(500_000)), ledger.Dr(mizan.AccVATInput, irr(50_000)), ledger.Cr(mizan.AccBank, irr(550_000)))
	e.set(dec25)
	if _, err := e.m.VAT.Amend(e.ctx, vat.AmendInput{IdemKey: "am0", Year: 1405, Period: 3}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("amending an unfiled period: %v", err)
	}
	orig, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "o1", Year: 1405, Period: 3})
	must(t, err)
	if _, err := e.m.VAT.Amend(e.ctx, vat.AmendInput{IdemKey: "am1", Year: 1405, Period: 3}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("nothing to amend: %v", err)
	}
	// A late sale of the period (tax 20,000) and a late purchase (tax 5,000) appear after filing.
	e.post(t, "a:late-sale", day1405, ledger.Dr(mizan.AccBank, irr(220_000)), ledger.Cr(mizan.AccRevenue, irr(200_000)), ledger.Cr(mizan.AccVATPayable, irr(20_000)))
	e.post(t, "a:late-buy", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(50_000)), ledger.Dr(mizan.AccVATInput, irr(5_000)), ledger.Cr(mizan.AccBank, irr(55_000)))

	var wg sync.WaitGroup
	errs := make([]error, 4)
	rets := make([]vat.Return, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rets[i], errs[i] = e.m.VAT.Amend(e.ctx, vat.AmendInput{IdemKey: fmt.Sprintf("am-%d", i), Year: 1405, Period: 3, Actor: "tester"})
		}()
	}
	wg.Wait()
	okN := 0
	var am vat.Return
	for i, err := range errs {
		if err == nil {
			okN++
			am = rets[i]
		} else if !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if okN != 1 || am.Revision != 1 || am.Amends == nil || *am.Amends != orig.ID || am.DeltaOut != 20_000 || am.DeltaIn != 5_000 || am.Output != 120_000 || am.Input != 55_000 || am.Net != 65_000 || am.Journal == nil {
		t.Fatalf("%d amended: %+v", okN, am)
	}
	if e.bal(t, mizan.AccVATSettlement) != 65_000 || e.bal(t, mizan.AccVATPayable) != 0 || e.bal(t, mizan.AccVATInput) != 0 {
		t.Fatalf("books: settlement %d", e.bal(t, mizan.AccVATSettlement))
	}
	// A retry with the key that won returns the same amendment.
	for i, err := range errs {
		if err == nil {
			again, err := e.m.VAT.Amend(e.ctx, vat.AmendInput{IdemKey: fmt.Sprintf("am-%d", i), Year: 1405, Period: 3})
			must(t, err)
			if again.ID != am.ID {
				t.Fatalf("retry: %v", again.ID)
			}
		}
	}
	// A credit note after the amendment: a second amendment books a negative difference.
	e.post(t, "a:credit-note", day1405, ledger.Dr(mizan.AccVATPayable, irr(10_000)), ledger.Dr(mizan.AccRevenue, irr(100_000)), ledger.Cr(mizan.AccBank, irr(110_000)))
	am2, err := e.m.VAT.Amend(e.ctx, vat.AmendInput{IdemKey: "am-second", Year: 1405, Period: 3})
	must(t, err)
	if am2.Revision != 2 || am2.DeltaOut != -10_000 || am2.Net != 55_000 || e.bal(t, mizan.AccVATSettlement) != 55_000 {
		t.Fatalf("second amendment: %+v settlement %d", am2, e.bal(t, mizan.AccVATSettlement))
	}
	// Penalty: unpaid 55,000, 2% per started month after the end of the month following the season (2027-01-20); asked on 2027-03-01 = 2 started months.
	e.set(dec25.AddDate(0, 2, 15))
	p, err := e.m.VAT.PenaltyFor(e.ctx, am2.ID, e.m.Ledger.Today())
	must(t, err)
	if p.Unpaid != 55_000 || p.Months != 2 || p.Penalty != 2_200 || p.RateBP != 200 {
		t.Fatalf("penalty: %+v", p)
	}
}

func TestVATApportionment(t *testing.T) {
	if r, n, err := vat.Apportion4(100, 50, 30, 200, 3_000, 1_000); err != nil || r != 280 || n != 100 { // 100 + 30 + 200*3/4, 50 + 200 - 150
		t.Fatalf("pure split: %d %d %v", r, n, err)
	}
	if _, _, err := vat.Apportion4(0, 0, 0, 10, 0, 0); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("shared input without turnover: %v", err)
	}
	e := newClockEnv(t)
	e.post(t, "p:sale", day1405, ledger.Dr(mizan.AccBank, irr(1_100_000)), ledger.Cr(mizan.AccRevenue, irr(1_000_000)), ledger.Cr(mizan.AccVATPayable, irr(100_000)))
	e.post(t, "p:buy", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(1_300_000)), ledger.Dr(mizan.AccVATInput, irr(130_000)), ledger.Cr(mizan.AccBank, irr(1_430_000)))
	e.set(dec25)
	in := vat.ApportionInput{IdemKey: "ap1", Year: 1405, Period: 3, DirectTaxable: 50_000, DirectExempt: 20_000, Machinery: 10_000, Common: 50_000, TaxableSales: 3_000_000, ExemptSales: 1_000_000, Actor: "tester"}
	bad := in
	bad.IdemKey, bad.Common = "ap-bad", 40_000
	if _, err := e.m.VAT.Apportion(e.ctx, bad); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("parts must add up to the period's input tax: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	got := make([]vat.Apportionment, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := in
			x.IdemKey = fmt.Sprintf("ap-c%d", i)
			got[i], errs[i] = e.m.VAT.Apportion(e.ctx, x)
		}()
	}
	wg.Wait()
	n := 0
	var a vat.Apportionment
	for i, err := range errs {
		if err == nil {
			n++
			a = got[i]
		} else if !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	// recoverable 50,000 + 10,000 + 50,000*3/4 = 97,500; non-recoverable 20,000 + 12,500 = 32,500
	if n != 1 || a.Recoverable != 97_500 || a.NonRecoverable != 32_500 || a.Journal == nil {
		t.Fatalf("%d done: %+v", n, a)
	}
	if e.bal(t, mizan.AccVATInput) != 97_500 || e.bal(t, mizan.AccVATNonDeductible) != 32_500 {
		t.Fatalf("books: input %d expense %d", e.bal(t, mizan.AccVATInput), e.bal(t, mizan.AccVATNonDeductible))
	}
	f, err := e.m.VAT.File(e.ctx, vat.FileInput{IdemKey: "ap-file", Year: 1405, Period: 3})
	must(t, err)
	if f.Input != 97_500 || f.Net != 2_500 {
		t.Fatalf("the return deducts only the deductible part: %+v", f)
	}
	if _, err := e.m.VAT.Apportion(e.ctx, vat.ApportionInput{IdemKey: "ap-late", Year: 1405, Period: 3}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("apportioning after filing: %v", err)
	}
}
