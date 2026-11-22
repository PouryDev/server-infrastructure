package mizan_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/payroll"
)

var jan1 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func (e env) employee(t *testing.T, code string, base int64) payroll.Employee {
	t.Helper()
	emp, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: code, Name: "Employee " + code, Actor: "tester"})
	must(t, err)
	_, err = e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: emp.ID, BaseSalary: irr(base), Insured: true, Start: jan1, Actor: "tester"})
	must(t, err)
	return emp
}

func (e env) slip(t *testing.T, emp payroll.Employee, month int) payroll.Payslip {
	t.Helper()
	p, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: 1405, Month: month, Actor: "tester"})
	must(t, err)
	return p
}

func TestPayslipBooksTheMonthAndIsIdempotent(t *testing.T) {
	e := setup(t, true)
	emp := e.employee(t, "E1", 300_000_000)
	p := e.slip(t, emp, 8)
	if p.Status != payroll.Issued || p.Journal == nil || p.Net != irr(279_000_000) || p.Days != 30 {
		t.Fatalf("%+v", p)
	}
	// Dr expense 300M + employer cost 69M; Cr payable 279M, insurance 21M + 69M, no tax below the exemption
	for acc, want := range map[string]int64{mizan.AccSalaryExpense: 300_000_000, mizan.AccEmployerInsuranceExpense: 69_000_000,
		mizan.AccSalariesPayable: 279_000_000, mizan.AccInsurancePayable: 90_000_000, mizan.AccPayrollTaxPayable: 0} {
		if got := e.bal(t, acc); got != want {
			t.Errorf("%s = %d, want %d", acc, got, want)
		}
	}
	again := e.slip(t, emp, 8)
	if again.ID != p.ID || e.bal(t, mizan.AccSalaryExpense) != 300_000_000 {
		t.Fatal("issuing the same (employee, month) again must return the payslip and book nothing")
	}
	e.balanced(t)
	rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Unclassified) != 0 {
		t.Fatalf("payroll accounts must map to the chart: %+v", rep.Unclassified)
	}
}

func TestPayslipWithTaxAndNoChart(t *testing.T) {
	e := setup(t, false) // coding stays optional
	emp := e.employee(t, "E1", 1_500_000_000)
	p := e.slip(t, emp, 8)
	if p.Tax != irr(165_560_442) || p.EmployeeIns != irr(81_465_195) {
		t.Fatalf("%+v", p.Breakdown)
	}
	if e.bal(t, mizan.AccPayrollTaxPayable) != 165_560_442 || e.bal(t, mizan.AccSalariesPayable) != p.Net.Amount() {
		t.Fatal("tax and net pay are booked")
	}
	e.balanced(t)
}

func TestFutureMonthAndMissingContractOrRule(t *testing.T) {
	e := setup(t, false)
	emp := e.employee(t, "E1", 300_000_000)
	for _, c := range []struct {
		name  string
		month int
		year  int
		code  string
	}{{"future month", 9, 1405, ""}, {"before the first rule version", 12, 1404, "no_rule_version"}} {
		_, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: c.year, Month: c.month})
		if !apperr.Is(err, apperr.KindInvalid) && !apperr.Is(err, apperr.KindConflict) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	other, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: "E2", Name: "No contract"})
	must(t, err)
	if _, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: other.ID, Year: 1405, Month: 8}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no contract: %v", err)
	}
	if _, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: "E2", Name: "dup"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("employee codes are unique: %v", err)
	}
}

func jdate(y, m, d int) time.Time {
	t, _ := jalali.Date{Year: y, Month: m, Day: d}.Time(time.UTC)
	return t
}

