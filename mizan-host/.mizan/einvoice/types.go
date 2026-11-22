// Package einvoice is the electronic-invoice context: it queues invoices for a tax-authority provider, sends them in
// batches, follows the provider's verdict and keeps the receipts. It is vendor neutral: a Provider adapter (see
// einvoice/moadian) does the talking, and the root package maps invoices of other contexts into the neutral Invoice.
package einvoice

import (
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Status of a document.
//
//	queued ─► sent ─► success
//	            └───► failed ─► (Requeue) queued
type Status string

const (
	Queued  Status = "queued"  // waiting to be sent (or a send attempt failed and will be retried)
	Sent    Status = "sent"    // the provider accepted the packet and gave a reference number; the verdict is pending
	Success Status = "success" // registered by the authority without errors
	Failed  Status = "failed"  // rejected by the authority: fix the data and Requeue
)

// Party is a seller or a buyer.
type Party struct {
	Name         string `json:"name,omitempty"`
	NationalID   string `json:"national_id,omitempty"`   // کد ملی / شناسه ملی
	EconomicCode string `json:"economic_code,omitempty"` // کد اقتصادی
	PostalCode   string `json:"postal_code,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Address      string `json:"address,omitempty"`
	// Kind is the person type for providers that need it: 1 natural, 2 legal, 3 civil partnership, 4 non-Iranian (Moadian "tob"). Zero: inferred from the id length when possible.
	Kind int `json:"kind,omitempty"`
	// PassportNo is the passport number of a non-resident buyer (airline tickets, currency sales): at most 9 characters.
	PassportNo string `json:"passport_no,omitempty"`
}

// Line is one invoice item, in minor units of the invoice currency.
type Line struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
	Discount    int64  `json:"discount"`
	Net         int64  `json:"net"`    // quantity * unit price - discount
	VATBP       int    `json:"vat_bp"` // basis points
	VAT         int64  `json:"vat"`
	Total       int64  `json:"total"` // net + VAT
	// Gold, jewellery and platinum (Moadian pattern 3): the wage, the seller's profit and the commission of the line, in rials; the VAT of their sum is 10%.
	WageFee      int64 `json:"wage_fee,omitempty"`
	SellerProfit int64 `json:"seller_profit,omitempty"`
	Commission   int64 `json:"commission,omitempty"`
	// Export (pattern 7): the customs figures of the line.
	Export *ExportLine `json:"export,omitempty"`
	// Currency sale (pattern 2): the foreign-currency figures of the line.
	FX *FXLine `json:"fx,omitempty"`
	// Other taxes and other legal levies of the line (odt/odr, olt/olr), as a subject and a rate in basis points of the amount after discount (of the wage, profit and commission
	// for gold, of the rial value for export).
	OtherTaxSubject  string `json:"other_tax_subject,omitempty"`
	OtherTaxBP       int    `json:"other_tax_bp,omitempty"`
	OtherLevySubject string `json:"other_levy_subject,omitempty"`
	OtherLevyBP      int    `json:"other_levy_bp,omitempty"`
	// PetroBaseE4 is the VAT base amount per unit (litre) of a petroleum product in ten-thousandths (vba): the VAT is then quantity x vba instead of a rate on the amount.
	PetroBaseE4 int64 `json:"petro_base_e4,omitempty"`
}

// FXLine holds the figures of a currency-sale line: the amount in foreign currency (4 decimals as ten-thousandths), the sale rate and the purchase rate (rial per unit, 8 decimals as
// hundred-millionths) and the VAT base in rials (sovat, between the margin and the whole amount).
type FXLine struct {
	Currency       string `json:"currency"`
	AmountE4       int64  `json:"amount_e4"`
	RateE8         int64  `json:"rate_e8"`
	PurchaseRateE8 int64  `json:"purchase_rate_e8"`
	VATBase        int64  `json:"vat_base"`
}

// ExportLine holds the customs figures of an export line: the currency (ISO 4217), the value in that currency in ten-thousandths (4 decimals), the rial value and the rial
// per foreign unit rate in hundred-millionths (8 decimals). Integers keep money out of floating point.
type ExportLine struct {
	Currency       string `json:"currency"`
	ForeignValueE4 int64  `json:"foreign_value_e4"`
	RialValue      int64  `json:"rial_value"`
	RateE8         int64  `json:"rate_e8"`
}

// Payment is one payment transaction of an invoice (Moadian payments array).
type Payment struct {
	Method     int       `json:"method"` // 1 cheque, 2 barter, 3 cash, 4 POS, 5 internet gateway, 6 card to card, 7 account transfer, 8 other
	Amount     int64     `json:"amount"`
	PaidAt     time.Time `json:"paid_at"`
	TraceNo    string    `json:"trace_no,omitempty"`
	CardNo     string    `json:"card_no,omitempty"`
	PayerID    string    `json:"payer_id,omitempty"`
	SwitchNo   string    `json:"switch_no,omitempty"`
	MerchantNo string    `json:"merchant_no,omitempty"`
	TerminalNo string    `json:"terminal_no,omitempty"`
}

// Customs are the customs references of an export invoice.
type Customs struct {
	CottageNo   string    `json:"cottage_no,omitempty"`
	CottageDate time.Time `json:"cottage_date,omitempty"`
	OfficeCode  string    `json:"office_code,omitempty"`
	LicenseNo   string    `json:"license_no,omitempty"`
}

// Invoice is the neutral content of an e-invoice. A provider adapter turns it into the authority's own format.
type Invoice struct {
	Number   string         `json:"number"`
	IssuedAt time.Time      `json:"issued_at"`
	Currency money.Currency `json:"currency"`
	Seller   Party          `json:"seller"`
	Buyer    Party          `json:"buyer"`
	Lines    []Line         `json:"lines"`
	Subtotal int64          `json:"subtotal"` // before discounts
	Discount int64          `json:"discount"`
	VAT      int64          `json:"vat"`
	Total    int64          `json:"total"`
	Memo     string         `json:"memo,omitempty"`
	// Provider details used by Moadian, all optional (zero = a plain original cash/credit sale). Pattern is the invoice pattern (1 sale, 3 gold, 4 contracting, 7 export);
	// Subject 1 original, 2 correction, 3 cancel, 4 return (2-4 need RefTaxID, the unique tax number of the invoice they refer to); Settlement 1 cash, 2 credit, 3 mixed (with
	// CashAmount, the pre-tax cash part).
	Pattern    int    `json:"pattern,omitempty"`
	Subject    int    `json:"subject,omitempty"`
	RefTaxID   string `json:"ref_taxid,omitempty"`
	Settlement int    `json:"settlement,omitempty"`
	CashAmount int64  `json:"cash_amount,omitempty"`
	ContractNo string `json:"contract_no,omitempty"`
	// Pattern 5: the bill id. Pattern 6: FlightType 1 domestic, 2 international, and the travel agency's economic code. Pattern 13: the insurance policy and endorsement ids.
	// Pattern 11: the commodity-exchange sale notice number and date. RegisteredAt is the registration time of a late invoice (Art. 9 of the store terminals law).
	BillID             string    `json:"bill_id,omitempty"`
	FlightType         int       `json:"flight_type,omitempty"`
	TravelAgencyCode   string    `json:"travel_agency_code,omitempty"`
	PolicyNo           string    `json:"policy_no,omitempty"`
	EndorsementNo      string    `json:"endorsement_no,omitempty"`
	ExchangeNoticeNo   string    `json:"exchange_notice_no,omitempty"`
	ExchangeNoticeDate time.Time `json:"exchange_notice_date,omitempty"`
	RegisteredAt       time.Time `json:"registered_at,omitempty"`
	Payments           []Payment `json:"payments,omitempty"`
	Customs            *Customs  `json:"customs,omitempty"`
}

// Issue is an error or warning the authority reported for a document.
type Issue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type,omitempty"` // ERROR or WARNING
}

// Document is one invoice on its way to the authority.
type Document struct {
	ID              uuid.UUID
	SourceType      string // e.g. "invoice"
	SourceID        string
	Status          Status
	TraceID         uuid.UUID // the provider echoes it back so a verdict is matched to the document
	FiscalID        string    // the taxpayer's fiscal-memory id the invoice is sent under
	Invoice         Invoice
	ReferenceNumber string
	Errors          []Issue
	Warnings        []Issue
	Attempts        int
	LastError       string
	SentAt          *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EnqueueInput queues an invoice. The same (SourceType, SourceID) always returns the same document.
type EnqueueInput struct {
	SourceType string
	SourceID   string
	Invoice    Invoice
	Actor      string
}

// Outcome of an inquiry.
type Outcome string

const (
	OutcomeSuccess  Outcome = "success"
	OutcomeFailed   Outcome = "failed"
	OutcomePending  Outcome = "pending"
	OutcomeNotFound Outcome = "not_found"
)
