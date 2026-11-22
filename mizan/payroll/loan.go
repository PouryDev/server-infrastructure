package payroll

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// LoanStatus of an employee loan or advance.
type LoanStatus string

const (
	LoanActive LoanStatus = "active"
	LoanClosed LoanStatus = "closed" // fully repaid
)

// Loan is money lent to an employee (وام / مساعده), repaid by monthly deductions from the net pay.
type Loan struct {
	ID          uuid.UUID
	IdemKey     string
	EmployeeID  uuid.UUID
	Principal   money.Money
	Installment money.Money // the monthly deduction; the last one is whatever remains
	Remaining   money.Money
	StartYear   int // first Jalali month the installment is deducted
	StartMonth  int
	Status      LoanStatus
	PayFrom     string // ledger account the money left
	Journal     *uuid.UUID
	Note        string
	CreatedAt   time.Time
	// WrittenOff is the part the employer forgave (WriteOffLoan): the loan is then closed with nothing remaining.
	WrittenOff money.Money
}

// LoanInput grants a loan.
type LoanInput struct {
	// IdemKey makes a retry return the same loan.
	IdemKey     string
	EmployeeID  uuid.UUID
	Principal   money.Money
	Installment money.Money
	StartYear   int
	StartMonth  int
	PayFrom     string // empty uses the default bank account
	Date        time.Time
	Note        string
	Actor       string
}

func (in LoanInput) validate() error {
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case !in.Principal.Valid() || !in.Principal.IsPositive():
		return apperr.InvalidField("principal", "must be a positive amount")
	case !in.Installment.Valid() || !in.Installment.IsPositive() || in.Installment.Currency() != in.Principal.Currency():
		return apperr.InvalidField("installment", "must be a positive amount in the currency of the principal")
	case in.Installment.Amount() > in.Principal.Amount():
		return apperr.InvalidField("installment", "must not exceed the principal")
	case len(in.Note) > 500:
		return apperr.InvalidField("note", "at most 500 characters")
	}
	_, _, _, err := MonthSpan(in.StartYear, in.StartMonth)
	return err
}

// LoanDue is the installment a loan asks for in a month.
type LoanDue struct {
	LoanID uuid.UUID
	Due    money.Money
}

// Deduction is an installment taken from a payslip.
type Deduction struct {
	LoanID uuid.UUID   `json:"loan_id"`
	Amount money.Money `json:"amount"`
}

// DueOf is what a loan asks for: the installment, or what remains when that is less.
func (l Loan) DueOf() money.Money {
	if l.Remaining.Amount() < l.Installment.Amount() {
		return l.Remaining
	}
	return l.Installment
}

// StartedBy reports whether deductions have begun by the Jalali month.
func (l Loan) StartedBy(year, month int) bool {
	return l.StartYear < year || (l.StartYear == year && l.StartMonth <= month)
}

// LoanRepository is the persistence port of the loan write-off (implemented by the same adapter as Repository).
type LoanRepository interface {
	// WriteOffLoan closes an active loan whose remaining equals a, recording the write-off.
	WriteOffLoan(ctx context.Context, id uuid.UUID, a money.Money, journal uuid.UUID, reason string) error
}

// WriteOffLoan forgives what an employee still owes on a loan (وام بخشوده): Dr the loan write-off expense, Cr the employee loans account for the remaining balance, and the loan is closed.
// The tax and social-security treatment of a forgiven loan could NOT be found in any text read (it may be a taxable benefit): this books the expense only and adds nothing to a
// payslip; the accountant decides whether to add it as an extra to the month's payslip. Idempotent: a closed loan returns as it is.
func (s *Service) WriteOffLoan(ctx context.Context, id uuid.UUID, reason, actor string) (Loan, error) {
	lr, ok := s.repo.(LoanRepository)
	if !ok {
		return Loan{}, apperr.New(apperr.KindConflict, "no_writeoff", "loan write-off is not available with this repository")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return Loan{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	if s.cfg.Accounts.LoanWriteOffExpense == "" {
		return Loan{}, apperr.New(apperr.KindConflict, "no_writeoff_account", "no loan write-off expense account configured")
	}
	var out Loan
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		l, err := s.repo.LoanForUpdate(ctx, id)
		if err != nil {
			return err
		}
		out = l
		if l.Status != LoanActive || !l.Remaining.IsPositive() {
			return nil
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "loan:" + l.ID.String() + ":writeoff", Date: dateOnly(s.books.Today()), Description: "Employee loan written off", RefType: "employee_loan", RefID: l.ID.String(), Actor: actor,
			Lines: []books.Line{books.Dr(s.cfg.Accounts.LoanWriteOffExpense, l.Remaining), books.Cr(s.cfg.Accounts.EmployeeLoans, l.Remaining)}})
		if err != nil {
			return err
		}
		if err := lr.WriteOffLoan(ctx, l.ID, l.Remaining, jid, reason); err != nil {
			return err
		}
		l.WrittenOff, l.Status, l.Remaining = l.Remaining, LoanClosed, money.Zero(l.Remaining.Currency())
		out = l
		return s.note(ctx, actor, "payroll.loan.written_off", "employee_loan", l.ID, "payroll.loan.written_off", map[string]any{"loan_id": l.ID, "employee_id": l.EmployeeID, "amount": l.WrittenOff.Amount(), "reason": reason})
	})
	return out, err
}
