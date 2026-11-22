package subscription

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in a transaction that repositories join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertPlan stores a plan; a code in use fails with an apperr conflict "plan_exists".
	InsertPlan(ctx context.Context, p Plan) error
	Plan(ctx context.Context, id uuid.UUID, forUpdate bool) (Plan, error)
	Plans(ctx context.Context) ([]Plan, error)
	UpdatePlan(ctx context.Context, p *Plan) error

	// InsertSubscription stores a subscription; the same key returns the stored one with created=false.
	InsertSubscription(ctx context.Context, s Subscription) (stored Subscription, created bool, err error)
	Subscription(ctx context.Context, id uuid.UUID, forUpdate bool) (Subscription, error)
	UpdateSubscription(ctx context.Context, s *Subscription) error
	ListSubscriptions(ctx context.Context, p query.Params) (query.Page[Subscription], error)
	// DueSubscriptions locks trialing or active subscriptions whose next period starts on or before the day, skipping ones
	// another worker holds.
	DueSubscriptions(ctx context.Context, day time.Time, limit int) ([]Subscription, error)

	// InsertPeriod stores a period; a taken (subscription, number) fails with an apperr conflict "period_exists".
	InsertPeriod(ctx context.Context, p Period) error
	Periods(ctx context.Context, subscription uuid.UUID) ([]Period, error)
	// NextPending locks one pending period that no other worker holds (least failed first); ok=false when none. It must be
	// called inside a transaction: the lock lasts until it ends.
	NextPending(ctx context.Context, maxAttempts int) (p Period, ok bool, err error)
	UpdatePeriod(ctx context.Context, p *Period) error
	// RecordFailure counts a failed attempt and keeps the error, outside the caller's rolled back transaction.
	RecordFailure(ctx context.Context, period uuid.UUID, message string) error
}
