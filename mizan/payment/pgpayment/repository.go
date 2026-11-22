// Package pgpayment is the PostgreSQL adapter of payment.Repository.
package pgpayment

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
)

// Repository implements payment.Repository.
type Repository struct {
	db           *postgres.DB
	schema       *query.Schema
	refundSchema *query.Schema
	sp           atomic.Int64
}

var _ payment.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	schema, err := query.NewSchemaFor[paymentRow]("p",
		query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}),
		query.WithTieBreaker("p.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	refundSchema, err := query.NewSchemaFor[refundRow]("r",
		query.WithDefaultSort(query.Sort{Field: "created_at"}), // oldest first: the agents' queue
		query.WithTieBreaker("r.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, schema: schema, refundSchema: refundSchema}, nil
}

type paymentRow struct {
	ID             uuid.UUID         `db:"id" json:"id"`
	IdemKey        string            `db:"idem_key" json:"-" query:"-"`
	Status         string            `db:"status" json:"status"`
	PurposeType    string            `db:"purpose_type" json:"purpose_type"`
	PurposeID      string            `db:"purpose_id" json:"purpose_id"`
	PayerRef       string            `db:"payer_ref" json:"payer_ref"`
	Amount         int64             `db:"amount" json:"amount"`
	Currency       string            `db:"currency" json:"currency" query:"nofilter,nosort"`
	Gateway        string            `db:"gateway" json:"gateway"`
	GatewayRef     *string           `db:"gateway_ref" json:"gateway_ref"`
	GatewayTxnRef  string            `db:"gateway_txn_ref" json:"gateway_txn_ref"`
	CardPAN        string            `db:"card_pan" json:"-" query:"-"`
	Description    string            `db:"description" json:"-" query:"-"`
	CallbackURL    string            `db:"callback_url" json:"-" query:"-"`
	PayURL         string            `db:"pay_url" json:"-" query:"-"`
	ExpiresAt      time.Time         `db:"expires_at" json:"expires_at"`
	PaidAt         *time.Time        `db:"paid_at" json:"paid_at"`
	FailureCode    string            `db:"failure_code" json:"failure_code"`
	FailureReason  string            `db:"failure_reason" json:"-" query:"-"`
	RefundedAmount int64             `db:"refunded_amount" json:"refunded_amount"`
	SettledJournal *uuid.UUID        `db:"settled_journal" json:"-" query:"-"`
	Metadata       map[string]string `db:"metadata" json:"-" query:"-"`
	Version        int               `db:"version" json:"-" query:"-"`
	CreatedAt      time.Time         `db:"created_at" json:"created_at"`
	UpdatedAt      time.Time         `db:"updated_at" json:"-" query:"-"`
}

const cols = `p.id, p.idem_key, p.status, p.purpose_type, p.purpose_id, p.payer_ref, p.amount, p.currency, p.gateway, p.gateway_ref,
	p.gateway_txn_ref, p.card_pan, p.description, p.callback_url, p.pay_url, p.expires_at, p.paid_at, p.failure_code, p.failure_reason,
	p.refunded_amount, p.settled_journal, p.metadata, p.version, p.created_at, p.updated_at`

func (r paymentRow) domain() payment.Payment {
	cur := money.Currency(r.Currency)
	amount, _ := money.New(r.Amount, cur)
	refunded, _ := money.New(r.RefundedAmount, cur)
	ref := ""
	if r.GatewayRef != nil {
		ref = *r.GatewayRef
	}
	md := r.Metadata
	if md == nil {
		md = map[string]string{}
	}
	return payment.Payment{
		ID: r.ID, IdemKey: r.IdemKey, Status: payment.Status(r.Status), Purpose: payment.Purpose{Type: r.PurposeType, ID: r.PurposeID},
		PayerRef: r.PayerRef, Amount: amount, Gateway: r.Gateway, GatewayRef: ref, GatewayTxnRef: r.GatewayTxnRef, CardPAN: r.CardPAN,
		Description: r.Description, CallbackURL: r.CallbackURL, PayURL: r.PayURL, ExpiresAt: r.ExpiresAt, PaidAt: r.PaidAt,
		FailureCode: r.FailureCode, FailureReason: r.FailureReason, Refunded: refunded, SettledJournal: r.SettledJournal,
		Metadata: md, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (r *Repository) one(ctx context.Context, where string, forUpdate bool, args ...any) (payment.Payment, error) {
	sql := `SELECT ` + cols + ` FROM mz_payments p WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	row, err := postgres.SelectOne[paymentRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return payment.Payment{}, apperr.NotFound("payment")
		}
		return payment.Payment{}, err
	}
	return row.domain(), nil
}

func (r *Repository) Insert(ctx context.Context, p payment.Payment) (payment.Payment, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_payments (id, idem_key, status, purpose_type, purpose_id, payer_ref, amount, currency, gateway, description,
		                         callback_url, expires_at, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14) ON CONFLICT (idem_key) DO NOTHING`,
		p.ID, p.IdemKey, string(p.Status), p.Purpose.Type, p.Purpose.ID, p.PayerRef, p.Amount.Amount(), string(p.Amount.Currency()),
		p.Gateway, p.Description, p.CallbackURL, p.ExpiresAt, p.Metadata, p.CreatedAt)
	if err != nil {
		return p, false, err
	}
	if n == 1 {
		return p, true, nil
	}
	stored, err := r.one(ctx, "p.idem_key = $1", false, p.IdemKey)
	return stored, false, err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, forUpdate bool) (payment.Payment, error) {
	return r.one(ctx, "p.id = $1", forUpdate, id)
}

func (r *Repository) ByGatewayRef(ctx context.Context, gateway, ref string, forUpdate bool) (payment.Payment, error) {
	return r.one(ctx, "p.gateway = $1 AND p.gateway_ref = $2", forUpdate, gateway, ref)
}

func (r *Repository) Update(ctx context.Context, p *payment.Payment) error {
	var ref *string
	if p.GatewayRef != "" {
		ref = &p.GatewayRef
	}
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_payments SET status = $3, gateway_ref = $4, gateway_txn_ref = $5, card_pan = $6, pay_url = $7, paid_at = $8,
		       failure_code = $9, failure_reason = $10, refunded_amount = $11, settled_journal = $12, metadata = $13,
		       version = version + 1, updated_at = now()
		 WHERE id = $1 AND version = $2`,
		p.ID, p.Version, string(p.Status), ref, p.GatewayTxnRef, p.CardPAN, p.PayURL, p.PaidAt, p.FailureCode, p.FailureReason,
		p.Refunded.Amount(), p.SettledJournal, p.Metadata)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.New(apperr.KindConflict, "concurrent_update", "the payment was changed concurrently, retry")
	}
	p.Version++
	return nil
}

func (r *Repository) AddTransition(ctx context.Context, id uuid.UUID, t payment.Transition) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx),
		`INSERT INTO mz_payment_transitions (payment_id, from_status, to_status, reason, at) VALUES ($1, $2, $3, $4, $5)`,
		id, string(t.From), string(t.To), t.Reason, t.At)
	return err
}

type transitionRow struct {
	From   string    `db:"from_status"`
	To     string    `db:"to_status"`
	Reason string    `db:"reason"`
	At     time.Time `db:"at"`
}

func (r *Repository) Transitions(ctx context.Context, id uuid.UUID) ([]payment.Transition, error) {
	rows, err := postgres.Select[transitionRow](ctx, r.db.Querier(ctx),
		`SELECT from_status, to_status, reason, at FROM mz_payment_transitions WHERE payment_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	out := make([]payment.Transition, len(rows))
	for i, t := range rows {
		out[i] = payment.Transition{From: payment.Status(t.From), To: payment.Status(t.To), Reason: t.Reason, At: t.At}
	}
	return out, nil
}

func (r *Repository) WithSavepoint(ctx context.Context, fn func(ctx context.Context) error) error {
	q := r.db.Querier(ctx)
	name := fmt.Sprintf("mz_sp_%d", r.sp.Add(1))
	if _, err := q.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return postgres.MapError(err)
	}
	if err := fn(ctx); err != nil {
		if _, rerr := q.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); rerr != nil {
			return postgres.MapError(rerr)
		}
		return err
	}
	_, err := q.Exec(ctx, "RELEASE SAVEPOINT "+name)
	return postgres.MapError(err)
}

