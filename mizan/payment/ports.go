package payment

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in a transaction that repositories and settlements join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port. Everything except the listing runs inside Service transactions.
type Repository interface {
	// Insert stores a new payment. When the idempotency key exists it returns the stored payment and created=false.
	Insert(ctx context.Context, p Payment) (stored Payment, created bool, err error)
	// Get returns the payment, locking its row when forUpdate is set. apperr.NotFound when absent.
	Get(ctx context.Context, id uuid.UUID, forUpdate bool) (Payment, error)
	ByGatewayRef(ctx context.Context, gateway, ref string, forUpdate bool) (Payment, error)
	// Update writes the mutable fields guarded by Version (optimistic lock) and bumps it.
	Update(ctx context.Context, p *Payment) error
	AddTransition(ctx context.Context, id uuid.UUID, t Transition) error
	Transitions(ctx context.Context, id uuid.UUID) ([]Transition, error)
	// WithSavepoint runs fn and rolls back only its writes when it fails.
	WithSavepoint(ctx context.Context, fn func(ctx context.Context) error) error

	// SaveEvent stores a raw callback; inserted=false means this exact event was seen before.
	SaveEvent(ctx context.Context, ev GatewayEvent) (inserted bool, err error)
	FinishEvent(ctx context.Context, gateway, eventKey string, paymentID *uuid.UUID, outcome string) error

	// InsertRefund stores a new refund. A tracking number already used by another refund of the same method
	// makes UpdateRefund fail with an apperr conflict "tracking_number_used".
	InsertRefund(ctx context.Context, r Refund) (stored Refund, created bool, err error)
	GetRefund(ctx context.Context, id uuid.UUID, forUpdate bool) (Refund, error)
	// RefundByKey returns the refund created with an idempotency key. apperr.NotFound when absent.
	RefundByKey(ctx context.Context, key string) (Refund, error)
	UpdateRefund(ctx context.Context, r *Refund) error
	// PendingRefundTotal is the sum of the refunds of a payment that still reserve their amount (pending, processing).
	PendingRefundTotal(ctx context.Context, paymentID uuid.UUID) (int64, error)
	Refunds(ctx context.Context, paymentID uuid.UUID) ([]Refund, error)
	ListRefunds(ctx context.Context, params query.Params) (query.Page[Refund], error)

	// Open returns created/pending payments last touched before the cutoff, oldest first.
	Open(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	// UnsettledPaid returns paid payments whose settlement failed or never ran.
	UnsettledPaid(ctx context.Context, limit int) ([]Payment, error)
	List(ctx context.Context, params query.Params) (query.Page[Payment], error)
}

// GatewayEvent is a callback exactly as received.
type GatewayEvent struct {
	Gateway  string
	EventKey string
	Raw      string
	RemoteIP string
}

// Settlement gives a payment purpose its meaning. Both methods run inside the payment's transaction and
// must be idempotent per payment: they may be called again after a crash between commit and response.
type Settlement interface {
	// OnPaid applies a verified payment (mark an invoice paid, credit a wallet) and returns the id of the
	// journal it posted, if any. A business error (invalid, conflict, not found) does not lose the payment:
	// it is recorded as paid with a settlement error for staff to resolve.
	OnPaid(ctx context.Context, p Payment) (*uuid.UUID, error)
	// CanRefund is the pre-check before a refund is requested or claimed: can `total` (everything reserved
	// for the payment, this refund included) still be returned?
	CanRefund(ctx context.Context, p Payment, total int64) error
	// OnRefunded applies a completed refund and returns the journal it posted, if any. r.Method tells where the
	// money left from: the gateway's clearing account for a gateway refund, the company's bank account for a
	// manual card-to-card payout.
	OnRefunded(ctx context.Context, p Payment, r Refund) (*uuid.UUID, error)
}
