package mizan_test

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/payout"
)

func shebaOf(body string) string {
	rem := 0
	for _, r := range body + "182700" {
		rem = (rem*10 + int(r-'0')) % 97
	}
	return "IR" + strconv.Itoa(98 - rem + 100)[1:] + body
}

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

// terms adds the default terms: 10% commission, 7 days of hold.
func (e env) terms(t *testing.T, scope payout.ScopeKind, key string, from time.Time, bp, hold int) payout.Terms {
	t.Helper()
	noVAT := 0 // most tests look at the commission only; VAT has its own test
	tm, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "t", Scope: scope, ScopeKey: key, EffectiveFrom: from, RateBP: bp, HoldDays: hold, VATBP: &noVAT, Actor: "tester"})
	must(t, err)
	return tm
}

func (e env) defaultTerms(t *testing.T) { e.terms(t, payout.ScopeDefault, "", d(2026, 4, 1), 1000, 7) }

func (e env) seller(t *testing.T, code string) payout.Seller {
	t.Helper()
	s, err := e.m.Payout.AddSeller(e.ctx, payout.SellerInput{Code: code, Name: "Seller " + code, Sheba: shebaOf("0170000000123456789012"), Actor: "tester"})
	must(t, err)
	return s
}

func (e env) sale(t *testing.T, key string, s payout.Seller, gross int64, delivered *time.Time) payout.Sale {
	t.Helper()
	in := payout.SaleInput{IdemKey: key, SellerID: s.ID, Gross: irr(gross), Reference: "order-" + key, Delivered: delivered, Actor: "tester"}
	if delivered != nil {
		in.Date = *delivered // sold and delivered the same day, in the past
	}
	sale, err := e.m.Payout.RecordSale(e.ctx, in)
	must(t, err)
	return sale
}

func at(t time.Time) *time.Time { return &t }

func TestSaleBooksCommissionAndIsIdempotent(t *testing.T) {
	e := setup(t, true)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	sale := e.sale(t, "k1", s, 1_000_001, nil) // 10% of 1,000,001 = 100,000.1 -> 100,000: the seller gets the remainder
	if sale.Commission != irr(100_000) || sale.Net != irr(900_001) || sale.Journal == nil || sale.Phase(now) != "awaiting_delivery" || sale.RateBP != 1000 {
		t.Fatalf("%+v", sale)
	}
	for acc, want := range map[string]int64{mizan.AccReceivable: 1_000_001, s.AccountCode: 900_001, mizan.AccCommissionRevenue: 100_000} {
		if got := e.bal(t, acc); got != want {
			t.Errorf("%s = %d, want %d", acc, got, want)
		}
	}
	again := e.sale(t, "k1", s, 1_000_001, nil)
	if again.ID != sale.ID || e.bal(t, mizan.AccReceivable) != 1_000_001 {
		t.Fatal("a retry must return the sale and book nothing")
	}
	_, err := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "k1", SellerID: s.ID, Gross: irr(5), Reference: "order-k1"})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a reused key with other data must conflict: %v", err)
	}
	e.balanced(t)
	rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Unclassified) != 0 {
		t.Fatalf("payout accounts must map to the chart: %+v", rep.Unclassified)
	}
}

func TestPayoutWorksWithoutAChart(t *testing.T) {
	e := setup(t, false) // coding stays optional
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	e.sale(t, "k1", s, 100_000, at(d(2026, 10, 1)))
	b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b1", Actor: "tester"})
	must(t, err)
	if b.Total != irr(90_000) || e.bal(t, s.AccountCode) != 0 {
		t.Fatalf("%+v", b)
	}
	e.balanced(t)
}

