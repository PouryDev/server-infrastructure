// Package pgpayroll is the PostgreSQL adapter of payroll.Repository.
package pgpayroll

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payroll"
)

// Repository implements payroll.Repository.
type Repository struct {
	db         *postgres.DB
	slipSchema *query.Schema
}

var _ payroll.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	ss, err := query.NewSchemaFor[payslipRow]("p", query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("p.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, slipSchema: ss}, nil
}

func cur(s string) money.Currency { return money.Currency(s) }

func amount(v int64, c string) money.Money { m, _ := money.New(v, cur(c)); return m }

func notFound(err error, what string) error {
	if apperr.Is(err, apperr.KindNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

// ---- employees ----

type employeeRow struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	NationalID  string    `db:"national_id"`
	InsuranceNo string    `db:"insurance_no"`
	Active      bool      `db:"active"`
	CreatedAt   time.Time `db:"created_at"`
}

const employeeCols = `id, code, name, national_id, insurance_no, active, created_at`

func (x employeeRow) domain() payroll.Employee {
	return payroll.Employee{ID: x.ID, Code: x.Code, Name: x.Name, NationalID: x.NationalID, InsuranceNo: x.InsuranceNo, Active: x.Active, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertEmployee(ctx context.Context, e payroll.Employee) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_pay_employees (`+employeeCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.ID, e.Code, e.Name, e.NationalID, e.InsuranceNo, e.Active, e.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_pay_employees_code_key") {
		return apperr.New(apperr.KindConflict, "employee_exists", "an employee with this code already exists")
	}
	return err
}

func (r *Repository) Employee(ctx context.Context, id uuid.UUID, forUpdate bool) (payroll.Employee, error) {
	sql := `SELECT ` + employeeCols + ` FROM mz_pay_employees WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[employeeRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return payroll.Employee{}, notFound(err, "employee")
	}
	return x.domain(), nil
}

func (r *Repository) Employees(ctx context.Context) ([]payroll.Employee, error) {
	rows, err := postgres.Select[employeeRow](ctx, r.db.Querier(ctx), `SELECT `+employeeCols+` FROM mz_pay_employees ORDER BY code`)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Employee, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

// ---- contracts ----

type contractRow struct {
	ID         uuid.UUID  `db:"id"`
	EmployeeID uuid.UUID  `db:"employee_id"`
	BaseSalary int64      `db:"base_salary"`
	Currency   string     `db:"currency"`
	Allowances string     `db:"allowances"`
	Insured    bool       `db:"insured"`
	Start      time.Time  `db:"start_date"`
	End        *time.Time `db:"end_date"`
	CreatedAt  time.Time  `db:"created_at"`
}

const contractCols = `id, employee_id, base_salary, currency, allowances::text AS allowances, insured, start_date, end_date, created_at`

func (x contractRow) domain() (payroll.Contract, error) {
	var al []payroll.Allowance
	if err := json.Unmarshal([]byte(x.Allowances), &al); err != nil {
		return payroll.Contract{}, err
	}
	return payroll.Contract{ID: x.ID, EmployeeID: x.EmployeeID, BaseSalary: amount(x.BaseSalary, x.Currency), Allowances: al, Insured: x.Insured,
		Start: x.Start, End: x.End, CreatedAt: x.CreatedAt}, nil
}

func (r *Repository) InsertContract(ctx context.Context, c payroll.Contract) error {
	al, err := json.Marshal(nonNil(c.Allowances))
	if err != nil {
		return err
	}
	_, err = postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_contracts (id, employee_id, base_salary, currency, allowances, insured, start_date, end_date, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)`,
		c.ID, c.EmployeeID, c.BaseSalary.Amount(), string(c.BaseSalary.Currency()), string(al), c.Insured, c.Start, c.End, c.CreatedAt)
	return err
}

func nonNil(a []payroll.Allowance) []payroll.Allowance {
	if a == nil {
		return []payroll.Allowance{}
	}
	return a
}

func (r *Repository) Contract(ctx context.Context, id uuid.UUID, forUpdate bool) (payroll.Contract, error) {
	sql := `SELECT ` + contractCols + ` FROM mz_pay_contracts WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[contractRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return payroll.Contract{}, notFound(err, "contract")
	}
	return x.domain()
}

func (r *Repository) Contracts(ctx context.Context, employee uuid.UUID) ([]payroll.Contract, error) {
	rows, err := postgres.Select[contractRow](ctx, r.db.Querier(ctx), `SELECT `+contractCols+` FROM mz_pay_contracts WHERE employee_id = $1 ORDER BY start_date, id`, employee)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Contract, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Repository) SetContractEnd(ctx context.Context, id uuid.UUID, end time.Time) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_contracts SET end_date = $2 WHERE id = $1`, id, end)
	return err
}

// ---- rule versions ----

type ruleRow struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	EffectiveFrom  time.Time `db:"effective_from"`
	Currency       string    `db:"currency"`
	EmployeeBP     int       `db:"employee_bp"`
	EmployerBP     int       `db:"employer_bp"`
	UnemploymentBP int       `db:"unemployment_bp"`
	CeilingDaily   int64     `db:"ceiling_daily"`
	FloorDaily     int64     `db:"floor_daily"`
	Deduct         bool      `db:"deduct_employee_insurance"`
	Brackets       string    `db:"brackets"`
	Terms          string    `db:"terms"`
	Source         string    `db:"source"`
	CreatedBy      string    `db:"created_by"`
	CreatedAt      time.Time `db:"created_at"`
}

const ruleCols = `id, name, effective_from, currency, employee_bp, employer_bp, unemployment_bp, ceiling_daily, floor_daily, deduct_employee_insurance,
	brackets::text AS brackets, terms::text AS terms, source, created_by, created_at`

func (x ruleRow) domain() (payroll.RuleVersion, error) {
	var br []payroll.Bracket
	if err := json.Unmarshal([]byte(x.Brackets), &br); err != nil {
		return payroll.RuleVersion{}, err
	}
	var terms payroll.Terms
	if err := json.Unmarshal([]byte(x.Terms), &terms); err != nil {
		return payroll.RuleVersion{}, err
	}
	return payroll.RuleVersion{ID: x.ID, Name: x.Name, EffectiveFrom: x.EffectiveFrom, Currency: cur(x.Currency), EmployeeBP: x.EmployeeBP,
		EmployerBP: x.EmployerBP, UnemploymentBP: x.UnemploymentBP, CeilingDaily: x.CeilingDaily, FloorDaily: x.FloorDaily,
		DeductEmployeeInsurance: x.Deduct, Brackets: br, Terms: terms, Source: x.Source, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt}, nil
}

func (r *Repository) InsertRule(ctx context.Context, v payroll.RuleVersion) error {
	br, err := json.Marshal(v.Brackets)
	if err != nil {
		return err
	}
	terms, err := json.Marshal(v.Terms)
	if err != nil {
		return err
	}
	_, err = postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_rule_versions (id, name, effective_from, currency, employee_bp, employer_bp, unemployment_bp, ceiling_daily, floor_daily,
		                                  deduct_employee_insurance, brackets, terms, source, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb, $13, $14, $15)`,
		v.ID, v.Name, v.EffectiveFrom, string(v.Currency), v.EmployeeBP, v.EmployerBP, v.UnemploymentBP, v.CeilingDaily, v.FloorDaily,
		v.DeductEmployeeInsurance, string(br), string(terms), v.Source, v.CreatedBy, v.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_pay_rule_versions_effective_from_key") {
		return apperr.New(apperr.KindConflict, "rule_date_taken", "a rule version already starts on this date")
	}
	return err
}

func (r *Repository) rule(ctx context.Context, where string, args ...any) (payroll.RuleVersion, error) {
	x, err := postgres.SelectOne[ruleRow](ctx, r.db.Querier(ctx), `SELECT `+ruleCols+` FROM mz_pay_rule_versions WHERE `+where, args...)
	if err != nil {
		return payroll.RuleVersion{}, notFound(err, "payroll rule version")
	}
	return x.domain()
}

func (r *Repository) Rule(ctx context.Context, id uuid.UUID) (payroll.RuleVersion, error) {
	return r.rule(ctx, `id = $1`, id)
}

func (r *Repository) RuleAt(ctx context.Context, day time.Time) (payroll.RuleVersion, error) {
	return r.rule(ctx, `effective_from = (SELECT max(effective_from) FROM mz_pay_rule_versions WHERE effective_from <= $1)`, day)
}

func (r *Repository) Rules(ctx context.Context) ([]payroll.RuleVersion, error) {
	rows, err := postgres.Select[ruleRow](ctx, r.db.Querier(ctx), `SELECT `+ruleCols+` FROM mz_pay_rule_versions ORDER BY effective_from`)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.RuleVersion, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Repository) DeleteRule(ctx context.Context, id uuid.UUID) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `DELETE FROM mz_pay_rule_versions WHERE id = $1`, id)
	if apperr.Is(err, apperr.KindConflict) {
		return apperr.New(apperr.KindConflict, "rule_in_use", "a payslip used this version: add a newer version instead")
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("payroll rule version")
	}
	return nil
}

// ---- payslips ----

type payslipRow struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	EmployeeID   uuid.UUID  `db:"employee_id" json:"employee_id"`
	ContractID   uuid.UUID  `db:"contract_id" json:"-" query:"-"`
	RuleID       uuid.UUID  `db:"rule_id" json:"-" query:"-"`
	Year         int        `db:"year" json:"year"`
	Month        int        `db:"month" json:"month"`
	Status       string     `db:"status" json:"status"`
	Currency     string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	Days         int        `db:"days" json:"-" query:"-"`
	MonthDays    int        `db:"month_days" json:"-" query:"-"`
	Lines        string     `db:"lines" json:"-" query:"-"`
	Gross        int64      `db:"gross" json:"gross"`
	Insurable    int64      `db:"insurable" json:"-" query:"-"`
	EmployeeIns  int64      `db:"employee_ins" json:"-" query:"-"`
	EmployerIns  int64      `db:"employer_ins" json:"-" query:"-"`
	Unemployment int64      `db:"unemployment" json:"-" query:"-"`
	Taxable      int64      `db:"taxable" json:"-" query:"-"`
	Tax          int64      `db:"tax" json:"tax"`
	Deductions   string     `db:"deductions" json:"-" query:"-"`
	DeductionTot int64      `db:"deduction_total" json:"-" query:"-"`
	Net          int64      `db:"net" json:"net"`
	Journal      *uuid.UUID `db:"journal_id" json:"-" query:"-"`
	VoidJournal  *uuid.UUID `db:"void_journal" json:"-" query:"-"`
	RunID        *uuid.UUID `db:"run_id" json:"run_id"`
	VoidReason   string     `db:"void_reason" json:"-" query:"-"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"-" query:"-"`
}