func TestRuleVersionsAreDataWithEffectiveDates(t *testing.T) {
	e := setup(t, false)
	emp := e.employee(t, "E1", 300_000_000)
	seed, err := e.m.Payroll.RuleAt(e.ctx, jdate(1405, 8, 1))
	must(t, err)
	if seed.EmployeeBP != 700 || !seed.EffectiveFrom.Equal(jdate(1405, 1, 1)) || len(seed.Brackets) != 6 {
		t.Fatalf("the 1405 starting data: %+v", seed)
	}
	july := e.slip(t, emp, 7) // month 7 is paid under the seeded version

	v2, err := e.m.Payroll.AddRule(e.ctx, payroll.RuleInput{Name: "Mid-year change", EffectiveFrom: jdate(1405, 8, 1), Currency: "IRR", EmployeeBP: 500, EmployerBP: 2000,
		UnemploymentBP: 300, CeilingDaily: seed.CeilingDaily, FloorDaily: seed.FloorDaily, DeductEmployeeInsurance: true, Brackets: seed.Brackets, Source: "test", Actor: "tester"})
	must(t, err)
	aban := e.slip(t, emp, 8)
	if aban.RuleID != v2.ID || aban.EmployeeIns != irr(15_000_000) || july.RuleID != seed.ID || july.EmployeeIns != irr(21_000_000) {
		t.Fatalf("each month uses the version in force: july %+v, aban %+v", july.Breakdown, aban.Breakdown)
	}
	// stored payslips are not recomputed
	stored, err := e.m.Payroll.Payslip(e.ctx, july.ID)
	must(t, err)
	if stored.EmployeeIns != irr(21_000_000) || len(stored.Lines) != 1 {
		t.Fatalf("%+v", stored)
	}
	// one version per effective date; invalid data is refused; unused versions can be removed, used ones not
	if _, err := e.m.Payroll.AddRule(e.ctx, payroll.RuleInput{Name: "dup", EffectiveFrom: jdate(1405, 8, 1), Currency: "IRR", Brackets: seed.Brackets}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("same date: %v", err)
	}
	if _, err := e.m.Payroll.AddRule(e.ctx, payroll.RuleInput{Name: "bad", EffectiveFrom: jdate(1406, 1, 1), Currency: "IRR", EmployeeBP: 20000, Brackets: seed.Brackets}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	v3, err := e.m.Payroll.AddRule(e.ctx, payroll.RuleInput{Name: "1406", EffectiveFrom: jdate(1406, 1, 1), Currency: "IRR", EmployeeBP: 700, Brackets: seed.Brackets})
	must(t, err)
	must(t, e.m.Payroll.DeleteRule(e.ctx, v3.ID, "tester"))
	if err := e.m.Payroll.DeleteRule(e.ctx, v2.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a used version is permanent: %v", err)
	}
	if list, err := e.m.Payroll.Rules(e.ctx); err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
}

func TestVoidReversesAndAllowsReissue(t *testing.T) {
	e := setup(t, false)
	emp := e.employee(t, "E1", 300_000_000)
	p := e.slip(t, emp, 8)
	v, err := e.m.Payroll.VoidPayslip(e.ctx, p.ID, "wrong month", "tester")
	must(t, err)
	if v.Status != payroll.Void || v.VoidJournal == nil {
		t.Fatalf("%+v", v)
	}
	for _, acc := range []string{mizan.AccSalaryExpense, mizan.AccSalariesPayable, mizan.AccInsurancePayable, mizan.AccEmployerInsuranceExpense} {
		if e.bal(t, acc) != 0 {
			t.Fatalf("%s must be reversed to zero", acc)
		}
	}
	if again, err := e.m.Payroll.VoidPayslip(e.ctx, p.ID, "again", "tester"); err != nil || again.Status != payroll.Void {
		t.Fatalf("voiding twice is a no-op: %+v %v", again, err)
	}
	p2 := e.slip(t, emp, 8)
	if p2.ID == p.ID || e.bal(t, mizan.AccSalaryExpense) != 300_000_000 {
		t.Fatal("a voided month can be issued again")
	}
	e.balanced(t)
}

func ids(ps ...payroll.Payslip) []uuid.UUID {
	out := make([]uuid.UUID, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func TestPayRunPaysOnceAndIsIdempotent(t *testing.T) {
	e := setup(t, true)
	a, b := e.employee(t, "A", 300_000_000), e.employee(t, "B", 200_000_000)
	pa, pb := e.slip(t, a, 8), e.slip(t, b, 8) // net 279M and 186M
	if _, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{Year: 1405, Month: 8}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a key is required: %v", err)
	}
	run, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r1", Year: 1405, Month: 8, PayslipIDs: ids(pa), Actor: "tester"})
	must(t, err)
	if run.Count != 1 || run.Total != pa.Net || run.Journal == nil {
		t.Fatalf("%+v", run)
	}
	if e.bal(t, mizan.AccSalariesPayable) != pb.Net.Amount() || e.bal(t, mizan.AccBank) != -pa.Net.Amount() {
		t.Fatal("the run debits salaries payable and credits the bank")
	}
	again, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r1", Year: 1405, Month: 8, PayslipIDs: ids(pa)})
	must(t, err)
	if again.ID != run.ID || e.bal(t, mizan.AccBank) != -pa.Net.Amount() {
		t.Fatal("the same key returns the same run and pays nothing again")
	}
	if _, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r1", Year: 1405, Month: 7}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a key cannot be reused for another run: %v", err)
	}
	// an already paid payslip cannot be selected again, and cannot be voided
	if _, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r2", Year: 1405, Month: 8, PayslipIDs: ids(pa, pb)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("paid payslip: %v", err)
	}
	if _, err := e.m.Payroll.VoidPayslip(e.ctx, pa.ID, "late", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a paid payslip cannot be voided: %v", err)
	}
	// no ids: everything still issued, paid from a second account
	second := e.secondAccount(t)
	run2, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r3", Year: 1405, Month: 8, PayFrom: second.LedgerCode, Actor: "tester"})
	must(t, err)
	if run2.Count != 1 || run2.Total != pb.Net || e.bal(t, second.LedgerCode) != -pb.Net.Amount() || e.bal(t, mizan.AccSalariesPayable) != 0 {
		t.Fatalf("%+v", run2)
	}
	if _, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: "r4", Year: 1405, Month: 8}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("nothing left to pay: %v", err)
	}
	e.balanced(t)
}

