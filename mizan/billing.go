package mizan

import (
	"context"
	"fmt"
	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/lifecycle"
	"github.com/PouryDev/server-infrastructure/mizan/coupon"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/subscription"
	"time"
)

// BillResult is what one BillSubscriptions run did.
type BillResult struct {
	Periods  int // billing periods created
	Invoiced int // periods turned into issued invoices
	Failed   int // periods whose invoicing failed (retried next run, up to subscription.MaxAttempts)
}

// BillSubscriptions bills every subscription that is due: it creates the periods that have started and turns each pending
// period into an issued invoice. It is safe to run often and from several instances: a period exists once (unique per
// subscription and number), the invoice is created under the key "subscription:<id>:period:<n>" and each period is invoiced in its
// own transaction under a row lock, so nothing is billed twice. A failing period is counted and retried; it never blocks the rest.
func (m *Mizan) BillSubscriptions(ctx context.Context) (BillResult, error) {
	var res BillResult
	for {
		n, err := m.Subscribe.CreateDuePeriods(ctx, 200)
		if err != nil {
			return res, err
		}
		res.Periods += n
		if n == 0 {
			break
		}
	}
	for {
		var done bool
		var periodID, subID string
		err := m.DB.WithTx(ctx, func(ctx context.Context) error {
			p, sub, ok, err := m.Subscribe.NextPending(ctx)
			if err != nil || !ok {
				done = !ok
				return err
			}
			periodID, subID = p.ID.String(), sub.ID.String()
			return m.invoicePeriod(ctx, p, sub)
		})
		switch {
		case err == nil && done:
			return res, nil
		case err == nil:
			res.Invoiced++
		case periodID == "":
			return res, err // could not even claim a period
		default:
			res.Failed++
			id, _ := uuid.Parse(periodID)
			m.log.ErrorContext(ctx, "subscription invoicing failed", "subscription", subID, "period", periodID, "error", err)
			if rerr := m.Subscribe.RecordFailure(ctx, id, err); rerr != nil {
				return res, rerr
			}
		}
	}
}

// invoicePeriod makes and issues the invoice of one period, applying the subscription's coupon to the first one. It runs inside
// the transaction that holds the period's lock.
func (m *Mizan) invoicePeriod(ctx context.Context, p subscription.Period, sub subscription.Subscription) error {
	var discount int64
	if sub.CouponCode != "" && p.No == 1 {
		r, err := m.Coupons.Redeem(ctx, coupon.RedeemInput{IdemKey: fmt.Sprintf("subscription:%s:period:1:coupon", sub.ID), Code: sub.CouponCode,
			CustomerRef: sub.CustomerRef, Amount: p.Amount, SubjectType: "subscription", SubjectID: sub.ID.String()})
		switch {
		case err == nil:
			discount = r.Discount.Amount()
		case apperr.Is(err, apperr.KindConflict) || apperr.Is(err, apperr.KindNotFound):
			// the coupon is no longer good (expired, used up): the customer is billed in full rather than not at all
		default:
			return err
		}
	}
	item := invoice.ItemInput{Description: fmt.Sprintf("%s (%s - %s)", sub.PlanName, jdate(p.Start), jdate(p.End)), Quantity: 1, UnitPrice: p.Amount.Amount(), Discount: discount, TaxBP: sub.TaxBP}
	inv, err := m.Invoices.Create(ctx, invoice.CreateInput{IdemKey: fmt.Sprintf("subscription:%s:period:%d", sub.ID, p.No), CustomerRef: sub.CustomerRef,
		Buyer: invoice.Buyer(sub.Buyer), Currency: p.Amount.Currency(), Items: []invoice.ItemInput{item}, Memo: "Subscription " + sub.ID.String(), Actor: "system"})
	if err != nil {
		return err
	}
	if inv, err = m.Invoices.Issue(ctx, inv.ID, "system"); err != nil {
		return err
	}
	return m.Subscribe.MarkInvoiced(ctx, p, inv.ID)
}

func jdate(t time.Time) string {
	if d, err := jalali.FromTime(t); err == nil {
		return d.String()
	}
	return t.Format("2006-01-02")
}

// SubscriptionBiller returns the runner that bills due subscriptions (default: every 10 minutes).
func (m *Mizan) SubscriptionBiller() lifecycle.Runner {
	return lifecycle.RunnerFunc(func(ctx context.Context) error {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if r, err := m.BillSubscriptions(ctx); err != nil && ctx.Err() == nil {
					m.log.ErrorContext(ctx, "subscription billing failed", "error", err)
				} else if r.Periods+r.Invoiced+r.Failed > 0 {
					m.log.InfoContext(ctx, "subscription billing", "periods", r.Periods, "invoiced", r.Invoiced, "failed", r.Failed)
				}
			}
		}
	})
}
