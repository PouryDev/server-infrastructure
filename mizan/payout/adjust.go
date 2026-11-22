package payout

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Clawback is what a seller owes the platform because money was paid out for a sale that was returned afterwards (in full or in part).
type Clawback struct {
	ID        uuid.UUID
	SaleID    uuid.UUID
	SellerID  uuid.UUID
	BatchID   *uuid.UUID
	Ref       string
	Amount    money.Money
	Recovered money.Money
	Reason    string
	CreatedAt time.Time
}

// Outstanding is what is still owed.
func (c Clawback) Outstanding() int64 { return c.Amount.Amount() - c.Recovered.Amount() }

// Adjustment is one recorded change of a sale's figures (a partial return).
type Adjustment struct {
	Key        string
	SaleID     uuid.UUID
	Gross      int64 // the gross returned
	Commission int64 // the commission given back
	VAT        int64
	Net        int64 // the seller's share taken back
	Reason     string
	Journal    *uuid.UUID
}

// Recovery is money taken back from a seller against a clawback: paid in cash, or offset against what the platform still owes the seller.
type Recovery struct {
	Key        string
	ClawbackID uuid.UUID
	Method     string // "cash" or "offset"
	Amount     int64
	Journal    *uuid.UUID
}

// AdjustmentRepository is the persistence port of returns after booking, clawbacks and rejected transfers (implemented by the same adapter as Repository).
type AdjustmentRepository interface {
	// InsertAdjustment stores a partial return; a taken key returns created=false.
	InsertAdjustment(ctx context.Context, a Adjustment) (created bool, err error)
	AdjustmentByKey(ctx context.Context, key string) (Adjustment, bool, error)
	// UpdateSaleFigures stores the current gross, commission, VAT and net of a sale.
	UpdateSaleFigures(ctx context.Context, s *Sale) error
	// InsertClawback stores a clawback; a taken (sale, ref) returns the stored one with created=false.
	InsertClawback(ctx context.Context, c Clawback) (stored Clawback, created bool, err error)
	Clawback(ctx context.Context, id uuid.UUID, forUpdate bool) (Clawback, error)
	Clawbacks(ctx context.Context, seller uuid.UUID, openOnly bool) ([]Clawback, error)
	AddRecovered(ctx context.Context, id uuid.UUID, amount int64) error
	InsertRecovery(ctx context.Context, r Recovery) (created bool, err error)
	RecoveryByKey(ctx context.Context, key string) (Recovery, bool, error)
	// PaidSalesOf locks the paid sales of a batch that belong to a seller.
	PaidSalesOf(ctx context.Context, batch, seller uuid.UUID) ([]Sale, error)
	// ReopenSales puts paid sales back to held, outside any batch.
	ReopenSales(ctx context.Context, ids []uuid.UUID) error
	RejectBatchItem(ctx context.Context, batch, seller uuid.UUID, reason string, journal uuid.UUID) error
}

func (s *Service) adj() (AdjustmentRepository, error) {
	a, ok := s.repo.(AdjustmentRepository)
	if !ok {
		return nil, apperr.New(apperr.KindConflict, "no_adjustments", "adjustments are not available with this repository")
	}
	return a, nil
}

// split computes the commission, VAT and net of a gross at the sale's own rates (the same arithmetic as RecordSale).
func (sale Sale) split(gross money.Money) (commission, vat, net money.Money, err error) {
	if commission, err = gross.Percent(int64(sale.RateBP), money.HalfUp); err != nil {
		return
	}
	if vat, err = commission.Percent(int64(sale.VATBP), money.HalfUp); err != nil {
		return
	}
	fees, err := commission.Add(vat)
	if err != nil {
		return
	}
	net, err = gross.Sub(fees)
	return
}

// ReturnInput returns part of a sale.
type ReturnInput struct {
	IdemKey string
	Gross   money.Money // the part of the gross that goes back to the customer
	Reason  string
	Actor   string
}

