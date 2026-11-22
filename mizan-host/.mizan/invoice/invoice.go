// Package invoice implements sales invoices (صورتحساب): items with discount and VAT computed in integers,
// an issue step that assigns the gap-free legal number and posts the accounting entry, payments that settle
// the invoice from the gateway or a wallet, refunds, and voiding.
package invoice

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Status of an invoice.
//
//	draft ─► issued ─► partially_paid ─► paid
//	  │         │
//	  └─────────┴─► void   (an issued invoice can be voided only while nothing is paid)
type Status string

const (
	Draft         Status = "draft"
	Issued        Status = "issued"
	PartiallyPaid Status = "partially_paid"
	Paid          Status = "paid"
	Void          Status = "void"
)

// Buyer holds the legal identity printed on the invoice. Everything is optional for a consumer sale; a
// business buyer needs the economic code for the VAT return and the e-invoice system.
type Buyer struct {
	Name         string `json:"name,omitempty"`
	NationalID   string `json:"national_id,omitempty"`   // کد ملی / شناسه ملی
	EconomicCode string `json:"economic_code,omitempty"` // کد اقتصادی
	PostalCode   string `json:"postal_code,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Address      string `json:"address,omitempty"`
}

// ItemInput is a line as the caller describes it.
type ItemInput struct {
	Description string
	Quantity    int64 // whole units; use a smaller unit for fractions (grams, not kilograms)
	UnitPrice   int64 // minor units of the invoice currency
	Discount    int64 // fixed discount of the line, minor units
	DiscountBP  int64 // percentage discount in basis points (100 = 1%), applied before Discount is subtracted
	TaxBP       *int  // VAT in basis points; nil = the service default
}

// Item is a computed line.
type Item struct {
	No          int
	Description string
	Quantity    int64
	UnitPrice   int64
	Discount    int64 // total discount of the line
	TaxBP       int
	Net         int64 // quantity * price - discount
	Tax         int64
	Total       int64 // net + tax
}

// Invoice is the aggregate.
type Invoice struct {
	ID            uuid.UUID
	IdemKey       string
	Number        *int64 // gap-free within the fiscal year, assigned when issued
	FiscalYearID  *int64
	Status        Status
	CustomerRef   string
	Buyer         Buyer
	Currency      money.Currency
	Items         []Item
	Subtotal      money.Money // sum of quantity * price, before discounts
	DiscountTotal money.Money
	TaxTotal      money.Money
	Total         money.Money
	Paid          money.Money
	IssueDate     *time.Time
	DueDate       *time.Time
	Memo          string
	IssueJournal  *uuid.UUID
	VoidJournal   *uuid.UUID
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Remaining is what is still owed.
func (i *Invoice) Remaining() money.Money {
	r, _ := i.Total.Sub(i.Paid)
	return r
}

// ComputeItem prices one line. All arithmetic is integer with half-up rounding of the percentage parts, and
// every multiplication is overflow-checked.
func ComputeItem(no int, in ItemInput, cur money.Currency, defaultTaxBP int) (Item, error) {
	field := func(f, m string) error { return apperr.InvalidField("items["+itoa(no-1)+"]."+f, m) }
	switch {
	case strings.TrimSpace(in.Description) == "":
		return Item{}, field("description", "required")
	case in.Quantity <= 0:
		return Item{}, field("quantity", "must be positive")
	case in.UnitPrice < 0:
		return Item{}, field("unit_price", "must not be negative")
	case in.Discount < 0 || in.DiscountBP < 0 || in.DiscountBP > 10_000:
		return Item{}, field("discount", "invalid discount")
	}
	tax := defaultTaxBP
	if in.TaxBP != nil {
		tax = *in.TaxBP
	}
	if tax < 0 || tax > 10_000 {
		return Item{}, field("tax_bp", "must be between 0 and 10000")
	}
	unit, err := money.New(in.UnitPrice, cur)
	if err != nil {
		return Item{}, err
	}
	gross, err := unit.MulRatio(in.Quantity, 1, money.HalfUp)
	if err != nil {
		return Item{}, field("quantity", "line total too large")
	}
	pct, err := gross.Percent(in.DiscountBP, money.HalfUp)
	if err != nil {
		return Item{}, field("discount", "invalid discount")
	}
	disc, err := pct.Add(money.Must(in.Discount, cur))
	if err != nil {
		return Item{}, field("discount", "invalid discount")
	}
	if c, _ := disc.Cmp(gross); c > 0 {
		return Item{}, field("discount", "discount exceeds the line amount")
	}
	net, _ := gross.Sub(disc)
	vat, err := net.Percent(int64(tax), money.HalfUp)
	if err != nil {
		return Item{}, field("tax_bp", "invalid tax")
	}
	total, err := net.Add(vat)
	if err != nil {
		return Item{}, field("quantity", "line total too large")
	}
	return Item{No: no, Description: in.Description, Quantity: in.Quantity, UnitPrice: in.UnitPrice, Discount: disc.Amount(), TaxBP: tax,
		Net: net.Amount(), Tax: vat.Amount(), Total: total.Amount()}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for ; n > 0; n /= 10 {
		i--
		b[i] = byte('0' + n%10)
	}
	return string(b[i:])
}

// Compute prices all lines and sets the totals.
func (i *Invoice) Compute(items []ItemInput, defaultTaxBP int) error {
	if len(items) == 0 || len(items) > 500 {
		return apperr.InvalidField("items", "between 1 and 500 items are required")
	}
	var sub, disc, tax, total int64
	i.Items = make([]Item, 0, len(items))
	for n, in := range items {
		it, err := ComputeItem(n+1, in, i.Currency, defaultTaxBP)
		if err != nil {
			return err
		}
		gross := it.Quantity * it.UnitPrice // safe: ComputeItem proved it fits
		for _, add := range []struct {
			dst *int64
			v   int64
		}{{&sub, gross}, {&disc, it.Discount}, {&tax, it.Tax}, {&total, it.Total}} {
			if *add.dst+add.v < *add.dst {
				return apperr.InvalidField("items", "invoice total too large")
			}
			*add.dst += add.v
		}
		i.Items = append(i.Items, it)
	}
	i.Subtotal, i.DiscountTotal = money.Must(sub, i.Currency), money.Must(disc, i.Currency)
	i.TaxTotal, i.Total = money.Must(tax, i.Currency), money.Must(total, i.Currency)
	return nil
}

// Net is the revenue part of the total (total minus VAT).
func (i *Invoice) Net() money.Money {
	n, _ := i.Total.Sub(i.TaxTotal)
	return n
}

// Payment is a movement of money against an invoice (negative for refunds).
type Payment struct {
	Source    string // "gateway" | "wallet"
	SourceRef string
	Amount    int64
	JournalID *uuid.UUID
	CreatedAt time.Time
}

// CreateInput creates a draft.
type CreateInput struct {
	IdemKey     string
	CustomerRef string
	Buyer       Buyer
	Currency    money.Currency
	Items       []ItemInput
	DueDate     *time.Time
	Memo        string
	Actor       string
}

// Validate checks the parts that need no pricing.
func (in CreateInput) Validate() error {
	var fe []apperr.FieldError
	if k := strings.TrimSpace(in.IdemKey); k == "" || len(k) > 180 {
		fe = append(fe, apperr.FieldError{Field: "idempotency_key", Message: "required, at most 180 characters"})
	}
	if strings.TrimSpace(in.CustomerRef) == "" || len(in.CustomerRef) > 200 {
		fe = append(fe, apperr.FieldError{Field: "customer_ref", Message: "required, at most 200 characters"})
	}
	if !in.Currency.Valid() {
		fe = append(fe, apperr.FieldError{Field: "currency", Message: "unsupported currency"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid invoice", fe...)
	}
	return nil
}

// Repository is the persistence port.
type Repository interface {
	// Insert stores a draft; with an existing idempotency key it returns the stored invoice and created=false.
	Insert(ctx context.Context, inv Invoice) (stored Invoice, created bool, err error)
	Get(ctx context.Context, id uuid.UUID, forUpdate bool) (Invoice, error)
	// Update writes status, number, dates, journals and paid total, guarded by Version.
	Update(ctx context.Context, inv *Invoice) error
	// AddPayment records the payment; inserted=false means the same (source, ref) was recorded before.
	AddPayment(ctx context.Context, id uuid.UUID, p Payment) (inserted bool, err error)
	Payments(ctx context.Context, id uuid.UUID) ([]Payment, error)
	List(ctx context.Context, p query.Params) (query.Page[Summary], error)
}

// Summary is an invoice row for listings.
type Summary struct {
	ID          uuid.UUID  `json:"id"`
	Number      *int64     `json:"number"`
	Status      Status     `json:"status"`
	CustomerRef string     `json:"customer_ref"`
	Currency    string     `json:"currency"`
	Total       int64      `json:"total"`
	Paid        int64      `json:"paid"`
	IssueDate   *time.Time `json:"issue_date"`
	DueDate     *time.Time `json:"due_date"`
	CreatedAt   time.Time  `json:"created_at"`
}
