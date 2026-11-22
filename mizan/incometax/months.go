package incometax

import (
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/jalali"
)

// addJalaliMonths moves a date forward by whole Jalali months, keeping the day (or the month's last day when it is shorter).
func addJalaliMonths(t time.Time, n int) (time.Time, error) {
	d, err := jalali.FromTime(t)
	if err != nil {
		return time.Time{}, err
	}
	idx := d.Year*12 + d.Month - 1 + n
	d.Year, d.Month = idx/12, idx%12+1
	d.Day = min(d.Day, jalali.DaysInMonth(d.Year, d.Month))
	return d.Time(time.UTC)
}

// dueAfterMonths is the deadline "n months after the end of the tax year" (art. 110): when the year ends on the last day of its month (the normal
// Esfand case), the last day of the month n months later; otherwise the same day number, clamped.
func dueAfterMonths(yearEnd time.Time, n int) (time.Time, error) {
	d, err := jalali.FromTime(yearEnd)
	if err != nil {
		return time.Time{}, err
	}
	last := d.Day == jalali.DaysInMonth(d.Year, d.Month)
	t, err := addJalaliMonths(yearEnd, n)
	if err != nil || !last {
		return t, err
	}
	e, err := jalali.FromTime(t)
	if err != nil {
		return time.Time{}, err
	}
	e.Day = jalali.DaysInMonth(e.Year, e.Month)
	return e.Time(time.UTC)
}