func TestConcurrentRunsPayEachPayslipOnce(t *testing.T) {
	e := setup(t, false)

	var total int64
	for i := 0; i < 5; i++ {
		p := e.slip(t, e.employee(t, fmt.Sprintf("E%d", i), 300_000_000), 8)

		total += p.Net.Amount()
	}
	var wg sync.WaitGroup
	var won int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.m.Payroll.PayRun(e.ctx, payroll.RunInput{IdemKey: fmt.Sprintf("race-%d", i), Year: 1405, Month: 8})
			if err == nil {
				atomic.AddInt32(&won, 1)
			}
		}(i)
	}
	wg.Wait()
	if won != 1 || e.bal(t, mizan.AccBank) != -total || e.bal(t, mizan.AccSalariesPayable) != 0 {
		t.Fatalf("won=%d bank=%d: money must leave exactly once", won, e.bal(t, mizan.AccBank))
	}
	e.balanced(t)
}

func TestConcurrentIssueMakesOnePayslip(t *testing.T) {
	e := setup(t, false)
	emp := e.employee(t, "E1", 300_000_000)
	var wg sync.WaitGroup
	got := make(chan uuid.UUID, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: 1405, Month: 8, Actor: "tester"}); err == nil {
				got <- p.ID
			}
		}()
	}
	wg.Wait()
	close(got)
	seen := map[uuid.UUID]bool{}
	for id := range got {
		seen[id] = true
	}
	if len(seen) != 1 || e.bal(t, mizan.AccSalaryExpense) != 300_000_000 {
		t.Fatalf("payslips %d, expense %d", len(seen), e.bal(t, mizan.AccSalaryExpense))
	}
}

func TestContractsDoNotOverlapAndPartMonthIsProrated(t *testing.T) {
	e := setup(t, false)
	emp, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: "E1", Name: "Ali"})
	must(t, err)
	first, err := e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: emp.ID, BaseSalary: irr(300_000_000), Insured: true, Start: jan1})
	must(t, err)
	if _, err := e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: emp.ID, BaseSalary: irr(400_000_000), Start: jdate(1405, 8, 11)}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an open contract blocks a new one: %v", err)
	}
	_, err = e.m.Payroll.EndContract(e.ctx, first.ID, jdate(1405, 8, 10), "tester")
	must(t, err)
	if _, err := e.m.Payroll.EndContract(e.ctx, first.ID, jdate(1405, 8, 12), "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an ended contract stays ended: %v", err)
	}
	_, err = e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: emp.ID, BaseSalary: irr(400_000_000), Insured: true, Start: jdate(1405, 8, 11)})
	must(t, err)
	// both contracts cover month 8: the salary change is paid by segments of ONE payslip (1-10: 300M for 10 of 30 days, 11-30: 400M for 20 of 30 days)
	m8 := e.slip(t, emp, 8)
	if m8.Gross != irr(100_000_000+266_666_667) || m8.Days != 30 {
		t.Fatalf("two contracts in a month: %+v", m8.Breakdown)
	}
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: 1405, Month: 8, Days: 25}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("worked days cannot be overridden with two contracts: %v", err)
	}
	p := e.slip(t, emp, 7) // month 7: only the first contract
	if p.Gross != irr(300_000_000) {
		t.Fatalf("%+v", p.Breakdown)
	}
}

