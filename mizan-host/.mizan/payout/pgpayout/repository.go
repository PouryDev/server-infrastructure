// Package pgpayout is the PostgreSQL adapter of payout.Repository.
package pgpayout

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payout"
)

// Repository implements payout.Repository.
type Repository struct {
	db         *postgres.DB
	saleSchema *query.Schema
}

var _ payout.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	ss, err := query.NewSchemaFor[saleRow]("s", query.WithDefaultSort(query.Sort{Field: "created_at", Desc: true}), query.WithTieBreaker("s.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, saleSchema: ss}, nil
}

func amount(v int64, c string) money.Money { m, _ := money.New(v, money.Currency(c)); return m }

func notFound(err error, what string) error {
	if apperr.Is(err, apperr.KindNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

// ---- sellers ----

type sellerRow struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Sheba       string    `db:"sheba"`
	Status      string    `db:"status"`
	BlockReason string    `db:"block_reason"`
	AccountCode string    `db:"account_code"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

const sellerCols = `id, code, name, sheba, status, block_reason, account_code, created_at, updated_at`

func (x sellerRow) domain() payout.Seller {
	return payout.Seller{ID: x.ID, Code: x.Code, Name: x.Name, Sheba: x.Sheba, Status: payout.SellerStatus(x.Status), BlockReason: x.BlockReason,
		AccountCode: x.AccountCode, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) InsertSeller(ctx context.Context, s payout.Seller) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_po_sellers (`+sellerCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		s.ID, s.Code, s.Name, s.Sheba, string(s.Status), s.BlockReason, s.AccountCode, s.CreatedAt, s.UpdatedAt)
	if postgres.IsUniqueViolation(err, "mz_po_sellers_code_key") {
		return apperr.New(apperr.KindConflict, "seller_exists", "a seller with this code already exists")
	}
	return err
}

func (r *Repository) Seller(ctx context.Context, id uuid.UUID, forUpdate bool) (payout.Seller, error) {
	sql := `SELECT ` + sellerCols + ` FROM mz_po_sellers WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[sellerRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return payout.Seller{}, notFound(err, "seller")
	}
	return x.domain(), nil
}

func (r *Repository) SellersByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]payout.Seller, error) {
	rows, err := postgres.Select[sellerRow](ctx, r.db.Querier(ctx), `SELECT `+sellerCols+` FROM mz_po_sellers WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]payout.Seller, len(rows))
	for _, x := range rows {
		out[x.ID] = x.domain()
	}
	return out, nil
}

