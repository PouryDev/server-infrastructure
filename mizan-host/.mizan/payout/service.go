package payout

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

// Accounts are the ledger accounts payout posts to. The composition root creates them.
type Accounts struct {
	Receivable string // asset: what customers owe for marketplace sales
	Commission string // revenue: commission kept by the platform
	VATPayable string // liability: VAT on the commission
	Bank       string // default account of a payout batch
	// SellerRecoverable is an asset account: what sellers owe the platform after a sale was returned once they had been paid (clawbacks).
	SellerRecoverable string
	// SellerCoding is the chart-of-accounts code seller payable accounts are mapped to (optional).
	SellerCoding string
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
	// DefaultVATBP is the VAT rate (basis points) of terms that do not set their own: the module's accounting setting.
	DefaultVATBP int
}

// Service is the payout application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) note(ctx context.Context, actor, action, entity string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: entity, EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	if topic == "" {
		return nil
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: entity, AggregateID: id.String(), Payload: details})
}

// day validates the date of a money movement: today when empty, never in the future.
func (s *Service) day(t time.Time, field string) (time.Time, error) {
	today := dateOnly(s.books.Today())
	if t.IsZero() {
		return today, nil
	}
	if t = dateOnly(t); t.After(today) {
		return time.Time{}, apperr.InvalidField(field, "must not be in the future")
	}
	return t, nil
}

// ---- sellers ----

// AddSeller registers a seller and opens the liability account that holds the seller's money. The account cannot go
// negative, so a seller can never be paid more than the platform owes.
func (s *Service) AddSeller(ctx context.Context, in SellerInput) (Seller, error) {
	in, err := in.validate()
	if err != nil {
		return Seller{}, err
	}
	now := s.clock()
	id := uuid.New()
	sel := Seller{ID: id, Code: in.Code, Name: in.Name, Sheba: in.Sheba, Status: SellerActive, AccountCode: AccountCodeFor(id), CreatedAt: now, UpdatedAt: now}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertSeller(ctx, sel); err != nil {
			return err
		}
		if err := s.books.EnsureAccount(ctx, books.Account{Code: sel.AccountCode, Name: "Seller " + sel.Code, Type: books.Liability, Currency: s.cfg.Currency,
			NonNegative: true, Coding: s.cfg.Accounts.SellerCoding, OwnerKind: "seller", OwnerID: id.String()}); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "payout.seller_added", "seller", id, "payout.seller.added", map[string]any{"seller_id": id, "code": sel.Code})
	})
	return sel, err
}

// Seller returns a seller.
func (s *Service) Seller(ctx context.Context, id uuid.UUID) (Seller, error) {
	return s.repo.Seller(ctx, id, false)
}

// Sellers lists the sellers.
func (s *Service) Sellers(ctx context.Context) ([]Seller, error) { return s.repo.Sellers(ctx) }

// UpdateSeller changes the name and destination IBAN of a seller. Payout batches keep the IBAN they used.
func (s *Service) UpdateSeller(ctx context.Context, id uuid.UUID, name, sheba, actor string) (Seller, error) {
	in, err := SellerInput{Code: "-", Name: name, Sheba: sheba}.validate()
	if err != nil {
		return Seller{}, err
	}
	var out Seller
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		sel, err := s.repo.Seller(ctx, id, true)
		if err != nil {
			return err
		}
		sel.Name, sel.Sheba, sel.UpdatedAt = in.Name, in.Sheba, s.clock()
		if err := s.repo.UpdateSeller(ctx, &sel); err != nil {
			return err
		}
		out = sel
		return s.note(ctx, actor, "payout.seller_updated", "seller", id, "", map[string]any{"seller_id": id})
	})
	return out, err
}

