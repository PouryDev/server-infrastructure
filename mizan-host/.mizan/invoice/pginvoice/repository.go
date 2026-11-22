// Package pginvoice is the PostgreSQL adapter of invoice.Repository.
package pginvoice

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Repository implements invoice.Repository.
type Repository struct {
	db     *postgres.DB
	schema *query.Schema
}

var _ invoice.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	schema, err := query.NewSchemaFor[summaryRow]("i",
		query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("i.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, schema: schema}, nil
}

type invoiceRow struct {
	ID            uuid.UUID       `db:"id"`
	IdemKey       string          `db:"idem_key"`
	Number        *int64          `db:"number"`
	FiscalYearID  *int64          `db:"fiscal_year_id"`
	Status        string          `db:"status"`
	CustomerRef   string          `db:"customer_ref"`
	Buyer         json.RawMessage `db:"buyer"`
	Currency      string          `db:"currency"`
	Subtotal      int64           `db:"subtotal"`
	DiscountTotal int64           `db:"discount_total"`
	TaxTotal      int64           `db:"tax_total"`
	Total         int64           `db:"total"`
	PaidTotal     int64           `db:"paid_total"`
	IssueDate     *time.Time      `db:"issue_date"`
	DueDate       *time.Time      `db:"due_date"`
	Memo          string          `db:"memo"`
	IssueJournal  *uuid.UUID      `db:"issue_journal"`
	VoidJournal   *uuid.UUID      `db:"void_journal"`
	Version       int             `db:"version"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

type itemRow struct {
	LineNo      int    `db:"line_no"`
	Description string `db:"description"`
	Quantity    int64  `db:"quantity"`
	UnitPrice   int64  `db:"unit_price"`
	Discount    int64  `db:"discount"`
	TaxBP       int    `db:"tax_bp"`
	Net         int64  `db:"net"`
	Tax         int64  `db:"tax"`
	Total       int64  `db:"total"`
}

const cols = `id, idem_key, number, fiscal_year_id, status, customer_ref, buyer, currency, subtotal, discount_total, tax_total, total,
	paid_total, issue_date, due_date, memo, issue_journal, void_journal, version, created_at, updated_at`

func (r invoiceRow) domain(items []itemRow) invoice.Invoice {
	cur := money.Currency(r.Currency)
	m := func(v int64) money.Money { x, _ := money.New(v, cur); return x }
	var buyer invoice.Buyer
	_ = json.Unmarshal(r.Buyer, &buyer)
	inv := invoice.Invoice{ID: r.ID, IdemKey: r.IdemKey, Number: r.Number, FiscalYearID: r.FiscalYearID, Status: invoice.Status(r.Status),
		CustomerRef: r.CustomerRef, Buyer: buyer, Currency: cur, Subtotal: m(r.Subtotal), DiscountTotal: m(r.DiscountTotal), TaxTotal: m(r.TaxTotal),
		Total: m(r.Total), Paid: m(r.PaidTotal), IssueDate: r.IssueDate, DueDate: r.DueDate, Memo: r.Memo, IssueJournal: r.IssueJournal,
		VoidJournal: r.VoidJournal, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	for _, it := range items {
		inv.Items = append(inv.Items, invoice.Item{No: it.LineNo, Description: it.Description, Quantity: it.Quantity, UnitPrice: it.UnitPrice,
			Discount: it.Discount, TaxBP: it.TaxBP, Net: it.Net, Tax: it.Tax, Total: it.Total})
	}
	return inv
}

func (r *Repository) Insert(ctx context.Context, inv invoice.Invoice) (invoice.Invoice, bool, error) {
	q := r.db.Querier(ctx)
	buyer, _ := json.Marshal(inv.Buyer)
	n, err := postgres.Exec(ctx, q, `
		INSERT INTO mz_invoices (id, idem_key, status, customer_ref, buyer, currency, subtotal, discount_total, tax_total, total, due_date, memo, created_at, updated_at)
		VALUES ($1, $2, 'draft', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12) ON CONFLICT (idem_key) DO NOTHING`,
		inv.ID, inv.IdemKey, inv.CustomerRef, buyer, string(inv.Currency), inv.Subtotal.Amount(), inv.DiscountTotal.Amount(), inv.TaxTotal.Amount(),
		inv.Total.Amount(), inv.DueDate, inv.Memo, inv.CreatedAt)
	if err != nil {
		return inv, false, err
	}
	if n == 0 {
		var stored invoice.Invoice
		row, err := postgres.SelectOne[invoiceRow](ctx, q, `SELECT `+cols+` FROM mz_invoices WHERE idem_key = $1`, inv.IdemKey)
		if err != nil {
			return inv, false, err
		}
		items, err := r.items(ctx, row.ID)
		if err != nil {
			return inv, false, err
		}
		stored = row.domain(items)
		return stored, false, nil
	}
	for _, it := range inv.Items {
		if _, err := postgres.Exec(ctx, q, `
			INSERT INTO mz_invoice_items (invoice_id, line_no, description, quantity, unit_price, discount, tax_bp, net, tax, total)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			inv.ID, it.No, it.Description, it.Quantity, it.UnitPrice, it.Discount, it.TaxBP, it.Net, it.Tax, it.Total); err != nil {
			return inv, false, err
		}
	}
	return inv, true, nil
}