func TestRemittancePaysOnlyWhatIsOwedAndOnce(t *testing.T) {
	e := setup(t, true)
	e.slip(t, e.employee(t, "A", 1_500_000_000), 8) // tax 165,560,442; insurance 81,465,195 + 267,671,355 = 349,136,550
	tax := e.bal(t, mizan.AccPayrollTaxPayable)
	if tax != 165_560_442 || e.bal(t, mizan.AccInsurancePayable) != 349_136_550 {
		t.Fatalf("tax %d", tax)
	}
	pay := func(key string, kind payroll.Liability, amount int64) (payroll.Remittance, error) {
		return e.m.Payroll.Remit(e.ctx, payroll.RemitInput{IdemKey: key, Kind: kind, Amount: irr(amount), PeriodYear: 1405, PeriodMonth: 8, Reference: "R-" + key, Actor: "tester"})
	}
	r, err := pay("t1", payroll.LiabilityTax, 100_000_000)
	must(t, err)
	if r.Journal == nil || e.bal(t, mizan.AccPayrollTaxPayable) != tax-100_000_000 || e.bal(t, mizan.AccBank) != -100_000_000 {
		t.Fatalf("%+v", r)
	}
	if again, err := pay("t1", payroll.LiabilityTax, 100_000_000); err != nil || again.ID != r.ID || e.bal(t, mizan.AccBank) != -100_000_000 {
		t.Fatalf("a retry pays nothing more: %+v %v", again, err)
	}
	if _, err := pay("t1", payroll.LiabilityTax, 5); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a key cannot be reused for another amount: %v", err)
	}
	if _, err := pay("t2", payroll.LiabilityTax, tax); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("more than is owed: %v", err)
	}
	if _, err := pay("i1", payroll.LiabilityInsurance, 349_136_550); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccInsurancePayable) != 0 {
		t.Fatal("the insurer was paid in full")
	}
	if list, err := e.m.Payroll.Remittances(e.ctx, ""); err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	e.balanced(t)
}

func TestConcurrentRemittancesNeverOverpay(t *testing.T) {
	e := setup(t, false)
	e.slip(t, e.employee(t, "A", 1_500_000_000), 8) // 165,560,442 of tax owed
	var wg sync.WaitGroup
	var won int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.m.Payroll.Remit(e.ctx, payroll.RemitInput{IdemKey: fmt.Sprintf("c%d", i), Kind: payroll.LiabilityTax, Amount: irr(50_000_000)})
			if err == nil {
				atomic.AddInt32(&won, 1)
			}
		}(i)
	}
	wg.Wait()
	if won != 3 || e.bal(t, mizan.AccPayrollTaxPayable) != 165_560_442-150_000_000 {
		t.Fatalf("won %d, payable %d: only 3 x 50M fit in 165.5M", won, e.bal(t, mizan.AccPayrollTaxPayable))
	}
	e.balanced(t)
}

