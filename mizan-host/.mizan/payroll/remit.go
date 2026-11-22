package payroll

import (
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Liability is a payroll debt to an authority.
type Liability string

const (
	LiabilityTax       Liability = "tax"       // income tax withheld, owed to the tax office
	LiabilityInsurance Liability = "insurance" // social-security shares, owed to the insurer
)

// Remittance is a payment of a withheld liability to its authority.
type Remittance struct {
	ID          uuid.UUID
	IdemKey     string
	Kind        Liability
	PeriodYear  int // Jalali period the payment is for (informational)
	PeriodMonth int
	Amount      money.Money
	PayFrom     string
	Reference   string // the authority's receipt or payment id
	Journal     *uuid.UUID
	Actor       string
	CreatedAt   time.Time
}

// RemitInput pays a liability.
type RemitInput struct {
	// IdemKey makes a retry return the same remittance.
	IdemKey     string
	Kind        Liability
	Amount      money.Money
	PeriodYear  int
	PeriodMonth int
	PayFrom     string // empty uses the default bank account
	Date        time.Time
	Reference   string
	Actor       string
}

func (in RemitInput) validate() error {
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case in.Kind != LiabilityTax && in.Kind != LiabilityInsurance:
		return apperr.InvalidField("kind", "tax or insurance")
	case !in.Amount.Valid() || !in.Amount.IsPositive():
		return apperr.InvalidField("amount", "must be a positive amount")
	case len(in.Reference) > 200:
		return apperr.InvalidField("reference", "at most 200 characters")
	case (in.PeriodYear == 0) != (in.PeriodMonth == 0):
		return apperr.InvalidField("period", "give both year and month, or neither")
	}
	if in.PeriodYear != 0 {
		if _, _, _, err := MonthSpan(in.PeriodYear, in.PeriodMonth); err != nil {
			return err
		}
	}
	return nil
}