func TestTermsResolutionAndSnapshots(t *testing.T) {
	e := setup(t, false)
	s, other := e.seller(t, "S1"), e.seller(t, "S2")
	_, err := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "x", SellerID: s.ID, Gross: irr(1000), Date: d(2026, 1, 1)})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no terms in force before the seeded version: %v", err)
	}
	def := e.terms(t, payout.ScopeDefault, "", d(2026, 4, 1), 1000, 7)
	e.terms(t, payout.ScopeCategory, "books", d(2026, 4, 1), 500, 3)
	e.terms(t, payout.ScopeSeller, s.ID.String(), d(2026, 4, 1), 200, 0)
	if _, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "dup", EffectiveFrom: d(2026, 4, 1), RateBP: 1}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken date must conflict: %v", err)
	}
	if _, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "ghost", Scope: payout.ScopeSeller, ScopeKey: uuid.NewString(), EffectiveFrom: d(2026, 5, 1)}); !apperr.Is(err, apperr.KindNotFound) {
		t.Fatalf("terms for an unknown seller: %v", err)
	}

	rate := func(sel payout.Seller, cat string) int {
		sale, err := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: sel.Code + cat, SellerID: sel.ID, Gross: irr(10_000), Category: cat})
		must(t, err)
		return sale.RateBP
	}
	if rate(other, "") != 1000 || rate(other, "books") != 500 || rate(s, "books") != 200 || rate(other, "toys") != 1000 {
		t.Fatal("seller terms beat category terms, which beat the default")
	}
	// a new default version applies from its day; the sale already booked keeps its snapshot
	old, _ := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "old", SellerID: other.ID, Gross: irr(10_000)})
	e.terms(t, payout.ScopeDefault, "", d(2026, 11, 9), 1500, 14)
	fresh, _ := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "fresh", SellerID: other.ID, Gross: irr(10_000)})
	past, _ := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "past", SellerID: other.ID, Gross: irr(10_000), Date: d(2026, 11, 8)})
	got, _ := e.m.Payout.Sale(e.ctx, old.ID)
	if got.RateBP != 1000 || fresh.RateBP != 1500 || fresh.HoldDays != 14 || past.RateBP != 1000 {
		t.Fatalf("old %d, fresh %d/%d, back-dated %d", got.RateBP, fresh.RateBP, fresh.HoldDays, past.RateBP)
	}
	// terms a sale used are permanent, unused ones can be corrected
	if err := e.m.Payout.DeleteTerms(e.ctx, def.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("used terms: %v", err)
	}
	unused := e.terms(t, payout.ScopeCategory, "toys", d(2027, 1, 1), 100, 1)
	must(t, e.m.Payout.DeleteTerms(e.ctx, unused.ID, "tester"))
	e.balanced(t)
}

func TestHoldReleaseAndPayout(t *testing.T) {
	e := setup(t, true)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	waiting, err := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "wait", SellerID: s.ID, Gross: irr(100_000), Date: d(2026, 11, 1)}) // never delivered: not payable
	must(t, err)
	held := e.sale(t, "held", s, 200_000, at(d(2026, 11, 5)))    // delivered 4 days ago, hold 7: releases on 11-12
	ready := e.sale(t, "ready", s, 300_000, at(d(2026, 10, 20))) // released on 10-27
	if held.ReleaseAt == nil || !held.ReleaseAt.Equal(d(2026, 11, 12)) || held.Phase(now) != "held" || ready.Phase(now) != "available" {
		t.Fatalf("held %+v ready %s", held.ReleaseAt, ready.Phase(now))
	}
	bal, err := e.m.Payout.Balance(e.ctx, s.ID)
	must(t, err)
	if bal.Owed != irr(540_000) || bal.Totals.AwaitingDelivery != 90_000 || bal.Totals.Held != 180_000 || bal.Totals.Available != 270_000 || bal.Totals.Paid != 0 {
		t.Fatalf("%+v", bal)
	}

	b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b1", Actor: "tester"})
	must(t, err)
	if b.Total != irr(270_000) || b.SaleCount != 1 || len(b.Items) != 1 || b.Items[0].Amount != irr(270_000) || b.Items[0].Sheba != s.Sheba || b.Journal == nil {
		t.Fatalf("%+v", b)
	}
	if e.bal(t, s.AccountCode) != 270_000 || e.bal(t, mizan.AccBank) != -270_000 {
		t.Fatalf("seller account %d, bank %d", e.bal(t, s.AccountCode), e.bal(t, mizan.AccBank))
	}
	again, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b1"})
	must(t, err)
	if again.ID != b.ID || len(again.Items) != 1 || e.bal(t, mizan.AccBank) != -270_000 {
		t.Fatal("a retry returns the stored batch and pays nothing again")
	}
	if _, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b2"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("nothing is due any more: %v", err)
	}
	if _, err := e.m.Payout.ReleaseNow(e.ctx, waiting.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an undelivered sale cannot be released: %v", err)
	}
	// deliver the waiting sale and release the held one early
	_, err = e.m.Payout.MarkDelivered(e.ctx, waiting.ID, d(2026, 11, 1), "tester")
	must(t, err)
	rel, err := e.m.Payout.ReleaseNow(e.ctx, held.ID, "tester")
	must(t, err)
	if rel.Phase(now) != "available" {
		t.Fatal(rel.Phase(now))
	}
	b2, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b2"})
	must(t, err)
	// the early-released sale (180,000) and the delivered-on-11-01 one (released 11-08: 90,000) are both due now
	if b2.Total != irr(270_000) || b2.SaleCount != 2 {
		t.Fatalf("%+v", b2)
	}
	if e.bal(t, s.AccountCode) != 0 {
		t.Fatalf("everything was paid, the seller's account is %d", e.bal(t, s.AccountCode))
	}
	if _, err := e.m.Payout.ReturnSale(e.ctx, ready.ID, "customer changed mind", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a paid sale cannot be returned: %v", err)
	}
	e.balanced(t)
}

