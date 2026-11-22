package payroll

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Transactor runs fn in a transaction that repositories and the books join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertEmployee stores an employee; a code already in use fails with an apperr conflict "employee_exists".
	InsertEmployee(ctx context.Context, e Employee) error
	Employee(ctx context.Context, id uuid.UUID, forUpdate bool) (Employee, error)
	Employees(ctx context.Context) ([]Employee, error)

	InsertContract(ctx context.Context, c Contract) error
	Contract(ctx context.Context, id uuid.UUID, forUpdate bool) (Contract, error)
	Contracts(ctx context.Context, employee uuid.UUID) ([]Contract, error)
	SetContractEnd(ctx context.Context, id uuid.UUID, end time.Time) error

	// InsertRule stores a version; an effective date already taken fails with a conflict "rule_date_taken".
	InsertRule(ctx context.Context, r RuleVersion) error
	Rule(ctx context.Context, id uuid.UUID) (RuleVersion, error)
	// RuleAt returns the version in force on a day, or apperr.NotFound when none starts on or before it.
	RuleAt(ctx context.Context, day time.Time) (RuleVersion, error)
	Rules(ctx context.Context) ([]RuleVersion, error)
	// DeleteRule removes an unused version; one a payslip used fails with a conflict "rule_in_use".
	DeleteRule(ctx context.Context, id uuid.UUID) error

	// InsertPayslip stores a payslip; the live payslip of the same (employee, month) is returned with created=false.
	InsertPayslip(ctx context.Context, p Payslip) (stored Payslip, created bool, err error)
	Payslip(ctx context.Context, id uuid.UUID, forUpdate bool) (Payslip, error)
	UpdatePayslip(ctx context.Context, p *Payslip) error
	ListPayslips(ctx context.Context, p query.Params) (query.Page[Payslip], error)
	// PayableSlips locks the issued payslips of the month (only ids when given), ordered by id.
	PayableSlips(ctx context.Context, year, month int, ids []uuid.UUID) ([]Payslip, error)
	MarkPaid(ctx context.Context, ids []uuid.UUID, run uuid.UUID) error

	SetProfile(ctx context.Context, employee uuid.UUID, p Profile) error
	Profile(ctx context.Context, employee uuid.UUID) (Profile, bool, error)
	SetWorkshop(ctx context.Context, w Workshop) error
	Workshop(ctx context.Context) (Workshop, bool, error)
	// MonthSlips lists the issued and paid payslips of a month, ordered by employee.
	MonthSlips(ctx context.Context, year, month int) ([]Payslip, error)

	// InsertLoan stores a loan; the same key returns the stored one with created=false.
	InsertLoan(ctx context.Context, l Loan) (stored Loan, created bool, err error)
	SetLoanJournal(ctx context.Context, id, journal uuid.UUID) error
	Loan(ctx context.Context, id uuid.UUID) (Loan, error)
	// LoanForUpdate returns a loan locked until the transaction ends.
	LoanForUpdate(ctx context.Context, id uuid.UUID) (Loan, error)
	LoansOf(ctx context.Context, employee uuid.UUID) ([]Loan, error)
	// DueLoans lists the active loans of the employee whose deductions have begun by the month, oldest first.
	DueLoans(ctx context.Context, employee uuid.UUID, year, month int, forUpdate bool) ([]Loan, error)
	// ApplyLoanDeduction lowers what a loan owes (closing it at zero); RestoreLoan undoes it.
	ApplyLoanDeduction(ctx context.Context, id uuid.UUID, amount money.Money) error
	RestoreLoan(ctx context.Context, id uuid.UUID, amount money.Money) error

	// InsertRemittance stores a payment of a liability; the same key returns the stored one with created=false.
	InsertRemittance(ctx context.Context, r Remittance) (stored Remittance, created bool, err error)
	SetRemittanceJournal(ctx context.Context, id, journal uuid.UUID) error
	// LockRemittances serialises the payments of one kind until the transaction ends.
	LockRemittances(ctx context.Context, kind string) error
	Remittances(ctx context.Context, kind string) ([]Remittance, error)

	// InsertRun stores a run; the same key returns the stored one with created=false.
	InsertRun(ctx context.Context, r Run) (stored Run, created bool, err error)
	UpdateRun(ctx context.Context, r *Run) error
}