const slipCols = `p.id, p.employee_id, p.contract_id, p.rule_id, p.year, p.month, p.status, p.currency, p.days, p.month_days, p.lines::text AS lines, p.gross,
	p.insurable, p.employee_ins, p.employer_ins, p.unemployment, p.taxable, p.tax, p.deductions::text AS deductions, p.deduction_total, p.net, p.journal_id, p.void_journal, p.run_id, p.void_reason,
	p.created_at, p.updated_at`

func (x payslipRow) domain() (payroll.Payslip, error) {
	var lines []payroll.Line
	if err := json.Unmarshal([]byte(x.Lines), &lines); err != nil {
		return payroll.Payslip{}, err
	}
	var deductions []payroll.Deduction
	if err := json.Unmarshal([]byte(x.Deductions), &deductions); err != nil {
		return payroll.Payslip{}, err
	}
	m := func(v int64) money.Money { return amount(v, x.Currency) }
	return payroll.Payslip{ID: x.ID, EmployeeID: x.EmployeeID, ContractID: x.ContractID, RuleID: x.RuleID, Year: x.Year, Month: x.Month,
		Status: payroll.PayslipStatus(x.Status),
		Breakdown: payroll.Breakdown{Days: x.Days, MonthDays: x.MonthDays, Lines: lines, Gross: m(x.Gross), Insurable: m(x.Insurable),
			EmployeeIns: m(x.EmployeeIns), EmployerIns: m(x.EmployerIns), Unemployment: m(x.Unemployment), TaxableIncome: m(x.Taxable), Tax: m(x.Tax), Deductions: deductions, DeductionTotal: m(x.DeductionTot), Net: m(x.Net)},
		Journal: x.Journal, VoidJournal: x.VoidJournal, RunID: x.RunID, VoidReason: x.VoidReason, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}, nil
}

