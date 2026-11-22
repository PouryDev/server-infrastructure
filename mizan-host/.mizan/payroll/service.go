package payroll

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Accounts are the ledger accounts payroll posts to. The composition root creates them.
type Accounts struct {
	SalaryExpense            string // expense: gross pay
	EmployerInsuranceExpense string // expense: employer's social-security and unemployment shares
	SalariesPayable          string // liability: net pay owed to employees (حقوق پرداختنی)
	TaxPayable               string // liability: income tax withheld, owed to the tax office
	InsurancePayable         string // liability: all social-security shares, owed to the insurer
	EmployeeLoans            string // asset: loans and advances owed by employees
	LoanWriteOffExpense      string // expense: loans forgiven (required only to write a loan off)
	Bank                     string // default account of a payment run
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
}

// Service is the payroll application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) note(ctx context.Context, actor, action, entity string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: entity, EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	if topic == "" {
		return nil
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: entity, AggregateID: id.String(), Payload: details})
}

// ---- employees and contracts ----

// AddEmployee registers an employee.
func (s *Service) AddEmployee(ctx context.Context, in EmployeeInput) (Employee, error) {
	in, err := in.validate()
	if err != nil {
		return Employee{}, err
	}
	e := Employee{ID: uuid.New(), Code: in.Code, Name: in.Name, NationalID: in.NationalID, InsuranceNo: in.InsuranceNo, Active: true, CreatedAt: s.clock()}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertEmployee(ctx, e); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "payroll.employee_added", "employee", e.ID, "", map[string]any{"employee_id": e.ID, "code": e.Code})
	})
	return e, err
}

// Employee returns an employee.
func (s *Service) Employee(ctx context.Context, id uuid.UUID) (Employee, error) {
	return s.repo.Employee(ctx, id, false)
}

// Employees lists the employees.
func (s *Service) Employees(ctx context.Context) ([]Employee, error) { return s.repo.Employees(ctx) }

// AddContract adds a contract. Contracts of an employee never overlap: end the open one first.
func (s *Service) AddContract(ctx context.Context, in ContractInput) (Contract, error) {
	in, err := in.validate()
	if err != nil {
		return Contract{}, err
	}
	c := Contract{ID: uuid.New(), EmployeeID: in.EmployeeID, BaseSalary: in.BaseSalary, Allowances: in.Allowances, Insured: in.Insured,
		Start: in.Start, End: in.End, CreatedAt: s.clock()}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.Employee(ctx, in.EmployeeID, true); err != nil { // serialises contract changes of the employee
			return err
		}
		existing, err := s.repo.Contracts(ctx, in.EmployeeID)
		if err != nil {
			return err
		}
		for _, o := range existing {
			if overlaps(o, c) {
				return apperr.New(apperr.KindConflict, "contract_overlap", "the contract overlaps another one of the employee; end that one first")
			}
		}
		if err := s.repo.InsertContract(ctx, c); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "payroll.contract_added", "contract", c.ID, "", map[string]any{"contract_id": c.ID, "employee_id": c.EmployeeID, "start": c.Start.Format("2006-01-02")})
	})
	return c, err
}

func overlaps(a, b Contract) bool {
	return (a.End == nil || !a.End.Before(b.Start)) && (b.End == nil || !b.End.Before(a.Start))
}

// EndContract closes an open contract on a day (inclusive).
func (s *Service) EndContract(ctx context.Context, id uuid.UUID, end time.Time, actor string) (Contract, error) {
	end = dateOnly(end)
	var out Contract
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		c, err := s.repo.Contract(ctx, id, true)
		if err != nil {
			return err
		}
		out = c
		switch {
		case c.End != nil:
			return apperr.New(apperr.KindConflict, "contract_ended", "the contract already has an end date")
		case end.Before(c.Start):
			return apperr.InvalidField("end", "must not be before the start")
		}
		if err := s.repo.SetContractEnd(ctx, id, end); err != nil {
			return err
		}
		out.End = &end
		return s.note(ctx, actor, "payroll.contract_ended", "contract", id, "", map[string]any{"contract_id": id, "end": end.Format("2006-01-02")})
	})
	return out, err
}

