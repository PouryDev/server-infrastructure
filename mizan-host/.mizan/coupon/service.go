package coupon

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Service is the coupon application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	events trail.Emitter
	audit  trail.Recorder
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, events trail.Emitter, audit trail.Recorder, opts ...Option) *Service {
	s := &Service{repo: repo, tx: tx, events: events, audit: audit, clock: time.Now}
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

// Create stores a coupon.
func (s *Service) Create(ctx context.Context, in Input) (Coupon, error) {
	c := in.coupon(uuid.New(), s.clock())
	if err := c.validate(); err != nil {
		return c, err
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertCoupon(ctx, c); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "coupon.created", "coupon", c.ID, "coupon.created", map[string]any{"coupon_id": c.ID, "code": c.Code, "kind": c.Kind})
	})
	return c, err
}

// Get returns a coupon.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Coupon, error) {
	return s.repo.Coupon(ctx, id, false)
}

// List lists coupons with filters, sorting and paging.
func (s *Service) List(ctx context.Context, p query.Params) (query.Page[Coupon], error) {
	return s.repo.ListCoupons(ctx, p)
}

// Update changes the limits of a coupon. The total limit can never drop below what was already redeemed.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (Coupon, error) {
	var out Coupon
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		c, err := s.repo.Coupon(ctx, id, true)
		if err != nil {
			return err
		}
		if in.Name != nil {
			c.Name = strings.TrimSpace(*in.Name)
		}
		if in.ValidFrom != nil {
			d := dateOnly(*in.ValidFrom)
			c.ValidFrom = &d
		}
		if in.ValidUntil != nil {
			d := dateOnly(*in.ValidUntil)
			c.ValidUntil = &d
		}
		if in.ClearValidFrom {
			c.ValidFrom = nil
		}
		if in.ClearValidUntil {
			c.ValidUntil = nil
		}
		if in.MinAmount != nil {
			c.MinAmount = *in.MinAmount
		}
		if in.MaxRedemptions != nil {
			c.MaxRedemptions = in.MaxRedemptions
		}
		if in.ClearMaxRedeem {
			c.MaxRedemptions = nil
		}
		if in.MaxPerCustomer != nil {
			c.MaxPerCustomer = in.MaxPerCustomer
		}
		if in.ClearMaxPerCustom {
			c.MaxPerCustomer = nil
		}
		if in.Active != nil {
			c.Active = *in.Active
		}
		if err := c.validate(); err != nil {
			return err
		}
		if c.MaxRedemptions != nil && *c.MaxRedemptions < c.Redeemed {
			return apperr.New(apperr.KindConflict, "limit_below_redeemed", "the total limit cannot be lower than the redemptions already made")
		}
		c.UpdatedAt = s.clock()
		if err := s.repo.UpdateCoupon(ctx, &c); err != nil {
			return err
		}
		out = c
		return s.note(ctx, in.Actor, "coupon.updated", "coupon", id, "", map[string]any{"coupon_id": id, "active": c.Active})
	})
	return out, err
}

// Preview says what a coupon would give a customer on an amount, without using it. The answer can change by the time the
// coupon is redeemed (another customer may take the last use): only Redeem is binding.
func (s *Service) Preview(ctx context.Context, code, customer string, amount money.Money) (money.Money, error) {
	c, err := s.repo.CouponByCode(ctx, NormalizeCode(code), false)
	if err != nil {
		return money.Money{}, err
	}
	if err := s.check(ctx, c, customer, amount); err != nil {
		return money.Money{}, err
	}
	return c.DiscountFor(amount)
}

func (s *Service) check(ctx context.Context, c Coupon, customer string, amount money.Money) error {
	if err := c.Usable(s.clock(), amount); err != nil {
		return err
	}
	if c.MaxPerCustomer != nil {
		n, err := s.repo.CustomerRedemptions(ctx, c.ID, customer)
		if err != nil {
			return err
		}
		if n >= *c.MaxPerCustomer {
			return apperr.New(apperr.KindConflict, "coupon_customer_limit", "the customer has used this coupon as many times as allowed")
		}
	}
	return nil
}

// Redeem uses a coupon on an amount. Idempotent per key: the same key returns the same redemption and counts once. The
// coupon row is locked while the limits are checked and the counter is raised, so concurrent redemptions never exceed
// the total or the per-customer limit.
func (s *Service) Redeem(ctx context.Context, in RedeemInput) (Redemption, error) {
	if err := in.validate(); err != nil {
		return Redemption{}, err
	}
	code := NormalizeCode(in.Code)
	var out Redemption
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		c, err := s.repo.CouponByCode(ctx, code, true)
		if err != nil {
			return err
		}
		if prev, err := s.repo.RedemptionByKey(ctx, in.IdemKey); err == nil {
			if prev.CouponID != c.ID || prev.CustomerRef != in.CustomerRef || prev.Amount != in.Amount {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different redemption")
			}
			out = prev
			return nil
		} else if !apperr.Is(err, apperr.KindNotFound) {
			return err
		}
		if err := s.check(ctx, c, in.CustomerRef, in.Amount); err != nil {
			return err
		}
		discount, err := c.DiscountFor(in.Amount)
		if err != nil {
			return err
		}
		r := Redemption{ID: uuid.New(), IdemKey: in.IdemKey, CouponID: c.ID, CustomerRef: in.CustomerRef, Amount: in.Amount, Discount: discount, Status: Redeemed,
			SubjectType: in.SubjectType, SubjectID: in.SubjectID, CreatedAt: s.clock()}
		if err := s.repo.InsertRedemption(ctx, r); err != nil {
			return err
		}
		c.Redeemed++
		c.UpdatedAt = s.clock()
		if err := s.repo.UpdateCoupon(ctx, &c); err != nil {
			return err
		}
		out = r
		return s.note(ctx, "system", "coupon.redeemed", "coupon_redemption", r.ID, "coupon.redeemed", map[string]any{"redemption_id": r.ID, "coupon_id": c.ID,
			"code": c.Code, "customer": r.CustomerRef, "amount": r.Amount.Amount(), "discount": discount.Amount(), "currency": discount.Currency()})
	})
	return out, err
}

// Release gives a redemption back (the invoice was voided, the order cancelled): the limits free up. Idempotent.
func (s *Service) Release(ctx context.Context, id uuid.UUID, reason, actor string) (Redemption, error) {
	if reason == "" || len(reason) > 500 {
		return Redemption{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	var out Redemption
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.Redemption(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		if r.Status == Released {
			return nil
		}
		c, err := s.repo.Coupon(ctx, r.CouponID, true)
		if err != nil {
			return err
		}
		r.Status, r.ReleasedReason = Released, reason
		if err := s.repo.UpdateRedemption(ctx, &r); err != nil {
			return err
		}
		c.Redeemed--
		c.UpdatedAt = s.clock()
		if err := s.repo.UpdateCoupon(ctx, &c); err != nil {
			return err
		}
		out = r
		return s.note(ctx, actor, "coupon.released", "coupon_redemption", id, "coupon.released", map[string]any{"redemption_id": id, "coupon_id": c.ID, "reason": reason})
	})
	return out, err
}

// Redemptions lists the redemptions of a coupon, newest first.
func (s *Service) Redemptions(ctx context.Context, coupon uuid.UUID) ([]Redemption, error) {
	return s.repo.Redemptions(ctx, coupon)
}
