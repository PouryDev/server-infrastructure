package mizan_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
)

func TestFixedAssetUnitsOfProduction(t *testing.T) {
	// Pure: 1,000,000 cost, 100,000 salvage, capacity 9,000 units.
	if a := fixedasset.UnitsAmount(1_000_000, 100_000, 0, 1_000, 9_000); a != 100_000 {
		t.Fatalf("1000 of 9000 units: %d", a)
	}
	if a := fixedasset.UnitsAmount(1_000_000, 100_000, 100_000, 10_000, 9_000); a != 800_000 { // use beyond capacity stops at the depreciable total
		t.Fatalf("beyond capacity: %d", a)
	}
	e := newClockEnv(t)
	cat := e.category(t, "press", fixedasset.Terms{Method: fixedasset.UnitsOfProduction, LifeMonths: 9_000}, 1000) // life_months = capacity in units
	a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "u1", Code: "U-1", Name: "Press", CategoryID: cat.ID, Cost: irr(1_000_000), Actor: "tester"})
	must(t, err)
	if err := e.m.FixedAssets.RecordUsage(e.ctx, a.ID, 1405, 9, 1_000, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.FixedAssets.RecordUsage(e.ctx, a.ID, 1405, 10, 2_000, "tester"); err != nil {
		t.Fatal(err)
	}
	e.set(jan25)
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 9, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester"); err != nil {
		t.Fatal(err)
	}
	got, err := e.m.FixedAssets.Asset(e.ctx, a.ID)
	must(t, err)
	if got.Accumulated.Amount() != 300_000 { // 900,000 x 3,000 / 9,000
		t.Fatalf("accumulated %d", got.Accumulated.Amount())
	}
	if err := e.m.FixedAssets.RecordUsage(e.ctx, a.ID, 1405, 10, 5, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("recording a depreciated month: %v", err)
	}
}