// Contracts lists the contracts of an employee, oldest first.
func (s *Service) Contracts(ctx context.Context, employee uuid.UUID) ([]Contract, error) {
	return s.repo.Contracts(ctx, employee)
}

// ---- rule versions ----

// AddRule stores a new version of the social-security and income-tax rules. It is the only way rules change.
func (s *Service) AddRule(ctx context.Context, in RuleInput) (RuleVersion, error) {
	r := in.version(uuid.New(), s.clock())
	if err := r.Validate(); err != nil {
		return r, err
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertRule(ctx, r); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "payroll.rule_added", "payroll_rule", r.ID, "payroll.rule.added",
			map[string]any{"rule_id": r.ID, "name": r.Name, "effective_from": r.EffectiveFrom.Format("2006-01-02")})
	})
	return r, err
}

// Rules lists the versions, oldest first.
func (s *Service) Rules(ctx context.Context) ([]RuleVersion, error) { return s.repo.Rules(ctx) }

// RuleAt returns the version in force on a day.
func (s *Service) RuleAt(ctx context.Context, day time.Time) (RuleVersion, error) {
	return s.repo.RuleAt(ctx, dateOnly(day))
}

// DeleteRule removes a version no payslip used yet (to correct a mistake). Used versions are permanent.
func (s *Service) DeleteRule(ctx context.Context, id uuid.UUID, actor string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.DeleteRule(ctx, id); err != nil {
			return err
		}
		return s.note(ctx, actor, "payroll.rule_deleted", "payroll_rule", id, "", map[string]any{"rule_id": id})
	})
}

// ---- payslips ----

// compute finds the contract and the rule version of the month and computes the payslip.
func (s *Service) compute(ctx context.Context, in PayslipInput, lockLoans bool) (Contract, RuleVersion, Breakdown, error) {
	first, last, _, err := MonthSpan(in.Year, in.Month)
	if err != nil {
		return Contract{}, RuleVersion{}, Breakdown{}, err
	}
	if _, err := s.repo.Employee(ctx, in.EmployeeID, false); err != nil {
		return Contract{}, RuleVersion{}, Breakdown{}, err
	}
	all, err := s.repo.Contracts(ctx, in.EmployeeID)
	if err != nil {
		return Contract{}, RuleVersion{}, Breakdown{}, err
	}
	var live []Contract
	for _, c := range all {
		if c.CoveredDays(first, last) > 0 {
			live = append(live, c)
		}
	}
	if len(live) == 0 {
		return Contract{}, RuleVersion{}, Breakdown{}, apperr.New(apperr.KindConflict, "no_contract", "the employee has no contract in this month")
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Start.Before(live[j].Start) })
	latest, earlier := live[len(live)-1], live[:len(live)-1] // contracts never overlap: a salary change mid-month is paid by segments of one payslip
	rule, err := s.repo.RuleAt(ctx, first)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return Contract{}, RuleVersion{}, Breakdown{}, apperr.New(apperr.KindConflict, "no_rule_version", "no payroll rule version is in force for this month; add one first")
		}
		return Contract{}, RuleVersion{}, Breakdown{}, err
	}
	ci := CalcInput{Contract: latest, Earlier: earlier, Rule: rule, Year: in.Year, Month: in.Month, Days: in.Days, Extras: in.Extras, Hours: in.Hours, LeaveDays: in.LeaveDays, BonusDays: in.BonusDays}
	if hr, ok := s.repo.(HolidayRepository); ok {
		hs, err := hr.Holidays(ctx, in.Year)
		if err != nil {
			return Contract{}, RuleVersion{}, Breakdown{}, err
		}
		for _, h := range hs {
			if h.Month == in.Month && h.Kind == "official" {
				ci.Holidays = append(ci.Holidays, h.Day)
			}
		}
		ci.HolidaysKnown = len(hs) > 0
	}
	if in.Severance {
		end := latest.End
		if end == nil || end.Before(first) || end.After(last) {
			return Contract{}, RuleVersion{}, Breakdown{}, apperr.New(apperr.KindConflict, "contract_not_ended", "severance needs the contract to end in this month")
		}
		ci.Severance = &ServicePeriod{Start: all[0].Start, End: *end}
	}
	loans, err := s.repo.DueLoans(ctx, in.EmployeeID, in.Year, in.Month, lockLoans)
	if err != nil {
		return Contract{}, RuleVersion{}, Breakdown{}, err
	}
	for _, l := range loans {
		ci.Loans = append(ci.Loans, LoanDue{LoanID: l.ID, Due: l.DueOf()})
	}
	b, err := Calculate(ci)
	return latest, rule, b, err
}

