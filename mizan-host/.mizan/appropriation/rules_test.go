package appropriation_test

import (
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
)

func TestComputeLegalReserve(t *testing.T) {
	// 1,000 profit, no earlier result: reserve 50, distributable 950, mandatory dividend 100.
	p := appropriation.Compute(appropriation.Facts{NetProfit: 1000, Capital: 10_000})
	if p.LegalReserve != 50 || p.Distributable != 950 || p.MinDividend != 100 || p.MaxBonusBP != 600 {
		t.Fatalf("%+v", p)
	}
	// The reserve is one twentieth of the profit AFTER earlier losses: 1,000 - 400 = 600 -> 30.
	p = appropriation.Compute(appropriation.Facts{NetProfit: 1000, RetainedAtStart: -400, Capital: 10_000})
	if p.LegalReserve != 30 || p.Distributable != 1000-400-30 {
		t.Fatalf("%+v", p)
	}
	// It stops at one tenth of the capital: 1,000 capital -> reserve may reach 100; 80 already there -> only 20 more.
	p = appropriation.Compute(appropriation.Facts{NetProfit: 10_000, Capital: 1000, LegalReserve: 80})
	if p.LegalReserve != 20 {
		t.Fatalf("%+v", p)
	}
	// Already at the cap: nothing is required (it becomes optional).
	if p = appropriation.Compute(appropriation.Facts{NetProfit: 10_000, Capital: 1000, LegalReserve: 100}); p.LegalReserve != 0 {
		t.Fatalf("%+v", p)
	}
	// A capital increase restarts it: capital 2,000 -> room 200, 100 held.
	if p = appropriation.Compute(appropriation.Facts{NetProfit: 10_000, Capital: 2000, LegalReserve: 100}); p.LegalReserve != 100 {
		t.Fatalf("%+v", p)
	}
}

func TestComputeDistributable(t *testing.T) {
	// Undistributed profit of earlier years adds to what can be distributed (article 239).
	p := appropriation.Compute(appropriation.Facts{NetProfit: 1000, RetainedAtStart: 500, Capital: 100_000})
	if p.LegalReserve != 50 || p.Distributable != 1000+500-50 || p.MinDividend != 100 {
		t.Fatalf("%+v", p)
	}
	// A loss year: nothing to set aside, nothing mandatory; earlier profit can still be distributed by decision.
	p = appropriation.Compute(appropriation.Facts{NetProfit: -300, RetainedAtStart: 1000, Capital: 100_000})
	if p.LegalReserve != 0 || p.Distributable != 700 || p.MinDividend != 0 {
		t.Fatalf("%+v", p)
	}
	// Earlier losses larger than the profit: nothing is distributable.
	p = appropriation.Compute(appropriation.Facts{NetProfit: 100, RetainedAtStart: -500, Capital: 100_000})
	if p.LegalReserve != 0 || p.Distributable != 0 || p.MinDividend != 0 {
		t.Fatalf("%+v", p)
	}
	if appropriation.Compute(appropriation.Facts{NetProfit: 1, PublicCompany: true}).MaxBonusBP != 300 {
		t.Fatal("a public company is limited to 3%")
	}
}

func TestValidateDecision(t *testing.T) {
	p := appropriation.Compute(appropriation.Facts{NetProfit: 1000, Capital: 10_000}) // distributable 950, minimum 100
	ok := []appropriation.Decision{{Dividend: 100}, {Dividend: 950}, {Dividend: 500, OptionalReserve: 450}}
	for _, d := range ok {
		if err := p.Validate(d); err != nil {
			t.Fatalf("%+v: %v", d, err)
		}
	}
	bad := []appropriation.Decision{{Dividend: 99}, {Dividend: 951}, {Dividend: 500, OptionalReserve: 451}, {Dividend: -1}, {}}
	for _, d := range bad {
		if p.Validate(d) == nil {
			t.Fatalf("%+v must be refused", d)
		}
	}
	// Reserves may use up the profit so that less than the minimum is left to distribute.
	if err := p.Validate(appropriation.Decision{Dividend: 50, OptionalReserve: 900}); err != nil {
		t.Fatalf("the minimum yields to what is left after reserves: %v", err)
	}
	// Nothing distributable: only a zero decision is valid.
	zero := appropriation.Compute(appropriation.Facts{NetProfit: -5})
	if zero.Validate(appropriation.Decision{}) != nil || zero.Validate(appropriation.Decision{Dividend: 1}) == nil {
		t.Fatal("zero plan")
	}
}
