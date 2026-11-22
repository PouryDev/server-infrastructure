package vat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Apportionment of one period's input tax between the deductible and the non-deductible part, for a taxpayer with taxable AND exempt supplies.
//
// Law (art. 8 of the law of 1400/03/02, text read): note 2 — a taxpayer with only exempt supplies cannot deduct, set off or recover the tax paid on purchases;
// note 3 — with mixed supplies only the tax on the inputs needed to produce the taxable goods and services is deductible; note 4 — tax on production-line
// machinery is deductible whether the supplies are exempt or taxable; note 5 — the part that cannot be deducted is an accepted expense of the direct-tax law.
// The law gives NO formula for inputs shared by both kinds of supply, so the share of those is taken here by the ratio of turnover (taxable / (taxable + exempt)),
// rounded down for the deductible part: that ratio is this module's assumption, the accountant must confirm it, and the direct attributions are the user's.
type Apportionment struct {
	ID             uuid.UUID
	Year, No, Mths int
	InputTotal     int64 // the period's whole input tax (must equal the sum of the four parts below)
	DirectTaxable  int64 // input tax of purchases used only for taxable supplies
	DirectExempt   int64 // input tax of purchases used only for exempt supplies
	Machinery      int64 // input tax of production-line machinery (always deductible, note 4)
	Common         int64 // input tax of purchases used for both
	TaxableSales   int64 // turnover (rials, before tax) of taxable supplies in the period
	ExemptSales    int64 // turnover of exempt supplies in the period
	Recoverable    int64
	NonRecoverable int64
	Journal        *uuid.UUID
	IdemKey        string
	Actor          string
	CreatedAt      time.Time
}

// ApportionInput is the user's classification of a period's input tax.
type ApportionInput struct {
	IdemKey                                        string
	Year, Period, Months                           int
	DirectTaxable, DirectExempt, Machinery, Common int64
	TaxableSales, ExemptSales                      int64
	Actor                                          string
}

// Apportion4 computes the deductible and non-deductible parts. It is pure.
func Apportion4(directTaxable, directExempt, machinery, common, taxableSales, exemptSales int64) (recoverable, nonRecoverable int64, err error) {
	for _, v := range []int64{directTaxable, directExempt, machinery, common, taxableSales, exemptSales} {
		if v < 0 {
			return 0, 0, apperr.Invalid("amounts must not be negative")
		}
	}
	commonRec := int64(0)
	if common > 0 {
		den := taxableSales + exemptSales
		if den <= 0 {
			return 0, 0, apperr.InvalidField("taxable_sales", "shared input tax needs the period's turnover of taxable and exempt supplies to be apportioned")
		}
		// split b = q*c + r so that no intermediate product overflows
		commonRec = mulDiv(common, taxableSales, den)
	}
	recoverable = directTaxable + machinery + commonRec
	nonRecoverable = directExempt + common - commonRec
	return recoverable, nonRecoverable, nil
}

// mulDiv computes a*b/c rounded down without overflowing for a, b up to int64 range when the result fits.
func mulDiv(a, b, c int64) int64 {
	q, r := b/c, b%c
	return a*q + a*r/c
}

// Apportion books the non-deductible part of the period's input tax as an expense (Dr expense, Cr input tax), once per period and before the return is
// filed, so the return then deducts only the deductible part. The parts must add up to the period's input tax read from the books.
func (s *Service) Apportion(ctx context.Context, in ApportionInput) (Apportionment, error) {
	if strings.TrimSpace(in.IdemKey) == "" || len(in.IdemKey) > 120 {
		return Apportionment{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if s.cfg.Accounts.Expense == "" {
		return Apportionment{}, apperr.New(apperr.KindConflict, "no_expense_account", "no expense account configured for non-deductible input tax")
	}
	rec, non, err := Apportion4(in.DirectTaxable, in.DirectExempt, in.Machinery, in.Common, in.TaxableSales, in.ExemptSales)
	if err != nil {
		return Apportionment{}, err
	}
	var out Apportionment
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		f, err := s.Preview(ctx, in.Year, in.Months, in.Period)
		if err != nil {
			return err
		}
		if err := s.repo.LockPeriod(ctx, f.Year, f.Months, f.No); err != nil {
			return err
		}
		if _, filed, err := s.repo.LatestReturn(ctx, f.Year, f.Months, f.No); err != nil {
			return err
		} else if filed {
			return apperr.New(apperr.KindConflict, "already_filed", "the period was filed already: apportion before filing")
		}
		if f, err = s.Preview(ctx, in.Year, f.Months, in.Period); err != nil {
			return err
		}
		if rec+non != f.Input {
			return apperr.New(apperr.KindConflict, "input_mismatch", fmt.Sprintf("the parts add up to %d but the period's input tax is %d", rec+non, f.Input))
		}
		a := Apportionment{ID: uuid.New(), Year: f.Year, No: f.No, Mths: f.Months, InputTotal: f.Input, DirectTaxable: in.DirectTaxable, DirectExempt: in.DirectExempt,
			Machinery: in.Machinery, Common: in.Common, TaxableSales: in.TaxableSales, ExemptSales: in.ExemptSales, Recoverable: rec, NonRecoverable: non, IdemKey: in.IdemKey, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertApportionment(ctx, a)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		if non > 0 {
			jid, err := s.books.Post(ctx, books.Entry{Key: "vat:apportion:" + a.ID.String(), Date: f.To, Description: fmt.Sprintf("VAT non-deductible input tax %d period %d", f.Year, f.No),
				RefType: "vat_apportionment", RefID: a.ID.String(), Actor: in.Actor,
				Lines: []books.Line{books.Dr(s.cfg.Accounts.Expense, s.amt(non)), books.Cr(s.cfg.Accounts.Input, s.amt(non))}})
			if err != nil {
				return err
			}
			a.Journal = &jid
			if err := s.repo.SetApportionmentJournal(ctx, a.ID, jid); err != nil {
				return err
			}
		}
		out = a
		det := map[string]any{"year": f.Year, "period": f.No, "recoverable": rec, "non_recoverable": non}
		if err := s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "vat.input_apportioned", EntityType: "vat_apportionment", EntityID: a.ID.String(), Details: det}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "vat.input.apportioned", AggregateType: "vat_apportionment", AggregateID: a.ID.String(), Payload: det})
	})
	return out, err
}

// Apportionments lists the stored apportionments.
func (s *Service) Apportionments(ctx context.Context) ([]Apportionment, error) {
	return s.repo.Apportionments(ctx)
}
