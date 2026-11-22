// Package pgreconcile is the PostgreSQL adapter of reconcile.Repository. It reads mz_payments as a read model.
package pgreconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/reconcile"
)

// Repository implements reconcile.Repository.
type Repository struct{ db *postgres.DB }

var _ reconcile.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type localRow struct {
	ID         uuid.UUID  `db:"id"`
	GatewayRef string     `db:"gateway_ref"`
	Amount     int64      `db:"amount"`
	Status     string     `db:"status"`
	PaidAt     *time.Time `db:"paid_at"`
}

func (r *Repository) Locals(ctx context.Context, gateway string, from, to time.Time) ([]reconcile.Local, error) {
	rows, err := postgres.Select[localRow](ctx, r.db.Querier(ctx), `
		SELECT id, COALESCE(gateway_ref, '') AS gateway_ref, amount, status, paid_at FROM mz_payments
		 WHERE gateway = $1 AND created_at >= $2 AND created_at < $3 AND gateway_ref IS NOT NULL ORDER BY created_at`, gateway, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]reconcile.Local, len(rows))
	for i, x := range rows {
		out[i] = reconcile.Local{ID: x.ID, GatewayRef: x.GatewayRef, Amount: x.Amount, Status: x.Status, PaidAt: x.PaidAt}
	}
	return out, nil
}

func (r *Repository) SaveRun(ctx context.Context, run reconcile.Run, items []reconcile.Mismatch) error {
	q := r.db.Querier(ctx)
	sum, _ := json.Marshal(run.Summary)
	if _, err := postgres.Exec(ctx, q, `
		INSERT INTO mz_recon_runs (id, gateway, from_time, to_time, summary, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		run.ID, run.Gateway, run.From, run.To, sum, run.CreatedBy, run.CreatedAt); err != nil {
		return err
	}
	for _, m := range items {
		if _, err := postgres.Exec(ctx, q, `
			INSERT INTO mz_recon_items (run_id, kind, payment_id, gateway_ref, our_amount, gateway_amount, details) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			run.ID, string(m.Kind), m.PaymentID, m.GatewayRef, m.OurAmount, m.GatewayAmount, m.Details); err != nil {
			return err
		}
	}
	return nil
}

type runRow struct {
	ID        uuid.UUID       `db:"id"`
	Gateway   string          `db:"gateway"`
	From      time.Time       `db:"from_time"`
	To        time.Time       `db:"to_time"`
	Summary   json.RawMessage `db:"summary"`
	CreatedBy string          `db:"created_by"`
	CreatedAt time.Time       `db:"created_at"`
}

func (r *Repository) GetRun(ctx context.Context, id uuid.UUID) (reconcile.Run, error) {
	x, err := postgres.SelectOne[runRow](ctx, r.db.Querier(ctx),
		`SELECT id, gateway, from_time, to_time, summary, created_by, created_at FROM mz_recon_runs WHERE id = $1`, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return reconcile.Run{}, apperr.NotFound("reconciliation")
		}
		return reconcile.Run{}, err
	}
	run := reconcile.Run{ID: x.ID, Gateway: x.Gateway, From: x.From, To: x.To, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt}
	_ = json.Unmarshal(x.Summary, &run.Summary)
	return run, nil
}

type itemRow struct {
	ID            int64      `db:"id"`
	Kind          string     `db:"kind"`
	PaymentID     *uuid.UUID `db:"payment_id"`
	GatewayRef    string     `db:"gateway_ref"`
	OurAmount     *int64     `db:"our_amount"`
	GatewayAmount *int64     `db:"gateway_amount"`
	Details       string     `db:"details"`
}

func (r *Repository) Items(ctx context.Context, runID uuid.UUID) ([]reconcile.Item, error) {
	rows, err := postgres.Select[itemRow](ctx, r.db.Querier(ctx),
		`SELECT id, kind, payment_id, gateway_ref, our_amount, gateway_amount, details FROM mz_recon_items WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	out := make([]reconcile.Item, len(rows))
	for i, x := range rows {
		out[i] = reconcile.Item{ID: x.ID, Mismatch: reconcile.Mismatch{Kind: reconcile.Kind(x.Kind), PaymentID: x.PaymentID, GatewayRef: x.GatewayRef,
			OurAmount: x.OurAmount, GatewayAmount: x.GatewayAmount, Details: x.Details}}
	}
	return out, nil
}

type disputeRow struct {
	ID         uuid.UUID       `db:"id"`
	PaymentID  *uuid.UUID      `db:"payment_id"`
	ReconItem  *int64          `db:"recon_item"`
	Kind       string          `db:"kind"`
	Status     string          `db:"status"`
	OpenedBy   string          `db:"opened_by"`
	Notes      json.RawMessage `db:"notes"`
	Resolution string          `db:"resolution"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
}

const disputeCols = `id, payment_id, recon_item, kind, status, opened_by, notes, resolution, created_at, updated_at`

func (x disputeRow) domain() reconcile.Dispute {
	d := reconcile.Dispute{ID: x.ID, PaymentID: x.PaymentID, ReconItem: x.ReconItem, Kind: x.Kind, Status: reconcile.DisputeStatus(x.Status),
		OpenedBy: x.OpenedBy, Resolution: x.Resolution, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
	_ = json.Unmarshal(x.Notes, &d.Notes)
	return d
}

func (r *Repository) InsertDispute(ctx context.Context, d reconcile.Dispute) error {
	notes, _ := json.Marshal(d.Notes)
	if d.Notes == nil {
		notes = []byte("[]")
	}
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_disputes (id, payment_id, recon_item, kind, status, opened_by, notes, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		d.ID, d.PaymentID, d.ReconItem, d.Kind, string(d.Status), d.OpenedBy, notes, d.CreatedAt)
	return err
}

func (r *Repository) GetDispute(ctx context.Context, id uuid.UUID, forUpdate bool) (reconcile.Dispute, error) {
	sql := `SELECT ` + disputeCols + ` FROM mz_disputes WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[disputeRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return reconcile.Dispute{}, apperr.NotFound("dispute")
		}
		return reconcile.Dispute{}, err
	}
	return x.domain(), nil
}

func (r *Repository) UpdateDispute(ctx context.Context, d *reconcile.Dispute) error {
	notes, _ := json.Marshal(d.Notes)
	_, err := postgres.Exec(ctx, r.db.Querier(ctx),
		`UPDATE mz_disputes SET status = $2, notes = $3, resolution = $4, updated_at = now() WHERE id = $1`, d.ID, string(d.Status), notes, d.Resolution)
	return err
}

func (r *Repository) Disputes(ctx context.Context, status string, limit int) ([]reconcile.Dispute, error) {
	sql, args := `SELECT `+disputeCols+` FROM mz_disputes ORDER BY created_at DESC LIMIT $1`, []any{limit}
	if status != "" {
		sql, args = `SELECT `+disputeCols+` FROM mz_disputes WHERE status = $2 ORDER BY created_at DESC LIMIT $1`, []any{limit, status}
	}
	rows, err := postgres.Select[disputeRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		return nil, fmt.Errorf("disputes: %w", err)
	}
	out := make([]reconcile.Dispute, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}
