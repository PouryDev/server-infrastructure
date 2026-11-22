// Package pgeinvoice is the PostgreSQL adapter of einvoice.Repository.
package pgeinvoice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

// Repository implements einvoice.Repository.
type Repository struct {
	db     *postgres.DB
	schema *query.Schema
}

var _ einvoice.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	s, err := query.NewSchemaFor[docRow]("d", query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("d.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, schema: s}, nil
}

type docRow struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	SourceType      string     `db:"source_type" json:"source_type"`
	SourceID        string     `db:"source_id" json:"source_id"`
	Status          string     `db:"status" json:"status"`
	TraceID         uuid.UUID  `db:"trace_id" json:"-" query:"-"`
	FiscalID        string     `db:"fiscal_id" json:"-" query:"-"`
	Invoice         string     `db:"invoice" json:"-" query:"-"`
	ReferenceNumber string     `db:"reference_number" json:"reference_number"`
	Errors          string     `db:"errors" json:"-" query:"-"`
	Warnings        string     `db:"warnings" json:"-" query:"-"`
	Attempts        int        `db:"attempts" json:"-" query:"-"`
	LastError       string     `db:"last_error" json:"-" query:"-"`
	SentAt          *time.Time `db:"sent_at" json:"sent_at"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"-" query:"-"`
}

const cols = `d.id, d.source_type, d.source_id, d.status, d.trace_id, d.fiscal_id, d.invoice::text AS invoice, d.reference_number, d.errors::text AS errors,
	d.warnings::text AS warnings, d.attempts, d.last_error, d.sent_at, d.created_at, d.updated_at`

func (x docRow) domain() (einvoice.Document, error) {
	d := einvoice.Document{ID: x.ID, SourceType: x.SourceType, SourceID: x.SourceID, Status: einvoice.Status(x.Status), TraceID: x.TraceID, FiscalID: x.FiscalID,
		ReferenceNumber: x.ReferenceNumber, Attempts: x.Attempts, LastError: x.LastError, SentAt: x.SentAt, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
	for raw, into := range map[string]any{x.Invoice: &d.Invoice, x.Errors: &d.Errors, x.Warnings: &d.Warnings} {
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			return d, err
		}
	}
	return d, nil
}

func marshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func issues(i []einvoice.Issue) []einvoice.Issue {
	if i == nil {
		return []einvoice.Issue{}
	}
	return i
}

func (r *Repository) Insert(ctx context.Context, d einvoice.Document) (einvoice.Document, bool, error) {
	inv, err := marshal(d.Invoice)
	if err != nil {
		return d, false, err
	}
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_ein_documents (id, source_type, source_id, status, trace_id, fiscal_id, invoice, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9) ON CONFLICT (source_type, source_id) DO NOTHING`,
		d.ID, d.SourceType, d.SourceID, string(d.Status), d.TraceID, d.FiscalID, inv, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return d, false, err
	}
	if n == 1 {
		return d, true, nil
	}
	x, err := postgres.SelectOne[docRow](ctx, r.db.Querier(ctx), `SELECT `+cols+` FROM mz_ein_documents d WHERE d.source_type = $1 AND d.source_id = $2`, d.SourceType, d.SourceID)
	if err != nil {
		return d, false, err
	}
	out, err := x.domain()
	return out, false, err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, forUpdate bool) (einvoice.Document, error) {
	sql := `SELECT ` + cols + ` FROM mz_ein_documents d WHERE d.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[docRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return einvoice.Document{}, apperr.NotFound("e-invoice")
		}
		return einvoice.Document{}, err
	}
	return x.domain()
}

func (r *Repository) Update(ctx context.Context, d *einvoice.Document) error {
	inv, err := marshal(d.Invoice)
	if err != nil {
		return err
	}
	errs, err := marshal(issues(d.Errors))
	if err != nil {
		return err
	}
	warns, err := marshal(issues(d.Warnings))
	if err != nil {
		return err
	}
	_, err = postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_ein_documents SET status = $2, trace_id = $3, invoice = $4::jsonb, reference_number = $5, errors = $6::jsonb, warnings = $7::jsonb,
		       attempts = $8, last_error = $9, sent_at = $10, updated_at = $11 WHERE id = $1`,
		d.ID, string(d.Status), d.TraceID, inv, d.ReferenceNumber, errs, warns, d.Attempts, d.LastError, d.SentAt, d.UpdatedAt)
	return err
}

func (r *Repository) List(ctx context.Context, p query.Params) (query.Page[einvoice.Document], error) {
	page, err := postgres.Paginate[docRow](ctx, r.db.Querier(ctx), r.schema, postgres.ListQuery{Select: "SELECT " + cols, From: "FROM mz_ein_documents d"}, p)
	if err != nil {
		return query.Page[einvoice.Document]{}, err
	}
	var convErr error
	out := query.MapPage(page, func(x docRow) einvoice.Document {
		d, err := x.domain()
		if err != nil {
			convErr = err
		}
		return d
	})
	return out, convErr
}

func (r *Repository) claim(ctx context.Context, status string, limit int) ([]einvoice.Document, error) {
	rows, err := postgres.Select[docRow](ctx, r.db.Querier(ctx),
		`SELECT `+cols+` FROM mz_ein_documents d WHERE d.status = $1 ORDER BY d.created_at, d.id LIMIT $2 FOR UPDATE SKIP LOCKED`, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]einvoice.Document, len(rows))
	for i, x := range rows {
		if out[i], err = x.domain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *Repository) ClaimQueued(ctx context.Context, limit int) ([]einvoice.Document, error) {
	return r.claim(ctx, "queued", limit)
}

func (r *Repository) ClaimSent(ctx context.Context, limit int) ([]einvoice.Document, error) {
	return r.claim(ctx, "sent", limit)
}
