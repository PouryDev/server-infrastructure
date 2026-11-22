package payroll

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func irr(v int64) money.Money { return money.Must(v, money.IRR) }

// testRule has the shape of a real rule set; the figures are test data, not legal advice.
func testRule() RuleVersion {
	return RuleVersion{Name: "test", EffectiveFrom: time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), Currency: money.IRR,
		EmployeeBP: 700, EmployerBP: 2000, UnemploymentBP: 300, CeilingDaily: 38_792_950, FloorDaily: 5_541_850, DeductEmployeeInsurance: true,
		Brackets: []Bracket{{400_000_000, 0}, {800_000_000, 1000}, {1_000_000_000, 1500}, {1_200_000_000, 2000}, {1_400_000_000, 2500}, {0, 3000}},
		Terms: Terms{HoursPerDayX100: 733, DaysPerMonth: 30, Overtime: Rate{14000, true, true}, Holiday: Rate{14000, true, true}, Night: Rate{3500, true, true},
			LeavePayout: Rate{10000, true, false}, Severance: Severance{DaysPerYear: 30, MinYears: 1}}}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestTaxIsProgressive(t *testing.T) {
	r := testRule()
	for _, c := range []struct{ taxable, tax int64 }{
		{0, 0}, {-5, 0}, {400_000_000, 0}, {500_000_000, 10_000_000}, {800_000_000, 40_000_000}, {900_000_000, 55_000_000},
		{1_400_000_000, 160_000_000}, {1_418_534_805, 165_560_442}, // 5,560,441.5 rounds half up
	} {
		if got := r.TaxOn(c.taxable); got != c.tax {
			t.Errorf("TaxOn(%d) = %d, want %d", c.taxable, got, c.tax)
		}
	}
}

func TestRuleValidation(t *testing.T) {
	if err := testRule().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*RuleVersion){
		"no brackets":           func(r *RuleVersion) { r.Brackets = nil },
		"last bracket limited":  func(r *RuleVersion) { r.Brackets[len(r.Brackets)-1].UpTo = 9 },
		"not increasing":        func(r *RuleVersion) { r.Brackets[2].UpTo = r.Brackets[1].UpTo },
		"rate over 100%":        func(r *RuleVersion) { r.EmployeeBP = 10001 },
		"floor above ceiling":   func(r *RuleVersion) { r.FloorDaily = r.CeilingDaily + 1 },
		"missing effective day": func(r *RuleVersion) { r.EffectiveFrom = time.Time{} },
		"no name":               func(r *RuleVersion) { r.Name = " " },
		"mid bracket unlimited": func(r *RuleVersion) { r.Brackets[1].UpTo = 0 },
	}
	for name, mutate := range bad {
		r := testRule()
		r.Brackets = append([]Bracket(nil), r.Brackets...)
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestCalculateFullMonth(t *testing.T) {
	c := Contract{BaseSalary: irr(300_000_000), Insured: true, Start: day(2026, 1, 1)}
	b, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8})
	if err != nil {
		t.Fatal(err)
	}
	// 1405/08 has 30 days. Employee 7% = 21M, employer 20% = 60M, unemployment 3% = 9M, below the exemption: no tax.
	if b.Days != 30 || b.Gross != irr(300_000_000) || b.EmployeeIns != irr(21_000_000) || b.EmployerIns != irr(60_000_000) ||
		b.Unemployment != irr(9_000_000) || b.Tax != irr(0) || b.Net != irr(279_000_000) {
		t.Fatalf("%+v", b)
	}
}

func TestCalculateCeilingAndTax(t *testing.T) {
	c := Contract{BaseSalary: irr(1_500_000_000), Insured: true, Start: day(2026, 1, 1),
		Allowances: []Allowance{{Code: "bon", Name: "Voucher", Amount: irr(100_000_000)}}} // neither taxable nor insurable
	b, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8})
	if err != nil {
		t.Fatal(err)
	}
	// ceiling: 38,792,950 x 30 days = 1,163,788,500; the shares of that base
	if b.Insurable != irr(1_163_788_500) || b.EmployeeIns != irr(81_465_195) || b.EmployerIns != irr(232_757_700) || b.Unemployment != irr(34_913_655) {
		t.Fatalf("%+v", b)
	}
	if b.TaxableIncome != irr(1_418_534_805) || b.Tax != irr(165_560_442) || b.Gross != irr(1_600_000_000) || b.Net != irr(1_352_974_363) {
		t.Fatalf("%+v", b)
	}
}