func TestDeliveryRules(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	sale := e.sale(t, "k", s, 10_000, nil)
	if _, err := e.m.Payout.MarkDelivered(e.ctx, sale.ID, d(2026, 11, 10), "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("future delivery: %v", err)
	}
	if _, err := e.m.Payout.MarkDelivered(e.ctx, sale.ID, d(2026, 11, 8), "tester"); !apperr.Is(err, apperr.KindInvalid) {
		// the sale was booked today, delivery cannot precede it
		t.Fatalf("delivery before the sale: %v", err)
	}
	got, err := e.m.Payout.MarkDelivered(e.ctx, sale.ID, time.Time{}, "tester") // today
	must(t, err)
	again, err := e.m.Payout.MarkDelivered(e.ctx, sale.ID, d(2026, 11, 9), "tester")
	must(t, err)
	if got.ReleaseAt == nil || !got.ReleaseAt.Equal(d(2026, 11, 16)) || !again.ReleaseAt.Equal(*got.ReleaseAt) {
		t.Fatalf("%+v", got)
	}
}

func TestReturnBeforePayoutReversesTheSale(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	sale := e.sale(t, "k", s, 100_000, at(d(2026, 10, 1)))
	if _, err := e.m.Payout.ReturnSale(e.ctx, sale.ID, "", "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a reason is required: %v", err)
	}
	r, err := e.m.Payout.ReturnSale(e.ctx, sale.ID, "customer returned the goods", "tester")
	must(t, err)
	if r.Status != payout.SaleReturned || r.ReturnJrnl == nil {
		t.Fatalf("%+v", r)
	}
	for _, acc := range []string{mizan.AccReceivable, s.AccountCode, mizan.AccCommissionRevenue} {
		if e.bal(t, acc) != 0 {
			t.Errorf("%s must be back to zero, is %d", acc, e.bal(t, acc))
		}
	}
	if _, err := e.m.Payout.ReturnSale(e.ctx, sale.ID, "again", "tester"); err != nil {
		t.Fatalf("returning twice is idempotent: %v", err)
	}
	if _, err := e.m.Payout.MarkDelivered(e.ctx, sale.ID, time.Time{}, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a returned sale cannot be delivered: %v", err)
	}
	if _, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a returned sale is never paid: %v", err)
	}
	e.balanced(t)
}