func TestFixedAssetVoidRunAndPartialDisposal(t *testing.T) {
	e := newClockEnv(t)
	cat := e.category(t, "machines", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 60}, 1000)
	a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "v1", Code: "V-1", Name: "Lathe", CategoryID: cat.ID, Cost: irr(6_000_000), Actor: "tester"})
	must(t, err)
	e.set(jan25)
	if _, err := e.m.FixedAssets.VoidRun(e.ctx, 1405, 9, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no run yet: %v", err)
	}
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 9, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester"); err != nil {
		t.Fatal(err)
	}
	// The earlier month cannot be voided while a later one stands.
	if _, err := e.m.FixedAssets.VoidRun(e.ctx, 1405, 9, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("voiding under a later run: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = e.m.FixedAssets.VoidRun(e.ctx, 1405, 10, "tester") }()
	}
	wg.Wait()
	okN := 0
	for _, err := range errs {
		if err == nil {
			okN++
		} else if !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if okN != 1 {
		t.Fatalf("%d voids went through", okN)
	}
	if b := e.bal(t, mizan.AccDepreciationExpense); b != 90_000 {
		t.Fatalf("expense after the void: %d", b)
	}
	// Re-running the voided month books it again.
	r, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester")
	must(t, err)
	if r.Total.Amount() != 90_000 || r.Journal == nil {
		t.Fatalf("rerun: %+v", r)
	}
	if b := e.bal(t, mizan.AccDepreciationExpense); b != 180_000 {
		t.Fatalf("expense after the rerun: %d", b)
	}

	if _, err := e.m.FixedAssets.DisposePartial(e.ctx, a.ID, fixedasset.PartialDisposeInput{IdemKey: "p0", PortionBP: 10000, Proceeds: irr(1)}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("the whole asset is not a partial disposal: %v", err)
	}
	// Dey is over and booked: 25% of the asset goes for 1,000,000 (accumulated 180,000: 45,000 leaves with it).
	got, err := e.m.FixedAssets.DisposePartial(e.ctx, a.ID, fixedasset.PartialDisposeInput{IdemKey: "p1", PortionBP: 2500, Proceeds: irr(1_000_000), Actor: "tester"})
	must(t, err)
	if got.Cost.Amount() != 4_500_000 || got.Accumulated.Amount() != 135_000 || got.Salvage.Amount() != 450_000 || got.Status != fixedasset.Active {
		t.Fatalf("after: %+v", got)
	}
	if again, err := e.m.FixedAssets.DisposePartial(e.ctx, a.ID, fixedasset.PartialDisposeInput{IdemKey: "p1", PortionBP: 2500, Proceeds: irr(1_000_000), Actor: "tester"}); err != nil || again.Cost.Amount() != 4_500_000 {
		t.Fatalf("retry: %+v %v", again, err)
	}
	// 1,500,000 cost - 45,000 accumulated = 1,455,000 carried, 1,000,000 received: a loss of 455,000.
	if e.bal(t, mizan.AccAssetDisposal) != 455_000 {
		t.Fatalf("loss %d", e.bal(t, mizan.AccAssetDisposal))
	}
	if e.bal(t, cat.CostAccount) != 4_500_000 {
		t.Fatalf("cost account %d", e.bal(t, cat.CostAccount))
	}
}

func TestFixedAssetComponentsAndSmallAssets(t *testing.T) {
	e := newClockEnv(t)
	body := e.category(t, "building", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 240}, 0)
	lift := e.category(t, "lift", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 120}, 0)
	same := e.category(t, "same", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 240}, 0)
	in := fixedasset.AcquireComponentsInput{IdemKey: "c1", Code: "B1", Name: "Building", PaidFrom: mizan.AccBank, Actor: "tester",
		Components: []fixedasset.Component{{Suffix: "BODY", CategoryID: body.ID, Cost: irr(8_500_000)}, {Suffix: "LIFT", CategoryID: lift.ID, Cost: irr(1_500_000)}}}
	if _, err := e.m.FixedAssets.AcquireComponents(e.ctx, in); !apperr.Is(err, apperr.KindConflict) { // 15% < 20%
		t.Fatalf("a part below 20%%: %v", err)
	}
	in.Components = []fixedasset.Component{{Suffix: "BODY", CategoryID: body.ID, Cost: irr(7_000_000)}, {Suffix: "LIFT", CategoryID: same.ID, Cost: irr(3_000_000)}}
	if _, err := e.m.FixedAssets.AcquireComponents(e.ctx, in); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a part with the same life: %v", err)
	}
	in.Components[1].CategoryID = lift.ID
	xs, err := e.m.FixedAssets.AcquireComponents(e.ctx, in)
	must(t, err)
	if len(xs) != 2 || xs[0].Cost.Amount() != 7_000_000 || xs[1].Code != "B1-LIFT" || xs[1].LifeMonths != 120 {
		t.Fatalf("components: %+v", xs)
	}
	if again, err := e.m.FixedAssets.AcquireComponents(e.ctx, in); err != nil || again[0].ID != xs[0].ID {
		t.Fatalf("retry: %v", err)
	}

	// Art. 12: 10% of the 1405 small-transaction limit (4,230,000,000) = 423,000,000 rials.
	limit, th, err := e.m.FixedAssets.ExpenseLimit(e.ctx, day1405)
	must(t, err)
	if limit != 423_000_000 || th.Verified {
		t.Fatalf("limit %d %+v", limit, th)
	}
	if _, err := e.m.FixedAssets.ExpenseSmall(e.ctx, fixedasset.ExpenseSmallInput{IdemKey: "s0", Name: "Big", Cost: irr(423_000_000)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("at the limit: %v", err)
	}
	jid, err := e.m.FixedAssets.ExpenseSmall(e.ctx, fixedasset.ExpenseSmallInput{IdemKey: "s1", Name: "Drill", Cost: irr(5_000_000), VAT: irr(500_000), Actor: "tester"})
	must(t, err)
	if jid == uuid.Nil || e.bal(t, mizan.AccSmallAssetExpense) != 5_000_000 {
		t.Fatalf("expense %d", e.bal(t, mizan.AccSmallAssetExpense))
	}
	if again, err := e.m.FixedAssets.ExpenseSmall(e.ctx, fixedasset.ExpenseSmallInput{IdemKey: "s1", Name: "Drill", Cost: irr(5_000_000), VAT: irr(500_000), Actor: "tester"}); err != nil || again != jid || e.bal(t, mizan.AccSmallAssetExpense) != 5_000_000 {
		t.Fatalf("idempotent: %v %v", again, err)
	}
	// The limit is versioned data: a newer version replaces it.
	if _, err := e.m.FixedAssets.AddThreshold(e.ctx, time.Date(2027, 3, 21, 0, 0, 0, 0, time.UTC), 5_000_000_000, false, fmt.Sprintf("test %d", 1), "tester"); err != nil {
		t.Fatal(err)
	}
	if l, _, _ := e.m.FixedAssets.ExpenseLimit(e.ctx, time.Date(2027, 4, 1, 0, 0, 0, 0, time.UTC)); l != 500_000_000 {
		t.Fatalf("new limit %d", l)
	}
}