// Block withholds the seller's payouts until Unblock (fraud, a dispute, a legal hold). Sales are still booked and
// returns still work: the books keep telling the truth.
func (s *Service) Block(ctx context.Context, id uuid.UUID, reason, actor string) (Seller, error) {
	if reason == "" || len(reason) > 500 {
		return Seller{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	return s.setStatus(ctx, id, SellerBlocked, reason, actor)
}

// Unblock lets the seller be paid again.
func (s *Service) Unblock(ctx context.Context, id uuid.UUID, actor string) (Seller, error) {
	return s.setStatus(ctx, id, SellerActive, "", actor)
}

func (s *Service) setStatus(ctx context.Context, id uuid.UUID, st SellerStatus, reason, actor string) (Seller, error) {
	var out Seller
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		sel, err := s.repo.Seller(ctx, id, true)
		if err != nil {
			return err
		}
		out = sel
		if sel.Status == st {
			return nil
		}
		sel.Status, sel.BlockReason, sel.UpdatedAt = st, reason, s.clock()
		if err := s.repo.UpdateSeller(ctx, &sel); err != nil {
			return err
		}
		out = sel
		return s.note(ctx, actor, "payout.seller_"+string(st), "seller", id, "payout.seller."+string(st), map[string]any{"seller_id": id, "reason": reason})
	})
	return out, err
}

// ---- terms ----

// AddTerms stores a new version of the commission rate and hold period. It is the only way they change.
func (s *Service) AddTerms(ctx context.Context, in TermsInput) (Terms, error) {
	t := in.version(uuid.New(), s.clock())
	if err := t.Validate(); err != nil {
		return t, err
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if t.Scope == ScopeSeller {
			id, _ := uuid.Parse(t.ScopeKey)
			if _, err := s.repo.Seller(ctx, id, false); err != nil {
				return err
			}
		}
		if err := s.repo.InsertTerms(ctx, t); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "payout.terms_added", "payout_terms", t.ID, "payout.terms.added", map[string]any{"terms_id": t.ID, "name": t.Name,
			"scope": t.Scope, "scope_key": t.ScopeKey, "rate_bp": t.RateBP, "vat_bp": t.VATBP, "hold_days": t.HoldDays, "effective_from": t.EffectiveFrom.Format("2006-01-02")})
	})
	return t, err
}

// Terms lists every version, ordered by scope and date.
func (s *Service) Terms(ctx context.Context) ([]Terms, error) { return s.repo.Terms(ctx) }

// TermsFor returns the version that would apply to a sale of the seller and category on a day.
func (s *Service) TermsFor(ctx context.Context, seller uuid.UUID, category string, day time.Time) (Terms, error) {
	if _, err := s.repo.Seller(ctx, seller, false); err != nil {
		return Terms{}, err
	}
	return s.termsFor(ctx, seller, category, day)
}

func (s *Service) termsFor(ctx context.Context, seller uuid.UUID, category string, day time.Time) (Terms, error) {
	all, err := s.repo.Terms(ctx)
	if err != nil {
		return Terms{}, err
	}
	t, ok := PickTerms(all, seller, category, day)
	if !ok {
		return Terms{}, apperr.New(apperr.KindConflict, "no_terms", "no commission terms are in force for this seller and day; add default terms first")
	}
	return t, nil
}

// DeleteTerms removes a version no sale used yet (to correct a mistake). Used versions are permanent.
func (s *Service) DeleteTerms(ctx context.Context, id uuid.UUID, actor string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.DeleteTerms(ctx, id); err != nil {
			return err
		}
		return s.note(ctx, actor, "payout.terms_deleted", "payout_terms", id, "", map[string]any{"terms_id": id})
	})
}

// ---- sales ----

