package payroll

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Bracket is one step of the progressive income-tax table, in monthly amounts of the books' currency. UpTo is the
// upper bound of the step (inclusive); 0 on the last bracket means "no limit". A 0% first bracket is the exemption.
type Bracket struct {
	UpTo   int64 `json:"up_to"`
	RateBP int   `json:"rate_bp"` // basis points: 1000 = 10%
}

// RuleVersion is one version of the social-security and income-tax rules, valid from EffectiveFrom until the next
// version starts. Versions are never edited: a change is a new version. A payslip keeps the version it used.
type RuleVersion struct {
	ID            uuid.UUID
	Name          string
	EffectiveFrom time.Time
	Currency      money.Currency

	EmployeeBP     int // employee's social-security share
	EmployerBP     int // employer's social-security share
	UnemploymentBP int // unemployment insurance, paid by the employer on top
	// CeilingDaily and FloorDaily bound the insurable pay per worked day (0 = no bound), so 30- and 31-day months
	// and part months need no special case.
	CeilingDaily int64
	FloorDaily   int64
	// DeductEmployeeInsurance subtracts the employee's share from the taxable income.
	DeductEmployeeInsurance bool
	Brackets                []Bracket
	Terms                   Terms
	Source                  string // where the numbers come from (law, circular, date)
	CreatedBy               string
	CreatedAt               time.Time
}

// Rate is a pay component with its multiplier and how it counts for tax and social security.
type Rate struct {
	BP        int  `json:"bp"` // basis points: 14000 = 140%
	Taxable   bool `json:"taxable"`
	Insurable bool `json:"insurable"`
}

// Severance is the end-of-service payment (سنوات پایان کار).
type Severance struct {
	DaysPerYear int  `json:"days_per_year"` // days of the wage per year of service
	MinYears    int  `json:"min_years"`     // no severance below this service
	Taxable     bool `json:"taxable"`
	Insurable   bool `json:"insurable"`
}

// Terms are the labour-law figures behind overtime, leave pay and severance. They are data like everything else in a
// rule version; a zero value switches the component off (using it is refused until a version configures it).
type Terms struct {
	HoursPerDayX100 int `json:"hours_per_day_x100"` // 733 = 7h20m, the divisor of the daily wage
	DaysPerMonth    int `json:"days_per_month"`     // days a monthly wage is divided into
	// Overtime and Holiday are paid at BP of the hourly wage per worked hour; Night is the premium paid on top of the
	// normal pay for hours worked at night.
	Overtime    Rate      `json:"overtime"`
	Holiday     Rate      `json:"holiday"`
	Night       Rate      `json:"night"`
	LeavePayout Rate      `json:"leave_payout"` // per unused leave day, BP of the daily wage
	Severance   Severance `json:"severance"`
	// Bonus is the annual bonus (عیدی و پاداش سالانه); zero DaysPerYear switches it off.
	Bonus Bonus `json:"bonus"`
	// MaxOvertimeMinutesPerDay caps the overtime of a month at this many minutes per worked day (Labour Law art. 59: 4 hours = 240); 0 = no cap.
	MaxOvertimeMinutesPerDay int `json:"max_overtime_minutes_per_day"`
}

// Bonus is the annual bonus: DaysPerYear days of the wage per year of work (prorated by the days of service over 365), at most CapMinWageDays days of the legal minimum daily wage
// (the rule version's FloorDaily; the cap is prorated the same way; 0 = no cap). It is not insurable. For tax, the part up to ExemptAmount is exempt (Direct Taxes Law art. 91 item 10:
// 1/12 of the annual exemption: the first, 0% bracket of the monthly table when ExemptAmount is 0) and the excess is taxable.
type Bonus struct {
	DaysPerYear    int   `json:"days_per_year"`
	CapMinWageDays int   `json:"cap_min_wage_days"`
	ExemptAmount   int64 `json:"exempt_amount"`
}