func (r *Repository) Sellers(ctx context.Context) ([]payout.Seller, error) {
	rows, err := postgres.Select[sellerRow](ctx, r.db.Querier(ctx), `SELECT `+sellerCols+` FROM mz_po_sellers ORDER BY code`)
	if err != nil {
		return nil, err
	}
	out := make([]payout.Seller, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) UpdateSeller(ctx context.Context, s *payout.Seller) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_sellers SET name = $2, sheba = $3, status = $4, block_reason = $5, updated_at = $6 WHERE id = $1`,
		s.ID, s.Name, s.Sheba, string(s.Status), s.BlockReason, s.UpdatedAt)
	return err
}

// ---- terms ----

type termsRow struct {
	ID            uuid.UUID `db:"id"`
	Name          string    `db:"name"`
	Scope         string    `db:"scope"`
	ScopeKey      string    `db:"scope_key"`
	EffectiveFrom time.Time `db:"effective_from"`
	RateBP        int       `db:"rate_bp"`
	HoldDays      int       `db:"hold_days"`
	VATBP         *int      `db:"vat_bp"`
	Source        string    `db:"source"`
	CreatedBy     string    `db:"created_by"`
	CreatedAt     time.Time `db:"created_at"`
}

func (x termsRow) domain() payout.Terms {
	return payout.Terms{ID: x.ID, Name: x.Name, Scope: payout.ScopeKind(x.Scope), ScopeKey: x.ScopeKey, EffectiveFrom: x.EffectiveFrom, RateBP: x.RateBP,
		HoldDays: x.HoldDays, VATBP: x.VATBP, Source: x.Source, CreatedBy: x.CreatedBy, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertTerms(ctx context.Context, t payout.Terms) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_po_terms (id, name, scope, scope_key, effective_from, rate_bp, hold_days, vat_bp, source, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		t.ID, t.Name, string(t.Scope), t.ScopeKey, t.EffectiveFrom, t.RateBP, t.HoldDays, t.VATBP, t.Source, t.CreatedBy, t.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_po_terms_scope_scope_key_effective_from_key") {
		return apperr.New(apperr.KindConflict, "terms_date_taken", "terms for this scope already start on this date")
	}
	return err
}

func (r *Repository) Terms(ctx context.Context) ([]payout.Terms, error) {
	rows, err := postgres.Select[termsRow](ctx, r.db.Querier(ctx), `
		SELECT id, name, scope, scope_key, effective_from, rate_bp, hold_days, vat_bp, source, created_by, created_at
		FROM mz_po_terms ORDER BY scope, scope_key, effective_from`)
	if err != nil {
		return nil, err
	}
	out := make([]payout.Terms, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) DeleteTerms(ctx context.Context, id uuid.UUID) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `DELETE FROM mz_po_terms WHERE id = $1`, id)
	if apperr.Is(err, apperr.KindConflict) {
		return apperr.New(apperr.KindConflict, "terms_in_use", "a sale used this version: add a newer version instead")
	}
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("payout terms")
	}
	return nil
}

// ---- sales ----

type saleRow struct {
	ID         uuid.UUID  `db:"id" json:"id"`
	IdemKey    string     `db:"idem_key" json:"-" query:"-"`
	SellerID   uuid.UUID  `db:"seller_id" json:"seller_id"`
	TermsID    uuid.UUID  `db:"terms_id" json:"-" query:"-"`
	Reference  string     `db:"reference" json:"reference"`
	Category   string     `db:"category" json:"category"`
	Currency   string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	Gross      int64      `db:"gross" json:"gross"`
	Commission int64      `db:"commission" json:"commission"`
	VAT        int64      `db:"vat" json:"vat"`
	Net        int64      `db:"net" json:"net"`
	RateBP     int        `db:"rate_bp" json:"-" query:"-"`
	VATBP      int        `db:"vat_bp" json:"-" query:"-"`
	HoldDays   int        `db:"hold_days" json:"-" query:"-"`
	Status     string     `db:"status" json:"status"`
	SaleDate   time.Time  `db:"sale_date" json:"sale_date"`
	Delivered  *time.Time `db:"delivered_at" json:"delivered_at"`
	ReleaseAt  *time.Time `db:"release_at" json:"release_at"`
	Journal    *uuid.UUID `db:"journal_id" json:"-" query:"-"`
	ReturnJrnl *uuid.UUID `db:"return_journal" json:"-" query:"-"`
	ReturnWhy  string     `db:"return_reason" json:"-" query:"-"`
	BatchID    *uuid.UUID `db:"batch_id" json:"batch_id"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at" json:"-" query:"-"`
}

const saleCols = `s.id, s.idem_key, s.seller_id, s.terms_id, s.reference, s.category, s.currency, s.gross, s.commission, s.vat, s.net, s.rate_bp, s.vat_bp, s.hold_days, s.status,
	s.sale_date, s.delivered_at, s.release_at, s.journal_id, s.return_journal, s.return_reason, s.batch_id, s.created_at, s.updated_at`