// RecordSale books a sale:
//
//	Dr customer receivable (gross), Cr the seller's payable (gross - commission - VAT), Cr commission revenue, Cr VAT payable
//
// The commission is the gross at the rate of the terms in force on the sale date, rounded half up. The VAT is the terms' VAT rate
// on the commission (not on the sale), also rounded half up, and is charged to the seller; the seller gets the
// rest. When the delivery date is known the release day is delivery + the hold days of those terms, otherwise the
// sale waits for MarkDelivered. Idempotent per key.
func (s *Service) RecordSale(ctx context.Context, in SaleInput) (Sale, error) {
	if err := in.validate(); err != nil {
		return Sale{}, err
	}
	if in.Gross.Currency() != s.cfg.Currency {
		return Sale{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "sales are in "+string(s.cfg.Currency))
	}
	var out Sale
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		date, err := s.day(in.Date, "date")
		if err != nil {
			return err
		}
		seller, err := s.repo.Seller(ctx, in.SellerID, false)
		if err != nil {
			return err
		}
		terms, err := s.termsFor(ctx, seller.ID, in.Category, date)
		if err != nil {
			return err
		}
		commission, err := in.Gross.Percent(int64(terms.RateBP), money.HalfUp)
		if err != nil {
			return err
		}
		vatBP := s.cfg.DefaultVATBP
		if terms.VATBP != nil {
			vatBP = *terms.VATBP
		}
		vat, err := commission.Percent(int64(vatBP), money.HalfUp)
		if err != nil {
			return err
		}
		fees, err := commission.Add(vat)
		if err != nil {
			return err
		}
		net, err := in.Gross.Sub(fees)
		if err != nil {
			return err
		}
		if net.IsNegative() {
			return apperr.New(apperr.KindConflict, "fees_exceed_gross", "the commission and its VAT are more than the sale; check the terms")
		}
		now := s.clock()
		sale := Sale{ID: uuid.New(), IdemKey: in.IdemKey, SellerID: seller.ID, TermsID: terms.ID, Reference: in.Reference, Category: in.Category,
			Gross: in.Gross, Commission: commission, VAT: vat, Net: net, RateBP: terms.RateBP, VATBP: vatBP, HoldDays: terms.HoldDays, Status: SaleHeld, SaleDate: date,
			CreatedAt: now, UpdatedAt: now}
		if in.Delivered != nil {
			if err := sale.deliver(dateOnly(*in.Delivered), s.books.Today()); err != nil {
				return err
			}
		}
		stored, created, err := s.repo.InsertSale(ctx, sale)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.SellerID != sale.SellerID || stored.Gross != sale.Gross || stored.Reference != sale.Reference {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different sale")
			}
			return nil
		}
		lines := []books.Line{books.Dr(s.cfg.Accounts.Receivable, in.Gross)}
		if net.IsPositive() {
			lines = append(lines, books.Cr(seller.AccountCode, net))
		}
		if commission.IsPositive() {
			lines = append(lines, books.Cr(s.cfg.Accounts.Commission, commission))
		}
		if vat.IsPositive() {
			lines = append(lines, books.Cr(s.cfg.Accounts.VATPayable, vat))
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "sale:" + sale.ID.String() + ":record", Date: date, Description: "Marketplace sale " + in.Reference,
			RefType: "payout_sale", RefID: sale.ID.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		sale.Journal = &jid
		if err := s.repo.UpdateSale(ctx, &sale); err != nil {
			return err
		}
		out = sale
		return s.note(ctx, in.Actor, "payout.sale.recorded", "payout_sale", sale.ID, "payout.sale.recorded", map[string]any{"sale_id": sale.ID, "seller_id": seller.ID,
			"reference": sale.Reference, "gross": sale.Gross.Amount(), "commission": commission.Amount(), "vat": vat.Amount(), "net": net.Amount(), "currency": sale.Gross.Currency()})
	})
	return out, err
}

// deliver sets the delivery day and the release day. It is a method of Sale so both entry points share the rules.
func (sale *Sale) deliver(day, today time.Time) error {
	switch {
	case day.After(dateOnly(today)):
		return apperr.InvalidField("delivered_at", "must not be in the future")
	case day.Before(sale.SaleDate):
		return apperr.InvalidField("delivered_at", "must not be before the sale date")
	}
	release := day.AddDate(0, 0, sale.HoldDays)
	sale.Delivered, sale.ReleaseAt = &day, &release
	return nil
}

