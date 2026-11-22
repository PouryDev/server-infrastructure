// Package pgcoupon is the PostgreSQL adapter of coupon.Repository.
package pgcoupon

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/coupon"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Repository implements coupon.Repository.
type Repository struct {
	db     *postgres.DB
	schema *query.Schema
}

var _ coupon.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	s, err := query.NewSchemaFor[couponRow]("c", query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("c.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, schema: s}, nil
}

func notFound(err error, what string) error {
	if apperr.Is(err, apperr.KindNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

type couponRow struct {
	ID             uuid.UUID  `db:"id" json:"id"`
	Code           string     `db:"code" json:"code"`
	Name           string     `db:"name" json:"name"`
	Kind           string     `db:"kind" json:"kind"`
	PercentBP      int        `db:"percent_bp" json:"-" query:"-"`
	Currency       string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	FixedAmount    int64      `db:"fixed_amount" json:"-" query:"-"`
	MinAmount      int64      `db:"min_amount" json:"-" query:"-"`
	ValidFrom      *time.Time `db:"valid_from" json:"valid_from"`
	ValidUntil     *time.Time `db:"valid_until" json:"valid_until"`
	MaxRedemptions *int       `db:"max_redemptions" json:"-" query:"-"`
	MaxPerCustomer *int       `db:"max_per_customer" json:"-" query:"-"`
	Active         bool       `db:"active" json:"active"`
	Redeemed       int        `db:"redeemed" json:"redeemed"`
	CreatedBy      string     `db:"created_by" json:"-" query:"-"`
	CreatedAt      time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at" json:"-" query:"-"`
}

const couponCols = `c.id, c.code, c.name, c.kind, c.percent_bp, c.currency, c.fixed_amount, c.min_amount, c.valid_from, c.valid_until, c.max_redemptions,
	c.max_per_customer, c.active, c.redeemed, c.created_by, c.created_at, c.updated_at`

func (x couponRow) domain() coupon.Coupon {
	return coupon.Coupon{ID: x.ID, Code: x.Code, Name: x.Name, Kind: coupon.Kind(x.Kind), PercentBP: x.PercentBP, Currency: money.Currency(x.Currency),
		FixedAmount: x.FixedAmount, MinAmount: x.MinAmount, ValidFrom: x.ValidFrom, ValidUntil: x.ValidUntil, MaxRedemptions: x.MaxRedemptions,
		MaxPerCustomer: x.MaxPerCustomer, Active: x.Active, Redeemed: x.Redeemed, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) InsertCoupon(ctx context.Context, c coupon.Coupon) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_cpn_coupons (id, code, name, kind, percent_bp, currency, fixed_amount, min_amount, valid_from, valid_until, max_redemptions, max_per_customer,
		                            active, redeemed, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		c.ID, c.Code, c.Name, string(c.Kind), c.PercentBP, string(c.Currency), c.FixedAmount, c.MinAmount, c.ValidFrom, c.ValidUntil, c.MaxRedemptions, c.MaxPerCustomer,
		c.Active, c.Redeemed, c.CreatedBy, c.CreatedAt, c.UpdatedAt)
	if postgres.IsUniqueViolation(err, "mz_cpn_coupons_code_key") {
		return apperr.New(apperr.KindConflict, "coupon_exists", "a coupon with this code already exists")
	}
	return err
}

func (r *Repository) one(ctx context.Context, where string, forUpdate bool, arg any) (coupon.Coupon, error) {
	sql := `SELECT ` + couponCols + ` FROM mz_cpn_coupons c WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[couponRow](ctx, r.db.Querier(ctx), sql, arg)
	if err != nil {
		return coupon.Coupon{}, notFound(err, "coupon")
	}
	return x.domain(), nil
}

func (r *Repository) Coupon(ctx context.Context, id uuid.UUID, forUpdate bool) (coupon.Coupon, error) {
	return r.one(ctx, `c.id = $1`, forUpdate, id)
}

func (r *Repository) CouponByCode(ctx context.Context, code string, forUpdate bool) (coupon.Coupon, error) {
	return r.one(ctx, `c.code = $1`, forUpdate, code)
}

func (r *Repository) UpdateCoupon(ctx context.Context, c *coupon.Coupon) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_cpn_coupons SET name = $2, min_amount = $3, valid_from = $4, valid_until = $5, max_redemptions = $6, max_per_customer = $7, active = $8,
		       redeemed = $9, updated_at = $10 WHERE id = $1`,
		c.ID, c.Name, c.MinAmount, c.ValidFrom, c.ValidUntil, c.MaxRedemptions, c.MaxPerCustomer, c.Active, c.Redeemed, c.UpdatedAt)
	return err
}

func (r *Repository) ListCoupons(ctx context.Context, p query.Params) (query.Page[coupon.Coupon], error) {
	page, err := postgres.Paginate[couponRow](ctx, r.db.Querier(ctx), r.schema, postgres.ListQuery{Select: "SELECT " + couponCols, From: "FROM mz_cpn_coupons c"}, p)
	if err != nil {
		return query.Page[coupon.Coupon]{}, err
	}
	return query.MapPage(page, couponRow.domain), nil
}

type redemptionRow struct {
	ID             uuid.UUID `db:"id"`
	IdemKey        string    `db:"idem_key"`
	CouponID       uuid.UUID `db:"coupon_id"`
	CustomerRef    string    `db:"customer_ref"`
	Currency       string    `db:"currency"`
	Amount         int64     `db:"amount"`
	Discount       int64     `db:"discount"`
	Status         string    `db:"status"`
	SubjectType    string    `db:"subject_type"`
	SubjectID      string    `db:"subject_id"`
	ReleasedReason string    `db:"released_reason"`
	CreatedAt      time.Time `db:"created_at"`
}

const redemptionCols = `id, idem_key, coupon_id, customer_ref, currency, amount, discount, status, subject_type, subject_id, released_reason, created_at`

func (x redemptionRow) domain() coupon.Redemption {
	cur := money.Currency(x.Currency)
	a, _ := money.New(x.Amount, cur)
	d, _ := money.New(x.Discount, cur)
	return coupon.Redemption{ID: x.ID, IdemKey: x.IdemKey, CouponID: x.CouponID, CustomerRef: x.CustomerRef, Amount: a, Discount: d, Status: coupon.RedemptionStatus(x.Status),
		SubjectType: x.SubjectType, SubjectID: x.SubjectID, ReleasedReason: x.ReleasedReason, CreatedAt: x.CreatedAt}
}

func (r *Repository) RedemptionByKey(ctx context.Context, key string) (coupon.Redemption, error) {
	x, err := postgres.SelectOne[redemptionRow](ctx, r.db.Querier(ctx), `SELECT `+redemptionCols+` FROM mz_cpn_redemptions WHERE idem_key = $1`, key)
	if err != nil {
		return coupon.Redemption{}, notFound(err, "redemption")
	}
	return x.domain(), nil
}

func (r *Repository) InsertRedemption(ctx context.Context, x coupon.Redemption) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_cpn_redemptions (`+redemptionCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		x.ID, x.IdemKey, x.CouponID, x.CustomerRef, string(x.Amount.Currency()), x.Amount.Amount(), x.Discount.Amount(), string(x.Status), x.SubjectType, x.SubjectID,
		x.ReleasedReason, x.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_cpn_redemptions_idem_key_key") {
		return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different redemption")
	}
	return err
}

func (r *Repository) Redemption(ctx context.Context, id uuid.UUID, forUpdate bool) (coupon.Redemption, error) {
	sql := `SELECT ` + redemptionCols + ` FROM mz_cpn_redemptions WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[redemptionRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return coupon.Redemption{}, notFound(err, "redemption")
	}
	return x.domain(), nil
}

func (r *Repository) UpdateRedemption(ctx context.Context, x *coupon.Redemption) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_cpn_redemptions SET status = $2, released_reason = $3 WHERE id = $1`, x.ID, string(x.Status), x.ReleasedReason)
	return err
}

func (r *Repository) CustomerRedemptions(ctx context.Context, couponID uuid.UUID, customer string) (int, error) {
	return postgres.Value[int](ctx, r.db.Querier(ctx), `SELECT count(*)::int FROM mz_cpn_redemptions WHERE coupon_id = $1 AND customer_ref = $2 AND status = 'redeemed'`, couponID, customer)
}

func (r *Repository) Redemptions(ctx context.Context, couponID uuid.UUID) ([]coupon.Redemption, error) {
	rows, err := postgres.Select[redemptionRow](ctx, r.db.Querier(ctx), `SELECT `+redemptionCols+` FROM mz_cpn_redemptions WHERE coupon_id = $1 ORDER BY created_at DESC, id LIMIT 500`, couponID)
	if err != nil {
		return nil, err
	}
	out := make([]coupon.Redemption, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}
