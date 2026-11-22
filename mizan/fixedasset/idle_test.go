package fixedasset

import "testing"

func span(fy, fm, ty, tm int) IdleSpan {
	return IdleSpan{From: PeriodIndex(fy, fm), To: PeriodIndex(ty, tm)}
}

func TestIdleApplies(t *testing.T) {
	p := func(m int) int { return PeriodIndex(1406, m) }
	cases := []struct {
		name  string
		spans []IdleSpan
		month int
		want  bool
	}{
		{"six months is not more than six", []IdleSpan{span(1406, 1, 1406, 6)}, 3, false},
		{"seven months applies to every idle month", []IdleSpan{span(1406, 1, 1406, 7)}, 1, true},
		{"seven months: the last idle month", []IdleSpan{span(1406, 1, 1406, 7)}, 7, true},
		{"month outside the span", []IdleSpan{span(1406, 1, 1406, 7)}, 8, false},
		{"touching spans merge", []IdleSpan{span(1406, 1, 1406, 4), span(1406, 5, 1406, 8)}, 2, true},
		{"a gap splits them", []IdleSpan{span(1406, 1, 1406, 4), span(1406, 6, 1406, 9)}, 2, false},
		{"another year does not count", []IdleSpan{span(1405, 8, 1405, 12), span(1406, 1, 1406, 3)}, 1, false},
	}
	for _, c := range cases {
		if got := IdleApplies(c.spans, p(c.month), 6); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestIdleStraightLineExtendsLife(t *testing.T) {
	// 1,200,000 over 12 months = 100,000 a month; two idle months take 30% each and push the end back by 1.4 months.
	s := State{Cost: 1_200_000, Terms: Terms{Method: StraightLine, LifeMonths: 12}}
	var total int64
	var amounts []int64
	for i := 0; i < 20 && total < s.Cost; i++ {
		if i < 2 {
			s.Idle, s.IdleBP = true, 3000
		}
		a := NextAmount(s)
		s = s.Advance(a)
		total += a
		amounts = append(amounts, a)
	}
	if amounts[0] != 30_000 || amounts[1] != 30_000 || amounts[2] != 100_000 {
		t.Fatalf("amounts %v", amounts)
	}
	if total != 1_200_000 || len(amounts) != 14 { // 12 + 1.4 months -> 14 monthly rows, the last one partial
		t.Fatalf("total %d rows %d: %v", total, len(amounts), amounts)
	}
}

func TestIdleDecliningTakesShareOfMonth(t *testing.T) {
	s := State{Cost: 1_200_000, Terms: Terms{Method: DecliningBalance, RateBP: 2500}}
	normal := NextAmount(s)
	s.Idle, s.IdleBP = true, 3000
	if got := NextAmount(s); got != roundDiv(normal, 3000, 10000) {
		t.Fatalf("idle %d normal %d", got, normal)
	}
}
