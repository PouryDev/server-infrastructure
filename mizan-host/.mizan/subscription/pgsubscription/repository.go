// Package pgsubscription is the PostgreSQL adapter of subscription.Repository.
package pgsubscription

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/subscription"
)

// Repository implements subscription.Repository.
type Repository struct {
	db     *postgres.DB
	schema *query.Schema
}

var _ subscription.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	s, err := query.NewSchemaFor[subRow]("s", query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("s.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, schema: s}, nil
}

func amount(v int64, c string) money.Money { m, _ := money.New(v, money.Currency(c)); return m }

func notFound(err error, what string) error {
	if apperr.Is(err, apperr.KindNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

// ---- plans ----

type planRow struct {
	ID        uuid.UUID `db:"id"`
	Code      string    `db:"code"`
	Name      string    `db:"name"`
	Currency  string    `db:"currency"`
	Price     int64     `db:"price"`
	Unit      string    `db:"interval_unit"`
	Count     int       `db:"interval_count"`
	TrialDays int       `db:"trial_days"`
	TaxBP     *int      `db:"tax_bp"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

const planCols = `id, code, name, currency, price, interval_unit, interval_count, trial_days, tax_bp, active, created_at, updated_at`

func (x planRow) domain() subscription.Plan {
	return subscription.Plan{ID: x.ID, Code: x.Code, Name: x.Name, Price: amount(x.Price, x.Currency), Unit: subscription.Unit(x.Unit), Count: x.Count,
		TrialDays: x.TrialDays, TaxBP: x.TaxBP, Active: x.Active, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) InsertPlan(ctx context.Context, p subscription.Plan) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_sub_plans (`+planCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		p.ID, p.Code, p.Name, string(p.Price.Currency()), p.Price.Amount(), string(p.Unit), p.Count, p.TrialDays, p.TaxBP, p.Active, p.CreatedAt, p.UpdatedAt)
	if postgres.IsUniqueViolation(err, "mz_sub_plans_code_key") {
		return apperr.New(apperr.KindConflict, "plan_exists", "a plan with this code already exists")
	}
	return err
}

func (r *Repository) Plan(ctx context.Context, id uuid.UUID, forUpdate bool) (subscription.Plan, error) {
	sql := `SELECT ` + planCols + ` FROM mz_sub_plans WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[planRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return subscription.Plan{}, notFound(err, "plan")
	}
	return x.domain(), nil
}

func (r *Repository) Plans(ctx context.Context) ([]subscription.Plan, error) {
	rows, err := postgres.Select[planRow](ctx, r.db.Querier(ctx), `SELECT `+planCols+` FROM mz_sub_plans ORDER BY code`)
	if err != nil {
		return nil, err
	}
	out := make([]subscription.Plan, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) UpdatePlan(ctx context.Context, p *subscription.Plan) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_sub_plans SET name = $2, price = $3, trial_days = $4, active = $5, updated_at = $6 WHERE id = $1`,
		p.ID, p.Name, p.Price.Amount(), p.TrialDays, p.Active, p.UpdatedAt)
	return err
}

// ---- subscriptions ----

type subRow struct {
	ID                uuid.UUID  `db:"id" json:"id"`
	IdemKey           string     `db:"idem_key" json:"-" query:"-"`
	PlanID            uuid.UUID  `db:"plan_id" json:"plan_id"`
	PlanName          string     `db:"plan_name" json:"-" query:"-"`
	CustomerRef       string     `db:"customer_ref" json:"customer_ref"`
	Buyer             string     `db:"buyer" json:"-" query:"-"`
	Status            string     `db:"status" json:"status"`
	Currency          string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	Price             int64      `db:"price" json:"price"`
	Unit              string     `db:"interval_unit" json:"-" query:"-"`
	Count             int        `db:"interval_count" json:"-" query:"-"`
	TaxBP             *int       `db:"tax_bp" json:"-" query:"-"`
	CouponCode        string     `db:"coupon_code" json:"-" query:"-"`
	StartDate         time.Time  `db:"start_date" json:"start_date"`
	AnchorDate        time.Time  `db:"anchor_date" json:"-" query:"-"`
	AnchorNo          int        `db:"anchor_no" json:"-" query:"-"`
	NextBill          *time.Time `db:"next_bill_date" json:"next_bill_date"`
	PeriodsBilled     int        `db:"periods_billed" json:"-" query:"-"`
	CancelAtPeriodEnd bool       `db:"cancel_at_period_end" json:"-" query:"-"`
	CanceledAt        *time.Time `db:"canceled_at" json:"-" query:"-"`
	CreatedAt         time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at" json:"-" query:"-"`
}

const subCols = `s.id, s.idem_key, s.plan_id, p.name AS plan_name, s.customer_ref, s.buyer::text AS buyer, s.status, s.currency, s.price, s.interval_unit, s.interval_count,
	s.tax_bp, s.coupon_code, s.start_date, s.anchor_date, s.anchor_no, s.next_bill_date, s.periods_billed, s.cancel_at_period_end, s.canceled_at, s.created_at, s.updated_at`

const subFrom = `FROM mz_sub_subscriptions s JOIN mz_sub_plans p ON p.id = s.plan_id`

func (x subRow) domain() (subscription.Subscription, error) {
	var b subscription.Buyer
	if err := json.Unmarshal([]byte(x.Buyer), &b); err != nil {
		return subscription.Subscription{}, err
	}
	return subscription.Subscription{ID: x.ID, IdemKey: x.IdemKey, PlanID: x.PlanID, PlanName: x.PlanName, CustomerRef: x.CustomerRef, Buyer: b, Status: subscription.Status(x.Status),
		Price: amount(x.Price, x.Currency), Unit: subscription.Unit(x.Unit), Count: x.Count, TaxBP: x.TaxBP, CouponCode: x.CouponCode, StartDate: x.StartDate,
		AnchorDate: x.AnchorDate, AnchorNo: x.AnchorNo, NextBill: x.NextBill, PeriodsBilled: x.PeriodsBilled, CancelAtPeriodEnd: x.CancelAtPeriodEnd,
		CanceledAt: x.CanceledAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}, nil
}

func (r *Repository) InsertSubscription(ctx context.Context, s subscription.Subscription) (subscription.Subscription, bool, error) {
	buyer, err := json.Marshal(s.Buyer)
	if err != nil {
		return s, false, err
	}
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_sub_subscriptions (id, idem_key, plan_id, customer_ref, buyer, status, currency, price, interval_unit, interval_count, tax_bp, coupon_code,
		                                  start_date, anchor_date, anchor_no, next_bill_date, periods_billed, cancel_at_period_end, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 0, false, $17, $18) ON CONFLICT (idem_key) DO NOTHING`,
		s.ID, s.IdemKey, s.PlanID, s.CustomerRef, string(buyer), string(s.Status), string(s.Price.Currency()), s.Price.Amount(), string(s.Unit), s.Count, s.TaxBP,
		s.CouponCode, s.StartDate, s.AnchorDate, s.AnchorNo, s.NextBill, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return s, false, err
	}
	if n == 1 {
		return s, true, nil
	}
	x, err := postgres.SelectOne[subRow](ctx, r.db.Querier(ctx), `SELECT `+subCols+` `+subFrom+` WHERE s.idem_key = $1`, s.IdemKey)
	if err != nil {
		return s, false, err
	}
	out, err := x.domain()
	return out, false, err
}

func (r *Repository) Subscription(ctx context.Context, id uuid.UUID, forUpdate bool) (subscription.Subscription, error) {
	sql := `SELECT ` + subCols + ` ` + subFrom + ` WHERE s.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF s`
	}
	x, err := postgres.SelectOne[subRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return subscription.Subscription{}, notFound(err, "subscription")
	}
	return x.domain()
}

func (r *Repository) UpdateSubscription(ctx context.Context, s *subscription.Subscription) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_sub_subscriptions SET status = $2, anchor_date = $3, anchor_no = $4, next_bill_date = $5, periods_billed = $6, cancel_at_period_end = $7,
		       canceled_at = $8, updated_at = $9 WHERE id = $1`,
		s.ID, string(s.Status), s.AnchorDate, s.AnchorNo, s.NextBill, s.PeriodsBilled, s.CancelAtPeriodEnd, s.CanceledAt, s.UpdatedAt)
	return err
}