// PartialReturn gives the customer back part of a booked sale. The commission, its VAT and the seller's share are recomputed on the reduced gross at the sale's own rates and the
// difference is booked: Dr commission, Dr VAT, Dr the seller's payable (Dr the sellers-recoverable account when the seller was ALREADY paid: that part becomes a clawback), Cr the
// customer receivable. Idempotent by key. A return of the whole gross is ReturnSale (held) or ReturnPaidSale (paid).
func (s *Service) PartialReturn(ctx context.Context, id uuid.UUID, in ReturnInput) (Sale, error) {
	ar, err := s.adj()
	if err != nil {
		return Sale{}, err
	}
	key, reason := strings.TrimSpace(in.IdemKey), strings.TrimSpace(in.Reason)
	if key == "" || len(key) > 200 {
		return Sale{}, apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	}
	if reason == "" || len(reason) > 500 {
		return Sale{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	if in.Gross.Currency() != s.cfg.Currency || !in.Gross.IsPositive() {
		return Sale{}, apperr.InvalidField("gross", "must be a positive amount in "+string(s.cfg.Currency))
	}
	var out Sale
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		sale, err := s.repo.Sale(ctx, id, true)
		if err != nil {
			return err
		}
		out = sale
		if prior, ok, err := ar.AdjustmentByKey(ctx, key); err != nil {
			return err
		} else if ok {
			if prior.SaleID != id {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different sale")
			}
			return nil // a retry
		}
		if sale.Status == SaleReturned {
			return apperr.Conflict("the sale is already returned")
		}
		left, err := sale.Gross.Sub(in.Gross)
		if err != nil || !left.IsPositive() {
			return apperr.InvalidField("gross", "must be below the sale's gross: use the full return for the whole sale")
		}
		c2, v2, n2, err := sale.split(left)
		if err != nil {
			return err
		}
		dc, dv, dn := sale.Commission.Amount()-c2.Amount(), sale.VAT.Amount()-v2.Amount(), sale.Net.Amount()-n2.Amount()
		if dc < 0 || dv < 0 || dn < 0 || n2.IsNegative() {
			return apperr.New(apperr.KindConflict, "return_not_computable", "the return does not fit the sale's commission terms: reverse the sale instead")
		}
		seller, err := s.repo.Seller(ctx, sale.SellerID, false)
		if err != nil {
			return err
		}
		netAccount := seller.AccountCode
		if sale.Status == SalePaid {
			netAccount = s.cfg.Accounts.SellerRecoverable
			if netAccount == "" {
				return apperr.New(apperr.KindConflict, "no_recoverable_account", "no sellers-recoverable account configured")
			}
		}
		var lines []books.Line
		for _, l := range []struct {
			acct string
			v    int64
		}{{s.cfg.Accounts.Commission, dc}, {s.cfg.Accounts.VATPayable, dv}, {netAccount, dn}} {
			if l.v > 0 {
				lines = append(lines, books.Dr(l.acct, money.Must(l.v, s.cfg.Currency)))
			}
		}
		lines = append(lines, books.Cr(s.cfg.Accounts.Receivable, in.Gross))
		jid, err := s.books.Post(ctx, books.Entry{Key: "sale:" + id.String() + ":adj:" + key, Date: dateOnly(s.books.Today()), Description: "Partial return of marketplace sale " + sale.Reference,
			RefType: "payout_sale", RefID: id.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		if _, err := ar.InsertAdjustment(ctx, Adjustment{Key: key, SaleID: id, Gross: in.Gross.Amount(), Commission: dc, VAT: dv, Net: dn, Reason: reason, Journal: &jid}); err != nil {
			return err
		}
		if sale.Status == SalePaid && dn > 0 {
			if _, _, err := ar.InsertClawback(ctx, Clawback{ID: uuid.New(), SaleID: id, SellerID: sale.SellerID, BatchID: sale.BatchID, Ref: "adj:" + key,
				Amount: money.Must(dn, s.cfg.Currency), Recovered: money.Zero(s.cfg.Currency), Reason: reason, CreatedAt: s.clock()}); err != nil {
				return err
			}
		}
		sale.Gross, sale.Commission, sale.VAT, sale.Net, sale.UpdatedAt = left, c2, v2, n2, s.clock()
		if err := ar.UpdateSaleFigures(ctx, &sale); err != nil {
			return err
		}
		out = sale
		return s.note(ctx, in.Actor, "payout.sale.partially_returned", "payout_sale", id, "payout.sale.partially_returned",
			map[string]any{"sale_id": id, "seller_id": sale.SellerID, "gross_returned": in.Gross.Amount(), "commission_back": dc, "vat_back": dv, "net_back": dn, "paid_out": sale.Status == SalePaid, "reason": reason})
	})
	return out, err
}

// ReturnPaidSale returns a sale whose seller was ALREADY paid: the sale is reversed (Dr commission, Dr VAT, Dr the sellers-recoverable account for the seller's share, Cr the
// customer receivable) and the seller's share becomes a clawback to recover (RecoverClawback). Idempotent: a repeat returns the same clawback.
func (s *Service) ReturnPaidSale(ctx context.Context, id uuid.UUID, reason, actor string) (Clawback, error) {
	ar, err := s.adj()
	if err != nil {
		return Clawback{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return Clawback{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	if s.cfg.Accounts.SellerRecoverable == "" {
		return Clawback{}, apperr.New(apperr.KindConflict, "no_recoverable_account", "no sellers-recoverable account configured")
	}
	var out Clawback
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		sale, err := s.repo.Sale(ctx, id, true)
		if err != nil {
			return err
		}
		if sale.Status == SaleReturned {
			cbs, err := ar.Clawbacks(ctx, sale.SellerID, false)
			if err != nil {
				return err
			}
			for _, c := range cbs {
				if c.SaleID == id && c.Ref == "full" {
					out = c
					return nil
				}
			}
			return apperr.Conflict("the sale was returned before it was paid: nothing to claw back")
		}
		if sale.Status != SalePaid {
			return apperr.New(apperr.KindConflict, "sale_not_paid", "the seller was not paid for this sale: use the ordinary return")
		}
		lines := []books.Line{}
		for _, l := range []struct {
			acct string
			v    money.Money
		}{{s.cfg.Accounts.Commission, sale.Commission}, {s.cfg.Accounts.VATPayable, sale.VAT}, {s.cfg.Accounts.SellerRecoverable, sale.Net}} {
			if l.v.IsPositive() {
				lines = append(lines, books.Dr(l.acct, l.v))
			}
		}
		lines = append(lines, books.Cr(s.cfg.Accounts.Receivable, sale.Gross))
		jid, err := s.books.Post(ctx, books.Entry{Key: "sale:" + id.String() + ":return_paid", Date: dateOnly(s.books.Today()), Description: "Return of paid marketplace sale " + sale.Reference,
			RefType: "payout_sale", RefID: id.String(), Actor: actor, Lines: lines})
		if err != nil {
			return err
		}
		batch := sale.BatchID
		sale.Status, sale.ReturnJrnl, sale.ReturnWhy, sale.BatchID, sale.UpdatedAt = SaleReturned, &jid, reason, nil, s.clock()
		if err := s.repo.UpdateSale(ctx, &sale); err != nil {
			return err
		}
		if sale.Net.IsPositive() {
			cb := Clawback{ID: uuid.New(), SaleID: id, SellerID: sale.SellerID, BatchID: batch, Ref: "full", Amount: sale.Net, Recovered: money.Zero(s.cfg.Currency), Reason: reason, CreatedAt: s.clock()}
			if out, _, err = ar.InsertClawback(ctx, cb); err != nil {
				return err
			}
		}
		return s.note(ctx, actor, "payout.sale.returned_after_payout", "payout_sale", id, "payout.sale.returned_after_payout",
			map[string]any{"sale_id": id, "seller_id": sale.SellerID, "gross": sale.Gross.Amount(), "claw_back": sale.Net.Amount(), "reason": reason})
	})
	return out, err
}

// RecoverInput takes money back from a seller against a clawback.
type RecoverInput struct {
	IdemKey string
	Amount  money.Money
	// Offset settles the clawback against what the platform still owes the seller (Dr the seller's payable, Cr sellers-recoverable) instead of cash coming in (Dr the bank account,
	// Cr sellers-recoverable). The seller's payable can never go below zero: an offset above what is owed is refused.
	Offset bool
	// Into is the account the cash lands in (default the payout bank account); ignored for an offset.
	Into  string
	Date  time.Time
	Actor string
}

// RecoverClawback records money taken back from a seller. Idempotent by key; row-locked, so concurrent recoveries never exceed the amount owed.
func (s *Service) RecoverClawback(ctx context.Context, id uuid.UUID, in RecoverInput) (Clawback, error) {
	ar, err := s.adj()
	if err != nil {
		return Clawback{}, err
	}
	key := strings.TrimSpace(in.IdemKey)
	if key == "" || len(key) > 200 {
		return Clawback{}, apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	}
	if in.Amount.Currency() != s.cfg.Currency || !in.Amount.IsPositive() {
		return Clawback{}, apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date, "date")
	if err != nil {
		return Clawback{}, err
	}
	var out Clawback
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		cb, err := ar.Clawback(ctx, id, true)
		if err != nil {
			return err
		}
		out = cb
		if prior, ok, err := ar.RecoveryByKey(ctx, key); err != nil {
			return err
		} else if ok {
			if prior.ClawbackID != id {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different clawback")
			}
			return nil
		}
		if in.Amount.Amount() > cb.Outstanding() {
			return apperr.New(apperr.KindConflict, "more_than_owed", fmt.Sprintf("the clawback has %d left to recover", cb.Outstanding()))
		}
		seller, err := s.repo.Seller(ctx, cb.SellerID, false)
		if err != nil {
			return err
		}
		from, method, desc := in.Into, "cash", "Clawback recovered in cash from seller "+seller.Code
		if from == "" {
			from = s.cfg.Accounts.Bank
		}
		if in.Offset {
			from, method, desc = seller.AccountCode, "offset", "Clawback offset against the payable to seller "+seller.Code
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "clawback:" + id.String() + ":" + key, Date: date, Description: desc, RefType: "payout_clawback", RefID: id.String(), Actor: in.Actor,
			Lines: []books.Line{books.Dr(from, in.Amount), books.Cr(s.cfg.Accounts.SellerRecoverable, in.Amount)}})
		if err != nil {
			return err
		}
		if _, err := ar.InsertRecovery(ctx, Recovery{Key: key, ClawbackID: id, Method: method, Amount: in.Amount.Amount(), Journal: &jid}); err != nil {
			return err
		}
		if err := ar.AddRecovered(ctx, id, in.Amount.Amount()); err != nil {
			return err
		}
		rec, err := s.recoveredSum(cb, in.Amount)
		if err != nil {
			return err
		}
		cb.Recovered = rec
		out = cb
		return s.note(ctx, in.Actor, "payout.clawback.recovered", "payout_clawback", id, "payout.clawback.recovered", map[string]any{"clawback_id": id, "seller_id": cb.SellerID, "amount": in.Amount.Amount(), "method": method})
	})
	return out, err
}