func (r *Repository) slip(ctx context.Context, where string, forUpdate bool, args ...any) (payroll.Payslip, error) {
	sql := `SELECT ` + slipCols + ` FROM mz_pay_payslips p WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[payslipRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		return payroll.Payslip{}, notFound(err, "payslip")
	}
	return x.domain()
}

func (r *Repository) InsertPayslip(ctx context.Context, p payroll.Payslip) (payroll.Payslip, bool, error) {
	lines, err := json.Marshal(p.Lines)
	if err != nil {
		return p, false, err
	}
	deductions, err := json.Marshal(nonNilDeductions(p.Deductions))
	if err != nil {
		return p, false, err
	}
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_payslips (id, employee_id, contract_id, rule_id, year, month, status, currency, days, month_days, lines, gross, insurable,
		                             employee_ins, employer_ins, unemployment, taxable, tax, net, deductions, deduction_total, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13, $14, $15, $16, $17, $18, $19, $20::jsonb, $21, $22, $22)
		ON CONFLICT (employee_id, year, month) WHERE status <> 'void' DO NOTHING`,
		p.ID, p.EmployeeID, p.ContractID, p.RuleID, p.Year, p.Month, string(p.Status), string(p.Gross.Currency()), p.Days, p.MonthDays, string(lines),
		p.Gross.Amount(), p.Insurable.Amount(), p.EmployeeIns.Amount(), p.EmployerIns.Amount(), p.Unemployment.Amount(), p.TaxableIncome.Amount(),
		p.Tax.Amount(), p.Net.Amount(), string(deductions), p.DeductionTotal.Amount(), p.CreatedAt)
	if err != nil {
		return p, false, err
	}
	if n == 1 {
		return p, true, nil
	}
	stored, err := r.slip(ctx, `p.employee_id = $1 AND p.year = $2 AND p.month = $3 AND p.status <> 'void'`, false, p.EmployeeID, p.Year, p.Month)
	return stored, false, err
}