func (x saleRow) domain() payout.Sale {
	return payout.Sale{ID: x.ID, IdemKey: x.IdemKey, SellerID: x.SellerID, TermsID: x.TermsID, Reference: x.Reference, Category: x.Category,
		Gross: amount(x.Gross, x.Currency), Commission: amount(x.Commission, x.Currency), VAT: amount(x.VAT, x.Currency), Net: amount(x.Net, x.Currency), RateBP: x.RateBP, VATBP: x.VATBP, HoldDays: x.HoldDays,
		Status: payout.SaleStatus(x.Status), SaleDate: x.SaleDate, Delivered: x.Delivered, ReleaseAt: x.ReleaseAt, Journal: x.Journal, ReturnJrnl: x.ReturnJrnl,
		ReturnWhy: x.ReturnWhy, BatchID: x.BatchID, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) InsertSale(ctx context.Context, s payout.Sale) (payout.Sale, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_po_sales (id, idem_key, seller_id, terms_id, reference, category, currency, gross, commission, vat, net, rate_bp, vat_bp, hold_days, status,
		                         sale_date, delivered_at, release_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (idem_key) DO NOTHING`,
		s.ID, s.IdemKey, s.SellerID, s.TermsID, s.Reference, s.Category, string(s.Gross.Currency()), s.Gross.Amount(), s.Commission.Amount(), s.VAT.Amount(), s.Net.Amount(),
		s.RateBP, s.VATBP, s.HoldDays, string(s.Status), s.SaleDate, s.Delivered, s.ReleaseAt, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return s, false, err
	}
	if n == 1 {
		return s, true, nil
	}
	x, err := postgres.SelectOne[saleRow](ctx, r.db.Querier(ctx), `SELECT `+saleCols+` FROM mz_po_sales s WHERE s.idem_key = $1`, s.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) Sale(ctx context.Context, id uuid.UUID, forUpdate bool) (payout.Sale, error) {
	sql := `SELECT ` + saleCols + ` FROM mz_po_sales s WHERE s.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[saleRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return payout.Sale{}, notFound(err, "sale")
	}
	return x.domain(), nil
}

func (r *Repository) UpdateSale(ctx context.Context, s *payout.Sale) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_po_sales SET status = $2, delivered_at = $3, release_at = $4, journal_id = $5, return_journal = $6, return_reason = $7, updated_at = $8, batch_id = $9
		WHERE id = $1`, s.ID, string(s.Status), s.Delivered, s.ReleaseAt, s.Journal, s.ReturnJrnl, s.ReturnWhy, s.UpdatedAt, s.BatchID)
	return err
}

func (r *Repository) ListSales(ctx context.Context, p query.Params) (query.Page[payout.Sale], error) {
	page, err := postgres.Paginate[saleRow](ctx, r.db.Querier(ctx), r.saleSchema,
		postgres.ListQuery{Select: "SELECT " + saleCols, From: "FROM mz_po_sales s"}, p)
	if err != nil {
		return query.Page[payout.Sale]{}, err
	}
	return query.MapPage(page, saleRow.domain), nil
}

func (r *Repository) DueSales(ctx context.Context, day time.Time, sellers []uuid.UUID) ([]payout.Sale, error) {
	sql := `SELECT ` + saleCols + ` FROM mz_po_sales s JOIN mz_po_sellers l ON l.id = s.seller_id
		WHERE s.status = 'held' AND s.release_at <= $1 AND s.net > 0 AND l.status = 'active'`
	args := []any{day}
	if len(sellers) > 0 {
		sql += ` AND s.seller_id = ANY($2)`
		args = append(args, sellers)
	}
	// FOR UPDATE re-checks the conditions once the lock is won, so a concurrent batch never picks a sale twice.
	rows, err := postgres.Select[saleRow](ctx, r.db.Querier(ctx), sql+` ORDER BY s.seller_id, s.id FOR UPDATE OF s`, args...)
	if err != nil {
		return nil, err
	}
	out := make([]payout.Sale, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) MarkSalesPaid(ctx context.Context, ids []uuid.UUID, batch uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_sales SET status = 'paid', batch_id = $2, updated_at = now() WHERE id = ANY($1)`, ids, batch)
	return err
}

type totalsRow struct {
	Awaiting  int64 `db:"awaiting"`
	Held      int64 `db:"held"`
	Available int64 `db:"available"`
	Paid      int64 `db:"paid"`
}

func (r *Repository) SellerTotals(ctx context.Context, seller uuid.UUID, day time.Time) (payout.Totals, error) {
	x, err := postgres.SelectOne[totalsRow](ctx, r.db.Querier(ctx), `
		SELECT coalesce(sum(net) FILTER (WHERE status = 'held' AND release_at IS NULL), 0)::bigint AS awaiting,
		       coalesce(sum(net) FILTER (WHERE status = 'held' AND release_at > $2), 0)::bigint AS held,
		       coalesce(sum(net) FILTER (WHERE status = 'held' AND release_at <= $2), 0)::bigint AS available,
		       coalesce(sum(net) FILTER (WHERE status = 'paid'), 0)::bigint AS paid
		FROM mz_po_sales WHERE seller_id = $1`, seller, day)
	return payout.Totals{AwaitingDelivery: x.Awaiting, Held: x.Held, Available: x.Available, Paid: x.Paid}, err
}

// ---- batches ----

type batchRow struct {
	ID        uuid.UUID  `db:"id"`
	IdemKey   string     `db:"idem_key"`
	PayFrom   string     `db:"pay_from"`
	PayDate   time.Time  `db:"pay_date"`
	Currency  string     `db:"currency"`
	Total     int64      `db:"total"`
	SaleCount int        `db:"sale_count"`
	Journal   *uuid.UUID `db:"journal_id"`
	Actor     string     `db:"actor"`
	CreatedAt time.Time  `db:"created_at"`
}

