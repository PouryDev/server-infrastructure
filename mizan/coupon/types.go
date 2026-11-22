// Package coupon is the discount-coupon context: a coupon has a percentage or a fixed amount and limits (dates, a minimum
// amount, total redemptions, redemptions per customer); a redemption is idempotent per key and the limits are enforced under a
// row lock, so concurrent redemptions never exceed them. It computes discounts only: the caller (the root package) puts the
// discount on an invoice.
package coupon

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Kind of a coupon.
type Kind string

const (
	Percent Kind = "percent" // PercentBP basis points of the amount
	Fixed   Kind = "fixed"   // a fixed amount, never more than the amount it is applied to
)

var codeRe = regexp.MustCompile(`^[A-Z0-9_-]{3,40}$`)

// Coupon is a discount code. Its limits are data and can be changed (Update); the kind and value cannot, so a code never
// changes meaning: issue a new one.
type Coupon struct {
	ID             uuid.UUID
	Code           string
	Name           string
	Kind           Kind
	PercentBP      int
	Currency       money.Currency
	FixedAmount    int64
	MinAmount      int64      // the amount must be at least this much (0 = no minimum)
	ValidFrom      *time.Time // inclusive day
	ValidUntil     *time.Time // inclusive day
	MaxRedemptions *int       // nil = unlimited
	MaxPerCustomer *int       // nil = unlimited
	Active         bool
	Redeemed       int // redemptions in force (released ones do not count)
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Input creates a coupon.
type Input struct {
	Code           string
	Name           string
	Kind           Kind
	PercentBP      int
	Currency       money.Currency
	FixedAmount    int64
	MinAmount      int64
	ValidFrom      *time.Time
	ValidUntil     *time.Time
	MaxRedemptions *int
	MaxPerCustomer *int
	Actor          string
}

// NormalizeCode upper-cases and trims a code.
func NormalizeCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func (c Coupon) validate() error {
	switch {
	case !codeRe.MatchString(c.Code):
		return apperr.InvalidField("code", "3 to 40 characters: letters, digits, - and _")
	case strings.TrimSpace(c.Name) == "" || len(c.Name) > 120:
		return apperr.InvalidField("name", "required, at most 120 characters")
	case !c.Currency.Valid():
		return apperr.InvalidField("currency", "IRR or IRT")
	case c.MinAmount < 0:
		return apperr.InvalidField("min_amount", "must not be negative")
	case c.MaxRedemptions != nil && *c.MaxRedemptions <= 0, c.MaxPerCustomer != nil && *c.MaxPerCustomer <= 0:
		return apperr.InvalidField("limits", "limits must be positive, or absent for unlimited")
	case c.ValidFrom != nil && c.ValidUntil != nil && c.ValidUntil.Before(*c.ValidFrom):
		return apperr.InvalidField("valid_until", "must not be before valid_from")
	}
	switch c.Kind {
	case Percent:
		if c.PercentBP <= 0 || c.PercentBP > 10000 || c.FixedAmount != 0 {
			return apperr.InvalidField("percent_bp", "a percentage coupon needs basis points between 1 and 10000 and no fixed amount")
		}
	case Fixed:
		if c.FixedAmount <= 0 || c.PercentBP != 0 {
			return apperr.InvalidField("fixed_amount", "a fixed coupon needs a positive amount and no percentage")
		}
	default:
		return apperr.InvalidField("kind", "percent or fixed")
	}
	return nil
}

func (in Input) coupon(id uuid.UUID, now time.Time) Coupon {
	day := func(t *time.Time) *time.Time {
		if t == nil {
			return nil
		}
		d := dateOnly(*t)
		return &d
	}
	return Coupon{ID: id, Code: NormalizeCode(in.Code), Name: strings.TrimSpace(in.Name), Kind: in.Kind, PercentBP: in.PercentBP, Currency: in.Currency,
		FixedAmount: in.FixedAmount, MinAmount: in.MinAmount, ValidFrom: day(in.ValidFrom), ValidUntil: day(in.ValidUntil), MaxRedemptions: in.MaxRedemptions,
		MaxPerCustomer: in.MaxPerCustomer, Active: true, CreatedBy: in.Actor, CreatedAt: now, UpdatedAt: now}
}

// DiscountFor is the discount this coupon gives on an amount: the percentage rounded half up, or the fixed amount, never
// more than the amount itself.
func (c Coupon) DiscountFor(amount money.Money) (money.Money, error) {
	var d money.Money
	var err error
	if c.Kind == Percent {
		d, err = amount.Percent(int64(c.PercentBP), money.HalfUp)
	} else {
		d, err = money.New(c.FixedAmount, c.Currency)
	}
	if err != nil {
		return money.Money{}, err
	}
	if cmp, _ := d.Cmp(amount); cmp > 0 {
		return amount, nil
	}
	return d, nil
}

// Usable says why a coupon cannot be redeemed on a day for an amount, or nil when it can. Customer and total limits are
// checked by the service under the lock.
func (c Coupon) Usable(day time.Time, amount money.Money) error {
	day = dateOnly(day)
	conflict := func(code, msg string) error { return apperr.New(apperr.KindConflict, code, msg) }
	switch {
	case !c.Active:
		return conflict("coupon_inactive", "the coupon is not active")
	case c.ValidFrom != nil && day.Before(*c.ValidFrom):
		return conflict("coupon_not_started", "the coupon is not valid yet")
	case c.ValidUntil != nil && day.After(*c.ValidUntil):
		return conflict("coupon_expired", "the coupon has expired")
	case amount.Currency() != c.Currency:
		return conflict("currency_mismatch", "the coupon is in "+string(c.Currency))
	case amount.Amount() < c.MinAmount:
		return conflict("coupon_min_amount", "the amount is below the coupon's minimum")
	case c.MaxRedemptions != nil && c.Redeemed >= *c.MaxRedemptions:
		return conflict("coupon_exhausted", "the coupon has been used as many times as allowed")
	}
	return nil
}

// RedemptionStatus of a redemption.
type RedemptionStatus string

const (
	Redeemed RedemptionStatus = "redeemed"
	Released RedemptionStatus = "released" // given back (the invoice was voided, the order cancelled); frees the limits
)

// Redemption is one use of a coupon.
type Redemption struct {
	ID             uuid.UUID
	IdemKey        string
	CouponID       uuid.UUID
	CustomerRef    string
	Amount         money.Money // what the discount was computed on
	Discount       money.Money
	Status         RedemptionStatus
	SubjectType    string // what it was used for, e.g. "subscription"
	SubjectID      string
	ReleasedReason string
	CreatedAt      time.Time
}

// RedeemInput uses a coupon.
type RedeemInput struct {
	IdemKey     string // the same key returns the same redemption
	Code        string
	CustomerRef string
	Amount      money.Money
	SubjectType string
	SubjectID   string
}

func (in RedeemInput) validate() error {
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case strings.TrimSpace(in.CustomerRef) == "" || len(in.CustomerRef) > 200:
		return apperr.InvalidField("customer_ref", "required, at most 200 characters")
	case !in.Amount.Valid() || !in.Amount.IsPositive():
		return apperr.InvalidField("amount", "must be a positive amount")
	case len(in.SubjectType) > 60 || len(in.SubjectID) > 200:
		return apperr.InvalidField("subject", "type at most 60, id at most 200 characters")
	}
	return nil
}

// UpdateInput changes the limits of a coupon; nil keeps a value. Use ClearMax* to remove a limit.
type UpdateInput struct {
	Name              *string
	ValidFrom         *time.Time
	ValidUntil        *time.Time
	ClearValidFrom    bool
	ClearValidUntil   bool
	MinAmount         *int64
	MaxRedemptions    *int
	ClearMaxRedeem    bool
	MaxPerCustomer    *int
	ClearMaxPerCustom bool
	Active            *bool
	Actor             string
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