func (r *Repository) Payslip(ctx context.Context, id uuid.UUID, forUpdate bool) (payroll.Payslip, error) {
	return r.slip(ctx, `p.id = $1`, forUpdate, id)
}

func (r *Repository) UpdatePayslip(ctx context.Context, p *payroll.Payslip) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_pay_payslips SET status = $2, journal_id = $3, void_journal = $4, void_reason = $5, updated_at = now() WHERE id = $1`,
		p.ID, string(p.Status), p.Journal, p.VoidJournal, p.VoidReason)
	return err
}

func (r *Repository) ListPayslips(ctx context.Context, p query.Params) (query.Page[payroll.Payslip], error) {
	page, err := postgres.Paginate[payslipRow](ctx, r.db.Querier(ctx), r.slipSchema,
		postgres.ListQuery{Select: "SELECT " + slipCols, From: "FROM mz_pay_payslips p"}, p)
	if err != nil {
		return query.Page[payroll.Payslip]{}, err
	}
	var convErr error
	out := query.MapPage(page, func(x payslipRow) payroll.Payslip {
		v, err := x.domain()
		if err != nil {
			convErr = err
		}
		return v
	})
	return out, convErr
}

func (r *Repository) PayableSlips(ctx context.Context, year, month int, ids []uuid.UUID) ([]payroll.Payslip, error) {
	sql := `SELECT ` + slipCols + ` FROM mz_pay_payslips p WHERE p.year = $1 AND p.month = $2 AND p.status = 'issued'`
	args := []any{year, month}
	if len(ids) > 0 {
		sql += ` AND p.id = ANY($3)`
		args = append(args, ids)
	}
	rows, err := postgres.Select[payslipRow](ctx, r.db.Querier(ctx), sql+` ORDER BY p.id FOR UPDATE`, args...)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Payslip, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Repository) MarkPaid(ctx context.Context, ids []uuid.UUID, run uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_payslips SET status = 'paid', run_id = $2, updated_at = now() WHERE id = ANY($1)`, ids, run)
	return err
}

// ---- runs ----

type runRow struct {
	ID        uuid.UUID  `db:"id"`
	IdemKey   string     `db:"idem_key"`
	Year      int        `db:"year"`
	Month     int        `db:"month"`
	PayFrom   string     `db:"pay_from"`
	Total     int64      `db:"total"`
	Currency  string     `db:"currency"`
	Count     int        `db:"count"`
	Journal   *uuid.UUID `db:"journal_id"`
	Actor     string     `db:"actor"`
	CreatedAt time.Time  `db:"created_at"`
}