// Preview computes a payslip without booking it.
func (s *Service) Preview(ctx context.Context, in PayslipInput) (Breakdown, error) {
	_, _, b, err := s.compute(ctx, in, false)
	return b, err
}

// IssuePayslip books the pay of an employee for a Jalali month:
//
//	Dr salary expense (gross), Dr employer insurance expense (employer + unemployment shares)
//	Cr salaries payable (net), Cr tax payable, Cr insurance payable (employee + employer + unemployment shares)
//
// It is idempotent per (employee, month): while the payslip is live, calling again returns it unchanged. Void it to
// issue a corrected one.
func (s *Service) IssuePayslip(ctx context.Context, in PayslipInput) (Payslip, error) {
	first, _, _, err := MonthSpan(in.Year, in.Month)
	if err != nil {
		return Payslip{}, err
	}
	if first.After(dateOnly(s.books.Today())) {
		return Payslip{}, apperr.InvalidField("month", "payroll cannot be issued for a month that has not started")
	}
	var out Payslip
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		contract, rule, b, err := s.compute(ctx, in, true)
		if err != nil {
			return err
		}
		now := s.clock()
		p := Payslip{ID: uuid.New(), EmployeeID: in.EmployeeID, ContractID: contract.ID, RuleID: rule.ID, Year: in.Year, Month: in.Month,
			Status: Issued, Breakdown: b, CreatedAt: now, UpdatedAt: now}
		stored, created, err := s.repo.InsertPayslip(ctx, p)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			return nil
		}
		for _, d := range b.Deductions {
			if err := s.repo.ApplyLoanDeduction(ctx, d.LoanID, d.Amount); err != nil {
				return err
			}
		}
		_, last, _, _ := MonthSpan(in.Year, in.Month)
		date := dateOnly(s.books.Today())
		if last.Before(date) {
			date = last // the expense belongs to the month it was earned in, never to the future
		}
		lines, err := s.issueLines(b)
		if err != nil {
			return err
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "payslip:" + p.ID.String() + ":issue", Date: date, Description: "Payroll " + monthLabel(in.Year, in.Month),
			RefType: "payslip", RefID: p.ID.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		p.Journal = &jid
		if err := s.repo.UpdatePayslip(ctx, &p); err != nil {
			return err
		}
		out = p
		return s.note(ctx, in.Actor, "payroll.payslip.issued", "payslip", p.ID, "payroll.payslip.issued", payslipPayload(p))
	})
	return out, err
}

func (s *Service) issueLines(b Breakdown) ([]books.Line, error) {
	a := s.cfg.Accounts
	employerCost, err := b.EmployerIns.Add(b.Unemployment)
	if err != nil {
		return nil, err
	}
	insurance, err := b.EmployeeIns.Add(employerCost)
	if err != nil {
		return nil, err
	}
	var lines []books.Line
	add := func(l books.Line) {
		if l.Amount.IsPositive() {
			lines = append(lines, l)
		}
	}
	add(books.Dr(a.SalaryExpense, b.Gross))
	add(books.Dr(a.EmployerInsuranceExpense, employerCost))
	add(books.Cr(a.SalariesPayable, b.Net))
	add(books.Cr(a.TaxPayable, b.Tax))
	add(books.Cr(a.InsurancePayable, insurance))
	add(books.Cr(a.EmployeeLoans, b.DeductionTotal))
	return lines, nil
}

