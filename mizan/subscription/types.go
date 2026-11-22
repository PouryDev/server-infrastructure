// Package subscription is the recurring-billing context: plans, subscriptions that copy a plan's terms, and billing periods. A
// billing period is created exactly once (unique per subscription and number) and is turned into an invoice by the root
// package, so neither a retry nor a concurrent run can bill a period twice. Periods are counted in Jalali months and years.
package subscription

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Unit of a billing interval.
type Unit string

const (
	Day   Unit = "day"
	Month Unit = "month" // Jalali month
	Year  Unit = "year"  // Jalali year (12 months)
)

// Plan is what can be subscribed to. Changing its price affects new subscriptions only.
type Plan struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Price     money.Money // per interval, before VAT
	Unit      Unit
	Count     int // every Count units
	TrialDays int
	TaxBP     *int // VAT override in basis points; nil follows the invoice default (the accounting setting)
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PlanInput creates a plan.
type PlanInput struct {
	Code      string
	Name      string
	Price     money.Money
	Unit      Unit
	Count     int
	TrialDays int
	TaxBP     *int
	Actor     string
}

func (p Plan) validate() error {
	switch {
	case strings.TrimSpace(p.Code) == "" || len(p.Code) > 60:
		return apperr.InvalidField("code", "required, at most 60 characters")
	case strings.TrimSpace(p.Name) == "" || len(p.Name) > 120:
		return apperr.InvalidField("name", "required, at most 120 characters")
	case !p.Price.Valid() || !p.Price.IsPositive():
		return apperr.InvalidField("price", "must be a positive amount")
	case p.Unit != Day && p.Unit != Month && p.Unit != Year:
		return apperr.InvalidField("unit", "day, month or year")
	case p.Count < 1 || p.Count > 366:
		return apperr.InvalidField("count", "between 1 and 366")
	case p.TrialDays < 0 || p.TrialDays > 366:
		return apperr.InvalidField("trial_days", "between 0 and 366")
	case p.TaxBP != nil && (*p.TaxBP < 0 || *p.TaxBP > 10000):
		return apperr.InvalidField("tax_bp", "basis points between 0 and 10000")
	}
	return nil
}

// Status of a subscription.
//
//	trialing ─► active ◄─► paused
//	    └──────────┴──► canceled
type Status string

const (
	Trialing Status = "trialing"
	Active   Status = "active"
	Paused   Status = "paused"
	Canceled Status = "canceled"
)

// Buyer is the identity printed on the subscription's invoices.
type Buyer struct {
	Name         string `json:"name,omitempty"`
	NationalID   string `json:"national_id,omitempty"`
	EconomicCode string `json:"economic_code,omitempty"`
	PostalCode   string `json:"postal_code,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Address      string `json:"address,omitempty"`
}

// Subscription is a customer's subscription to a plan, with the plan's terms copied at the start.
type Subscription struct {
	ID          uuid.UUID
	IdemKey     string
	PlanID      uuid.UUID
	PlanName    string
	CustomerRef string
	Buyer       Buyer
	Status      Status
	Price       money.Money
	Unit        Unit
	Count       int
	TaxBP       *int
	// CouponCode, when set, is redeemed on the first billed period only.
	CouponCode string
	StartDate  time.Time
	// AnchorDate is the start of period number AnchorNo; every period start is computed from it, so month lengths never drift.
	AnchorDate        time.Time
	AnchorNo          int
	NextBill          *time.Time // the start of the next period to bill; nil once canceled
	PeriodsBilled     int
	CancelAtPeriodEnd bool
	CanceledAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// SubscribeInput starts a subscription.
type SubscribeInput struct {
	IdemKey     string // the same key returns the same subscription
	PlanID      uuid.UUID
	CustomerRef string
	Buyer       Buyer
	Start       time.Time // today when zero
	CouponCode  string
	Actor       string
}

func (in SubscribeInput) validate() error {
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case in.PlanID == uuid.Nil:
		return apperr.InvalidField("plan_id", "required")
	case strings.TrimSpace(in.CustomerRef) == "" || len(in.CustomerRef) > 200:
		return apperr.InvalidField("customer_ref", "required, at most 200 characters")
	case len(in.CouponCode) > 40:
		return apperr.InvalidField("coupon_code", "at most 40 characters")
	}
	return nil
}

// PeriodStatus of a billing period.
type PeriodStatus string

const (
	PeriodPending  PeriodStatus = "pending"  // created, the invoice is not made yet
	PeriodInvoiced PeriodStatus = "invoiced" // the invoice exists
)

// Period is one billed stretch of a subscription (Start and End are both inclusive days).
type Period struct {
	ID             uuid.UUID
	SubscriptionID uuid.UUID
	No             int
	Start          time.Time
	End            time.Time
	Amount         money.Money
	Status         PeriodStatus
	InvoiceID      *uuid.UUID
	Attempts       int
	LastError      string
	CreatedAt      time.Time
}

// PeriodStart is the first day of period number no. Months and years count in the Jalali calendar and keep the anchor's day
// of month, shortened for a shorter month (the 31st becomes the 30th or 29th). It is computed from the anchor each time.
func (s Subscription) PeriodStart(no int) time.Time {
	return Advance(s.AnchorDate, s.Unit, s.Count*(no-s.AnchorNo))
}

// Advance moves a day forward by n units (n may be zero).
func Advance(d time.Time, u Unit, n int) time.Time {
	switch u {
	case Day:
		return d.AddDate(0, 0, n)
	case Year:
		n *= 12
	}
	jd, err := jalali.FromTime(d)
	if err != nil {
		return d.AddDate(0, n, 0) // outside the Jalali tables: a Gregorian month is the best available
	}
	total := jd.Year*12 + (jd.Month - 1) + n
	y, m := total/12, total%12+1
	day := jd.Day
	if dim := jalali.DaysInMonth(y, m); day > dim {
		day = dim
	}
	t, err := jalali.Date{Year: y, Month: m, Day: day}.Time(time.UTC)
	if err != nil {
		return d.AddDate(0, n, 0)
	}
	return t
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