func TestLoanIsRepaidFromPayslipsAndRestoredOnVoid(t *testing.T) {
	e := setup(t, true)
	emp := e.employee(t, "A", 300_000_000) // net 279M a month
	grant := func(key string) (payroll.Loan, error) {
		return e.m.Payroll.GrantLoan(e.ctx, payroll.LoanInput{IdemKey: key, EmployeeID: emp.ID, Principal: irr(100_000_000), Installment: irr(60_000_000),
			StartYear: 1405, StartMonth: 7, Actor: "tester"})
	}
	loan, err := grant("l1")
	must(t, err)
	if again, err := grant("l1"); err != nil || again.ID != loan.ID || e.bal(t, mizan.AccEmployeeLoans) != 100_000_000 {
		t.Fatalf("a retry lends nothing more: %+v %v", again, err)
	}
	if e.bal(t, mizan.AccBank) != -100_000_000 {
		t.Fatal("the loan leaves the bank and becomes a receivable from the employee")
	}
	if p := e.slip(t, emp, 6); !p.DeductionTotal.IsZero() { // before the start month
		t.Fatalf("%+v", p.Breakdown)
	}
	m7 := e.slip(t, emp, 7)
	if m7.DeductionTotal != irr(60_000_000) || m7.Net != irr(219_000_000) || e.bal(t, mizan.AccEmployeeLoans) != 40_000_000 {
		t.Fatalf("%+v", m7.Breakdown)
	}
	m8 := e.slip(t, emp, 8) // the last installment is what remains
	if m8.DeductionTotal != irr(40_000_000) {
		t.Fatalf("%+v", m8.Breakdown)
	}
	if got, _ := e.m.Payroll.Loan(e.ctx, loan.ID); got.Status != payroll.LoanClosed || !got.Remaining.IsZero() || e.bal(t, mizan.AccEmployeeLoans) != 0 {
		t.Fatalf("%+v", got)
	}
	// voiding a payslip gives its installment back and reopens the loan
	_, err = e.m.Payroll.VoidPayslip(e.ctx, m8.ID, "redo", "tester")
	must(t, err)
	if got, _ := e.m.Payroll.Loan(e.ctx, loan.ID); got.Status != payroll.LoanActive || got.Remaining != irr(40_000_000) || e.bal(t, mizan.AccEmployeeLoans) != 40_000_000 {
		t.Fatalf("%+v", got)
	}
	if again := e.slip(t, emp, 8); again.DeductionTotal != irr(40_000_000) {
		t.Fatalf("%+v", again.Breakdown)
	}
	if _, err := e.m.Payroll.GrantLoan(e.ctx, payroll.LoanInput{IdemKey: "bad", EmployeeID: emp.ID, Principal: irr(10), Installment: irr(20), StartYear: 1405, StartMonth: 8}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("installment above principal: %v", err)
	}
	e.balanced(t)
}

func TestOvertimeLeaveAndSeveranceAreBookedFromRuleTerms(t *testing.T) {
	e := setup(t, false)
	emp := e.employee(t, "A", 300_000_000)
	p, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: 1405, Month: 8, Actor: "tester",
		Hours: payroll.Hours{OvertimeMinutes: 120}, LeaveDays: 3})
	must(t, err)
	if p.Gross != irr(300_000_000+3_819_918+30_000_000) {
		t.Fatalf("%+v", p.Breakdown)
	}
	// severance needs the contract to end in the month
	other := e.employee(t, "B", 300_000_000)
	if _, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: other.ID, Year: 1405, Month: 8, Severance: true}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("open contract: %v", err)
	}
	cs, err := e.m.Payroll.Contracts(e.ctx, other.ID)
	must(t, err)
	_, err = e.m.Payroll.EndContract(e.ctx, cs[0].ID, jdate(1405, 8, 15), "tester")
	must(t, err)
	s, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: other.ID, Year: 1405, Month: 8, Severance: true, Actor: "tester"})
	if !apperr.Is(err, apperr.KindConflict) { // service is 1 Jan 2026 .. 6 Nov 2026: under a year
		t.Fatalf("under a year of service: %+v %v", s, err)
	}
	// a long-serving employee: 2024-01-01 .. 2026-11-06 is 1041 days; severance is neither taxed nor insured and is booked as pay
	vet, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: "V", Name: "Veteran"})
	must(t, err)
	end := jdate(1405, 8, 15)
	_, err = e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: vet.ID, BaseSalary: irr(300_000_000), Insured: true, Start: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), End: &end})
	must(t, err)
	sv, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: vet.ID, Year: 1405, Month: 8, Severance: true, Actor: "tester"})
	must(t, err)
	if want := irr(150_000_000 + 855_616_438); sv.Gross != want || sv.Tax != irr(0) || sv.EmployeeIns != irr(10_500_000) {
		t.Fatalf("gross %v (want %v), tax %v, insurance %v: only the 16 days of base pay are insured", sv.Gross, want, sv.Tax, sv.EmployeeIns)
	}
	// a rule version without terms refuses to pay overtime, and a newer version with terms works again
	seed, err := e.m.Payroll.RuleAt(e.ctx, jdate(1405, 8, 1))
	must(t, err)
	_, err = e.m.Payroll.AddRule(e.ctx, payroll.RuleInput{Name: "no terms", EffectiveFrom: jdate(1405, 6, 1), Currency: "IRR", EmployeeBP: 700, EmployerBP: 2000, UnemploymentBP: 300,
		CeilingDaily: seed.CeilingDaily, FloorDaily: seed.FloorDaily, DeductEmployeeInsurance: true, Brackets: seed.Brackets})
	must(t, err)
	third := e.employee(t, "C", 300_000_000)
	if _, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: third.ID, Year: 1405, Month: 7, Hours: payroll.Hours{OvertimeMinutes: 60}}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("terms are not configured in that version: %v", err)
	}
	e.balanced(t)
}