// MarkDelivered records the delivery of a sale; the seller's share is released hold-days later. Repeating it changes
// nothing.
func (s *Service) MarkDelivered(ctx context.Context, id uuid.UUID, day time.Time, actor string) (Sale, error) {
	var out Sale
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		sale, err := s.repo.Sale(ctx, id, true)
		if err != nil {
			return err
		}
		out = sale
		switch {
		case sale.Status == SaleReturned:
			return apperr.New(apperr.KindConflict, "sale_returned", "the sale was returned")
		case sale.Delivered != nil:
			return nil
		}
		d, err := s.day(day, "delivered_at")
		if err != nil {
			return err
		}
		if err := sale.deliver(d, s.books.Today()); err != nil {
			return err
		}
		sale.UpdatedAt = s.clock()
		if err := s.repo.UpdateSale(ctx, &sale); err != nil {
			return err
		}
		out = sale
		return s.note(ctx, actor, "payout.sale.delivered", "payout_sale", id, "payout.sale.delivered",
			map[string]any{"sale_id": id, "delivered_at": d.Format("2006-01-02"), "release_at": sale.ReleaseAt.Format("2006-01-02")})
	})
	return out, err
}

// ReleaseNow lets finance release a delivered sale's share before the hold ends. It never releases an undelivered sale.
func (s *Service) ReleaseNow(ctx context.Context, id uuid.UUID, actor string) (Sale, error) {
	var out Sale
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		sale, err := s.repo.Sale(ctx, id, true)
		if err != nil {
			return err
		}
		out = sale
		today := dateOnly(s.books.Today())
		switch {
		case sale.Status != SaleHeld:
			return apperr.New(apperr.KindConflict, "sale_not_held", "the sale is "+string(sale.Status))
		case sale.ReleaseAt == nil:
			return apperr.New(apperr.KindConflict, "not_delivered", "the sale has not been delivered")
		case !sale.ReleaseAt.After(today):
			return nil
		}
		sale.ReleaseAt, sale.UpdatedAt = &today, s.clock()
		if err := s.repo.UpdateSale(ctx, &sale); err != nil {
			return err
		}
		out = sale
		return s.note(ctx, actor, "payout.sale.released", "payout_sale", id, "payout.sale.released", map[string]any{"sale_id": id})
	})
	return out, err
}

// ReturnSale cancels a sale that was returned or refunded, with a reversing journal; the seller's share and the
// commission both disappear. It is refused once the seller was paid: that money needs a manual clawback.
func (s *Service) ReturnSale(ctx context.Context, id uuid.UUID, reason, actor string) (Sale, error) {
	if reason == "" || len(reason) > 500 {
		return Sale{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	var out Sale
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		sale, err := s.repo.Sale(ctx, id, true)
		if err != nil {
			return err
		}
		out = sale
		switch sale.Status {
		case SaleReturned:
			return nil
		case SalePaid:
			return apperr.New(apperr.KindConflict, "sale_paid_out", "the seller was already paid for this sale")
		}
		if sale.Journal == nil {
			return fmt.Errorf("payout: sale %s has no journal", id)
		}
		rj, err := s.books.Reverse(ctx, *sale.Journal, "sale:"+id.String()+":return", reason, actor)
		if err != nil {
			return err
		}
		sale.Status, sale.ReturnJrnl, sale.ReturnWhy, sale.UpdatedAt = SaleReturned, &rj, reason, s.clock()
		if err := s.repo.UpdateSale(ctx, &sale); err != nil {
			return err
		}
		out = sale
		return s.note(ctx, actor, "payout.sale.returned", "payout_sale", id, "payout.sale.returned",
			map[string]any{"sale_id": id, "seller_id": sale.SellerID, "gross": sale.Gross.Amount(), "reason": reason})
	})
	return out, err
}

// Sale returns a sale.
func (s *Service) Sale(ctx context.Context, id uuid.UUID) (Sale, error) {
	return s.repo.Sale(ctx, id, false)
}

// ListSales lists sales with filters, sorting and paging.
func (s *Service) ListSales(ctx context.Context, p query.Params) (query.Page[Sale], error) {
	return s.repo.ListSales(ctx, p)
}

// Balance reports what the platform owes a seller and how much of it is held, available or already paid.
func (s *Service) Balance(ctx context.Context, id uuid.UUID) (Balance, error) {
	sel, err := s.repo.Seller(ctx, id, false)
	if err != nil {
		return Balance{}, err
	}
	owed, err := s.books.Balance(ctx, sel.AccountCode)
	if err != nil {
		return Balance{}, err
	}
	t, err := s.repo.SellerTotals(ctx, id, s.books.Today())
	return Balance{Owed: owed, Totals: t}, err
}

