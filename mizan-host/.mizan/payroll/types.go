// Package payroll is the payroll context: employees, contracts, monthly payslips by Jalali month and the payment
// run. Social-security and income-tax rules are DATA (RuleVersion rows with an effective date) that finance adds
// through the API; no legal number lives in code. The context posts through books.Poster and knows no other context.
package payroll

import (
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Employee is a person on the payroll.
type Employee struct {
	ID          uuid.UUID
	Code        string // unique personnel code
	Name        string
	NationalID  string
	InsuranceNo string // شماره بیمه
	Active      bool
	CreatedAt   time.Time
}

// EmployeeInput registers an employee.
type EmployeeInput struct {
	Code, Name, NationalID, InsuranceNo, Actor string
}

func (in EmployeeInput) validate() (EmployeeInput, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	in.NationalID, in.InsuranceNo = digits(in.NationalID), strings.TrimSpace(in.InsuranceNo)
	switch {
	case in.Code == "" || len(in.Code) > 40:
		return in, apperr.InvalidField("code", "required, at most 40 characters")
	case in.Name == "" || len(in.Name) > 200:
		return in, apperr.InvalidField("name", "required, at most 200 characters")
	case in.NationalID != "" && len(in.NationalID) != 10:
		return in, apperr.InvalidField("national_id", "must be 10 digits")
	case len(in.InsuranceNo) > 40:
		return in, apperr.InvalidField("insurance_no", "at most 40 characters")
	}
	return in, nil
}

// digits keeps the digits of s, converting Persian and Arabic-Indic ones.
func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case unicode.IsDigit(r) && r < 128:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Allowance is a monthly pay item of a contract (or a one-off extra of a payslip). The two flags are data the
// accountant sets per item: whether it counts for income tax and for social-security.
type Allowance struct {
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Amount    money.Money `json:"amount"`
	Taxable   bool        `json:"taxable"`
	Insurable bool        `json:"insurable"`
	// WageBase marks the item as part of the wage that overtime, leave pay and severance are computed on (the base
	// salary always is). Which allowances belong to it is the accountant's call, so it is a flag, not a rule in code.
	WageBase bool `json:"wage_base"`
}

func (a Allowance) validate(field string) error {
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 120 {
		return apperr.InvalidField(field, "every item needs a name of at most 120 characters")
	}
	if !a.Amount.Valid() || a.Amount.IsNegative() {
		return apperr.InvalidField(field, "item amounts must not be negative")
	}
	return nil
}

// Contract is the pay agreement of an employee for a period.
type Contract struct {
	ID         uuid.UUID
	EmployeeID uuid.UUID
	BaseSalary money.Money // monthly
	Allowances []Allowance
	Insured    bool // pays social security
	Start      time.Time
	End        *time.Time // inclusive; nil while open-ended
	CreatedAt  time.Time
}

// ContractInput adds a contract.
type ContractInput struct {
	EmployeeID uuid.UUID
	BaseSalary money.Money
	Allowances []Allowance
	Insured    bool
	Start      time.Time
	End        *time.Time
	Actor      string
}

func (in ContractInput) validate() (ContractInput, error) {
	if !in.BaseSalary.Valid() || !in.BaseSalary.IsPositive() {
		return in, apperr.InvalidField("base_salary", "must be a positive amount")
	}
	if in.Start.IsZero() {
		return in, apperr.InvalidField("start", "required")
	}
	in.Start = dateOnly(in.Start)
	if in.End != nil {
		e := dateOnly(*in.End)
		if e.Before(in.Start) {
			return in, apperr.InvalidField("end", "must not be before the start")
		}
		in.End = &e
	}
	for _, a := range in.Allowances {
		if err := a.validate("allowances"); err != nil {
			return in, err
		}
		if a.Amount.Currency() != in.BaseSalary.Currency() {
			return in, apperr.InvalidField("allowances", "must use the currency of the base salary")
		}
	}
	return in, nil
}

// Covers reports whether the contract is in force on a day.
func (c Contract) Covers(day time.Time) bool {
	return !day.Before(c.Start) && (c.End == nil || !day.After(*c.End))
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
