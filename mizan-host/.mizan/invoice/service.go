package invoice

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Transactor runs fn in one transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Accounts are the ledger accounts an invoice posts to.
type Accounts struct {
	Receivable string // Dr when issued
	Revenue    string // Cr net amount
	VATPayable string // Cr VAT
}

// Config of the invoice service.
type Config struct {
	DefaultTaxBP int // VAT in basis points applied to items that do not say otherwise (1000 = 10%)
	Accounts     Accounts
}

// Service is the invoice application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config) *Service {
	return &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
}

// Create prices and stores a draft. Idempotent per key.
func (s *Service) Create(ctx context.Context, in CreateInput) (Invoice, error) {
	if err := in.Validate(); err != nil {
		return Invoice{}, err
	}
	now := s.clock()
	inv := Invoice{ID: uuid.New(), IdemKey: in.IdemKey, Status: Draft, CustomerRef: in.CustomerRef, Buyer: in.Buyer, Currency: in.Currency,
		DueDate: in.DueDate, Memo: in.Memo, Paid: money.Zero(in.Currency), Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := inv.Compute(in.Items, s.cfg.DefaultTaxBP); err != nil {
		return Invoice{}, err
	}
	var out Invoice
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.Insert(ctx, inv)
		if err != nil {
			return err
		}
		if !created && (stored.Total != inv.Total || stored.CustomerRef != inv.CustomerRef) {
			return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different invoice")
		}
		out = stored
		if created {
			return s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "invoice.create", EntityType: "invoice", EntityID: inv.ID.String(),
				Details: map[string]any{"total": inv.Total.Amount(), "customer": inv.CustomerRef}})
		}
		return nil
	})
	return out, err
}

// Get returns an invoice with its items.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Invoice, error) {
	return s.repo.Get(ctx, id, false)
}

// Payments lists the money movements of an invoice.
func (s *Service) Payments(ctx context.Context, id uuid.UUID) ([]Payment, error) {
	return s.repo.Payments(ctx, id)
}

// List lists invoices with the client's filters, sort and pagination.
func (s *Service) List(ctx context.Context, p query.Params) (query.Page[Summary], error) {
	return s.repo.List(ctx, p)
}

// Issue makes a draft official: it gets the next legal number of the fiscal year and the sale is booked
// (Dr receivable; Cr revenue and VAT payable). Issuing twice returns the issued invoice.
func (s *Service) Issue(ctx context.Context, id uuid.UUID, actor string) (Invoice, error) {
	var out Invoice
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		inv, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = inv
		if inv.Status != Draft {
			if inv.Status == Void {
				return apperr.New(apperr.KindConflict, "invoice_void", "the invoice is void")
			}
			return nil
		}
		if !inv.Total.IsPositive() {
			return apperr.New(apperr.KindInvalid, "empty_invoice", "an invoice with a zero total cannot be issued")
		}
		date := s.books.Today()
		fy, number, err := s.books.NextNumber(ctx, "invoice", date)
		if err != nil {
			return err
		}
		a := s.cfg.Accounts
		lines := []books.Line{books.Dr(a.Receivable, inv.Total), books.Cr(a.Revenue, inv.Net())}
		if inv.TaxTotal.IsPositive() {
			lines = append(lines, books.Cr(a.VATPayable, inv.TaxTotal))
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "invoice:" + inv.ID.String() + ":issue", Date: date, Description: fmt.Sprintf("Invoice %d", number),
			RefType: "invoice", RefID: inv.ID.String(), Actor: actor, Lines: lines})
		if err != nil {
			return err
		}
		inv.Status, inv.Number, inv.FiscalYearID, inv.IssueDate, inv.IssueJournal = Issued, &number, &fy, &date, &jid
		if err := s.repo.Update(ctx, &inv); err != nil {
			return err
		}
		out = inv
		if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "invoice.issue", EntityType: "invoice", EntityID: inv.ID.String(),
			Details: map[string]any{"number": number, "journal": jid}}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "invoice.issued", AggregateType: "invoice", AggregateID: inv.ID.String(), Payload: map[string]any{
			"invoice_id": inv.ID, "number": number, "customer_ref": inv.CustomerRef, "total": inv.Total.Amount(), "currency": inv.Currency, "tax": inv.TaxTotal.Amount()}})
	})
	return out, err
}

// PayInput applies money to an invoice.
type PayInput struct {
	InvoiceID    uuid.UUID
	Source       string // "gateway" | "wallet"
	SourceRef    string // payment id, or the wallet payment's idempotency key; unique per Source
	Amount       money.Money
	DebitAccount string // where the money came in: the gateway clearing account, or the wallet's account
	Actor        string
}

