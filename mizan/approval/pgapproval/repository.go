// Package pgapproval is the PostgreSQL adapter of approval.Repository.
package pgapproval

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/approval"
)

// Repository implements approval.Repository.
type Repository struct{ db *postgres.DB }

var _ approval.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type row struct {
	ID          uuid.UUID  `db:"id"`
	Kind        string     `db:"kind"`
	Subject     string     `db:"subject"`
	Payload     []byte     `db:"payload"`
	Status      string     `db:"status"`
	RequestedBy string     `db:"requested_by"`
	DecidedBy   string     `db:"decided_by"`
	Reason      string     `db:"reason"`
	Result      []byte     `db:"result"`
	IdemKey     string     `db:"idem_key"`
	CreatedAt   time.Time  `db:"created_at"`
	DecidedAt   *time.Time `db:"decided_at"`
}

const cols = `id, kind, subject, payload, status, requested_by, decided_by, reason, result, idem_key, created_at, decided_at`

func (x row) domain() approval.Approval {
	return approval.Approval{ID: x.ID, Kind: x.Kind, Subject: x.Subject, Payload: json.RawMessage(x.Payload), Status: approval.Status(x.Status), RequestedBy: x.RequestedBy,
		DecidedBy: x.DecidedBy, Reason: x.Reason, Result: json.RawMessage(x.Result), IdemKey: x.IdemKey, CreatedAt: x.CreatedAt, DecidedAt: x.DecidedAt}
}

func (r *Repository) Insert(ctx context.Context, a approval.Approval) (approval.Approval, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_approvals (id, kind, subject, payload, status, requested_by, idem_key, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (idem_key) DO NOTHING`,
		a.ID, a.Kind, a.Subject, []byte(a.Payload), string(a.Status), a.RequestedBy, a.IdemKey, a.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_approvals_one_pending") {
		return a, false, apperr.New(apperr.KindConflict, "already_pending", "a request for this action is already waiting for approval")
	}
	if err != nil {
		return a, false, err
	}
	if n == 1 {
		return a, true, nil
	}
	x, err := postgres.SelectOne[row](ctx, q, `SELECT `+cols+` FROM mz_approvals WHERE idem_key = $1`, a.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, forUpdate bool) (approval.Approval, error) {
	q := `SELECT ` + cols + ` FROM mz_approvals WHERE id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[row](ctx, r.db.Querier(ctx), q, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return approval.Approval{}, apperr.NotFound("approval")
	}
	return x.domain(), err
}

func (r *Repository) List(ctx context.Context, status approval.Status) ([]approval.Approval, error) {
	q := `SELECT ` + cols + ` FROM mz_approvals`
	var args []any
	if status != "" {
		q += ` WHERE status = $1`
		args = append(args, string(status))
	}
	rows, err := postgres.Select[row](ctx, r.db.Querier(ctx), q+` ORDER BY created_at DESC`, args...)
	out := make([]approval.Approval, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) Decide(ctx context.Context, id uuid.UUID, status approval.Status, by, reason string, result json.RawMessage, at time.Time) error {
	var res any
	if len(result) > 0 {
		res = []byte(result)
	}
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_approvals SET status = $2, decided_by = $3, reason = $4, result = $5, decided_at = $6 WHERE id = $1`, id, string(status), by, reason, res, at)
	return err
}
