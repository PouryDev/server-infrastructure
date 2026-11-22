package coupon

import (
	"context"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in a transaction that repositories join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertCoupon stores a coupon; a code in use fails with an apperr conflict "coupon_exists".
	InsertCoupon(ctx context.Context, c Coupon) error
	Coupon(ctx context.Context, id uuid.UUID, forUpdate bool) (Coupon, error)
	// CouponByCode finds a coupon by its normalised code; apperr.NotFound when there is none.
	CouponByCode(ctx context.Context, code string, forUpdate bool) (Coupon, error)
	UpdateCoupon(ctx context.Context, c *Coupon) error
	ListCoupons(ctx context.Context, p query.Params) (query.Page[Coupon], error)

	// RedemptionByKey returns the redemption stored under an idempotency key, or apperr.NotFound.
	RedemptionByKey(ctx context.Context, key string) (Redemption, error)
	// InsertRedemption stores a redemption; a key used concurrently fails with an apperr conflict "idempotency_key_reused".
	InsertRedemption(ctx context.Context, r Redemption) error
	Redemption(ctx context.Context, id uuid.UUID, forUpdate bool) (Redemption, error)
	UpdateRedemption(ctx context.Context, r *Redemption) error
	// CustomerRedemptions counts the redemptions in force of a customer.
	CustomerRedemptions(ctx context.Context, coupon uuid.UUID, customer string) (int, error)
	Redemptions(ctx context.Context, coupon uuid.UUID) ([]Redemption, error)
}