func (t Terms) validate() error {
	for _, r := range []Rate{t.Overtime, t.Holiday, t.Night, t.LeavePayout} {
		if r.BP < 0 || r.BP > 100000 {
			return apperr.InvalidField("terms", "rates are basis points between 0 and 100000")
		}
	}
	sv := t.Severance
	if t.HoursPerDayX100 < 0 || t.HoursPerDayX100 > 2400 || t.DaysPerMonth < 0 || t.DaysPerMonth > 31 || sv.DaysPerYear < 0 || sv.DaysPerYear > 366 || sv.MinYears < 0 {
		return apperr.InvalidField("terms", "hours per day (x100), days per month and severance days are out of range")
	}
	if t.Bonus.DaysPerYear < 0 || t.Bonus.DaysPerYear > 366 || t.Bonus.CapMinWageDays < 0 || t.Bonus.ExemptAmount < 0 || t.MaxOvertimeMinutesPerDay < 0 || t.MaxOvertimeMinutesPerDay > 1440 {
		return apperr.InvalidField("terms", "bonus days, cap, exemption and the overtime cap are out of range")
	}
	if t.Bonus.DaysPerYear > 0 && t.DaysPerMonth == 0 {
		return apperr.InvalidField("terms", "the bonus needs days_per_month")
	}
	if t.HoursPerDayX100 == 0 && (t.Overtime.BP+t.Holiday.BP+t.Night.BP > 0) {
		return apperr.InvalidField("terms", "hourly components need hours_per_day_x100")
	}
	if t.DaysPerMonth == 0 && (t.Overtime.BP+t.Holiday.BP+t.Night.BP+t.LeavePayout.BP+sv.DaysPerYear > 0) {
		return apperr.InvalidField("terms", "wage-based components need days_per_month")
	}
	return nil
}

// Validate checks the numbers are coherent. It cannot check them against the law: that is the accountant's job.
func (r RuleVersion) Validate() error {
	switch {
	case strings.TrimSpace(r.Name) == "" || len(r.Name) > 120:
		return apperr.InvalidField("name", "required, at most 120 characters")
	case r.EffectiveFrom.IsZero():
		return apperr.InvalidField("effective_from", "required")
	case !r.Currency.Valid():
		return apperr.InvalidField("currency", "IRR or IRT")
	case !validBP(r.EmployeeBP) || !validBP(r.EmployerBP) || !validBP(r.UnemploymentBP):
		return apperr.InvalidField("rates", "rates are basis points between 0 and 10000")
	case r.CeilingDaily < 0 || r.FloorDaily < 0 || (r.CeilingDaily > 0 && r.FloorDaily > r.CeilingDaily):
		return apperr.InvalidField("ceiling_daily", "bounds must not be negative and the floor not above the ceiling")
	case len(r.Source) > 500:
		return apperr.InvalidField("source", "at most 500 characters")
	case len(r.Brackets) == 0:
		return apperr.InvalidField("brackets", "at least one bracket is required")
	}
	if err := r.Terms.validate(); err != nil {
		return err
	}
	prev := int64(0)
	for i, b := range r.Brackets {
		last := i == len(r.Brackets)-1
		switch {
		case !validBP(b.RateBP):
			return apperr.InvalidField("brackets", "bracket rates are basis points between 0 and 10000")
		case last && b.UpTo != 0:
			return apperr.InvalidField("brackets", "the last bracket must have no limit (up_to 0)")
		case !last && b.UpTo <= prev:
			return apperr.InvalidField("brackets", "up_to values must be positive and strictly increasing")
		}
		prev = b.UpTo
	}
	return nil
}

func validBP(v int) bool { return v >= 0 && v <= 10000 }

// TaxOn computes the progressive income tax of a monthly taxable income. Each bracket taxes only the part of the
// income inside it; the exact sum is rounded once, half up.
func (r RuleVersion) TaxOn(taxable int64) int64 {
	if taxable <= 0 {
		return 0
	}
	var sum, prev int64
	for _, b := range r.Brackets {
		top := taxable
		if b.UpTo != 0 && b.UpTo < taxable {
			top = b.UpTo
		}
		if top > prev {
			sum += (top - prev) * int64(b.RateBP)
		}
		if b.UpTo == 0 || taxable <= b.UpTo {
			break
		}
		prev = b.UpTo
	}
	return (sum + 5000) / 10000
}

// RuleInput adds a rule version.
type RuleInput struct {
	Name                    string
	EffectiveFrom           time.Time
	Currency                money.Currency
	EmployeeBP              int
	EmployerBP              int
	UnemploymentBP          int
	CeilingDaily            int64
	FloorDaily              int64
	DeductEmployeeInsurance bool
	Brackets                []Bracket
	Terms                   Terms
	Source                  string
	Actor                   string
}

func (in RuleInput) version(id uuid.UUID, now time.Time) RuleVersion {
	return RuleVersion{ID: id, Name: strings.TrimSpace(in.Name), EffectiveFrom: dateOnly(in.EffectiveFrom), Currency: in.Currency,
		EmployeeBP: in.EmployeeBP, EmployerBP: in.EmployerBP, UnemploymentBP: in.UnemploymentBP, CeilingDaily: in.CeilingDaily,
		FloorDaily: in.FloorDaily, DeductEmployeeInsurance: in.DeductEmployeeInsurance, Brackets: in.Brackets, Terms: in.Terms,
		Source: strings.TrimSpace(in.Source), CreatedBy: in.Actor, CreatedAt: now}
}
