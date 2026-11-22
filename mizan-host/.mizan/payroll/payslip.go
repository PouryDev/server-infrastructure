package payroll

import (
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// PayslipStatus is the lifecycle position of a payslip: issued ─► paid, or issued ─► void.
type PayslipStatus string

const (
	Issued PayslipStatus = "issued" // booked: the net pay is owed to the employee
	Paid   PayslipStatus = "paid"   // included in a payment run
	Void   PayslipStatus = "void"   // cancelled with a reversing journal
)

// Payslip is the booked pay of one employee for one Jalali month; there is at most one live payslip per
// (employee, month).
type Payslip struct {
	ID         uuid.UUID
	EmployeeID uuid.UUID
	ContractID uuid.UUID
	RuleID     uuid.UUID // the rule version the figures were computed with
	Year       int
	Month      int
	Status     PayslipStatus
	Breakdown
	Journal     *uuid.UUID
	VoidJournal *uuid.UUID
	RunID       *uuid.UUID
	VoidReason  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PayslipInput issues a payslip.
type PayslipInput struct {
	EmployeeID uuid.UUID
	Year       int
	Month      int
	// Days overrides the worked days (unpaid leave); 0 = all days the contract covers.
	Days   int
	Extras []Allowance
	// Hours of overtime, holiday and night work; LeaveDays of unused leave paid out; Severance pays the end-of-service
	// amount (the employee's contract must have ended in the month).
	Hours     Hours
	LeaveDays int
	// BonusDays pays the annual bonus (عیدی) for this many days of service in the year (365 = a full year).
	BonusDays int
	Severance bool
	Actor     string
}

// Run is one batch payment of payslips from a bank account.
type Run struct {
	ID        uuid.UUID
	IdemKey   string
	Year      int
	Month     int
	PayFrom   string // ledger account paid from
	Total     money.Money
	Count     int
	Journal   *uuid.UUID
	Actor     string
	CreatedAt time.Time
}

// RunInput pays payslips.
type RunInput struct {
	// IdemKey makes a retry return the same run.
	IdemKey string
	Year    int
	Month   int
	// PayslipIDs selects payslips; empty pays every issued payslip of the month.
	PayslipIDs []uuid.UUID
	// PayFrom is the ledger account of the bank account; empty uses the configured default.
	PayFrom string
	// Date of the payment; zero = today.
	Date  time.Time
	Actor string
}
