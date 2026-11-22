// Package jalali converts between the Gregorian and the Solar Hijri (Jalali) calendars and provides the
// fiscal calendar helpers Iranian accounting needs (the fiscal year is the Jalali year by default).
//
// The leap rule is the 33-year arithmetic rule, which agrees with the official calendar for the
// foreseeable range (1206-1500 SH); dates outside it are rejected rather than guessed.
package jalali

import (
	"errors"
	"fmt"
	"time"
)

// Date is a Jalali calendar date.
type Date struct{ Year, Month, Day int }

const (
	minYear = 1206
	maxYear = 1500
	// 1 Farvardin 1403 fell on 20 March 2024; every conversion counts days from it.
	anchorYear = 1403
)

var anchor = time.Date(2024, time.March, 20, 0, 0, 0, 0, time.UTC)

// MonthNames are the Persian month names.
var MonthNames = [12]string{"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}

// IsLeap reports whether the Jalali year has 366 days.
func IsLeap(year int) bool { return ((25*year+11)%33+33)%33 < 8 }

// DaysInMonth returns 31 (months 1-6), 30 (7-11) or 29/30 (Esfand).
func DaysInMonth(year, month int) int {
	switch {
	case month >= 1 && month <= 6:
		return 31
	case month >= 7 && month <= 11:
		return 30
	case month == 12:
		if IsLeap(year) {
			return 30
		}
		return 29
	}
	return 0
}

func daysInYear(y int) int {
	if IsLeap(y) {
		return 366
	}
	return 365
}

// Valid reports whether d is a real date in the supported range.
func (d Date) Valid() bool {
	return d.Year >= minYear && d.Year <= maxYear && d.Month >= 1 && d.Month <= 12 &&
		d.Day >= 1 && d.Day <= DaysInMonth(d.Year, d.Month)
}

// String formats as 1405/08/18.
func (d Date) String() string { return fmt.Sprintf("%04d/%02d/%02d", d.Year, d.Month, d.Day) }

// ErrOutOfRange is returned for dates outside 1206-1500 SH.
var ErrOutOfRange = errors.New("jalali: date out of supported range")

// FromTime converts the calendar day of t (in t's own location) to a Jalali date.
func FromTime(t time.Time) (Date, error) {
	y, m, d := t.Date()
	days := int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(anchor).Hours() / 24)
	year := anchorYear
	for days < 0 {
		year--
		days += daysInYear(year)
	}
	for days >= daysInYear(year) {
		days -= daysInYear(year)
		year++
	}
	if year < minYear || year > maxYear {
		return Date{}, ErrOutOfRange
	}
	month := 1
	for days >= DaysInMonth(year, month) {
		days -= DaysInMonth(year, month)
		month++
	}
	return Date{year, month, days + 1}, nil
}

// Time returns midnight of d in loc (UTC when loc is nil).
func (d Date) Time(loc *time.Location) (time.Time, error) {
	if !d.Valid() {
		return time.Time{}, ErrOutOfRange
	}
	if loc == nil {
		loc = time.UTC
	}
	days := 0
	if d.Year >= anchorYear {
		for y := anchorYear; y < d.Year; y++ {
			days += daysInYear(y)
		}
	} else {
		for y := d.Year; y < anchorYear; y++ {
			days -= daysInYear(y)
		}
	}
	for m := 1; m < d.Month; m++ {
		days += DaysInMonth(d.Year, m)
	}
	days += d.Day - 1
	g := anchor.AddDate(0, 0, days)
	return time.Date(g.Year(), g.Month(), g.Day(), 0, 0, 0, 0, loc), nil
}

// YearBounds returns the first day of the Jalali year and the first day of the next one (half-open).
func YearBounds(year int, loc *time.Location) (start, nextStart time.Time, err error) {
	if start, err = (Date{year, 1, 1}).Time(loc); err != nil {
		return
	}
	nextStart, err = (Date{year + 1, 1, 1}).Time(loc)
	return
}

// MonthBounds returns the first day of the month and of the following one (half-open).
func MonthBounds(year, month int, loc *time.Location) (start, nextStart time.Time, err error) {
	if start, err = (Date{year, month, 1}).Time(loc); err != nil {
		return
	}
	days := DaysInMonth(year, month)
	nextStart = start.AddDate(0, 0, days)
	return
}