func TestBlockedSellerIsNotPaid(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	a, b := e.seller(t, "A"), e.seller(t, "B")
	e.sale(t, "a", a, 100_000, at(d(2026, 10, 1)))
	e.sale(t, "b", b, 200_000, at(d(2026, 10, 1)))
	if _, err := e.m.Payout.Block(e.ctx, a.ID, "", "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a reason is required: %v", err)
	}
	blocked, err := e.m.Payout.Block(e.ctx, a.ID, "chargeback investigation", "tester")
	must(t, err)
	if blocked.Status != payout.SellerBlocked {
		t.Fatal("not blocked")
	}
	e.sale(t, "a2", a, 50_000, at(d(2026, 10, 2))) // sales are still booked for a blocked seller
	batch, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b1"})
	must(t, err)
	if len(batch.Items) != 1 || batch.Items[0].SellerID != b.ID || e.bal(t, a.AccountCode) != 135_000 {
		t.Fatalf("only the other seller is paid: %+v", batch)
	}
	if _, err := e.m.Payout.Unblock(e.ctx, a.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	batch2, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b2", SellerIDs: []uuid.UUID{a.ID}})
	must(t, err)
	if batch2.Total != irr(135_000) || e.bal(t, a.AccountCode) != 0 {
		t.Fatalf("%+v", batch2)
	}
	e.balanced(t)
}

func TestFullCommissionAndZeroCommission(t *testing.T) {
	e := setup(t, false)
	s := e.seller(t, "S1")
	e.terms(t, payout.ScopeDefault, "", d(2026, 4, 1), 10000, 0)
	all := e.sale(t, "all", s, 5000, at(d(2026, 10, 1)))
	if all.Net != irr(0) || e.bal(t, mizan.AccCommissionRevenue) != 5000 || e.bal(t, s.AccountCode) != 0 {
		t.Fatalf("%+v", all)
	}
	if _, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("there is nothing to pay the seller: %v", err)
	}
	e.terms(t, payout.ScopeDefault, "", d(2026, 11, 1), 0, 0)
	free := e.sale(t, "free", s, 7000, at(d(2026, 11, 2)))
	if free.Commission != irr(0) || free.Net != irr(7000) || e.bal(t, s.AccountCode) != 7000 {
		t.Fatalf("%+v", free)
	}
	e.balanced(t)
}

func TestConcurrentPayoutsPayOnce(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	var want int64
	for i := 0; i < 6; i++ {
		s := e.seller(t, fmt.Sprintf("S%d", i))
		for j := 0; j < 5; j++ {
			sale := e.sale(t, fmt.Sprintf("k-%d-%d", i, j), s, int64(100_000+i*1000+j), at(d(2026, 10, 1)))
			want += sale.Net.Amount()
		}
	}
	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	totals := make([]int64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: fmt.Sprintf("race-%d", w)})
			results[w], totals[w] = err, b.Total.Amount()
		}(w)
	}
	wg.Wait()
	wins, paid := 0, int64(0)
	for w, err := range results {
		switch {
		case err == nil:
			wins++
			paid += totals[w]
		case !apperr.Is(err, apperr.KindConflict):
			t.Errorf("worker %d: %v", w, err)
		}
	}
	if wins != 1 || paid != want || e.bal(t, mizan.AccBank) != -want {
		t.Fatalf("wins %d, paid %d (want %d), bank %d", wins, paid, want, e.bal(t, mizan.AccBank))
	}
	e.balanced(t)
}

func TestConcurrentRetriesOfOnePayoutKey(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	sale := e.sale(t, "k", s, 1_000_000, at(d(2026, 10, 1)))
	const workers = 8
	ids := make([]uuid.UUID, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "same"})
			ids[w], errs[w] = b.ID, err
		}(w)
	}
	wg.Wait()
	for w := range ids {
		must(t, errs[w])
		if ids[w] != ids[0] {
			t.Fatalf("every retry must return the same batch: %v", ids)
		}
	}
	if e.bal(t, mizan.AccBank) != -sale.Net.Amount() {
		t.Fatalf("paid more than once: bank %d", e.bal(t, mizan.AccBank))
	}
}

