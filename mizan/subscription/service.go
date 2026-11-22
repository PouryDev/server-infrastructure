package subscription

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// MaxAttempts is how many times a period's invoicing may fail before it is left for a person (it stays visible, pending).
const MaxAttempts = 10

// Service is the subscription application layer.
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

func (s *Service) today() time.Time { return dateOnly(s.clock()) }

func (s *Service) note(ctx context.Context, actor, action, entity string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: entity, EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	if topic == "" {
		return nil
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: entity, AggregateID: id.String(), Payload: details})
}

// ---- plans ----

// AddPlan stores a plan.
func (s *Service) AddPlan(ctx context.Context, in PlanInput) (Plan, error) {
	now := s.clock()
	p := Plan{ID: uuid.New(), Code: in.Code, Name: in.Name, Price: in.Price, Unit: in.Unit, Count: in.Count, TrialDays: in.TrialDays, TaxBP: in.TaxBP,
		Active: true, CreatedAt: now, UpdatedAt: now}
	if err := p.validate(); err != nil {
		return p, err
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertPlan(ctx, p); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "subscription.plan_added", "subscription_plan", p.ID, "", map[string]any{"plan_id": p.ID, "code": p.Code})
	})
	return p, err
}

// PlanUpdate changes a plan; nil keeps a value. The price change applies to subscriptions started afterwards.
type PlanUpdate struct {
	Name      *string
	Price     *money.Money
	TrialDays *int
	Active    *bool
	Actor     string
}

// UpdatePlan changes a plan.
func (s *Service) UpdatePlan(ctx context.Context, id uuid.UUID, in PlanUpdate) (Plan, error) {
	var out Plan
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.repo.Plan(ctx, id, true)
		if err != nil {
			return err
		}
		if in.Name != nil {
			p.Name = *in.Name
		}
		if in.Price != nil {
			p.Price = *in.Price
		}
		if in.TrialDays != nil {
			p.TrialDays = *in.TrialDays
		}
		if in.Active != nil {
			p.Active = *in.Active
		}
		if err := p.validate(); err != nil {
			return err
		}
		p.UpdatedAt = s.clock()
		if err := s.repo.UpdatePlan(ctx, &p); err != nil {
			return err
		}
		out = p
		return s.note(ctx, in.Actor, "subscription.plan_updated", "subscription_plan", id, "", map[string]any{"plan_id": id, "price": p.Price.Amount(), "active": p.Active})
	})
	return out, err
}

// Plan returns a plan.
func (s *Service) Plan(ctx context.Context, id uuid.UUID) (Plan, error) {
	return s.repo.Plan(ctx, id, false)
}

// Plans lists the plans.
func (s *Service) Plans(ctx context.Context) ([]Plan, error) { return s.repo.Plans(ctx) }

// ---- subscriptions ----

// Subscribe starts a subscription: the plan's price, interval and VAT are copied onto it. With a trial the first period
// starts when the trial ends; otherwise it starts on the start day and is billed right away. Idempotent per key.
func (s *Service) Subscribe(ctx context.Context, in SubscribeInput) (Subscription, error) {
	if err := in.validate(); err != nil {
		return Subscription{}, err
	}
	var out Subscription
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		plan, err := s.repo.Plan(ctx, in.PlanID, false)
		if err != nil {
			return err
		}
		if !plan.Active {
			return apperr.New(apperr.KindConflict, "plan_inactive", "the plan is not open for new subscriptions")
		}
		start := s.today()
		if !in.Start.IsZero() {
			start = dateOnly(in.Start)
		}
		first, status := start, Active
		if plan.TrialDays > 0 {
			first, status = start.AddDate(0, 0, plan.TrialDays), Trialing
		}
		now := s.clock()
		sub := Subscription{ID: uuid.New(), IdemKey: in.IdemKey, PlanID: plan.ID, PlanName: plan.Name, CustomerRef: in.CustomerRef, Buyer: in.Buyer, Status: status,
			Price: plan.Price, Unit: plan.Unit, Count: plan.Count, TaxBP: plan.TaxBP, CouponCode: in.CouponCode, StartDate: start, AnchorDate: first, AnchorNo: 1,
			NextBill: &first, CreatedAt: now, UpdatedAt: now}
		stored, created, err := s.repo.InsertSubscription(ctx, sub)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.PlanID != sub.PlanID || stored.CustomerRef != sub.CustomerRef {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different subscription")
			}
			return nil
		}
		return s.note(ctx, in.Actor, "subscription.started", "subscription", sub.ID, "subscription.started",
			map[string]any{"subscription_id": sub.ID, "plan_id": plan.ID, "customer": sub.CustomerRef, "price": sub.Price.Amount(), "currency": sub.Price.Currency()})
	})
	return out, err
}

// Get returns a subscription.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Subscription, error) {
	return s.repo.Subscription(ctx, id, false)
}

// List lists subscriptions with filters, sorting and paging.
func (s *Service) List(ctx context.Context, p query.Params) (query.Page[Subscription], error) {
	return s.repo.ListSubscriptions(ctx, p)
}

// Periods lists the billed periods of a subscription.
func (s *Service) Periods(ctx context.Context, id uuid.UUID) ([]Period, error) {
	return s.repo.Periods(ctx, id)
}