// ApplyPayment books a payment (Dr DebitAccount; Cr receivable) and settles the invoice. It is idempotent
// per (Source, SourceRef) and joins the caller's transaction. Paying more than is owed is refused.
func (s *Service) ApplyPayment(ctx context.Context, in PayInput) (uuid.UUID, error) {
	if in.SourceRef == "" || (in.Source != "gateway" && in.Source != "wallet") {
		return uuid.Nil, apperr.Invalid("a payment needs a source and a source reference")
	}
	if !in.Amount.Valid() || !in.Amount.IsPositive() {
		return uuid.Nil, apperr.InvalidField("amount", "must be a positive amount")
	}
	var jid uuid.UUID
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		inv, err := s.repo.Get(ctx, in.InvoiceID, true)
		if err != nil {
			return err
		}
		if inv.Status != Issued && inv.Status != PartiallyPaid {
			return apperr.New(apperr.KindConflict, "invoice_not_payable", "the invoice is "+string(inv.Status))
		}
		if in.Amount.Currency() != inv.Currency {
			return apperr.New(apperr.KindInvalid, "currency_mismatch", "the invoice is in "+string(inv.Currency))
		}
		if c, _ := in.Amount.Cmp(inv.Remaining()); c > 0 {
			return apperr.New(apperr.KindConflict, "overpayment", "the payment is larger than the amount still owed")
		}
		jid, err = s.books.Post(ctx, books.Entry{Key: "invoice:" + inv.ID.String() + ":pay:" + in.Source + ":" + in.SourceRef, Date: s.books.Today(),
			Description: fmt.Sprintf("Payment of invoice %d", *inv.Number), RefType: "invoice", RefID: inv.ID.String(), Actor: in.Actor,
			Lines: []books.Line{books.Dr(in.DebitAccount, in.Amount), books.Cr(s.cfg.Accounts.Receivable, in.Amount)}})
		if err != nil {
			return err
		}
		inserted, err := s.repo.AddPayment(ctx, inv.ID, Payment{Source: in.Source, SourceRef: in.SourceRef, Amount: in.Amount.Amount(), JournalID: &jid, CreatedAt: s.clock()})
		if err != nil {
			return err
		}
		if !inserted {
			return nil // a retry: the books and the invoice already reflect it
		}
		inv.Paid, _ = inv.Paid.Add(in.Amount)
		inv.Status = PartiallyPaid
		if inv.Paid == inv.Total {
			inv.Status = Paid
		}
		if err := s.repo.Update(ctx, &inv); err != nil {
			return err
		}
		topic := "invoice.payment_received"
		if inv.Status == Paid {
			topic = "invoice.paid"
		}
		return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "invoice", AggregateID: inv.ID.String(), Payload: map[string]any{
			"invoice_id": inv.ID, "number": inv.Number, "amount": in.Amount.Amount(), "paid": inv.Paid.Amount(), "total": inv.Total.Amount(), "source": in.Source, "customer_ref": inv.CustomerRef}})
	})
	return jid, err
}

// RefundInput returns money for an invoice that was paid.
type RefundInput struct {
	InvoiceID     uuid.UUID
	SourceRef     string
	Amount        money.Money
	CreditAccount string // where the money goes out: the gateway clearing account
	Actor         string
}

// ApplyRefund books a refund (Dr receivable; Cr CreditAccount): the invoice owes again by that amount.
// Idempotent per SourceRef.
func (s *Service) ApplyRefund(ctx context.Context, in RefundInput) (uuid.UUID, error) {
	var jid uuid.UUID
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		inv, err := s.repo.Get(ctx, in.InvoiceID, true)
		if err != nil {
			return err
		}
		if inv.Status == Draft || inv.Status == Void {
			return apperr.New(apperr.KindConflict, "invoice_not_refundable", "the invoice is "+string(inv.Status))
		}
		if c, _ := in.Amount.Cmp(inv.Paid); c > 0 || !in.Amount.IsPositive() {
			return apperr.New(apperr.KindConflict, "refund_exceeds_paid", "the refund is larger than what was paid")
		}
		jid, err = s.books.Post(ctx, books.Entry{Key: "invoice:" + inv.ID.String() + ":refund:" + in.SourceRef, Date: s.books.Today(),
			Description: fmt.Sprintf("Refund on invoice %d", *inv.Number), RefType: "invoice", RefID: inv.ID.String(), Actor: in.Actor,
			Lines: []books.Line{books.Dr(s.cfg.Accounts.Receivable, in.Amount), books.Cr(in.CreditAccount, in.Amount)}})
		if err != nil {
			return err
		}
		inserted, err := s.repo.AddPayment(ctx, inv.ID, Payment{Source: "gateway", SourceRef: "refund:" + in.SourceRef, Amount: -in.Amount.Amount(), JournalID: &jid, CreatedAt: s.clock()})
		if err != nil || !inserted {
			return err
		}
		inv.Paid, _ = inv.Paid.Sub(in.Amount)
		switch {
		case inv.Paid.IsZero():
			inv.Status = Issued
		default:
			inv.Status = PartiallyPaid
		}
		if err := s.repo.Update(ctx, &inv); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "invoice.refunded", AggregateType: "invoice", AggregateID: inv.ID.String(), Payload: map[string]any{
			"invoice_id": inv.ID, "amount": in.Amount.Amount(), "paid": inv.Paid.Amount()}})
	})
	return jid, err
}

// Void cancels an invoice. A draft is simply marked; an issued invoice with nothing paid is reversed in the
// books. A paid or partially paid invoice must be refunded first.
func (s *Service) Void(ctx context.Context, id uuid.UUID, reason, actor string) (Invoice, error) {
	var out Invoice
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		inv, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = inv
		switch {
		case inv.Status == Void:
			return nil
		case inv.Status == Paid || inv.Status == PartiallyPaid || !inv.Paid.IsZero():
			return apperr.New(apperr.KindConflict, "invoice_has_payments", "refund the payments before voiding the invoice")
		}
		if inv.Status == Issued && inv.IssueJournal != nil {
			rj, err := s.books.Reverse(ctx, *inv.IssueJournal, "invoice:"+inv.ID.String()+":void", reason, actor)
			if err != nil {
				return err
			}
			inv.VoidJournal = &rj
		}
		inv.Status = Void
		if err := s.repo.Update(ctx, &inv); err != nil {
			return err
		}
		out = inv
		if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "invoice.void", EntityType: "invoice", EntityID: inv.ID.String(), Details: map[string]any{"reason": reason}}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "invoice.voided", AggregateType: "invoice", AggregateID: inv.ID.String(),
			Payload: map[string]any{"invoice_id": inv.ID, "number": inv.Number, "reason": reason}})
	})
	return out, err
}
