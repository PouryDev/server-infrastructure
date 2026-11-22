package subscription

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func jd(y, m, d int) time.Time {
	t, err := jalali.Date{Year: y, Month: m, Day: d}.Time(time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestAdvanceCountsJalaliMonthsAndKeepsTheDay(t *testing.T) {
	for _, c := range []struct {
		from time.Time
		unit Unit
		n    int
		want time.Time
	}{
		{jd(1405, 1, 15), Month, 1, jd(1405, 2, 15)},
		{jd(1405, 6, 31), Month, 1, jd(1405, 7, 30)},  // the 31st becomes the 30th in a 30-day month
		{jd(1405, 6, 31), Month, 6, jd(1405, 12, 29)}, // 1405 is not leap: Esfand has 29 days
		{jd(1405, 11, 20), Month, 3, jd(1406, 2, 20)}, // crosses the year
		{jd(1405, 8, 18), Year, 1, jd(1406, 8, 18)},
		{jd(1403, 12, 30), Year, 1, jd(1404, 12, 29)}, // the leap day of 1403 falls back in a normal year
		{jd(1405, 8, 18), Day, 10, jd(1405, 8, 28)},
		{jd(1405, 8, 18), Month, 0, jd(1405, 8, 18)},
		{jd(1405, 8, 18), Month, 12, jd(1406, 8, 18)},
	} {
		if got := Advance(c.from, c.unit, c.n); !got.Equal(c.want) {
			t.Errorf("%v + %d %s = %v, want %v", c.from.Format("2006-01-02"), c.n, c.unit, got.Format("2006-01-02"), c.want.Format("2006-01-02"))
		}
	}
	if !jalali.IsLeap(1403) || jalali.IsLeap(1405) {
		t.Fatal("the test assumes 1403 is leap and 1405 is not")
	}
}

func TestPeriodStartsAreComputedFromTheAnchorNotChained(t *testing.T) {
	s := Subscription{Unit: Month, Count: 1, AnchorDate: jd(1405, 6, 31), AnchorNo: 1}
	// chained month arithmetic would drift to the 30th after Mehr; from the anchor the 31st comes back in Bahman
	want := []time.Time{jd(1405, 6, 31), jd(1405, 7, 30), jd(1405, 8, 30), jd(1405, 9, 30), jd(1405, 10, 30), jd(1405, 11, 30), jd(1405, 12, 29), jd(1406, 1, 31)}
	for i, w := range want {
		if got := s.PeriodStart(i + 1); !got.Equal(w) {
			t.Errorf("period %d starts %s, want %s", i+1, got.Format("2006-01-02"), w.Format("2006-01-02"))
		}
	}
	q := Subscription{Unit: Month, Count: 3, AnchorDate: jd(1405, 1, 1), AnchorNo: 1}
	if !q.PeriodStart(2).Equal(jd(1405, 4, 1)) {
		t.Errorf("every three months: %v", q.PeriodStart(2))
	}
	// a re-anchor (resume) starts counting at the later period number
	r := Subscription{Unit: Month, Count: 1, AnchorDate: jd(1405, 9, 10), AnchorNo: 4}
	if !r.PeriodStart(4).Equal(jd(1405, 9, 10)) || !r.PeriodStart(5).Equal(jd(1405, 10, 10)) {
		t.Errorf("%v %v", r.PeriodStart(4), r.PeriodStart(5))
	}
}

func TestPlanValidation(t *testing.T) {
	ok := Plan{Code: "pro", Name: "Pro", Price: money.Must(100_000, money.IRR), Unit: Month, Count: 1}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	bad := 10001
	for name, mut := range map[string]func(*Plan){
		"no code":       func(p *Plan) { p.Code = "" },
		"no name":       func(p *Plan) { p.Name = " " },
		"free":          func(p *Plan) { p.Price = money.Must(0, money.IRR) },
		"unknown unit":  func(p *Plan) { p.Unit = "week" },
		"zero count":    func(p *Plan) { p.Count = 0 },
		"long trial":    func(p *Plan) { p.TrialDays = 400 },
		"tax above 100": func(p *Plan) { p.TaxBP = &bad },
	} {
		v := ok
		mut(&v)
		if err := v.validate(); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
}