func TestConcurrentReturnAndPayoutNeverBoth(t *testing.T) {
	e := setup(t, false)
	e.defaultTerms(t)
	for i := 0; i < 5; i++ {
		s := e.seller(t, fmt.Sprintf("S%d", i))
		sale := e.sale(t, fmt.Sprintf("k%d", i), s, 100_000, at(d(2026, 10, 1)))
		var wg sync.WaitGroup
		var retErr, payErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, retErr = e.m.Payout.ReturnSale(e.ctx, sale.ID, "return", "tester") }()
		go func() {
			defer wg.Done()
			_, payErr = e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: fmt.Sprintf("p%d", i), SellerIDs: []uuid.UUID{s.ID}})
		}()
		wg.Wait()
		got, err := e.m.Payout.Sale(e.ctx, sale.ID)
		must(t, err)
		switch got.Status {
		case payout.SaleReturned:
			if payErr == nil || retErr != nil || e.bal(t, s.AccountCode) != 0 {
				t.Fatalf("round %d: returned but pay=%v ret=%v account=%d", i, payErr, retErr, e.bal(t, s.AccountCode))
			}
		case payout.SalePaid:
			if retErr == nil || payErr != nil || e.bal(t, s.AccountCode) != 0 {
				t.Fatalf("round %d: paid but pay=%v ret=%v account=%d", i, payErr, retErr, e.bal(t, s.AccountCode))
			}
		default:
			t.Fatalf("round %d: status %s", i, got.Status)
		}
	}
	e.balanced(t)
}

func TestSellerValidationAndDuplicates(t *testing.T) {
	e := setup(t, false)
	e.seller(t, "S1")
	if _, err := e.m.Payout.AddSeller(e.ctx, payout.SellerInput{Code: "S1", Name: "dup"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("duplicate code: %v", err)
	}
	if _, err := e.m.Payout.AddSeller(e.ctx, payout.SellerInput{Code: "S2", Name: "bad", Sheba: "IR000000000000000000000000"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("bad sheba: %v", err)
	}
	if _, err := e.m.Payout.AddSeller(e.ctx, payout.SellerInput{Code: "S3", Name: "no account"}); err != nil {
		t.Fatalf("the destination is optional: %v", err)
	}
}

func TestVATOnTheCommissionFollowsTheAccountingSetting(t *testing.T) {
	e := setup(t, true) // the module's VAT setting is the default 10%
	// terms that say nothing about VAT use the accounting setting; the tax base is the commission, not the sale
	if _, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "default", EffectiveFrom: d(2026, 4, 1), RateBP: 1000, HoldDays: 7, Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	s := e.seller(t, "S1")
	sale := e.sale(t, "k1", s, 1_000_005, at(d(2026, 10, 1))) // commission 100,000.5 -> 100,001; VAT 10,000.1 -> 10,000
	if sale.Commission != irr(100_001) || sale.VAT != irr(10_000) || sale.Net != irr(890_004) || sale.VATBP != 1000 {
		t.Fatalf("%+v", sale)
	}
	for acc, want := range map[string]int64{mizan.AccReceivable: 1_000_005, mizan.AccCommissionRevenue: 100_001, mizan.AccVATPayable: 10_000, s.AccountCode: 890_004} {
		if got := e.bal(t, acc); got != want {
			t.Errorf("%s = %d, want %d", acc, got, want)
		}
	}
	b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "b1"})
	must(t, err)
	if b.Total != irr(890_004) {
		t.Fatalf("the seller is paid after the commission and its VAT: %+v", b)
	}
	e.balanced(t)
	rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Unclassified) != 0 {
		t.Fatalf("%+v", rep.Unclassified)
	}

	// a return before payout takes the VAT back too
	s2 := e.seller(t, "S2")
	r := e.sale(t, "k2", s2, 100_000, nil)
	if _, err := e.m.Payout.ReturnSale(e.ctx, r.ID, "returned", "tester"); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccVATPayable) != 10_000 || e.bal(t, mizan.AccCommissionRevenue) != 100_001 {
		t.Fatal("the returned sale's commission and VAT must be reversed")
	}

	// a category can set its own VAT (here exempt: 0), which beats the setting
	zero := 0
	if _, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "exempt", Scope: payout.ScopeCategory, ScopeKey: "exempt", EffectiveFrom: d(2026, 4, 1), RateBP: 1000, VATBP: &zero}); err != nil {
		t.Fatal(err)
	}
	ex, err := e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "ex", SellerID: s.ID, Gross: irr(1000), Category: "exempt"})
	must(t, err)
	if ex.VAT != irr(0) || ex.Net != irr(900) {
		t.Fatalf("%+v", ex)
	}

	// fees that eat more than the sale are refused instead of making the seller owe money
	if _, err := e.m.Payout.AddTerms(e.ctx, payout.TermsInput{Name: "greedy", Scope: payout.ScopeCategory, ScopeKey: "x", EffectiveFrom: d(2026, 4, 1), RateBP: 10000}); err != nil {
		t.Fatal(err)
	}
	_, err = e.m.Payout.RecordSale(e.ctx, payout.SaleInput{IdemKey: "g", SellerID: s.ID, Gross: irr(1000), Category: "x"})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("%v", err)
	}
}

