// Package payout is the marketplace settlement context: sellers, the commission the platform keeps on each sale,
// the period the seller's money is held, and batched payouts to the sellers' bank accounts. It posts through
// books.Poster and knows no other context.
//
// Every sale is booked when it happens (Dr customer receivable, Cr the seller's payable account, Cr commission
// revenue). The seller's share is released N days after delivery and then paid in batches (Dr seller payables,
// Cr bank). The commission rate and N are versioned data with effective dates, never constants.
package payout

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// SellerStatus of a seller.
type SellerStatus string

const (
	SellerActive  SellerStatus = "active"
	SellerBlocked SellerStatus = "blocked" // payouts are withheld; sales are still booked
)

// Seller is a marketplace seller. Its money is the balance of a liability account in the ledger.
type Seller struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Sheba       string // destination IBAN (شبا), optional
	Status      SellerStatus
	BlockReason string
	AccountCode string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SellerInput registers a seller.
type SellerInput struct {
	Code  string
	Name  string
	Sheba string
	Actor string
}

func (in SellerInput) validate() (SellerInput, error) {
	in.Code, in.Name = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name)
	switch {
	case in.Code == "" || len(in.Code) > 60:
		return in, apperr.InvalidField("code", "required, at most 60 characters")
	case in.Name == "" || len(in.Name) > 200:
		return in, apperr.InvalidField("name", "required, at most 200 characters")
	}
	sheba, err := NormalizeSheba(in.Sheba)
	in.Sheba = sheba
	return in, err
}

// AccountCodeFor is the ledger account that holds a seller's money.
func AccountCodeFor(id uuid.UUID) string { return "payout.seller." + id.String() }

// NormalizeSheba upper-cases an IBAN, removes spaces and checks the Iranian shape (IR + 24 digits) and the ISO 13616
// check digits. Empty is allowed.
func NormalizeSheba(s string) (string, error) {
	s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(s)))
	if s == "" {
		return "", nil
	}
	if len(s) != 26 || !strings.HasPrefix(s, "IR") {
		return "", apperr.InvalidField("sheba", "an Iranian IBAN is IR followed by 24 digits")
	}
	for _, r := range s[2:] {
		if r < '0' || r > '9' {
			return "", apperr.InvalidField("sheba", "an Iranian IBAN is IR followed by 24 digits")
		}
	}
	// ISO 13616: move the first four characters to the end, letters become 10..35, the number must be 1 mod 97.
	rearranged := s[4:] + s[:4]
	rem := 0
	for _, r := range rearranged {
		if r >= '0' && r <= '9' {
			rem = (rem*10 + int(r-'0')) % 97
		} else {
			rem = (rem*100 + int(r-'A') + 10) % 97
		}
	}
	if rem != 1 {
		return "", apperr.InvalidField("sheba", "the check digits are wrong")
	}
	return s, nil
}

// ScopeKind says whom a terms version applies to.
type ScopeKind string

const (
	ScopeDefault  ScopeKind = "default"
	ScopeCategory ScopeKind = "category"
	ScopeSeller   ScopeKind = "seller"
)

// Terms is one version of the commission rate and hold period for a scope, valid from EffectiveFrom until the next
// version of the same scope starts. Versions are never edited: a change is a new version. A sale keeps the version
// it was booked with.
type Terms struct {
	ID            uuid.UUID
	Name          string
	Scope         ScopeKind
	ScopeKey      string // "" for the default, the category, or the seller's id
	EffectiveFrom time.Time
	RateBP        int // commission in basis points: 1000 = 10%
	HoldDays      int // days after delivery before the seller's share is released
	// VATBP is the VAT on the commission, charged to the seller on top of it; the commission is the tax base. Nil follows the
	// accounting setting (Config.DefaultVATBP), so a change there applies without touching the terms; 0 means none.
	VATBP     *int
	Source    string
	CreatedBy string
	CreatedAt time.Time
}

// Validate checks the numbers are coherent.
func (t Terms) Validate() error {
	switch {
	case strings.TrimSpace(t.Name) == "" || len(t.Name) > 120:
		return apperr.InvalidField("name", "required, at most 120 characters")
	case t.EffectiveFrom.IsZero():
		return apperr.InvalidField("effective_from", "required")
	case t.RateBP < 0 || t.RateBP > 10000:
		return apperr.InvalidField("rate_bp", "basis points between 0 and 10000")
	case t.VATBP != nil && (*t.VATBP < 0 || *t.VATBP > 10000):
		return apperr.InvalidField("vat_bp", "basis points between 0 and 10000")
	case t.HoldDays < 0 || t.HoldDays > 365:
		return apperr.InvalidField("hold_days", "between 0 and 365")
	case len(t.Source) > 500:
		return apperr.InvalidField("source", "at most 500 characters")
	}
	switch t.Scope {
	case ScopeDefault:
		if t.ScopeKey != "" {
			return apperr.InvalidField("scope_key", "the default scope has no key")
		}
	case ScopeCategory:
		if t.ScopeKey == "" || len(t.ScopeKey) > 100 {
			return apperr.InvalidField("scope_key", "the category is required, at most 100 characters")
		}
	case ScopeSeller:
		if _, err := uuid.Parse(t.ScopeKey); err != nil {
			return apperr.InvalidField("scope_key", "the seller id is required")
		}
	default:
		return apperr.InvalidField("scope", "default, category or seller")
	}
	return nil
}