func (s *Service) change(ctx context.Context, id uuid.UUID, actor, action string, fn func(*Subscription) error) (Subscription, error) {
	var out Subscription
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		sub, err := s.repo.Subscription(ctx, id, true)
		if err != nil {
			return err
		}
		out = sub
		if err := fn(&sub); err != nil {
			return err
		}
		sub.UpdatedAt = s.clock()
		if err := s.repo.UpdateSubscription(ctx, &sub); err != nil {
			return err
		}
		out = sub
		return s.note(ctx, actor, "subscription."+action, "subscription", id, "subscription."+action, map[string]any{"subscription_id": id, "status": sub.Status})
	})
	return out, err
}

// Pause stops billing until Resume. Nothing is billed for the paused time.
func (s *Service) Pause(ctx context.Context, id uuid.UUID, actor string) (Subscription, error) {
	return s.change(ctx, id, actor, "paused", func(sub *Subscription) error {
		if sub.Status != Active && sub.Status != Trialing {
			return apperr.New(apperr.KindConflict, "not_running", "only a trialing or active subscription can be paused; this one is "+string(sub.Status))
		}
		sub.Status = Paused
		return nil
	})
}

// Resume restarts a paused subscription: the next period starts today (the paused time is not billed or made up).
func (s *Service) Resume(ctx context.Context, id uuid.UUID, actor string) (Subscription, error) {
	return s.change(ctx, id, actor, "resumed", func(sub *Subscription) error {
		if sub.Status != Paused {
			return apperr.New(apperr.KindConflict, "not_paused", "the subscription is "+string(sub.Status))
		}
		today := s.today()
		sub.Status, sub.AnchorDate, sub.AnchorNo, sub.NextBill = Active, today, sub.PeriodsBilled+1, &today
		return nil
	})
}

// Cancel ends a subscription: at once (nothing more is billed; invoices already made stand) or at the end of the period that
// was already billed. Cancelling again changes nothing.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID, atPeriodEnd bool, actor string) (Subscription, error) {
	return s.change(ctx, id, actor, "canceled", func(sub *Subscription) error {
		switch {
		case sub.Status == Canceled:
			return nil
		case atPeriodEnd && sub.PeriodsBilled > 0 && sub.Status != Paused:
			sub.CancelAtPeriodEnd = true
		default:
			today := s.today()
			sub.Status, sub.NextBill, sub.CanceledAt, sub.CancelAtPeriodEnd = Canceled, nil, &today, false
		}
		return nil
	})
}

// ---- billing ----

// CreateDuePeriods creates the billing period of every subscription whose next period has started, advancing each by one
// period (call again to catch up several). It does not make invoices: the root turns pending periods into invoices.
// Concurrent callers take different subscriptions (SKIP LOCKED), and a period exists once whatever happens. Returns how many
// periods it created.
func (s *Service) CreateDuePeriods(ctx context.Context, limit int) (int, error) {
	var created int
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		created = 0
		subs, err := s.repo.DueSubscriptions(ctx, s.today(), limit)
		if err != nil {
			return err
		}
		for i := range subs {
			sub := &subs[i]
			now := s.clock()
			sub.UpdatedAt = now
			if sub.CancelAtPeriodEnd {
				today := s.today()
				sub.Status, sub.NextBill, sub.CanceledAt, sub.CancelAtPeriodEnd = Canceled, nil, &today, false
				if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
					return err
				}
				if err := s.note(ctx, "system", "subscription.ended", "subscription", sub.ID, "subscription.canceled", map[string]any{"subscription_id": sub.ID}); err != nil {
					return err
				}
				continue
			}
			no := sub.PeriodsBilled + 1
			start, next := sub.PeriodStart(no), sub.PeriodStart(no+1)
			p := Period{ID: uuid.New(), SubscriptionID: sub.ID, No: no, Start: start, End: next.AddDate(0, 0, -1), Amount: sub.Price, Status: PeriodPending, CreatedAt: now}
			if err := s.repo.InsertPeriod(ctx, p); err != nil {
				return err
			}
			sub.Status, sub.PeriodsBilled, sub.NextBill = Active, no, &next
			if err := s.repo.UpdateSubscription(ctx, sub); err != nil {
				return err
			}
			if err := s.note(ctx, "system", "subscription.period_created", "subscription", sub.ID, "", map[string]any{"subscription_id": sub.ID, "period": no,
				"start": start.Format("2006-01-02"), "end": p.End.Format("2006-01-02")}); err != nil {
				return err
			}
			created++
		}
		return nil
	})
	return created, err
}

// NextPending locks the next period waiting for its invoice. Call it inside the transaction that makes the invoice and calls
// MarkInvoiced, so the lock lasts and a crash leaves the period pending.
func (s *Service) NextPending(ctx context.Context) (Period, Subscription, bool, error) {
	p, ok, err := s.repo.NextPending(ctx, MaxAttempts)
	if err != nil || !ok {
		return Period{}, Subscription{}, false, err
	}
	sub, err := s.repo.Subscription(ctx, p.SubscriptionID, false)
	return p, sub, true, err
}

// MarkInvoiced records the invoice of a period.
func (s *Service) MarkInvoiced(ctx context.Context, period Period, invoice uuid.UUID) error {
	period.Status, period.InvoiceID = PeriodInvoiced, &invoice
	if err := s.repo.UpdatePeriod(ctx, &period); err != nil {
		return err
	}
	return s.note(ctx, "system", "subscription.invoiced", "subscription", period.SubscriptionID, "subscription.invoiced",
		map[string]any{"subscription_id": period.SubscriptionID, "period": period.No, "invoice_id": invoice})
}

// RecordFailure counts a failed attempt to invoice a period (in its own transaction, so it survives the rollback).
func (s *Service) RecordFailure(ctx context.Context, period uuid.UUID, err error) error {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return s.repo.RecordFailure(ctx, period, msg)
}