func TestPartialReturnBeforeAndAfterPayout(t *testing.T) {
	e := setup(t, true)
	e.defaultTerms(t) // 10% commission, no VAT
	s := e.seller(t, "S1")
	held := e.sale(t, "p1", s, 1_000_000, nil)
	got, err := e.m.Payout.PartialReturn(e.ctx, held.ID, payout.ReturnInput{IdemKey: "r1", Gross: irr(200_000), Reason: "one item back", Actor: "tester"})
	must(t, err)
	// 800,000 left: commission 80,000, net 720,000
	if got.Gross != irr(800_000) || got.Commission != irr(80_000) || got.Net != irr(720_000) {
		t.Fatalf("%+v", got)
	}
	if e.bal(t, mizan.AccReceivable) != 800_000 || e.bal(t, s.AccountCode) != 720_000 || e.bal(t, mizan.AccCommissionRevenue) != 80_000 {
		t.Fatalf("receivable %d seller %d commission %d", e.bal(t, mizan.AccReceivable), e.bal(t, s.AccountCode), e.bal(t, mizan.AccCommissionRevenue))
	}
	if again, err := e.m.Payout.PartialReturn(e.ctx, held.ID, payout.ReturnInput{IdemKey: "r1", Gross: irr(200_000), Reason: "one item back"}); err != nil || again.Gross != irr(800_000) || e.bal(t, mizan.AccReceivable) != 800_000 {
		t.Fatalf("a retry books nothing: %v", err)
	}
	if _, err := e.m.Payout.PartialReturn(e.ctx, held.ID, payout.ReturnInput{IdemKey: "r2", Gross: irr(800_000), Reason: "all"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("the whole gross is a full return: %v", err)
	}

	// After the payout the seller's share of a return is a clawback.
	paid := e.sale(t, "p2", s, 500_000, at(d(2026, 10, 1)))
	_, err = e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "pb", SellerIDs: []uuid.UUID{s.ID}, Actor: "tester"})
	must(t, err)
	got, err = e.m.Payout.PartialReturn(e.ctx, paid.ID, payout.ReturnInput{IdemKey: "r3", Gross: irr(100_000), Reason: "damaged", Actor: "tester"})
	must(t, err)
	if got.Status != payout.SalePaid || got.Gross != irr(400_000) || e.bal(t, mizan.AccSellerRecoverable) != 90_000 {
		t.Fatalf("%+v recoverable %d", got, e.bal(t, mizan.AccSellerRecoverable))
	}
	cbs, err := e.m.Payout.Clawbacks(e.ctx, s.ID, true)
	must(t, err)
	if len(cbs) != 1 || cbs[0].Amount != irr(90_000) {
		t.Fatalf("%+v", cbs)
	}
	e.balanced(t)
}