func TestCalculateFloorAndUninsured(t *testing.T) {
	r := testRule()
	low := Contract{BaseSalary: irr(100_000_000), Insured: true, Start: day(2026, 1, 1)}
	b, err := Calculate(CalcInput{Contract: low, Rule: r, Year: 1405, Month: 8})
	if err != nil {
		t.Fatal(err)
	}
	if b.Insurable != irr(166_255_500) { // 5,541,850 x 30
		t.Fatalf("the floor applies: %+v", b)
	}
	low.Insured = false
	b, err = Calculate(CalcInput{Contract: low, Rule: r, Year: 1405, Month: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !b.EmployeeIns.IsZero() || !b.EmployerIns.IsZero() || b.Net != irr(100_000_000) {
		t.Fatalf("an uninsured employee pays no social security: %+v", b)
	}
}

func TestCalculateProratesPartMonth(t *testing.T) {
	// the contract starts on 1405/08/11: 20 of 30 days
	start, _, _, _ := MonthSpan(1405, 8)
	c := Contract{BaseSalary: irr(300_000_000), Insured: true, Start: start.AddDate(0, 0, 10),
		Allowances: []Allowance{{Name: "Housing", Amount: irr(30_000_000), Taxable: true}}}
	b, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8, Extras: []Allowance{{Name: "Bonus", Amount: irr(5_000_000), Taxable: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Days != 20 || b.Lines[0].Amount != irr(200_000_000) || b.Lines[1].Amount != irr(20_000_000) || b.Lines[2].Amount != irr(5_000_000) {
		t.Fatalf("pay is prorated, extras are not: %+v", b.Lines)
	}
	if b.Insurable != irr(200_000_000) { // housing and bonus are not insurable
		t.Fatalf("%+v", b)
	}
	// days outside what the contract covers are refused
	if _, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8, Days: 21}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("got %v", err)
	}
	if _, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 7}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a month before the contract has no days: %v", err)
	}
}

func TestInsuranceBaseUsesDaysOfTheMonth(t *testing.T) {
	// 1405/01 has 31 days, so the ceiling is 31 daily ceilings
	c := Contract{BaseSalary: irr(5_000_000_000), Insured: true, Start: day(2026, 1, 1)}
	b, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 1})
	if err != nil {
		t.Fatal(err)
	}
	if b.MonthDays != 31 || b.Insurable != irr(38_792_950*31) {
		t.Fatalf("%+v", b)
	}
}

func TestContractOverlap(t *testing.T) {
	end := day(2026, 6, 30)
	a := Contract{Start: day(2026, 1, 1), End: &end}
	if overlaps(a, Contract{Start: day(2026, 7, 1)}) {
		t.Fatal("the next day starts a new contract")
	}
	if !overlaps(a, Contract{Start: day(2026, 6, 30)}) || !overlaps(Contract{Start: day(2025, 1, 1)}, a) {
		t.Fatal("an open contract overlaps")
	}
}

func TestEmployeeInputNormalisesDigits(t *testing.T) {
	in, err := (EmployeeInput{Code: "E1", Name: "Ali", NationalID: "۰۰۱۲۳۴۵۶۷۸"}).validate()
	if err != nil || in.NationalID != "0012345678" {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := (EmployeeInput{Code: "E1", Name: "Ali", NationalID: "123"}).validate(); err == nil {
		t.Fatal("a national id has 10 digits")
	}
}

func TestOvertimeHolidayNightLeaveAndSeverance(t *testing.T) {
	c := Contract{BaseSalary: irr(300_000_000), Insured: true, Start: day(2024, 1, 1),
		Allowances: []Allowance{{Name: "Seniority", Amount: irr(0), WageBase: true}, {Name: "Housing", Amount: irr(30_000_000), Taxable: true}}}
	in := CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8, Hours: Hours{OvertimeMinutes: 120, HolidayMinutes: 120, NightMinutes: 60}, LeaveDays: 3}
	b, err := Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, l := range b.Lines {
		got[l.Code] = l.Amount.Amount()
	}
	// the wage base is 300M (housing is not flagged): 2h x 1.4 x 300M / (30 x 7.33); night is the 35% premium alone; a leave day is wage/30
	if got["overtime"] != 3_819_918 || got["holiday"] != 3_819_918 || got["night"] != 477_490 || got["leave_payout"] != 30_000_000 {
		t.Fatalf("%v", got)
	}
	// leave payout is not insurable (per the terms), overtime is
	if want := int64(300_000_000 + 3_819_918*2 + 477_490); b.Insurable.Amount() != want {
		t.Fatalf("insurable %d, want %d", b.Insurable.Amount(), want)
	}

	// 2025-01-01 .. 2026-12-31 is 730 days = 2 years: two months of wage, tax-free and insurance-free
	in = CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8, Severance: &ServicePeriod{Start: day(2025, 1, 1), End: day(2026, 12, 31)}}
	b, err = Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	sv := b.Lines[len(b.Lines)-1]
	if sv.Code != "severance" || sv.Amount != irr(600_000_000) || sv.Taxable || sv.Insurable {
		t.Fatalf("%+v", sv)
	}
	if b.Insurable != irr(300_000_000) || b.TaxableIncome != irr(330_000_000-21_000_000) {
		t.Fatalf("severance must not enter the insurance or tax base: %+v", b)
	}
	in.Severance = &ServicePeriod{Start: day(2026, 6, 1), End: day(2026, 12, 31)}
	if _, err := Calculate(in); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("less than the minimum service: %v", err)
	}
}