const batchCols = `id, idem_key, pay_from, pay_date, currency, total, sale_count, journal_id, actor, created_at`

type itemRow struct {
	BatchID   uuid.UUID `db:"batch_id"`
	SellerID  uuid.UUID `db:"seller_id"`
	Sheba     string    `db:"sheba"`
	Amount    int64     `db:"amount"`
	SaleCount int       `db:"sale_count"`
	Rejected  bool      `db:"rejected"`
	Reason    string    `db:"reject_reason"`
}

func (x batchRow) domain(items []payout.BatchItem) payout.Batch {
	return payout.Batch{ID: x.ID, IdemKey: x.IdemKey, PayFrom: x.PayFrom, Date: x.PayDate, Total: amount(x.Total, x.Currency), SaleCount: x.SaleCount,
		Journal: x.Journal, Items: items, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertBatch(ctx context.Context, b payout.Batch) (payout.Batch, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_po_batches (id, idem_key, pay_from, pay_date, currency, actor, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idem_key) DO NOTHING`, b.ID, b.IdemKey, b.PayFrom, b.Date, string(b.Total.Currency()), b.Actor, b.CreatedAt)
	if err != nil {
		return b, false, err
	}
	if n == 1 {
		return b, true, nil
	}
	x, err := postgres.SelectOne[batchRow](ctx, r.db.Querier(ctx), `SELECT `+batchCols+` FROM mz_po_batches WHERE idem_key = $1`, b.IdemKey)
	if err != nil {
		return b, false, err
	}
	items, err := r.items(ctx, []uuid.UUID{x.ID}, x.Currency)
	return x.domain(items[x.ID]), false, err
}

func (r *Repository) FinishBatch(ctx context.Context, b *payout.Batch) error {
	q := r.db.Querier(ctx)
	if _, err := postgres.Exec(ctx, q, `UPDATE mz_po_batches SET total = $2, sale_count = $3, journal_id = $4 WHERE id = $1`, b.ID, b.Total.Amount(), b.SaleCount, b.Journal); err != nil {
		return err
	}
	for _, it := range b.Items {
		if _, err := postgres.Exec(ctx, q, `INSERT INTO mz_po_batch_items (batch_id, seller_id, sheba, amount, sale_count) VALUES ($1, $2, $3, $4, $5)`,
			b.ID, it.SellerID, it.Sheba, it.Amount.Amount(), it.SaleCount); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) items(ctx context.Context, ids []uuid.UUID, currency string) (map[uuid.UUID][]payout.BatchItem, error) {
	rows, err := postgres.Select[itemRow](ctx, r.db.Querier(ctx), `SELECT batch_id, seller_id, sheba, amount, sale_count, rejected, reject_reason FROM mz_po_batch_items WHERE batch_id = ANY($1) ORDER BY seller_id`, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]payout.BatchItem, len(ids))
	for _, x := range rows {
		out[x.BatchID] = append(out[x.BatchID], payout.BatchItem{SellerID: x.SellerID, Sheba: x.Sheba, Amount: amount(x.Amount, currency), SaleCount: x.SaleCount, Rejected: x.Rejected, RejectReason: x.Reason})
	}
	return out, nil
}

func (r *Repository) Batch(ctx context.Context, id uuid.UUID) (payout.Batch, error) {
	x, err := postgres.SelectOne[batchRow](ctx, r.db.Querier(ctx), `SELECT `+batchCols+` FROM mz_po_batches WHERE id = $1`, id)
	if err != nil {
		return payout.Batch{}, notFound(err, "payout batch")
	}
	items, err := r.items(ctx, []uuid.UUID{id}, x.Currency)
	return x.domain(items[id]), err
}

func (r *Repository) Batches(ctx context.Context) ([]payout.Batch, error) {
	rows, err := postgres.Select[batchRow](ctx, r.db.Querier(ctx), `SELECT `+batchCols+` FROM mz_po_batches ORDER BY created_at DESC, id LIMIT 200`)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, x := range rows {
		ids[i] = x.ID
	}
	if len(rows) == 0 {
		return []payout.Batch{}, nil
	}
	items, err := r.items(ctx, ids, rows[0].Currency) // the books have one currency
	if err != nil {
		return nil, err
	}
	out := make([]payout.Batch, len(rows))
	for i, x := range rows {
		out[i] = x.domain(items[x.ID])
	}
	return out, nil
}