func TestReturnPaidSaleClawbackAndRecovery(t *testing.T) {
	e := setup(t, true)
	e.defaultTerms(t)
	s := e.seller(t, "S1")
	paid := e.sale(t, "c1", s, 1_000_000, at(d(2026, 10, 1)))
	_, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "pb", Actor: "tester"})
	must(t, err)
	if _, err := e.m.Payout.ReturnSale(e.ctx, paid.ID, "x", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("the ordinary return refuses a paid sale: %v", err)
	}
	cb, err := e.m.Payout.ReturnPaidSale(e.ctx, paid.ID, "customer returned it", "tester")
	must(t, err)
	if cb.Amount != irr(900_000) || cb.Outstanding() != 900_000 || e.bal(t, mizan.AccSellerRecoverable) != 900_000 || e.bal(t, mizan.AccReceivable) != 0 || e.bal(t, mizan.AccCommissionRevenue) != 0 {
		t.Fatalf("%+v recoverable %d", cb, e.bal(t, mizan.AccSellerRecoverable))
	}
	if again, err := e.m.Payout.ReturnPaidSale(e.ctx, paid.ID, "customer returned it", "tester"); err != nil || again.ID != cb.ID {
		t.Fatalf("a retry returns the same clawback: %v", err)
	}
	// 300,000 comes back in cash, 200,000 is offset against a new sale's payable, then the rest is refused above what is owed.
	if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: "k1", Amount: irr(300_000), Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	bankAfter := e.bal(t, mizan.AccBank)
	if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: "k1", Amount: irr(300_000)}); err != nil || e.bal(t, mizan.AccBank) != bankAfter {
		t.Fatalf("a retry of a recovery books nothing: %v", err)
	}
	e.sale(t, "c2", s, 500_000, nil) // the seller is owed 450,000 for it
	if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: "k2", Amount: irr(200_000), Offset: true, Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, s.AccountCode) != 250_000 || e.bal(t, mizan.AccSellerRecoverable) != 400_000 {
		t.Fatalf("seller %d recoverable %d", e.bal(t, s.AccountCode), e.bal(t, mizan.AccSellerRecoverable))
	}
	if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: "k3", Amount: irr(400_001)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("more than owed: %v", err)
	}
	if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: "k4", Amount: irr(300_000), Offset: true}); err == nil {
		t.Fatal("an offset above the seller's payable must be refused (the account cannot go negative)")
	}
	// concurrent cash recoveries never exceed what is owed
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := e.m.Payout.RecoverClawback(e.ctx, cb.ID, payout.RecoverInput{IdemKey: fmt.Sprintf("cc%d", i), Amount: irr(150_000), Actor: "tester"}); err == nil {
				mu.Lock()
				okN++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if okN != 2 || e.bal(t, mizan.AccSellerRecoverable) != 100_000 { // 400,000 owed: two of 150,000 fit
		t.Fatalf("%d recoveries went through, recoverable %d", okN, e.bal(t, mizan.AccSellerRecoverable))
	}
	e.balanced(t)
}

func TestRejectedPayoutTransfer(t *testing.T) {
	e := setup(t, true)
	e.defaultTerms(t)
	s1, s2 := e.seller(t, "S1"), e.seller(t, "S2")
	e.sale(t, "r1", s1, 100_000, at(d(2026, 10, 1)))
	e.sale(t, "r2", s2, 200_000, at(d(2026, 10, 1)))
	b, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "pb", Actor: "tester"})
	must(t, err)
	bank := e.bal(t, mizan.AccBank)
	b2, err := e.m.Payout.RejectPayoutItem(e.ctx, b.ID, s1.ID, "account closed", "tester")
	must(t, err)
	if e.bal(t, mizan.AccBank) != bank+90_000 || e.bal(t, s1.AccountCode) != 90_000 || e.bal(t, s2.AccountCode) != 0 {
		t.Fatalf("bank %d s1 %d s2 %d", e.bal(t, mizan.AccBank), e.bal(t, s1.AccountCode), e.bal(t, s2.AccountCode))
	}
	rejected := 0
	for _, it := range b2.Items {
		if it.Rejected {
			rejected++
			if it.SellerID != s1.ID {
				t.Fatal("the wrong item was rejected")
			}
		}
	}
	if rejected != 1 {
		t.Fatalf("%+v", b2.Items)
	}
	if again, err := e.m.Payout.RejectPayoutItem(e.ctx, b.ID, s1.ID, "account closed", "tester"); err != nil || e.bal(t, mizan.AccBank) != bank+90_000 || len(again.Items) != 2 {
		t.Fatalf("a repeat books nothing: %v", err)
	}
	// the seller's sale is payable again in the next batch, once
	nb, err := e.m.Payout.CreatePayout(e.ctx, payout.PayoutInput{IdemKey: "pb2", Actor: "tester"})
	must(t, err)
	if nb.Total != irr(90_000) || e.bal(t, s1.AccountCode) != 0 {
		t.Fatalf("%+v", nb)
	}
	e.balanced(t)
}