func (r *Repository) ListSubscriptions(ctx context.Context, p query.Params) (query.Page[subscription.Subscription], error) {
	page, err := postgres.Paginate[subRow](ctx, r.db.Querier(ctx), r.schema, postgres.ListQuery{Select: "SELECT " + subCols, From: subFrom}, p)
	if err != nil {
		return query.Page[subscription.Subscription]{}, err
	}
	var convErr error
	out := query.MapPage(page, func(x subRow) subscription.Subscription {
		v, err := x.domain()
		if err != nil {
			convErr = err
		}
		return v
	})
	return out, convErr
}

func (r *Repository) DueSubscriptions(ctx context.Context, day time.Time, limit int) ([]subscription.Subscription, error) {
	rows, err := postgres.Select[subRow](ctx, r.db.Querier(ctx), `SELECT `+subCols+` `+subFrom+`
		WHERE s.status IN ('trialing', 'active') AND s.next_bill_date <= $1 ORDER BY s.next_bill_date, s.id LIMIT $2 FOR UPDATE OF s SKIP LOCKED`, day, limit)
	if err != nil {
		return nil, err
	}
	out := make([]subscription.Subscription, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---- periods ----

type periodRow struct {
	ID             uuid.UUID  `db:"id"`
	SubscriptionID uuid.UUID  `db:"subscription_id"`
	No             int        `db:"no"`
	Start          time.Time  `db:"period_start"`
	End            time.Time  `db:"period_end"`
	Currency       string     `db:"currency"`
	Amount         int64      `db:"amount"`
	Status         string     `db:"status"`
	InvoiceID      *uuid.UUID `db:"invoice_id"`
	Attempts       int        `db:"attempts"`
	LastError      string     `db:"last_error"`
	CreatedAt      time.Time  `db:"created_at"`
}

const periodCols = `id, subscription_id, no, period_start, period_end, currency, amount, status, invoice_id, attempts, last_error, created_at`

func (x periodRow) domain() subscription.Period {
	return subscription.Period{ID: x.ID, SubscriptionID: x.SubscriptionID, No: x.No, Start: x.Start, End: x.End, Amount: amount(x.Amount, x.Currency),
		Status: subscription.PeriodStatus(x.Status), InvoiceID: x.InvoiceID, Attempts: x.Attempts, LastError: x.LastError, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertPeriod(ctx context.Context, p subscription.Period) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_sub_periods (id, subscription_id, no, period_start, period_end, currency, amount, status, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		p.ID, p.SubscriptionID, p.No, p.Start, p.End, string(p.Amount.Currency()), p.Amount.Amount(), string(p.Status), p.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_sub_periods_subscription_id_no_key") {
		return apperr.New(apperr.KindConflict, "period_exists", "this period was already created")
	}
	return err
}

func (r *Repository) Periods(ctx context.Context, sub uuid.UUID) ([]subscription.Period, error) {
	rows, err := postgres.Select[periodRow](ctx, r.db.Querier(ctx), `SELECT `+periodCols+` FROM mz_sub_periods WHERE subscription_id = $1 ORDER BY no`, sub)
	if err != nil {
		return nil, err
	}
	out := make([]subscription.Period, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) NextPending(ctx context.Context, maxAttempts int) (subscription.Period, bool, error) {
	rows, err := postgres.Select[periodRow](ctx, r.db.Querier(ctx), `SELECT `+periodCols+` FROM mz_sub_periods WHERE status = 'pending' AND attempts < $1
		ORDER BY attempts, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`, maxAttempts)
	if err != nil || len(rows) == 0 {
		return subscription.Period{}, false, err
	}
	return rows[0].domain(), true, nil
}

func (r *Repository) UpdatePeriod(ctx context.Context, p *subscription.Period) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_sub_periods SET status = $2, invoice_id = $3 WHERE id = $1`, p.ID, string(p.Status), p.InvoiceID)
	return err
}

func (r *Repository) RecordFailure(ctx context.Context, period uuid.UUID, message string) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_sub_periods SET attempts = attempts + 1, last_error = $2 WHERE id = $1`, period, message)
	return err
}