func TestSSAFilesFromPayslips(t *testing.T) {
	e := setup(t, true)
	emp := e.employee(t, "S1", 300_000_000)
	if _, _, err := e.m.SSAFiles(e.ctx, 1405, 8, nil); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no workshop yet: %v", err)
	}
	must(t, e.m.Payroll.SetWorkshop(e.ctx, payroll.Workshop{Code: "1234567890", Name: "Test Co", ListNo: "000000000001", PaymentMonth: "001", Rate: 30, EmployerShareBP: 2000, UnemploymentBP: 300}))
	e.slip(t, emp, 8)
	if _, _, err := e.m.SSAFiles(e.ctx, 1405, 8, nil); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no profile yet: %v", err)
	}
	// National id and insurance number are required by the file; the employee helper left them empty.
	must(t, e.m.Payroll.SetProfile(e.ctx, emp.ID, payroll.Profile{JobCode: "111111", Sex: "M", BirthDate: "70/01/01"}))
	if _, _, err := e.m.SSAFiles(e.ctx, 1405, 8, nil); err == nil {
		t.Fatal("an employee without national id and insurance number must refuse the build")
	}
	// A fresh books with one fully identified employee builds.
	e = setup(t, true)
	must(t, e.m.Payroll.SetWorkshop(e.ctx, payroll.Workshop{Code: "1234567890", Name: "Test Co", ListNo: "000000000001", PaymentMonth: "001", Rate: 30, EmployerShareBP: 2000, UnemploymentBP: 300}))
	emp2, err := e.m.Payroll.AddEmployee(e.ctx, payroll.EmployeeInput{Code: "S2", Name: "Sara Test", NationalID: "0123456789", InsuranceNo: "12345678", Actor: "tester"})
	must(t, err)
	_, err = e.m.Payroll.AddContract(e.ctx, payroll.ContractInput{EmployeeID: emp2.ID, BaseSalary: irr(300_000_000), Insured: true, Start: jan1, Actor: "tester"})
	must(t, err)
	must(t, e.m.Payroll.SetProfile(e.ctx, emp2.ID, payroll.Profile{JobCode: "222222", Sex: "F"}))
	e.slip(t, emp2, 8)
	kar, wor, err := e.m.SSAFiles(e.ctx, 1405, 8, nil)
	must(t, err)
	if len(kar) != 32+32*23+1+287+1 || len(wor) != 32+32*27+1+417+1 {
		t.Fatalf("sizes %d %d", len(kar), len(wor))
	}
}