func (r *Repository) SaveEvent(ctx context.Context, ev payment.GatewayEvent) (bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_gateway_events (gateway, event_key, raw, remote_ip) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		ev.Gateway, ev.EventKey, ev.Raw, ev.RemoteIP)
	return n == 1, err
}

func (r *Repository) FinishEvent(ctx context.Context, gateway, key string, id *uuid.UUID, outcome string) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_gateway_events SET processed_at = now(), payment_id = $3, outcome = $4 WHERE gateway = $1 AND event_key = $2`,
		gateway, key, id, outcome)
	return err
}

type refundRow struct {
	ID                    uuid.UUID  `db:"id" json:"id"`
	PaymentID             uuid.UUID  `db:"payment_id" json:"payment_id"`
	IdemKey               string     `db:"idem_key" json:"-" query:"-"`
	Amount                int64      `db:"amount" json:"amount"`
	Currency              string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	Reason                string     `db:"reason" json:"-" query:"-"`
	Status                string     `db:"status" json:"status"`
	Method                string     `db:"method" json:"method"`
	GatewayRef            string     `db:"gateway_ref" json:"-" query:"-"`
	Failure               string     `db:"failure" json:"-" query:"-"`
	JournalID             *uuid.UUID `db:"journal_id" json:"-" query:"-"`
	ClaimedBy             string     `db:"claimed_by" json:"claimed_by"`
	ClaimedAt             *time.Time `db:"claimed_at" json:"claimed_at"`
	DestinationCardMasked string     `db:"destination_card_masked" json:"-" query:"-"`
	TrackingNumber        string     `db:"tracking_number" json:"tracking_number"`
	PaidAt                *time.Time `db:"paid_at" json:"paid_at"`
	Note                  string     `db:"note" json:"-" query:"-"`
	CreatedAt             time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt             time.Time  `db:"updated_at" json:"-" query:"-"`
}

const refundCols = `r.id, r.payment_id, r.idem_key, r.amount, p.currency, r.reason, r.status, r.method, r.gateway_ref, r.failure, r.journal_id,
	r.claimed_by, r.claimed_at, r.destination_card_masked, r.tracking_number, r.paid_at, r.note, r.created_at, r.updated_at`
const refundFrom = ` FROM mz_refunds r JOIN mz_payments p ON p.id = r.payment_id `

func (x refundRow) domain() payment.Refund {
	amount, _ := money.New(x.Amount, money.Currency(x.Currency))
	return payment.Refund{ID: x.ID, PaymentID: x.PaymentID, IdemKey: x.IdemKey, Amount: amount, Reason: x.Reason, Status: payment.RefundStatus(x.Status),
		Method: payment.RefundMethod(x.Method), GatewayRef: x.GatewayRef, Failure: x.Failure, JournalID: x.JournalID, ClaimedBy: x.ClaimedBy,
		ClaimedAt: x.ClaimedAt, DestinationCardMasked: x.DestinationCardMasked, TrackingNumber: x.TrackingNumber, PaidAt: x.PaidAt, Note: x.Note,
		CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) refund(ctx context.Context, where string, forUpdate bool, args ...any) (payment.Refund, error) {
	sql := `SELECT ` + refundCols + refundFrom + `WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE OF r`
	}
	row, err := postgres.SelectOne[refundRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return payment.Refund{}, apperr.NotFound("refund")
		}
		return payment.Refund{}, err
	}
	return row.domain(), nil
}

func (r *Repository) InsertRefund(ctx context.Context, rf payment.Refund) (payment.Refund, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_refunds (id, payment_id, idem_key, amount, reason, status, method, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) ON CONFLICT (idem_key) DO NOTHING`,
		rf.ID, rf.PaymentID, rf.IdemKey, rf.Amount.Amount(), rf.Reason, string(rf.Status), string(rf.Method), rf.CreatedAt)
	if err != nil {
		return rf, false, err
	}
	if n == 1 {
		return rf, true, nil
	}
	stored, err := r.refund(ctx, "r.idem_key = $1", false, rf.IdemKey)
	return stored, false, err
}