func monthLabel(year, month int) string { return fmt.Sprintf("%04d/%02d", year, month) }

func payslipPayload(p Payslip) map[string]any {
	return map[string]any{"payslip_id": p.ID, "employee_id": p.EmployeeID, "year": p.Year, "month": p.Month, "status": p.Status,
		"gross": p.Gross.Amount(), "net": p.Net.Amount(), "tax": p.Tax.Amount(), "employee_insurance": p.EmployeeIns.Amount(),
		"employer_insurance": p.EmployerIns.Amount(), "deductions": p.DeductionTotal.Amount(), "unemployment": p.Unemployment.Amount(), "currency": p.Gross.Currency()}
}

// Payslip returns a payslip.
func (s *Service) Payslip(ctx context.Context, id uuid.UUID) (Payslip, error) {
	return s.repo.Payslip(ctx, id, false)
}

// ListPayslips lists payslips with filters and paging.
func (s *Service) ListPayslips(ctx context.Context, p query.Params) (query.Page[Payslip], error) {
	return s.repo.ListPayslips(ctx, p)
}

// VoidPayslip cancels an issued payslip with a reversing journal. A paid payslip cannot be voided: the money has left.
func (s *Service) VoidPayslip(ctx context.Context, id uuid.UUID, reason, actor string) (Payslip, error) {
	var out Payslip
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.repo.Payslip(ctx, id, true)
		if err != nil {
			return err
		}
		out = p
		switch p.Status {
		case Void:
			return nil
		case Paid:
			return apperr.New(apperr.KindConflict, "payslip_paid", "a paid payslip cannot be voided")
		}
		if p.Journal == nil {
			return apperr.New(apperr.KindConflict, "payslip_not_booked", "the payslip has no journal")
		}
		rid, err := s.books.Reverse(ctx, *p.Journal, "payslip:"+p.ID.String()+":void", reason, actor)
		if err != nil {
			return err
		}
		for _, d := range p.Deductions { // the installments were not repaid after all
			if err := s.repo.RestoreLoan(ctx, d.LoanID, d.Amount); err != nil {
				return err
			}
		}
		p.Status, p.VoidJournal, p.VoidReason, p.UpdatedAt = Void, &rid, reason, s.clock()
		if err := s.repo.UpdatePayslip(ctx, &p); err != nil {
			return err
		}
		out = p
		return s.note(ctx, actor, "payroll.payslip.void", "payslip", p.ID, "payroll.payslip.void", payslipPayload(p))
	})
	return out, err
}

// ---- payment run ----