// TermsInput adds a terms version.
type TermsInput struct {
	Name          string
	Scope         ScopeKind
	ScopeKey      string
	EffectiveFrom time.Time
	RateBP        int
	HoldDays      int
	VATBP         *int
	Source        string
	Actor         string
}

func (in TermsInput) version(id uuid.UUID, now time.Time) Terms {
	if in.Scope == "" {
		in.Scope = ScopeDefault
	}
	return Terms{ID: id, Name: strings.TrimSpace(in.Name), Scope: in.Scope, ScopeKey: strings.TrimSpace(in.ScopeKey), EffectiveFrom: dateOnly(in.EffectiveFrom),
		RateBP: in.RateBP, HoldDays: in.HoldDays, VATBP: in.VATBP, Source: strings.TrimSpace(in.Source), CreatedBy: in.Actor, CreatedAt: now}
}

// PickTerms returns the version in force on a day for a seller and category: the seller's own terms win over the
// category's, which win over the default; inside a scope the latest version that started on or before the day.
func PickTerms(all []Terms, seller uuid.UUID, category string, day time.Time) (Terms, bool) {
	day = dateOnly(day)
	for _, sc := range []struct {
		kind ScopeKind
		key  string
		ok   bool
	}{{ScopeSeller, seller.String(), true}, {ScopeCategory, category, category != ""}, {ScopeDefault, "", true}} {
		if !sc.ok {
			continue
		}
		var best Terms
		found := false
		for _, t := range all {
			if t.Scope != sc.kind || t.ScopeKey != sc.key || t.EffectiveFrom.After(day) {
				continue
			}
			if !found || t.EffectiveFrom.After(best.EffectiveFrom) {
				best, found = t, true
			}
		}
		if found {
			return best, true
		}
	}
	return Terms{}, false
}

// SaleStatus is the stored status of a sale.
type SaleStatus string

const (
	SaleHeld     SaleStatus = "held" // booked, the seller's share not paid yet
	SalePaid     SaleStatus = "paid"
	SaleReturned SaleStatus = "returned"
)

// Sale is one booked marketplace sale.
type Sale struct {
	ID         uuid.UUID
	IdemKey    string
	SellerID   uuid.UUID
	TermsID    uuid.UUID
	Reference  string // the marketplace order
	Category   string
	Gross      money.Money // what the customer owes
	Commission money.Money // kept by the platform
	VAT        money.Money // VAT on the commission, owed to the tax authority and deducted from the seller
	Net        money.Money // owed to the seller: gross - commission - VAT
	RateBP     int
	VATBP      int
	HoldDays   int
	Status     SaleStatus
	SaleDate   time.Time
	Delivered  *time.Time
	ReleaseAt  *time.Time // the first day the seller's share can be paid
	Journal    *uuid.UUID
	ReturnJrnl *uuid.UUID
	ReturnWhy  string
	BatchID    *uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Phase is where a sale stands on a day: awaiting_delivery, held, available, paid or returned.
func (s Sale) Phase(day time.Time) string {
	switch {
	case s.Status != SaleHeld:
		return string(s.Status)
	case s.ReleaseAt == nil:
		return "awaiting_delivery"
	case s.ReleaseAt.After(dateOnly(day)):
		return "held"
	}
	return "available"
}

// SaleInput books a sale.
type SaleInput struct {
	IdemKey   string // the same key returns the same sale
	SellerID  uuid.UUID
	Gross     money.Money
	Reference string
	Category  string
	Date      time.Time // the sale date; today when zero
	Delivered *time.Time
	Actor     string
}

func (in SaleInput) validate() error {
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case in.SellerID == uuid.Nil:
		return apperr.InvalidField("seller_id", "required")
	case !in.Gross.Valid() || !in.Gross.IsPositive():
		return apperr.InvalidField("gross", "must be a positive amount")
	case len(in.Reference) > 200:
		return apperr.InvalidField("reference", "at most 200 characters")
	case len(in.Category) > 100:
		return apperr.InvalidField("category", "at most 100 characters")
	}
	return nil
}

// Totals are a seller's sales by phase, in the books' currency.
type Totals struct {
	AwaitingDelivery int64
	Held             int64
	Available        int64
	Paid             int64
}

// Balance is what the platform owes a seller and how much of it can be paid.
type Balance struct {
	Owed   money.Money // the seller's ledger account
	Totals Totals
}

// BatchItem is the part of a payout batch that goes to one seller.
type BatchItem struct {
	SellerID  uuid.UUID
	Sheba     string
	Amount    money.Money
	SaleCount int
	// Rejected is set when the bank rejected the transfer (RejectPayoutItem): the money came back and the sales are payable again.
	Rejected     bool
	RejectReason string
}

// Batch is one payout run.
type Batch struct {
	ID        uuid.UUID
	IdemKey   string
	PayFrom   string
	Date      time.Time
	Total     money.Money
	SaleCount int
	Journal   *uuid.UUID
	Items     []BatchItem
	Actor     string
	CreatedAt time.Time
}

// PayoutInput pays every seller whose released sales are due.
type PayoutInput struct {
	IdemKey   string
	PayFrom   string      // empty uses the default bank account
	Date      time.Time   // payout date; today when zero
	SellerIDs []uuid.UUID // limit the batch to these sellers; empty means all
	Actor     string
}

func (in PayoutInput) validate() error {
	if in.IdemKey == "" || len(in.IdemKey) > 200 {
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	}
	return nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