func (r *Repository) GetRefund(ctx context.Context, id uuid.UUID, forUpdate bool) (payment.Refund, error) {
	return r.refund(ctx, "r.id = $1", forUpdate, id)
}

func (r *Repository) RefundByKey(ctx context.Context, key string) (payment.Refund, error) {
	return r.refund(ctx, "r.idem_key = $1", false, key)
}

func (r *Repository) UpdateRefund(ctx context.Context, rf *payment.Refund) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_refunds SET status = $2, method = $3, gateway_ref = $4, failure = $5, journal_id = $6, claimed_by = $7, claimed_at = $8,
		       destination_card_masked = $9, tracking_number = $10, paid_at = $11, note = $12, updated_at = now()
		 WHERE id = $1`,
		rf.ID, string(rf.Status), string(rf.Method), rf.GatewayRef, rf.Failure, rf.JournalID, rf.ClaimedBy, rf.ClaimedAt,
		rf.DestinationCardMasked, rf.TrackingNumber, rf.PaidAt, rf.Note)
	if postgres.IsUniqueViolation(err, "mz_refunds_tracking") {
		return apperr.New(apperr.KindConflict, "tracking_number_used", "this tracking number is already recorded for another refund")
	}
	return err
}

func (r *Repository) PendingRefundTotal(ctx context.Context, id uuid.UUID) (int64, error) {
	return postgres.Value[int64](ctx, r.db.Querier(ctx),
		`SELECT COALESCE(sum(amount), 0)::bigint FROM mz_refunds WHERE payment_id = $1 AND status IN ('pending', 'processing')`, id)
}

func (r *Repository) Refunds(ctx context.Context, id uuid.UUID) ([]payment.Refund, error) {
	rows, err := postgres.Select[refundRow](ctx, r.db.Querier(ctx),
		`SELECT `+refundCols+refundFrom+`WHERE r.payment_id = $1 ORDER BY r.created_at, r.id`, id)
	if err != nil {
		return nil, err
	}
	out := make([]payment.Refund, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) ListRefunds(ctx context.Context, params query.Params) (query.Page[payment.Refund], error) {
	page, err := postgres.Paginate[refundRow](ctx, r.db.Querier(ctx), r.refundSchema,
		postgres.ListQuery{Select: "SELECT " + refundCols, From: "FROM mz_refunds r JOIN mz_payments p ON p.id = r.payment_id"}, params)
	if err != nil {
		return query.Page[payment.Refund]{}, err
	}
	return query.MapPage(page, refundRow.domain), nil
}

func (r *Repository) list(ctx context.Context, where string, args ...any) ([]payment.Payment, error) {
	rows, err := postgres.Select[paymentRow](ctx, r.db.Querier(ctx), `SELECT `+cols+` FROM mz_payments p WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	out := make([]payment.Payment, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) Open(ctx context.Context, before time.Time, limit int) ([]payment.Payment, error) {
	return r.list(ctx, `p.status IN ('created', 'pending') AND p.created_at < $1 ORDER BY p.created_at LIMIT $2`, before, limit)
}

func (r *Repository) UnsettledPaid(ctx context.Context, limit int) ([]payment.Payment, error) {
	return r.list(ctx, `p.status IN ('paid', 'partially_refunded') AND p.metadata ->> 'settlement_error' IS NOT NULL ORDER BY p.created_at LIMIT $1`, limit)
}

func (r *Repository) List(ctx context.Context, params query.Params) (query.Page[payment.Payment], error) {
	page, err := postgres.Paginate[paymentRow](ctx, r.db.Querier(ctx), r.schema,
		postgres.ListQuery{Select: "SELECT " + cols, From: "FROM mz_payments p"}, params)
	if err != nil {
		return query.Page[payment.Payment]{}, err
	}
	return query.MapPage(page, paymentRow.domain), nil
}
