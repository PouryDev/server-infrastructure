package treasury

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Settlement is a payout of a gateway to our bank account: the gateway keeps its fee and sends the rest.
// Books: Dr bank (net), Dr gateway fees (fee), Cr gateway clearing (gross).
type Settlement struct {
	ID        uuid.UUID
	Gateway   string
	Ref       string // the gateway's reference of the payout (batch id, bank tracking number)
	AccountID uuid.UUID
	Gross     money.Money
	Fee       money.Money
	Net       money.Money
	SettledOn time.Time
	JournalID uuid.UUID
	LineID    *uuid.UUID // the statement line that confirmed the money arrived
	Note      string
	Actor     string
	CreatedAt time.Time
}

// SettlementInput records a payout. Idempotent per (Gateway, Ref).
type SettlementInput struct {
	Gateway   string
	Ref       string
	AccountID uuid.UUID
	Gross     money.Money // what the gateway collected for the payout
	Fee       money.Money // what it kept (VAT included); zero when the fee is billed separately
	// FeeVAT is the part of Fee that is VAT (deductible input tax, booked to the input-VAT account instead of the fee expense); zero = none. It needs Accounts.VATInput.
	FeeVAT    money.Money
	SettledOn time.Time // the day the money reached the bank; default today
	Note      string
	Actor     string
}

func (in SettlementInput) validate() (SettlementInput, error) {
	in.Gateway, in.Ref, in.Note = strings.TrimSpace(in.Gateway), strings.TrimSpace(in.Ref), strings.TrimSpace(in.Note)
	if in.Fee.Currency() == "" && in.Fee.IsZero() { // no fee given
		in.Fee = money.Zero(in.Gross.Currency())
	}
	switch {
	case in.Gateway == "" || len(in.Gateway) > 60:
		return in, apperr.InvalidField("gateway", "required, at most 60 characters")
	case in.Ref == "" || len(in.Ref) > 120:
		return in, apperr.InvalidField("ref", "required, at most 120 characters")
	case in.AccountID == uuid.Nil:
		return in, apperr.InvalidField("bank_account_id", "required")
	case !in.Gross.Valid() || !in.Gross.IsPositive():
		return in, apperr.InvalidField("gross", "must be a positive amount")
	case !in.Fee.Valid() || in.Fee.IsNegative() || in.Fee.Currency() != in.Gross.Currency():
		return in, apperr.InvalidField("fee", "must be zero or positive, in the same currency")
	case len(in.Note) > 500:
		return in, apperr.InvalidField("note", "at most 500 characters")
	}
	if in.FeeVAT.Currency() == "" && in.FeeVAT.IsZero() {
		in.FeeVAT = money.Zero(in.Gross.Currency())
	}
	if !in.FeeVAT.Valid() || in.FeeVAT.IsNegative() || in.FeeVAT.Currency() != in.Gross.Currency() {
		return in, apperr.InvalidField("fee_vat", "must be zero or positive, in the same currency")
	}
	if c, _ := in.FeeVAT.Cmp(in.Fee); c > 0 {
		return in, apperr.InvalidField("fee_vat", "must not exceed the fee")
	}
	if c, _ := in.Fee.Cmp(in.Gross); c >= 0 {
		return in, apperr.InvalidField("fee", "must be smaller than the gross amount")
	}
	if !in.SettledOn.IsZero() {
		in.SettledOn = DateOnly(in.SettledOn)
	}
	return in, nil
}