// PayRun pays issued payslips in one batch: Dr salaries payable, Cr the bank account, for the sum of their net pay.
// Idempotent per key. The payslips are locked, so two runs never pay the same payslip.
func (s *Service) PayRun(ctx context.Context, in RunInput) (Run, error) {
	if in.IdemKey == "" || len(in.IdemKey) > 200 {
		return Run{}, apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	}
	if _, _, _, err := MonthSpan(in.Year, in.Month); err != nil {
		return Run{}, err
	}
	payFrom := in.PayFrom
	if payFrom == "" {
		payFrom = s.cfg.Accounts.Bank
	}
	var out Run
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		date := dateOnly(s.books.Today())
		if !in.Date.IsZero() {
			if date = dateOnly(in.Date); date.After(dateOnly(s.books.Today())) {
				return apperr.InvalidField("date", "must not be in the future")
			}
		}
		run := Run{ID: uuid.New(), IdemKey: in.IdemKey, Year: in.Year, Month: in.Month, PayFrom: payFrom, Total: money.Zero(s.cfg.Currency), Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertRun(ctx, run)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.Year != in.Year || stored.Month != in.Month {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different run")
			}
			return nil
		}
		slips, err := s.repo.PayableSlips(ctx, in.Year, in.Month, in.PayslipIDs)
		if err != nil {
			return err
		}
		if len(slips) == 0 || (len(in.PayslipIDs) > 0 && len(slips) != len(uniqueIDs(in.PayslipIDs))) {
			return apperr.New(apperr.KindConflict, "nothing_to_pay", "no issued payslips match: they may be paid, void or of another month")
		}
		total, ids := money.Zero(s.cfg.Currency), make([]uuid.UUID, len(slips))
		for i, p := range slips {
			if total, err = total.Add(p.Net); err != nil {
				return err
			}
			ids[i] = p.ID
		}
		run.Total, run.Count = total, len(slips)
		if total.IsPositive() {
			jid, err := s.books.Post(ctx, books.Entry{Key: "payroll_run:" + run.ID.String(), Date: date, Description: "Salary payment " + monthLabel(in.Year, in.Month),
				RefType: "payroll_run", RefID: run.ID.String(), Actor: in.Actor,
				Lines: []books.Line{books.Dr(s.cfg.Accounts.SalariesPayable, total), books.Cr(payFrom, total)}})
			if err != nil {
				return err
			}
			run.Journal = &jid
		}
		if err := s.repo.MarkPaid(ctx, ids, run.ID); err != nil {
			return err
		}
		if err := s.repo.UpdateRun(ctx, &run); err != nil {
			return err
		}
		out = run
		return s.note(ctx, in.Actor, "payroll.run.paid", "payroll_run", run.ID, "payroll.run.paid",
			map[string]any{"run_id": run.ID, "year": run.Year, "month": run.Month, "count": run.Count, "total": total.Amount(), "currency": total.Currency()})
	})
	return out, err
}

func uniqueIDs(ids []uuid.UUID) map[uuid.UUID]struct{} {
	m := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

// ---- loans and advances ----

// GrantLoan lends money to an employee (Dr employee loans, Cr the bank account). Idempotent per key. The installment
// is deducted from the net pay of the payslips from the start month on, after tax and never below a zero net.
func (s *Service) GrantLoan(ctx context.Context, in LoanInput) (Loan, error) {
	if err := in.validate(); err != nil {
		return Loan{}, err
	}
	if in.Principal.Currency() != s.cfg.Currency {
		return Loan{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "loans are in "+string(s.cfg.Currency))
	}
	payFrom := in.PayFrom
	if payFrom == "" {
		payFrom = s.cfg.Accounts.Bank
	}
	var out Loan
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.Employee(ctx, in.EmployeeID, false); err != nil {
			return err
		}
		date, err := s.payDate(in.Date)
		if err != nil {
			return err
		}
		l := Loan{ID: uuid.New(), IdemKey: in.IdemKey, EmployeeID: in.EmployeeID, Principal: in.Principal, Installment: in.Installment, Remaining: in.Principal,
			StartYear: in.StartYear, StartMonth: in.StartMonth, Status: LoanActive, PayFrom: payFrom, Note: in.Note, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertLoan(ctx, l)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.EmployeeID != l.EmployeeID || stored.Principal != l.Principal {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different loan")
			}
			return nil
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "loan:" + l.ID.String() + ":grant", Date: date, Description: "Employee loan",
			RefType: "employee_loan", RefID: l.ID.String(), Actor: in.Actor,
			Lines: []books.Line{books.Dr(s.cfg.Accounts.EmployeeLoans, l.Principal), books.Cr(payFrom, l.Principal)}})
		if err != nil {
			return err
		}
		l.Journal = &jid
		if err := s.repo.SetLoanJournal(ctx, l.ID, jid); err != nil {
			return err
		}
		out = l
		return s.note(ctx, in.Actor, "payroll.loan.granted", "employee_loan", l.ID, "payroll.loan.granted",
			map[string]any{"loan_id": l.ID, "employee_id": l.EmployeeID, "principal": l.Principal.Amount(), "installment": l.Installment.Amount(), "currency": l.Principal.Currency()})
	})
	return out, err
}

