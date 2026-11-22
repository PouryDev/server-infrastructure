package incometax

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

var rule = Rule{RateBP: 2500, CooperativeDiscountBP: 2500}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestComputeBreakdown(t *testing.T) {
	r, err := Compute(Input{Profit: 10_000_000, AddBacks: []Item{{"fines", 500_000}, {"gifts over the limit", 300_000}}, Deductions: []Item{{"exempt income", 1_000_000}},
		DepreciationAdj: 200_000, Rule: rule, YearStart: day(2026, 3, 21)})
	if err != nil {
		t.Fatal(err)
	}
	// 10,000,000 + 800,000 - 1,000,000 + 200,000 = 10,000,000
	if r.TaxableBeforeLoss != 10_000_000 || r.Taxable != 10_000_000 || r.RateBP != 2500 || r.Tax != 2_500_000 || r.NewLoss != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestComputeLossesAreSetOffOldestFirst(t *testing.T) {
	l1, l2 := uuid.New(), uuid.New()
	losses := []Loss{{AssessmentID: l1, FiscalYear: "1402", YearStart: day(2023, 3, 21), Remaining: 3_000_000}, {AssessmentID: l2, FiscalYear: "1403", YearStart: day(2024, 3, 20), Remaining: 4_000_000}}
	r, err := Compute(Input{Profit: 5_000_000, Rule: rule, YearStart: day(2026, 3, 21), Losses: losses})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.LossUsed) != 2 || r.LossUsed[0].Amount != 3_000_000 || r.LossUsed[1].Amount != 2_000_000 || r.Taxable != 0 || r.Tax != 0 {
		t.Fatalf("%+v", r)
	}
	// With a five-year limit the oldest loss is out of reach for a later year.
	five := 5
	rl := rule
	rl.LossCarryYears = &five
	r, err = Compute(Input{Profit: 5_000_000, Rule: rl, YearStart: day(2028, 3, 20), Losses: losses[:1]})
	if err != nil || r.LossUsedTotal != 3_000_000 {
		t.Fatalf("within 5 years: %+v %v", r, err)
	}
	r, err = Compute(Input{Profit: 5_000_000, Rule: rl, YearStart: day(2029, 3, 21), Losses: losses[:1]})
	if err != nil || r.LossUsedTotal != 0 || r.Tax != 1_250_000 {
		t.Fatalf("expired: %+v %v", r, err)
	}
}

func TestComputeTaxLossAndRates(t *testing.T) {
	r, err := Compute(Input{Profit: -2_000_000, AddBacks: []Item{{"x", 500_000}}, Rule: rule, YearStart: day(2026, 3, 21)})
	if err != nil || r.NewLoss != 1_500_000 || r.Taxable != 0 || r.Tax != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// Cooperative: 25% off the 25% rate = 18.75%.
	r, err = Compute(Input{Profit: 1_000_000, Rule: rule, Cooperative: true, YearStart: day(2026, 3, 21)})
	if err != nil || r.RateBP != 1875 || r.Tax != 187_500 {
		t.Fatalf("%+v %v", r, err)
	}
	// Explicit reduction of three points.
	r, err = Compute(Input{Profit: 1_000_000, Rule: rule, RateReductionBP: 300, YearStart: day(2026, 3, 21)})
	if err != nil || r.RateBP != 2200 || r.Tax != 220_000 {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = Compute(Input{Profit: 1, Rule: rule, RateReductionBP: 2600, YearStart: day(2026, 3, 21)}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a reduction above the rate: %v", err)
	}
	if _, err = Compute(Input{Profit: 1, Rule: Rule{RateBP: 2500}, Cooperative: true}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("cooperative without a discount in the rule: %v", err)
	}
	if _, err = Compute(Input{Profit: 1, Rule: rule, AddBacks: []Item{{"", 5}}}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("an unlabeled item: %v", err)
	}
	// Large figures do not overflow.
	r, err = Compute(Input{Profit: 4_000_000_000_000_000_000, Rule: rule, YearStart: day(2026, 3, 21)})
	if err != nil || r.Tax != 1_000_000_000_000_000_000 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDueAfterMonths(t *testing.T) {
	// Tax year 1405 ends 1405/12/29 (2027-03-20); four months later = end of Tir 1406 (1406/04/31 = 2027-07-22).
	got, err := dueAfterMonths(day(2027, 3, 20), 4)
	if err != nil || got.Format("2006-01-02") != "2027-07-22" {
		t.Fatalf("%v %v", got, err)
	}
}