func (x runRow) domain() payroll.Run {
	return payroll.Run{ID: x.ID, IdemKey: x.IdemKey, Year: x.Year, Month: x.Month, PayFrom: x.PayFrom, Total: amount(x.Total, x.Currency), Count: x.Count,
		Journal: x.Journal, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertRun(ctx context.Context, run payroll.Run) (payroll.Run, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_runs (id, idem_key, year, month, pay_from, total, currency, count, actor, created_at) VALUES ($1, $2, $3, $4, $5, 0, $6, 0, $7, $8)
		ON CONFLICT (idem_key) DO NOTHING`, run.ID, run.IdemKey, run.Year, run.Month, run.PayFrom, string(run.Total.Currency()), run.Actor, run.CreatedAt)
	if err != nil {
		return run, false, err
	}
	if n == 1 {
		return run, true, nil
	}
	x, err := postgres.SelectOne[runRow](ctx, r.db.Querier(ctx),
		`SELECT id, idem_key, year, month, pay_from, total, currency, count, journal_id, actor, created_at FROM mz_pay_runs WHERE idem_key = $1`, run.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) UpdateRun(ctx context.Context, run *payroll.Run) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_runs SET total = $2, count = $3, journal_id = $4 WHERE id = $1`,
		run.ID, run.Total.Amount(), run.Count, run.Journal)
	return err
}

func nonNilDeductions(d []payroll.Deduction) []payroll.Deduction {
	if d == nil {
		return []payroll.Deduction{}
	}
	return d
}

// ---- loans ----

type loanRow struct {
	ID          uuid.UUID  `db:"id"`
	IdemKey     string     `db:"idem_key"`
	EmployeeID  uuid.UUID  `db:"employee_id"`
	Currency    string     `db:"currency"`
	Principal   int64      `db:"principal"`
	Installment int64      `db:"installment"`
	Remaining   int64      `db:"remaining"`
	StartYear   int        `db:"start_year"`
	StartMonth  int        `db:"start_month"`
	Status      string     `db:"status"`
	PayFrom     string     `db:"pay_from"`
	Journal     *uuid.UUID `db:"journal_id"`
	Note        string     `db:"note"`
	CreatedAt   time.Time  `db:"created_at"`
	WrittenOff  int64      `db:"written_off"`
}

const loanCols = `id, idem_key, employee_id, currency, principal, installment, remaining, start_year, start_month, status, pay_from, journal_id, note, created_at, written_off`

func (x loanRow) domain() payroll.Loan {
	return payroll.Loan{ID: x.ID, IdemKey: x.IdemKey, EmployeeID: x.EmployeeID, Principal: amount(x.Principal, x.Currency), Installment: amount(x.Installment, x.Currency),
		Remaining: amount(x.Remaining, x.Currency), StartYear: x.StartYear, StartMonth: x.StartMonth, Status: payroll.LoanStatus(x.Status), PayFrom: x.PayFrom,
		Journal: x.Journal, Note: x.Note, CreatedAt: x.CreatedAt, WrittenOff: amount(x.WrittenOff, x.Currency)}
}

func (r *Repository) InsertLoan(ctx context.Context, l payroll.Loan) (payroll.Loan, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_loans (id, idem_key, employee_id, currency, principal, installment, remaining, start_year, start_month, status, pay_from, journal_id, note, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) ON CONFLICT (idem_key) DO NOTHING`,
		l.ID, l.IdemKey, l.EmployeeID, string(l.Principal.Currency()), l.Principal.Amount(), l.Installment.Amount(), l.Remaining.Amount(), l.StartYear, l.StartMonth,
		string(l.Status), l.PayFrom, l.Journal, l.Note, l.CreatedAt)
	if err != nil {
		return l, false, err
	}
	if n == 1 {
		return l, true, nil
	}
	x, err := postgres.SelectOne[loanRow](ctx, r.db.Querier(ctx), `SELECT `+loanCols+` FROM mz_pay_loans WHERE idem_key = $1`, l.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) LoanForUpdate(ctx context.Context, id uuid.UUID) (payroll.Loan, error) {
	x, err := postgres.SelectOne[loanRow](ctx, r.db.Querier(ctx), `SELECT `+loanCols+` FROM mz_pay_loans WHERE id = $1 FOR UPDATE`, id)
	if err != nil {
		return payroll.Loan{}, notFound(err, "loan")
	}
	return x.domain(), nil
}

func (r *Repository) SetLoanJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_loans SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

func (r *Repository) Loan(ctx context.Context, id uuid.UUID) (payroll.Loan, error) {
	x, err := postgres.SelectOne[loanRow](ctx, r.db.Querier(ctx), `SELECT `+loanCols+` FROM mz_pay_loans WHERE id = $1`, id)
	if err != nil {
		return payroll.Loan{}, notFound(err, "loan")
	}
	return x.domain(), nil
}

func (r *Repository) loans(ctx context.Context, sql string, args ...any) ([]payroll.Loan, error) {
	rows, err := postgres.Select[loanRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Loan, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) LoansOf(ctx context.Context, employee uuid.UUID) ([]payroll.Loan, error) {
	return r.loans(ctx, `SELECT `+loanCols+` FROM mz_pay_loans WHERE employee_id = $1 ORDER BY created_at, id`, employee)
}

func (r *Repository) DueLoans(ctx context.Context, employee uuid.UUID, year, month int, forUpdate bool) ([]payroll.Loan, error) {
	sql := `SELECT ` + loanCols + ` FROM mz_pay_loans WHERE employee_id = $1 AND status = 'active' AND (start_year, start_month) <= ($2, $3) ORDER BY created_at, id`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	return r.loans(ctx, sql, employee, year, month)
}

func (r *Repository) ApplyLoanDeduction(ctx context.Context, id uuid.UUID, a money.Money) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx),
		`UPDATE mz_pay_loans SET remaining = remaining - $2, status = CASE WHEN remaining - $2 = 0 THEN 'closed' ELSE status END WHERE id = $1 AND remaining >= $2`, id, a.Amount())
	if err == nil && n == 0 {
		err = apperr.New(apperr.KindConflict, "loan_overpaid", "the deduction is more than the loan still owes")
	}
	return err
}

func (r *Repository) RestoreLoan(ctx context.Context, id uuid.UUID, a money.Money) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_loans SET remaining = remaining + $2, status = 'active' WHERE id = $1`, id, a.Amount())
	return err
}

