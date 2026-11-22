package fixedasset_test

import (
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
)

func TestStraightLineAddsUpExactly(t *testing.T) {
	s := fixedasset.State{Cost: 1_000_003, Salvage: 3, Terms: fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 7}}
	var total int64
	for i := 0; i < 7; i++ {
		s = s.ForYear(i == 0)
		a := fixedasset.NextAmount(s)
		if a <= 0 {
			t.Fatalf("month %d: %d", i+1, a)
		}
		total += a
		s = s.Advance(a)
	}
	if total != 1_000_000 || s.Accumulated != 1_000_000 {
		t.Fatalf("total %d", total)
	}
	if fixedasset.NextAmount(s) != 0 {
		t.Fatal("a fully depreciated asset must not depreciate further")
	}
}

func TestStraightLineHugeCostDoesNotOverflow(t *testing.T) {
	s := fixedasset.State{Cost: 9_000_000_000_000_000_000, Terms: fixedasset.Terms{Method: fixedasset.StraightLine, LifeMonths: 1200}}
	if a := fixedasset.NextAmount(s); a != 7_500_000_000_000_000 {
		t.Fatalf("got %d", a)
	}
}

func TestDecliningBalanceUsesYearStartBookValue(t *testing.T) {
	// 25% of 1,200,000 = 300,000 a year = 25,000 a month; the next year uses the new book value.
	s := fixedasset.State{Cost: 1_200_000, Terms: fixedasset.Terms{Method: fixedasset.DecliningBalance, RateBP: 2500}}
	for i := 0; i < 12; i++ {
		s = s.ForYear(i == 0)
		a := fixedasset.NextAmount(s)
		if a != 25_000 {
			t.Fatalf("month %d: %d", i+1, a)
		}
		s = s.Advance(a)
	}
	if s.Accumulated != 300_000 || fixedasset.NextAmount(s.ForYear(true)) != 18_750 { // 25% of 900,000 / 12
		t.Fatalf("accumulated %d", s.Accumulated)
	}
}

func TestDecliningBalanceStopsAtSalvage(t *testing.T) {
	s := fixedasset.State{Cost: 1000, Salvage: 900, Terms: fixedasset.Terms{Method: fixedasset.DecliningBalance, RateBP: 10000}}
	var total int64
	for i := 0; i < 12; i++ {
		s = s.ForYear(i == 0)
		a := fixedasset.NextAmount(s)
		total += a
		s = s.Advance(a)
	}
	if total != 100 {
		t.Fatalf("total %d, want the 100 above salvage", total)
	}
}

func TestTermsValidate(t *testing.T) {
	bad := []fixedasset.Terms{{}, {Method: fixedasset.StraightLine}, {Method: fixedasset.StraightLine, LifeMonths: 5000},
		{Method: fixedasset.DecliningBalance}, {Method: fixedasset.DecliningBalance, RateBP: 10001}}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Fatalf("%+v must be refused", b)
		}
	}
	if fixedasset.PeriodIndex(1405, 1) != 1405*12 {
		t.Fatal("period index")
	}
	if y, m := fixedasset.PeriodOf(fixedasset.PeriodIndex(1405, 12)); y != 1405 || m != 12 {
		t.Fatal("period of")
	}
}

func TestDecliningBalanceWritesOffTheRestBelowFivePercent(t *testing.T) {
	// 90% a year: after the first year 10% is left, after the second 1% (below 5%): the third year takes all of it.
	s := fixedasset.State{Cost: 100_000, Terms: fixedasset.Terms{Method: fixedasset.DecliningBalance, RateBP: 9000}}
	for year := 0; year < 3; year++ {
		var taken int64
		for m := 0; m < 12; m++ {
			s = s.ForYear(m == 0)
			a := fixedasset.NextAmount(s)
			taken += a
			s = s.Advance(a)
		}
		if year == 2 && (taken != 1_000 || s.Accumulated != 100_000) {
			t.Fatalf("third year took %d, accumulated %d", taken, s.Accumulated)
		}
	}
}