// ---- payouts ----

// CreatePayout pays every active seller the released share of its sales in one batch (Dr each seller's payable, Cr the
// bank). Sales already paid, returned or still held are never included, so concurrent or repeated batches cannot pay
// twice. Idempotent per key: a retry returns the stored batch. Fails with "nothing_to_pay" when nothing is due.
func (s *Service) CreatePayout(ctx context.Context, in PayoutInput) (Batch, error) {
	if err := in.validate(); err != nil {
		return Batch{}, err
	}
	payFrom := in.PayFrom
	if payFrom == "" {
		payFrom = s.cfg.Accounts.Bank
	}
	var out Batch
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		date, err := s.day(in.Date, "date")
		if err != nil {
			return err
		}
		zero := money.Zero(s.cfg.Currency)
		b := Batch{ID: uuid.New(), IdemKey: in.IdemKey, PayFrom: payFrom, Date: date, Total: zero, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertBatch(ctx, b)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.PayFrom != payFrom {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different payout")
			}
			return nil
		}
		sales, err := s.repo.DueSales(ctx, date, in.SellerIDs)
		if err != nil {
			return err
		}
		if len(sales) == 0 {
			return apperr.New(apperr.KindConflict, "nothing_to_pay", "no released sale of an active seller is waiting for payment")
		}
		items, ids, sellers, err := s.group(ctx, sales, zero)
		if err != nil {
			return err
		}
		lines, total := make([]books.Line, 0, len(items)+1), zero
		for _, it := range items {
			lines = append(lines, books.Dr(sellers[it.SellerID].AccountCode, it.Amount))
			if total, err = total.Add(it.Amount); err != nil {
				return err
			}
		}
		lines = append(lines, books.Cr(payFrom, total))
		jid, err := s.books.Post(ctx, books.Entry{Key: "payout:" + b.ID.String(), Date: date, Description: "Seller payout", RefType: "payout_batch", RefID: b.ID.String(),
			Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		if err := s.repo.MarkSalesPaid(ctx, ids, b.ID); err != nil {
			return err
		}
		b.Total, b.SaleCount, b.Journal, b.Items = total, len(ids), &jid, items
		if err := s.repo.FinishBatch(ctx, &b); err != nil {
			return err
		}
		out = b
		return s.note(ctx, in.Actor, "payout.batch.paid", "payout_batch", b.ID, "payout.batch.paid", map[string]any{"batch_id": b.ID, "total": total.Amount(),
			"currency": total.Currency(), "sellers": len(items), "sales": len(ids)})
	})
	return out, err
}

// group sums the sales per seller (they arrive ordered by seller) and snapshots each destination.
func (s *Service) group(ctx context.Context, sales []Sale, zero money.Money) ([]BatchItem, []uuid.UUID, map[uuid.UUID]Seller, error) {
	var items []BatchItem
	ids := make([]uuid.UUID, 0, len(sales))
	var sellerIDs []uuid.UUID
	for _, sale := range sales {
		ids = append(ids, sale.ID)
		if n := len(items); n == 0 || items[n-1].SellerID != sale.SellerID {
			items = append(items, BatchItem{SellerID: sale.SellerID, Amount: zero})
			sellerIDs = append(sellerIDs, sale.SellerID)
		}
		it := &items[len(items)-1]
		sum, err := it.Amount.Add(sale.Net)
		if err != nil {
			return nil, nil, nil, err
		}
		it.Amount, it.SaleCount = sum, it.SaleCount+1
	}
	sellers, err := s.repo.SellersByID(ctx, sellerIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	for i := range items {
		items[i].Sheba = sellers[items[i].SellerID].Sheba
	}
	return items, ids, sellers, nil
}

// Payout returns a batch with its items.
func (s *Service) Payout(ctx context.Context, id uuid.UUID) (Batch, error) {
	return s.repo.Batch(ctx, id)
}

// Payouts lists the batches, newest first.
func (s *Service) Payouts(ctx context.Context) ([]Batch, error) { return s.repo.Batches(ctx) }
