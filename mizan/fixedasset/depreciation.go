// Package fixedasset keeps the register of fixed assets (دارایی‌های ثابت) and computes and books their depreciation.
//
// Rates and lives are DATA, not code: every asset category carries its own method, life or rate and the legal source the
// operator took them from, and an asset copies them when it is acquired, so a later change of a category never rewrites an
// existing asset. No legal depreciation table is built in (several versions of the direct-tax-law table exist and none could
// be verified against the official text).
package fixedasset

import (
	"math/big"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Method of depreciation.
type Method string

const (
	// StraightLine spreads (cost - salvage) evenly over LifeMonths (خط مستقیم).
	StraightLine Method = "straight_line"
	// DecliningBalance applies an annual rate to the book value at the start of each Jalali year, spread evenly over the
	// months of that year (نزولی). When the book value falls below 5% of the cost the rest is depreciated in the next year (article 9-b
	// of the regulation). It never takes the book value below the salvage value.
	DecliningBalance Method = "declining_balance"
	// UnitsOfProduction depreciates (cost - salvage) in proportion to the units produced or used (regulation art. 9-c). Terms.LifeMonths holds the asset's TOTAL CAPACITY
	// in units for this method; the units of each month are recorded with RecordUsage before the month is depreciated.
	UnitsOfProduction Method = "units_of_production"
)

// Valid reports whether m is a known method.
func (m Method) Valid() bool {
	return m == StraightLine || m == DecliningBalance || m == UnitsOfProduction
}

// Terms are the depreciation terms of a category or an asset.
type Terms struct {
	Method     Method
	LifeMonths int // straight line: months to depreciate over
	RateBP     int // declining balance: annual rate in basis points of the book value (2500 = 25%)
}

// Validate checks the terms.
func (t Terms) Validate() error {
	switch {
	case !t.Method.Valid():
		return apperr.InvalidField("method", "must be straight_line, declining_balance or units_of_production")
	case t.Method == StraightLine && (t.LifeMonths < 1 || t.LifeMonths > 1200):
		return apperr.InvalidField("life_months", "must be between 1 and 1200 for straight_line")
	case t.Method == UnitsOfProduction && (t.LifeMonths < 1):
		return apperr.InvalidField("life_months", "for units_of_production this is the total capacity in units: must be positive")
	case t.Method == DecliningBalance && (t.RateBP < 1 || t.RateBP > 10000):
		return apperr.InvalidField("rate_bp", "must be between 1 and 10000 (basis points) for declining_balance")
	}
	return nil
}

// State is what the calculator needs to know about an asset before a month.
type State struct {
	Cost, Salvage, Accumulated int64
	Terms
	MonthsDone int // months of depreciation taken so far (all years)
	// DecliningBalance: the accumulated depreciation at the start of the current Jalali year and the months already taken
	// in it. The caller resets them at a year change with ForYear.
	YearStartAccumulated int64
	MonthsInYear         int
	// ProgressMilli is how far the asset is through its life in thousandths of a month: a normal month adds 1000, an idle month under the art. 6 remark adds IdleBP/10 (300
	// for 30%), so the unused 70% of an idle month is added to the remaining life. Zero means MonthsDone*1000 (assets that never had an idle month).
	ProgressMilli int
	// Idle marks the month being computed as an idle month of the art. 6 remark, IdleBP is the share (basis points) of a normal month's depreciation it takes.
	Idle   bool
	IdleBP int
}

// progress is the life progress before the next month.
func (s State) progress() int {
	if s.ProgressMilli == 0 {
		return s.MonthsDone * 1000
	}
	return s.ProgressMilli
}

// delta is the life progress of the month being computed.
func (s State) delta() int {
	if s.Idle {
		return s.IdleBP / 10
	}
	return 1000
}

// roundDiv returns round-half-up(a*b/c) for non-negative inputs without overflowing int64 intermediates.
func roundDiv(a, b, c int64) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	n.Mul(n, big.NewInt(2))
	n.Add(n, big.NewInt(c))
	n.Div(n, new(big.Int).Mul(big.NewInt(c), big.NewInt(2)))
	return n.Int64()
}

// NextAmount is the depreciation of the next month. Every cumulative share is rounded once, so the monthly amounts add up
// exactly to the total with no drift, and the last month takes the remainder.
func NextAmount(s State) int64 {
	depreciable := s.Cost - s.Salvage
	remaining := depreciable - s.Accumulated
	if remaining <= 0 {
		return 0
	}
	var amount int64
	switch s.Method {
	case StraightLine:
		before := int64(s.progress())
		after := before + int64(s.delta())
		life := int64(s.LifeMonths) * 1000
		if after >= life {
			return remaining
		}
		amount = roundDiv(depreciable, after, life) - roundDiv(depreciable, before, life)
	case DecliningBalance:
		book := s.Cost - s.YearStartAccumulated
		annual := roundDiv(book, int64(s.RateBP), 10000)
		// Rule of article 9-b of the regulation: once the book value is below 5% of the full cost, all of it is depreciated in
		// the next year.
		if book*20 < s.Cost {
			annual = book - s.Salvage
		}
		k := int64(s.MonthsInYear + 1)
		if k > 12 {
			return 0
		}
		amount = roundDiv(annual, k, 12) - roundDiv(annual, k-1, 12)
		if s.Idle {
			amount = roundDiv(amount, int64(s.IdleBP), 10000)
		}
	}
	return min(max(amount, 0), remaining)
}

// ForYear resets the yearly figures when the next month falls in a new Jalali year. Call it before NextAmount.
func (s State) ForYear(newYear bool) State {
	if newYear {
		s.YearStartAccumulated, s.MonthsInYear = s.Accumulated, 0
	}
	return s
}

// Advance returns the state after taking amount.
func (s State) Advance(amount int64) State {
	s.Accumulated += amount
	s.ProgressMilli = s.progress() + s.delta()
	s.MonthsDone++
	s.MonthsInYear++
	s.Idle = false
	return s
}

// PeriodIndex numbers Jalali months consecutively: year*12 + month-1.
func PeriodIndex(year, month int) int { return year*12 + month - 1 }

// PeriodOf is the inverse of PeriodIndex.
func PeriodOf(idx int) (year, month int) { return idx / 12, idx%12 + 1 }

// dateOnly strips the time of day.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// UnitsAmount is the depreciation due after cumulativeUnits of use: the cumulative share is rounded once (no drift), the amount is the share not yet taken. Use beyond the
// capacity depreciates nothing more.
func UnitsAmount(cost, salvage, accumulated, cumulativeUnits, capacity int64) int64 {
	depreciable := cost - salvage
	if capacity <= 0 || depreciable <= accumulated {
		return 0
	}
	u := min(max(cumulativeUnits, 0), capacity)
	return min(max(roundDiv(depreciable, u, capacity)-accumulated, 0), depreciable-accumulated)
}