// ---- remittances ----

type remitRow struct {
	ID          uuid.UUID  `db:"id"`
	IdemKey     string     `db:"idem_key"`
	Kind        string     `db:"kind"`
	PeriodYear  int        `db:"period_year"`
	PeriodMonth int        `db:"period_month"`
	Amount      int64      `db:"amount"`
	Currency    string     `db:"currency"`
	PayFrom     string     `db:"pay_from"`
	Reference   string     `db:"reference"`
	Journal     *uuid.UUID `db:"journal_id"`
	Actor       string     `db:"actor"`
	CreatedAt   time.Time  `db:"created_at"`
}

const remitCols = `id, idem_key, kind, period_year, period_month, amount, currency, pay_from, reference, journal_id, actor, created_at`

func (x remitRow) domain() payroll.Remittance {
	return payroll.Remittance{ID: x.ID, IdemKey: x.IdemKey, Kind: payroll.Liability(x.Kind), PeriodYear: x.PeriodYear, PeriodMonth: x.PeriodMonth,
		Amount: amount(x.Amount, x.Currency), PayFrom: x.PayFrom, Reference: x.Reference, Journal: x.Journal, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertRemittance(ctx context.Context, m payroll.Remittance) (payroll.Remittance, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_pay_remittances (`+remitCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) ON CONFLICT (idem_key) DO NOTHING`,
		m.ID, m.IdemKey, string(m.Kind), m.PeriodYear, m.PeriodMonth, m.Amount.Amount(), string(m.Amount.Currency()), m.PayFrom, m.Reference, m.Journal, m.Actor, m.CreatedAt)
	if err != nil {
		return m, false, err
	}
	if n == 1 {
		return m, true, nil
	}
	x, err := postgres.SelectOne[remitRow](ctx, r.db.Querier(ctx), `SELECT `+remitCols+` FROM mz_pay_remittances WHERE idem_key = $1`, m.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) SetRemittanceJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_remittances SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

func (r *Repository) LockRemittances(ctx context.Context, kind string) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `SELECT pg_advisory_xact_lock(hashtext('mizan:payroll_remit:' || $1))`, kind)
	return err
}

func (r *Repository) Remittances(ctx context.Context, kind string) ([]payroll.Remittance, error) {
	sql, args := `SELECT `+remitCols+` FROM mz_pay_remittances`, []any{}
	if kind != "" {
		sql, args = sql+` WHERE kind = $1`, []any{kind}
	}
	rows, err := postgres.Select[remitRow](ctx, r.db.Querier(ctx), sql+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Remittance, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) SetProfile(ctx context.Context, employee uuid.UUID, p payroll.Profile) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_pay_profiles (employee_id, data) VALUES ($1, $2) ON CONFLICT (employee_id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, employee, raw)
	return err
}

func (r *Repository) Profile(ctx context.Context, employee uuid.UUID) (payroll.Profile, bool, error) {
	rows, err := postgres.Select[struct {
		Data []byte `db:"data"`
	}](ctx, r.db.Querier(ctx), `SELECT data FROM mz_pay_profiles WHERE employee_id = $1`, employee)
	if err != nil || len(rows) == 0 {
		return payroll.Profile{}, false, err
	}
	var p payroll.Profile
	return p, true, json.Unmarshal(rows[0].Data, &p)
}

func (r *Repository) SetWorkshop(ctx context.Context, w payroll.Workshop) error {
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_pay_workshop (id, data) VALUES (1, $1) ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`, raw)
	return err
}

func (r *Repository) Workshop(ctx context.Context) (payroll.Workshop, bool, error) {
	rows, err := postgres.Select[struct {
		Data []byte `db:"data"`
	}](ctx, r.db.Querier(ctx), `SELECT data FROM mz_pay_workshop WHERE id = 1`)
	if err != nil || len(rows) == 0 {
		return payroll.Workshop{}, false, err
	}
	var w payroll.Workshop
	return w, true, json.Unmarshal(rows[0].Data, &w)
}

func (r *Repository) MonthSlips(ctx context.Context, year, month int) ([]payroll.Payslip, error) {
	rows, err := postgres.Select[payslipRow](ctx, r.db.Querier(ctx), `SELECT `+slipCols+` FROM mz_pay_payslips p WHERE p.year = $1 AND p.month = $2 AND p.status IN ('issued','paid') ORDER BY p.employee_id`, year, month)
	if err != nil {
		return nil, err
	}
	out := make([]payroll.Payslip, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---- loan write-off and the holiday calendar ----

func (r *Repository) WriteOffLoan(ctx context.Context, id uuid.UUID, a money.Money, journal uuid.UUID, reason string) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_pay_loans SET remaining = 0, status = 'closed', written_off = $2, write_off_journal = $3, write_off_reason = $4 WHERE id = $1 AND remaining = $2 AND status = 'active'`,
		id, a.Amount(), journal, reason)
	if err == nil && n == 0 {
		err = apperr.New(apperr.KindConflict, "loan_changed", "the loan changed while it was being written off")
	}
	return err
}

