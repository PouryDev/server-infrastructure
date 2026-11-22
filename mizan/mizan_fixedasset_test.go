package mizan_test

import (
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

// Jalali 1405: Aban = month 8 (the test clock starts on 1405/08/18), Azar = 9 (ends 2026-12-21), Dey = 10 (ends 2027-01-20).
var (
	dec25 = time.Date(2026, 12, 25, 10, 0, 0, 0, time.UTC) // 1405/10/04: Azar is over, Dey is not
	jan25 = time.Date(2027, 1, 25, 10, 0, 0, 0, time.UTC)  // 1405/11/05: Dey is over
)

func (e env) category(t *testing.T, code string, terms fixedasset.Terms, salvageBP int) fixedasset.Category {
	t.Helper()
	c, err := e.m.FixedAssets.CreateCategory(e.ctx, fixedasset.CategoryInput{Code: code, Name: code, Terms: terms, SalvageBP: salvageBP, Source: "test table"}, "tester")
	must(t, err)
	return c
}

func TestFixedAssetLifecycle(t *testing.T) {
	e := newClockEnv(t)
	cat := e.category(t, "machines", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 60}, 1000)
	if _, err := e.m.FixedAssets.CreateCategory(e.ctx, fixedasset.CategoryInput{Code: "x", Name: "x", Terms: fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 60}}, "t"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a category without a source must be refused: %v", err)
	}

	a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "a1", Code: "M-1", Name: "Press", CategoryID: cat.ID, Cost: irr(6_000_000), Actor: "tester"})
	must(t, err)
	if a.Salvage.Amount() != 600_000 || a.AcquisitionJournal == nil {
		t.Fatalf("asset: %+v", a)
	}
	if again, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "a1", Code: "M-1", Name: "Press", CategoryID: cat.ID, Cost: irr(6_000_000)}); err != nil || again.ID != a.ID {
		t.Fatalf("a retry must return the same asset: %v", err)
	}
	if b := e.bal(t, cat.CostAccount); b != 6_000_000 {
		t.Fatalf("cost account %d", b)
	}

	// The month is not over; an earlier month has nothing to do (the asset starts the month after it entered service).
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an unfinished month must be refused: %v", err)
	}
	e.set(dec25)
	if r, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 8, "tester"); err != nil || r.Journal != nil {
		t.Fatalf("the month the asset entered service books nothing: %+v %v", r, err)
	}

	// Months run in order.
	e.set(jan25)
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a run that skips Azar must be refused: %v", err)
	}
	// Two concurrent runs of the same month book it once.
	var wg sync.WaitGroup
	runs := make([]fixedasset.Run, 4)
	errs := make([]error, 4)
	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[i], errs[i] = e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 9, "tester")
		}()
	}
	wg.Wait()
	for i := range runs {
		must(t, errs[i])
		if runs[i].Journal == nil || *runs[i].Journal != *runs[0].Journal || runs[i].Total.Amount() != 90_000 {
			t.Fatalf("run %d: %+v", i, runs[i])
		}
	}
	if b := e.bal(t, mizan.AccDepreciationExpense); b != 90_000 {
		t.Fatalf("depreciation expense %d", b)
	}
	if n, err := postgres.Value[int](e.ctx, e.m.DB.Querier(e.ctx), `SELECT count(*) FROM mz_fa_depreciations`); err != nil || n != 1 {
		t.Fatalf("depreciation rows %d %v", n, err)
	}
	if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, 10, "tester"); err != nil {
		t.Fatal(err)
	}
	got, err := e.m.FixedAssets.Asset(e.ctx, a.ID)
	must(t, err)
	if got.Accumulated.Amount() != 180_000 || got.BookValue() != 5_820_000 {
		t.Fatalf("asset after two months: %+v", got)
	}
	if b := e.bal(t, cat.AccDepAccount); b != -180_000 { // a contra asset: its natural balance is negative
		t.Fatalf("accumulated depreciation account %d", b)
	}
	sched, err := e.m.FixedAssets.Schedule(e.ctx, a.ID)
	must(t, err)
	var rest int64
	for _, r := range sched {
		rest += r.Amount
	}
	if len(sched) != 58 || rest != 5_400_000-180_000 || sched[0].Month != 11 {
		t.Fatalf("schedule: %d rows, rest %d", len(sched), rest)
	}
	e.balanced(t)

	// Dispose in Bahman (month 11) at a loss: depreciation is booked through Dey, the month before.
	e.set(time.Date(2027, 1, 30, 10, 0, 0, 0, time.UTC))
	d, err := e.m.FixedAssets.Dispose(e.ctx, a.ID, fixedasset.DisposeInput{IdemKey: "d1", Proceeds: irr(5_500_000), Actor: "tester"})
	must(t, err)
	if d.Status != fixedasset.Disposed {
		t.Fatalf("status %s", d.Status)
	}
	if e.bal(t, cat.CostAccount) != 0 || e.bal(t, cat.AccDepAccount) != 0 || e.bal(t, mizan.AccAssetDisposal) != 5_820_000-5_500_000 {
		t.Fatalf("books after disposal: cost %d accdep %d loss %d", e.bal(t, cat.CostAccount), e.bal(t, cat.AccDepAccount), e.bal(t, mizan.AccAssetDisposal))
	}
	if again, err := e.m.FixedAssets.Dispose(e.ctx, a.ID, fixedasset.DisposeInput{IdemKey: "d1", Proceeds: irr(5_500_000)}); err != nil || again.ID != a.ID {
		t.Fatalf("a retry of the disposal: %v", err)
	}
	if _, err := e.m.FixedAssets.Dispose(e.ctx, a.ID, fixedasset.DisposeInput{IdemKey: "d2", Proceeds: irr(1)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a second disposal must be refused: %v", err)
	}
	e.balanced(t)
}

