package payroll

import (
	"fmt"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Line is one earning of a payslip.
type Line struct {
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Amount    money.Money `json:"amount"`
	Taxable   bool        `json:"taxable"`
	Insurable bool        `json:"insurable"`
}

// Breakdown is the computed pay of one employee for one month.
type Breakdown struct {
	Days, MonthDays int
	Lines           []Line
	Gross           money.Money // all earnings
	Insurable       money.Money // the social-security base after floor and ceiling
	EmployeeIns     money.Money // withheld from the employee
	EmployerIns     money.Money // employer's share, a cost on top of gross
	Unemployment    money.Money // employer's, on top
	TaxableIncome   money.Money
	Tax             money.Money // withheld from the employee
	Deductions      []Deduction // loan installments taken from the pay
	DeductionTotal  money.Money
	Net             money.Money // what the employee receives: gross - employee insurance - tax - deductions
}

// Hours are the extra working times of the month, in minutes.
type Hours struct {
	OvertimeMinutes int `json:"overtime_minutes"`
	HolidayMinutes  int `json:"holiday_minutes"`
	NightMinutes    int `json:"night_minutes"`
}

// Service is the employee's time at the company, for severance.
type ServicePeriod struct{ Start, End time.Time }

// Days of service, both ends included.
func (s ServicePeriod) Days() int { return int(s.End.Sub(s.Start).Hours()/24) + 1 }

// CalcInput is everything the computation needs; it reads nothing else, so it is a pure function.
type CalcInput struct {
	Contract Contract
	Rule     RuleVersion
	Year     int
	Month    int
	Days     int // worked days; 0 = every day the contract covers in the month
	Extras   []Allowance
	// Hours of overtime, holiday and night work, paid by the rule version's terms.
	Hours Hours
	// LeaveDays are unused leave days paid out.
	LeaveDays int
	// Severance pays the end-of-service amount for this service.
	Severance *ServicePeriod
	// Loans due this month; installments are taken after tax, never below a zero net.
	Loans []LoanDue
	// Earlier are contracts of the employee that ended earlier in the same month (the salary changed mid-month): each is paid for the days it covers, the figures are summed
	// into one payslip. Contract is then the latest one, whose wage is the base of overtime, leave and severance. Not combinable with Days.
	Earlier []Contract
	// BonusDays pays the annual bonus for this many days of service in the year (365 = a full year; 0 = no bonus).
	BonusDays int
	// Holidays are the days of the month (1..31) that are official holidays; HolidaysKnown says the calendar of the year is loaded, otherwise only Fridays count as rest days when
	// the holiday work of the month is checked.
	Holidays      []int
	HolidaysKnown bool
}

// MonthSpan returns the first day, the last day and the length of a Jalali month.
func MonthSpan(year, month int) (first, last time.Time, days int, err error) {
	start, next, err := jalali.MonthBounds(year, month, time.UTC)
	if err != nil {
		return start, next, 0, apperr.Invalid("not a valid Jalali month")
	}
	return start, next.AddDate(0, 0, -1), jalali.DaysInMonth(year, month), nil
}

// CoveredDays counts the days of the month the contract is in force.
func (c Contract) CoveredDays(first, last time.Time) int {
	from, to := first, last
	if c.Start.After(from) {
		from = c.Start
	}
	if c.End != nil && c.End.Before(to) {
		to = *c.End
	}
	if to.Before(from) {
		return 0
	}
	return int(to.Sub(from).Hours()/24) + 1
}

// Calculate computes a payslip. Pay of a part month is prorated by days; extras are paid in full. Amounts round half
// up, once per figure.
func Calculate(in CalcInput) (Breakdown, error) {
	first, last, monthDays, err := MonthSpan(in.Year, in.Month)
	if err != nil {
		return Breakdown{}, err
	}
	covered := in.Contract.CoveredDays(first, last)
	for _, e := range in.Earlier {
		covered += e.CoveredDays(first, last)
	}
	if len(in.Earlier) > 0 && in.Days != 0 {
		return Breakdown{}, apperr.New(apperr.KindInvalid, "days_with_two_contracts", "worked days cannot be overridden in a month with two contracts")
	}
	days := in.Days
	if days == 0 {
		days = covered
	}
	if days < 1 || days > covered {
		return Breakdown{}, apperr.New(apperr.KindInvalid, "days_out_of_range", "worked days must be between 1 and the days the contract covers in the month")
	}
	cur := in.Contract.BaseSalary.Currency()
	if in.Rule.Currency != cur {
		return Breakdown{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "the rule version and the contract use different currencies")
	}
	partOf := func(d int, m money.Money) (money.Money, error) {
		if d == monthDays {
			return m, nil
		}
		return m.MulRatio(int64(d), int64(monthDays), money.HalfUp)
	}

	b := Breakdown{Days: days, MonthDays: monthDays}
	var gross, insurable, taxable int64
	add := func(l Line) {
		b.Lines = append(b.Lines, l)
		gross += l.Amount.Amount()
		if l.Insurable {
			insurable += l.Amount.Amount()
		}
		if l.Taxable {
			taxable += l.Amount.Amount()
		}
	}
	// the contracts of the month, earlier ones first; with one contract the days are the worked days (which may be fewer than it covers)
	insuredDays := 0
	segments := append(append([]Contract(nil), in.Earlier...), in.Contract)
	for i, c := range segments {
		d := days
		suffix, label := "", ""
		if len(in.Earlier) > 0 {
			d = c.CoveredDays(first, last)
			if len(segments) > 1 {
				suffix, label = fmt.Sprintf("#%d", i+1), fmt.Sprintf(" (contract %d)", i+1)
			}
		}
		if c.BaseSalary.Currency() != cur {
			return b, apperr.New(apperr.KindInvalid, "currency_mismatch", "the contracts of the month use different currencies")
		}
		if c.Insured {
			insuredDays += d
		}
		base, err := partOf(d, c.BaseSalary)
		if err != nil {
			return b, err
		}
		add(Line{Code: "base" + suffix, Name: "Base salary" + label, Amount: base, Taxable: true, Insurable: c.Insured})
		for _, a := range c.Allowances {
			amt, err := partOf(d, a.Amount)
			if err != nil {
				return b, err
			}
			add(Line{Code: a.Code + suffix, Name: a.Name + label, Amount: amt, Taxable: a.Taxable, Insurable: a.Insurable && c.Insured})
		}
	}
	for _, x := range in.Extras {
		if err := x.validate("extras"); err != nil {
			return b, err
		}
		if x.Amount.Currency() != cur {
			return b, apperr.InvalidField("extras", "must use the currency of the contract")
		}
		add(Line{Code: x.Code, Name: x.Name, Amount: x.Amount, Taxable: x.Taxable, Insurable: x.Insurable})
	}

	if err := checkHours(in, first, last, days); err != nil {
		return b, err
	}
	if err := addTerms(in, base0(in.Contract), cur, add); err != nil {
		return b, err
	}

	var empIns, emprIns, unemp int64
	if insuredDays > 0 {
		if f := in.Rule.FloorDaily * int64(insuredDays); f > 0 && insurable < f {
			insurable = f
		}
		if c := in.Rule.CeilingDaily * int64(insuredDays); c > 0 && insurable > c {
			insurable = c
		}
		base := money.Must(insurable, cur)
		if empIns, err = share(base, in.Rule.EmployeeBP); err != nil {
			return b, err
		}
		if emprIns, err = share(base, in.Rule.EmployerBP); err != nil {
			return b, err
		}
		if unemp, err = share(base, in.Rule.UnemploymentBP); err != nil {
			return b, err
		}
	} else {
		insurable = 0
	}
	if in.Rule.DeductEmployeeInsurance {
		taxable -= empIns
	}
	if taxable < 0 {
		taxable = 0
	}
	tax := in.Rule.TaxOn(taxable)
	net := gross - empIns - tax
	if net < 0 {
		return b, apperr.New(apperr.KindInvalid, "negative_net", "the deductions exceed the gross pay")
	}
	var deducted int64
	for _, l := range in.Loans {
		if l.Due.Currency() != cur {
			return b, apperr.New(apperr.KindInvalid, "currency_mismatch", "a loan uses another currency")
		}
		d := l.Due.Amount()
		if d > net {
			d = net
		}
		if d <= 0 {
			continue
		}
		b.Deductions = append(b.Deductions, Deduction{LoanID: l.LoanID, Amount: money.Must(d, cur)})
		net -= d
		deducted += d
	}
	m := func(v int64) money.Money { return money.Must(v, cur) }
	b.DeductionTotal = m(deducted)
	b.Gross, b.Insurable, b.EmployeeIns, b.EmployerIns, b.Unemployment = m(gross), m(insurable), m(empIns), m(emprIns), m(unemp)
	b.TaxableIncome, b.Tax, b.Net = m(taxable), m(tax), m(net)
	return b, nil
}

func share(base money.Money, bp int) (int64, error) {
	v, err := base.Percent(int64(bp), money.HalfUp)
	return v.Amount(), err
}

// base0 is the monthly wage the hourly, daily and severance amounts are computed on: the base salary plus the
// allowances flagged as wage base, whole, not prorated.
func base0(c Contract) int64 {
	v := c.BaseSalary.Amount()
	for _, a := range c.Allowances {
		if a.WageBase {
			v += a.Amount.Amount()
		}
	}
	return v
}

func notConfigured(what string) error {
	return apperr.New(apperr.KindConflict, "terms_not_configured", what+" is not configured in the rule version in force: add a version with its terms")
}

// addTerms adds the lines of overtime, holiday, night, leave payout and severance.
func addTerms(in CalcInput, wage int64, cur money.Currency, add func(Line)) error {
	t := in.Rule.Terms
	h := in.Hours
	if h.OvertimeMinutes < 0 || h.HolidayMinutes < 0 || h.NightMinutes < 0 || in.LeaveDays < 0 {
		return apperr.Invalid("hours and days must not be negative")
	}
	wageMoney := money.Must(wage, cur)
	hourly := func(name, code string, r Rate, minutes int) error {
		if minutes == 0 {
			return nil
		}
		if r.BP == 0 || t.HoursPerDayX100 == 0 || t.DaysPerMonth == 0 {
			return notConfigured(name)
		}
		// wage / (days x hours per day) x multiplier x hours
		amt, err := wageMoney.MulRatio(int64(r.BP)*int64(minutes)*100, int64(t.DaysPerMonth)*int64(t.HoursPerDayX100)*10000*60, money.HalfUp)
		if err != nil {
			return err
		}
		add(Line{Code: code, Name: name, Amount: amt, Taxable: r.Taxable, Insurable: r.Insurable})
		return nil
	}
	if err := hourly("Overtime", "overtime", t.Overtime, h.OvertimeMinutes); err != nil {
		return err
	}
	if err := hourly("Holiday work", "holiday", t.Holiday, h.HolidayMinutes); err != nil {
		return err
	}
	if err := hourly("Night work premium", "night", t.Night, h.NightMinutes); err != nil {
		return err
	}
	if in.LeaveDays > 0 {
		if t.LeavePayout.BP == 0 || t.DaysPerMonth == 0 {
			return notConfigured("leave payout")
		}
		amt, err := wageMoney.MulRatio(int64(t.LeavePayout.BP)*int64(in.LeaveDays), int64(t.DaysPerMonth)*10000, money.HalfUp)
		if err != nil {
			return err
		}
		add(Line{Code: "leave_payout", Name: "Unused leave payout", Amount: amt, Taxable: t.LeavePayout.Taxable, Insurable: t.LeavePayout.Insurable})
	}
	if in.BonusDays != 0 {
		bn := t.Bonus
		if in.BonusDays < 0 || in.BonusDays > 366 {
			return apperr.InvalidField("bonus_days", "must be between 1 and 366")
		}
		if bn.DaysPerYear == 0 || t.DaysPerMonth == 0 {
			return notConfigured("the annual bonus")
		}
		// wage / days per month x bonus days per year x service days / 365, at most the cap (a multiple of the minimum daily wage, prorated the same way)
		amt, err := wageMoney.MulRatio(int64(bn.DaysPerYear)*int64(in.BonusDays), int64(t.DaysPerMonth)*365, money.HalfUp)
		if err != nil {
			return err
		}
		if bn.CapMinWageDays > 0 && in.Rule.FloorDaily > 0 {
			capAmt, err := money.Must(in.Rule.FloorDaily, cur).MulRatio(int64(bn.CapMinWageDays)*int64(in.BonusDays), 365, money.HalfUp)
			if err != nil {
				return err
			}
			if capAmt.Amount() < amt.Amount() {
				amt = capAmt
			}
		}
		exempt := bn.ExemptAmount
		if exempt == 0 && len(in.Rule.Brackets) > 0 && in.Rule.Brackets[0].RateBP == 0 {
			exempt = in.Rule.Brackets[0].UpTo
		}
		exemptPart := min(amt.Amount(), exempt)
		if exemptPart > 0 {
			add(Line{Code: "bonus", Name: "Annual bonus (exempt part)", Amount: money.Must(exemptPart, cur), Taxable: false, Insurable: false})
		}
		if rest := amt.Amount() - exemptPart; rest > 0 {
			add(Line{Code: "bonus_taxable", Name: "Annual bonus (taxable excess)", Amount: money.Must(rest, cur), Taxable: true, Insurable: false})
		}
	}
	if in.Severance != nil {
		sv := t.Severance
		if sv.DaysPerYear == 0 || t.DaysPerMonth == 0 {
			return notConfigured("severance")
		}
		days := in.Severance.Days()
		if days < 365*sv.MinYears {
			return apperr.New(apperr.KindConflict, "service_too_short", "the service is shorter than the minimum for severance")
		}
		// wage x (days per year / days per month) x years of service; a year is taken as 365 days
		amt, err := wageMoney.MulRatio(int64(sv.DaysPerYear)*int64(days), int64(t.DaysPerMonth)*365, money.HalfUp)
		if err != nil {
			return err
		}
		add(Line{Code: "severance", Name: "Severance", Amount: amt, Taxable: sv.Taxable, Insurable: sv.Insurable})
	}
	return nil
}

// checkHours refuses overtime above the legal cap per worked day (Labour Law art. 59, set in the rule version) and holiday work above what the rest days of the month can hold.
func checkHours(in CalcInput, first, last time.Time, days int) error {
	t := in.Rule.Terms
	if cap := t.MaxOvertimeMinutesPerDay; cap > 0 && in.Hours.OvertimeMinutes > cap*days {
		return apperr.New(apperr.KindConflict, "overtime_over_cap", fmt.Sprintf("overtime is limited to %d minutes per worked day: at most %d minutes in this month", cap, cap*days))
	}
	if in.Hours.HolidayMinutes > 0 && t.HoursPerDayX100 > 0 {
		rest := map[int]bool{}
		for d, day := 1, first; !day.After(last); d, day = d+1, day.AddDate(0, 0, 1) {
			if day.Weekday() == time.Friday {
				rest[d] = true
			}
		}
		if in.HolidaysKnown {
			for _, d := range in.Holidays {
				rest[d] = true
			}
		}
		if limit := len(rest) * t.HoursPerDayX100 * 60 / 100; in.Hours.HolidayMinutes > limit {
			return apperr.New(apperr.KindConflict, "holiday_work_over_calendar", fmt.Sprintf("the month has %d rest days (Fridays and official holidays): at most %d minutes of holiday work", len(rest), limit))
		}
	}
	return nil
}