func TestTermsMustBeConfiguredToBeUsed(t *testing.T) {
	r := testRule()
	r.Terms = Terms{}
	c := Contract{BaseSalary: irr(300_000_000), Start: day(2024, 1, 1)}
	for name, in := range map[string]CalcInput{
		"overtime": {Hours: Hours{OvertimeMinutes: 60}}, "leave": {LeaveDays: 1}, "severance": {Severance: &ServicePeriod{Start: day(2020, 1, 1), End: day(2026, 12, 1)}},
	} {
		in.Contract, in.Rule, in.Year, in.Month = c, r, 1405, 8
		if _, err := Calculate(in); !apperr.Is(err, apperr.KindConflict) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8, Hours: Hours{OvertimeMinutes: -1}}); err == nil {
		t.Error("negative hours")
	}
	bad := testRule()
	bad.Terms.HoursPerDayX100 = 0
	if err := bad.Validate(); err == nil {
		t.Error("hourly components need the hours per day")
	}
}

func TestLoanInstallmentsComeAfterTaxAndNeverBelowZero(t *testing.T) {
	c := Contract{BaseSalary: irr(300_000_000), Insured: true, Start: day(2026, 1, 1)}
	l1, l2 := uuid.New(), uuid.New()
	b, err := Calculate(CalcInput{Contract: c, Rule: testRule(), Year: 1405, Month: 8,
		Loans: []LoanDue{{l1, irr(200_000_000)}, {l2, irr(200_000_000)}}})
	if err != nil {
		t.Fatal(err)
	}
	// net before loans is 279M: the first loan takes 200M, the second only what is left
	if len(b.Deductions) != 2 || b.Deductions[0].Amount != irr(200_000_000) || b.Deductions[1].Amount != irr(79_000_000) || b.DeductionTotal != irr(279_000_000) || !b.Net.IsZero() {
		t.Fatalf("%+v", b)
	}
	if b.Tax != irr(0) || b.EmployeeIns != irr(21_000_000) {
		t.Fatal("loans never change tax or insurance")
	}
}

func TestLoanDueAndStart(t *testing.T) {
	l := Loan{Installment: irr(100), Remaining: irr(60), StartYear: 1405, StartMonth: 8}
	if l.DueOf() != irr(60) || (Loan{Installment: irr(100), Remaining: irr(500)}).DueOf() != irr(100) {
		t.Fatal("the last installment is what remains")
	}
	if l.StartedBy(1405, 7) || !l.StartedBy(1405, 8) || !l.StartedBy(1406, 1) {
		t.Fatal("deductions begin at the start month")
	}
	in := LoanInput{IdemKey: "k", Principal: irr(100), Installment: irr(200), StartYear: 1405, StartMonth: 8}
	if in.validate() == nil {
		t.Fatal("an installment above the principal")
	}
}

func TestRemitInputValidation(t *testing.T) {
	ok := RemitInput{IdemKey: "k", Kind: LiabilityTax, Amount: irr(5), PeriodYear: 1405, PeriodMonth: 8}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RemitInput){
		"kind": func(r *RemitInput) { r.Kind = "x" }, "amount": func(r *RemitInput) { r.Amount = irr(0) }, "key": func(r *RemitInput) { r.IdemKey = "" },
		"half a period": func(r *RemitInput) { r.PeriodMonth = 0 }, "bad month": func(r *RemitInput) { r.PeriodMonth = 13 },
	} {
		r := ok
		mutate(&r)
		if r.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