func (s *Service) recoveredSum(cb Clawback, add money.Money) (money.Money, error) {
	return cb.Recovered.Add(add)
}

// Clawbacks lists a seller's clawbacks (only the unpaid ones when openOnly).
func (s *Service) Clawbacks(ctx context.Context, seller uuid.UUID, openOnly bool) ([]Clawback, error) {
	ar, err := s.adj()
	if err != nil {
		return nil, err
	}
	return ar.Clawbacks(ctx, seller, openOnly)
}

// RejectPayoutItem records that the bank rejected the transfer to one seller of a payout batch (wrong or closed account, for example): the money is back in the bank account (Dr the
// batch's pay-from account, Cr the seller's payable) and the seller's sales are available again for the next batch. Refused when any sale of that item was returned or partly returned
// since (settle the clawbacks first: the item no longer equals the sum of the sales). Idempotent: a rejected item stays rejected.
func (s *Service) RejectPayoutItem(ctx context.Context, batchID, sellerID uuid.UUID, reason, actor string) (Batch, error) {
	ar, err := s.adj()
	if err != nil {
		return Batch{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return Batch{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	var out Batch
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		seller, err := s.repo.Seller(ctx, sellerID, true)
		if err != nil {
			return err
		}
		b, err := s.repo.Batch(ctx, batchID)
		if err != nil {
			return err
		}
		out = b
		var item *BatchItem
		for i := range b.Items {
			if b.Items[i].SellerID == sellerID {
				item = &b.Items[i]
			}
		}
		if item == nil {
			return apperr.NotFound("payout batch item")
		}
		if item.Rejected {
			return nil
		}
		sales, err := ar.PaidSalesOf(ctx, batchID, sellerID)
		if err != nil {
			return err
		}
		var sum int64
		ids := make([]uuid.UUID, len(sales))
		for i, sl := range sales {
			sum += sl.Net.Amount()
			ids[i] = sl.ID
		}
		if sum != item.Amount.Amount() {
			return apperr.New(apperr.KindConflict, "item_changed", "a sale of this item was returned or adjusted after the payout: recover its clawback first")
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "payout:" + batchID.String() + ":reject:" + sellerID.String(), Date: dateOnly(s.books.Today()),
			Description: "Bank rejected the payout transfer to seller " + seller.Code, RefType: "payout_batch", RefID: batchID.String(), Actor: actor,
			Lines: []books.Line{books.Dr(b.PayFrom, item.Amount), books.Cr(seller.AccountCode, item.Amount)}})
		if err != nil {
			return err
		}
		if err := ar.ReopenSales(ctx, ids); err != nil {
			return err
		}
		if err := ar.RejectBatchItem(ctx, batchID, sellerID, reason, jid); err != nil {
			return err
		}
		if out, err = s.repo.Batch(ctx, batchID); err != nil {
			return err
		}
		return s.note(ctx, actor, "payout.item.rejected", "payout_batch", batchID, "payout.item.rejected", map[string]any{"batch_id": batchID, "seller_id": sellerID, "amount": item.Amount.Amount(), "reason": reason})
	})
	return out, err
}