func TestFixedAssetOpeningBalanceAndGain(t *testing.T) {
	e := newClockEnv(t)
	cat := e.category(t, "vehicles", fixedasset.Terms{Method: fixedasset.DecliningBalance, RateBP: 2500}, 0)
	// An asset already in the books: cost and 300,000 of depreciation (stands for 12 months) are in the ledger.
	e.post(t, "open", day1405, ledger.Dr(cat.CostAccount, irr(1_200_000)), ledger.Cr(cat.AccDepAccount, irr(300_000)), ledger.Cr(mizan.AccBank, irr(900_000)))
	opening := irr(300_000)
	a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "v1", Code: "V-1", Name: "Van", CategoryID: cat.ID, Cost: irr(1_200_000),
		AcquiredOn: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), AlreadyBooked: true, OpeningAccumulated: &opening, MonthsDone: 12})
	must(t, err)
	if a.AcquisitionJournal != nil {
		t.Fatal("an asset that is already booked must not be booked again")
	}
	if b := e.bal(t, cat.CostAccount); b != 1_200_000 {
		t.Fatalf("cost account %d", b)
	}
	// 1404/05/10 in service -> months from 1404/06; 12 months done -> resumes 1405/06 (Shahrivar). Run up to Azar.
	e.set(dec25)
	for m := 6; m <= 9; m++ {
		if _, err := e.m.FixedAssets.RunDepreciation(e.ctx, 1405, m, "tester"); err != nil {
			t.Fatalf("month %d: %v", m, err)
		}
	}
	got, err := e.m.FixedAssets.Asset(e.ctx, a.ID)
	must(t, err)
	// 25% of the book value at the start of 1405 (900,000) = 225,000 a year = 18,750 a month, four months.
	if got.Accumulated.Amount() != 300_000+4*18_750 {
		t.Fatalf("accumulated %d", got.Accumulated.Amount())
	}
	// Sold at a gain.
	d, err := e.m.FixedAssets.Dispose(e.ctx, a.ID, fixedasset.DisposeInput{IdemKey: "d", Proceeds: irr(1_000_000)})
	must(t, err)
	if d.Status != fixedasset.Disposed || e.bal(t, mizan.AccAssetDisposal) != -(1_000_000+375_000-1_200_000) {
		t.Fatalf("gain: %d", e.bal(t, mizan.AccAssetDisposal))
	}
	e.balanced(t)
}

func TestCategoryFromLegalTable(t *testing.T) {
	e := newClockEnv(t)
	c, err := e.m.FixedAssets.CreateCategoryFromLegalTable(e.ctx, "g13-r4", "cars", "", fixedasset.CategoryInput{}, "tester")
	must(t, err)
	if c.Method != fixedasset.StraightLine || c.LifeMonths != 72 || c.SalvageBP != 0 || c.Name == "" || len(c.Source) < 20 {
		t.Fatalf("%+v", c)
	}
	if _, err := e.m.FixedAssets.CreateCategoryFromLegalTable(e.ctx, "g21-rA6", "dam", "", fixedasset.CategoryInput{}, "tester"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a row that is not selectable: %v", err)
	}
	if _, err := e.m.FixedAssets.CreateCategoryFromLegalTable(e.ctx, "nope", "x", "", fixedasset.CategoryInput{}, "tester"); !apperr.Is(err, apperr.KindNotFound) {
		t.Fatalf("an unknown row: %v", err)
	}
}

func TestFixedAssetPurchaseBooksDeductibleVAT(t *testing.T) {
	e := newClockEnv(t)
	cat := e.category(t, "it", fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 36}, 0)
	a, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "vat1", Code: "PC-1", Name: "Server", CategoryID: cat.ID, Cost: irr(10_000_000), VAT: irr(1_000_000), Actor: "tester"})
	must(t, err)
	if a.Cost.Amount() != 10_000_000 || e.bal(t, cat.CostAccount) != 10_000_000 || e.bal(t, mizan.AccVATInput) != 1_000_000 {
		t.Fatalf("cost %d, input VAT %d", e.bal(t, cat.CostAccount), e.bal(t, mizan.AccVATInput))
	}
	if _, err := e.m.FixedAssets.Acquire(e.ctx, fixedasset.AcquireInput{IdemKey: "vat2", Code: "PC-2", Name: "Old", CategoryID: cat.ID, Cost: irr(1000), VAT: irr(100), AlreadyBooked: true}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("VAT on an asset that is already booked: %v", err)
	}
	e.balanced(t)
}