type holidayRow struct {
	Year     int    `db:"year"`
	Month    int    `db:"month"`
	Day      int    `db:"day"`
	Name     string `db:"name"`
	Kind     string `db:"kind"`
	Verified bool   `db:"verified"`
	Source   string `db:"source"`
}

func (r *Repository) InsertHoliday(ctx context.Context, h payroll.Holiday) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_pay_holidays (year, month, day, name, kind, verified, source) VALUES ($1,$2,$3,$4,$5,$6,$7)`, h.Year, h.Month, h.Day, h.Name, h.Kind, h.Verified, h.Source)
	if postgres.IsUniqueViolation(err, "mz_pay_holidays_pkey") {
		return apperr.New(apperr.KindConflict, "holiday_exists", "that date is already in the calendar")
	}
	return err
}

func (r *Repository) Holidays(ctx context.Context, year int) ([]payroll.Holiday, error) {
	rows, err := postgres.Select[holidayRow](ctx, r.db.Querier(ctx), `SELECT year, month, day, name, kind, verified, source FROM mz_pay_holidays WHERE year = $1 ORDER BY month, day`, year)
	out := make([]payroll.Holiday, len(rows))
	for i, x := range rows {
		out[i] = payroll.Holiday{Year: x.Year, Month: x.Month, Day: x.Day, Name: x.Name, Kind: x.Kind, Verified: x.Verified, Source: x.Source}
	}
	return out, err
}

func (r *Repository) DeleteHoliday(ctx context.Context, year, month, day int) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `DELETE FROM mz_pay_holidays WHERE year = $1 AND month = $2 AND day = $3`, year, month, day)
	if err == nil && n == 0 {
		err = apperr.NotFound("holiday")
	}
	return err
}