func TestBonusOvertimeCapHolidaysAndLoanWriteOff(t *testing.T) {
	e := setup(t, true)
	emp := e.employee(t, "A", 300_000_000) // 10M a day
	// The seeded 1405 terms: bonus 60 days of wage, cap 90 days of the minimum daily wage (floor 5,541,850), the exempt part is the 0% bracket (400,000,000).
	p, err := e.m.Payroll.IssuePayslip(e.ctx, payroll.PayslipInput{EmployeeID: emp.ID, Year: 1405, Month: 8, BonusDays: 365, Actor: "tester"})
	must(t, err)
	// 60 days x 10,000,000 = 600,000,000; the cap 90 x 5,541,850 = 498,766,500 applies; 400,000,000 exempt, 98,766,500 taxable; not insurable
	var exempt, excess int64
	for _, l := range p.Lines {
		switch l.Code {
		case "bonus":
			exempt = l.Amount.Amount()
			if l.Taxable || l.Insurable {
				t.Fatalf("the exempt part is neither taxable nor insurable: %+v", l)
			}
		case "bonus_taxable":
			excess = l.Amount.Amount()
			if !l.Taxable || l.Insurable {
				t.Fatalf("the excess is taxable, not insurable: %+v", l)
			}
		}
	}
	if exempt != 400_000_000 || excess != 98_766_500 || p.Insurable != irr(300_000_000) {
		t.Fatalf("exempt %d excess %d insurable %v", exempt, excess, p.Insurable)
	}
	// half a year of service: prorated 182/365 of both the amount and the cap
	emp2 := e.employee(t, "B", 300_000_000)
	pv, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, BonusDays: 182})
	must(t, err)
	if pv.Gross.Amount() != 300_000_000+248_700_008 { // min(300M x 2 x 182/365 = 299,178,082, the cap 498,766,500 x 182/365 = 248,700,008)
		t.Fatalf("%+v", pv.Lines)
	}

	// Overtime is capped at 4 hours per worked day (240 x 30 = 7,200 minutes in this month).
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, Hours: payroll.Hours{OvertimeMinutes: 7201}}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("overtime over the cap: %v", err)
	}
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, Hours: payroll.Hours{OvertimeMinutes: 7200}}); err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	// Holiday work needs rest days: 1405/08 has 5 Fridays (22 Aban is a Friday and a holiday) -> 5 x 7h20 = 2,199 minutes at most.
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, Hours: payroll.Hours{HolidayMinutes: 2200}}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("holiday work beyond the rest days: %v", err)
	}
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, Hours: payroll.Hours{HolidayMinutes: 2199}}); err != nil {
		t.Fatalf("within the rest days: %v", err)
	}
	hs, err := e.m.Payroll.Holidays(e.ctx, 1405)
	must(t, err)
	if len(hs) != 26 { // 25 official + Workers' Day
		t.Fatalf("%d holidays seeded", len(hs))
	}
	if _, err := e.m.Payroll.AddHoliday(e.ctx, payroll.Holiday{Year: 1405, Month: 8, Day: 5, Name: "test holiday", Source: "test"}, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Payroll.AddHoliday(e.ctx, payroll.Holiday{Year: 1405, Month: 8, Day: 5, Name: "again", Source: "test"}, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken date: %v", err)
	}
	if _, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 8, Hours: payroll.Hours{HolidayMinutes: 2638}}); err != nil { // now 6 rest days: 2,638
		t.Fatalf("a new holiday adds a rest day: %v", err)
	}

	// A forgiven loan: expense, receivable gone, loan closed; nothing more is deducted; idempotent.
	loan, err := e.m.Payroll.GrantLoan(e.ctx, payroll.LoanInput{IdemKey: "wo", EmployeeID: emp2.ID, Principal: irr(100_000_000), Installment: irr(10_000_000), StartYear: 1405, StartMonth: 9, Actor: "tester"})
	must(t, err)
	wo, err := e.m.Payroll.WriteOffLoan(e.ctx, loan.ID, "forgiven by the board", "tester")
	must(t, err)
	if wo.Status != payroll.LoanClosed || wo.WrittenOff != irr(100_000_000) || !wo.Remaining.IsZero() || e.bal(t, mizan.AccEmployeeLoans) != 0 || e.bal(t, mizan.AccLoanWriteOff) != 100_000_000 {
		t.Fatalf("%+v receivable %d expense %d", wo, e.bal(t, mizan.AccEmployeeLoans), e.bal(t, mizan.AccLoanWriteOff))
	}
	if again, err := e.m.Payroll.WriteOffLoan(e.ctx, loan.ID, "forgiven by the board", "tester"); err != nil || again.WrittenOff != irr(100_000_000) || e.bal(t, mizan.AccLoanWriteOff) != 100_000_000 {
		t.Fatalf("a repeat books nothing: %v", err)
	}
	if s9, err := e.m.Payroll.Preview(e.ctx, payroll.PayslipInput{EmployeeID: emp2.ID, Year: 1405, Month: 9}); err != nil || !s9.DeductionTotal.IsZero() {
		t.Fatalf("a written-off loan is not deducted: %+v %v", s9, err)
	}
	e.balanced(t)
}