func (r *Repository) items(ctx context.Context, id uuid.UUID) ([]itemRow, error) {
	return postgres.Select[itemRow](ctx, r.db.Querier(ctx),
		`SELECT line_no, description, quantity, unit_price, discount, tax_bp, net, tax, total FROM mz_invoice_items WHERE invoice_id = $1 ORDER BY line_no`, id)
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, forUpdate bool) (invoice.Invoice, error) {
	sql := `SELECT ` + cols + ` FROM mz_invoices WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	row, err := postgres.SelectOne[invoiceRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return invoice.Invoice{}, apperr.NotFound("invoice")
		}
		return invoice.Invoice{}, err
	}
	items, err := r.items(ctx, id)
	if err != nil {
		return invoice.Invoice{}, err
	}
	return row.domain(items), nil
}

func (r *Repository) Update(ctx context.Context, inv *invoice.Invoice) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_invoices SET status = $3, number = $4, fiscal_year_id = $5, issue_date = $6, issue_journal = $7, void_journal = $8,
		       paid_total = $9, version = version + 1, updated_at = now()
		 WHERE id = $1 AND version = $2`,
		inv.ID, inv.Version, string(inv.Status), inv.Number, inv.FiscalYearID, inv.IssueDate, inv.IssueJournal, inv.VoidJournal, inv.Paid.Amount())
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.New(apperr.KindConflict, "concurrent_update", "the invoice was changed concurrently, retry")
	}
	inv.Version++
	return nil
}

func (r *Repository) AddPayment(ctx context.Context, id uuid.UUID, p invoice.Payment) (bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_invoice_payments (invoice_id, source, source_ref, amount, journal_id, created_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, source_ref) DO NOTHING`, id, p.Source, p.SourceRef, p.Amount, p.JournalID, p.CreatedAt)
	return n == 1, err
}

type paymentRow struct {
	Source    string     `db:"source"`
	SourceRef string     `db:"source_ref"`
	Amount    int64      `db:"amount"`
	JournalID *uuid.UUID `db:"journal_id"`
	CreatedAt time.Time  `db:"created_at"`
}

func (r *Repository) Payments(ctx context.Context, id uuid.UUID) ([]invoice.Payment, error) {
	rows, err := postgres.Select[paymentRow](ctx, r.db.Querier(ctx),
		`SELECT source, source_ref, amount, journal_id, created_at FROM mz_invoice_payments WHERE invoice_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	out := make([]invoice.Payment, len(rows))
	for i, p := range rows {
		out[i] = invoice.Payment{Source: p.Source, SourceRef: p.SourceRef, Amount: p.Amount, JournalID: p.JournalID, CreatedAt: p.CreatedAt}
	}
	return out, nil
}

type summaryRow struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	Number      *int64     `db:"number" json:"number"`
	Status      string     `db:"status" json:"status"`
	CustomerRef string     `db:"customer_ref" json:"customer_ref"`
	Currency    string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	Total       int64      `db:"total" json:"total"`
	Paid        int64      `db:"paid_total" json:"paid"`
	IssueDate   *time.Time `db:"issue_date" json:"issue_date"`
	DueDate     *time.Time `db:"due_date" json:"due_date"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
}

func (r *Repository) List(ctx context.Context, p query.Params) (query.Page[invoice.Summary], error) {
	page, err := postgres.Paginate[summaryRow](ctx, r.db.Querier(ctx), r.schema, postgres.ListQuery{
		Select: "SELECT i.id, i.number, i.status, i.customer_ref, i.currency, i.total, i.paid_total, i.issue_date, i.due_date, i.created_at",
		From:   "FROM mz_invoices i"}, p)
	if err != nil {
		return query.Page[invoice.Summary]{}, err
	}
	return query.MapPage(page, func(s summaryRow) invoice.Summary {
		return invoice.Summary{ID: s.ID, Number: s.Number, Status: invoice.Status(s.Status), CustomerRef: s.CustomerRef, Currency: s.Currency,
			Total: s.Total, Paid: s.Paid, IssueDate: s.IssueDate, DueDate: s.DueDate, CreatedAt: s.CreatedAt}
	}), nil
}
