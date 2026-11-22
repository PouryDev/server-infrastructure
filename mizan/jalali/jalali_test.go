package jalali_test

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/jalali"
)

func TestKnownDates(t *testing.T) {
	cases := []struct {
		g time.Time
		j jalali.Date
	}{
		{time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC), jalali.Date{1403, 1, 1}},
		{time.Date(2025, 3, 21, 0, 0, 0, 0, time.UTC), jalali.Date{1404, 1, 1}},
		{time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), jalali.Date{1405, 1, 1}},
		{time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), jalali.Date{1405, 8, 18}},
		{time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC), jalali.Date{1403, 12, 30}}, // 1403 is a leap year
		{time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), jalali.Date{1378, 10, 11}},
	}
	for _, c := range cases {
		got, err := jalali.FromTime(c.g)
		if err != nil || got != c.j {
			t.Errorf("FromTime(%s) = %v (%v), want %v", c.g.Format("2006-01-02"), got, err, c.j)
		}
		back, err := c.j.Time(time.UTC)
		if err != nil || !back.Equal(c.g) {
			t.Errorf("%v.Time() = %v (%v), want %v", c.j, back, err, c.g)
		}
	}
}

func TestRoundTripEveryDay(t *testing.T) {
	d := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	for ; d.Year() < 2060; d = d.AddDate(0, 0, 1) {
		j, err := jalali.FromTime(d)
		if err != nil || !j.Valid() {
			t.Fatalf("%v -> %v %v", d, j, err)
		}
		if back, _ := j.Time(time.UTC); !back.Equal(d) {
			t.Fatalf("%v -> %v -> %v", d, j, back)
		}
	}
}

func TestLeapYears(t *testing.T) {
	for y, want := range map[int]bool{1399: true, 1403: true, 1404: false, 1408: true} {
		if jalali.IsLeap(y) != want {
			t.Errorf("IsLeap(%d) != %v", y, want)
		}
	}
}

func TestBounds(t *testing.T) {
	s, n, err := jalali.YearBounds(1404, time.UTC)
	if err != nil || n.Sub(s).Hours()/24 != 365 {
		t.Fatalf("%v %v %v", s, n, err)
	}
	if _, _, err := jalali.YearBounds(900, time.UTC); err == nil {
		t.Fatal("expected out of range")
	}
}