// Loan returns a loan.
func (s *Service) Loan(ctx context.Context, id uuid.UUID) (Loan, error) { return s.repo.Loan(ctx, id) }

// Loans lists the loans of an employee.
func (s *Service) Loans(ctx context.Context, employee uuid.UUID) ([]Loan, error) {
	return s.repo.LoansOf(ctx, employee)
}

// payDate validates the date of a money movement: today when empty, never in the future.
func (s *Service) payDate(t time.Time) (time.Time, error) {
	today := dateOnly(s.books.Today())
	if t.IsZero() {
		return today, nil
	}
	if t = dateOnly(t); t.After(today) {
		return time.Time{}, apperr.InvalidField("date", "must not be in the future")
	}
	return t, nil
}

// ---- remitting what was withheld ----

// Remit pays withheld income tax or social security to its authority (Dr tax or insurance payable, Cr the bank
// account). It cannot pay more than is owed. Idempotent per key; payments of one kind are serialised.
func (s *Service) Remit(ctx context.Context, in RemitInput) (Remittance, error) {
	if err := in.validate(); err != nil {
		return Remittance{}, err
	}
	if in.Amount.Currency() != s.cfg.Currency {
		return Remittance{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "remittances are in "+string(s.cfg.Currency))
	}
	payFrom, owed := in.PayFrom, s.cfg.Accounts.TaxPayable
	if payFrom == "" {
		payFrom = s.cfg.Accounts.Bank
	}
	if in.Kind == LiabilityInsurance {
		owed = s.cfg.Accounts.InsurancePayable
	}
	var out Remittance
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		date, err := s.payDate(in.Date)
		if err != nil {
			return err
		}
		r := Remittance{ID: uuid.New(), IdemKey: in.IdemKey, Kind: in.Kind, PeriodYear: in.PeriodYear, PeriodMonth: in.PeriodMonth, Amount: in.Amount,
			PayFrom: payFrom, Reference: in.Reference, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertRemittance(ctx, r)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.Kind != r.Kind || stored.Amount != r.Amount {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different remittance")
			}
			return nil
		}
		if err := s.repo.LockRemittances(ctx, string(in.Kind)); err != nil { // the balance check below must not race
			return err
		}
		bal, err := s.books.Balance(ctx, owed)
		if err != nil {
			return err
		}
		if c, err := bal.Cmp(in.Amount); err != nil || c < 0 {
			if err != nil {
				return err
			}
			return apperr.New(apperr.KindConflict, "exceeds_payable", "the amount is more than what is owed on the payable account")
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "payroll_remit:" + r.ID.String(), Date: date, Description: "Payroll " + string(in.Kind) + " payment",
			RefType: "payroll_remittance", RefID: r.ID.String(), Actor: in.Actor,
			Lines: []books.Line{books.Dr(owed, in.Amount), books.Cr(payFrom, in.Amount)}})
		if err != nil {
			return err
		}
		r.Journal = &jid
		if err := s.repo.SetRemittanceJournal(ctx, r.ID, jid); err != nil {
			return err
		}
		out = r
		return s.note(ctx, in.Actor, "payroll.remittance.paid", "payroll_remittance", r.ID, "payroll.remittance.paid",
			map[string]any{"remittance_id": r.ID, "kind": r.Kind, "amount": r.Amount.Amount(), "currency": r.Amount.Currency(), "reference": r.Reference})
	})
	return out, err
}

// Remittances lists the payments, newest first.
func (s *Service) Remittances(ctx context.Context, kind Liability) ([]Remittance, error) {
	return s.repo.Remittances(ctx, string(kind))
}