func TestFixedAssetIdleRule(t *testing.T) {
	e := newClockEnv(t)
	e.set(time.Date(2027, 3, 10, 10, 0, 0, 0, time.UTC)) // 1405/12/19
	cat := e.category(t, "idlemach", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 120}, 1000)
	acq := func(key string) fixedasset.Asset {
		a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: key, Code: key, Name: key, CategoryID: cat.ID, Cost: irr(12_000_000), Actor: "tester"})
		must(t, err)
		return a
	}
	six, seven := acq("I-6"), acq("I-7")
	if _, err := e.m.FixedAssets.AddIdleSpan(e.ctx, six.ID, 1406, 1, 1406, 6, "six", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.FixedAssets.AddIdleSpan(e.ctx, seven.ID, 1406, 1, 1406, 7, "seven", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.FixedAssets.AddIdleSpan(e.ctx, seven.ID, 1406, 12, 1407, 1, "x", "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a span across a year: %v", err)
	}
	e.set(time.Date(2027, 12, 1, 10, 0, 0, 0, time.UTC)) // 1406/09/10: months 1..8 are over
	if _, err := e.m.Ledger.OpenFiscalYear(e.ctx, 1406); err != nil {
		t.Fatal(err)
	}
	for m := 1; m <= 8; m++ {
		if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1406, m, "tester"); err != nil {
			t.Fatalf("month %d: %v", m, err)
		}
	}
	acc := func(id uuid.UUID) int64 {
		a, err := e.m.FixedAssets.Asset(e.ctx, id)
		must(t, err)
		return a.Accumulated.Amount()
	}
	if got := acc(six.ID); got != 720_000 { // six idle months do not qualify: full depreciation (10% salvage: 90,000 a month)
		t.Fatalf("six-month idle asset: %d", got)
	}
	if got := acc(seven.ID); got != 279_000 { // 7 x 27,000 + 90,000
		t.Fatalf("seven-month idle asset: %d", got)
	}
	if _, err := e.m.FixedAssets.AddIdleSpan(e.ctx, seven.ID, 1406, 8, 1406, 9, "late", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a span over depreciated months: %v", err)
	}
	// Voiding month 8 then 7 restores the accumulated depreciation and the life progress.
	if _, err := e.m.FixedAssets.VoidRun(e.ctx, 1406, 8, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.FixedAssets.VoidRun(e.ctx, 1406, 7, "tester"); err != nil {
		t.Fatal(err)
	}
	if got := acc(seven.ID); got != 162_000 {
		t.Fatalf("after voiding: %d", got)
	}
	a, _ := e.m.FixedAssets.Asset(e.ctx, seven.ID)
	if a.ProgressMilli != 1800 {
		t.Fatalf("progress %d", a.ProgressMilli)
	}
	// Buildings are exempt.
	bcat, err := e.m.FixedAssets.CreateCategory(e.ctx, fixedasset.CategoryInput{Code: "bldg", Name: "Building", Terms: fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 240}, Source: "test", IdleExempt: true}, "tester")
	must(t, err)
	b, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "B-1", Code: "B-1", Name: "Hall", CategoryID: bcat.ID, Cost: irr(1_000_000), Actor: "tester"})
	must(t, err)
	if _, err := e.m.FixedAssets.AddIdleSpan(e.ctx, b.ID, 1406, 9, 1406, 12, "x", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("exempt category: %v", err)
	}
}
