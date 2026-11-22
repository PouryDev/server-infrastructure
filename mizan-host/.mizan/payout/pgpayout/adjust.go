package pgpayout

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/payout"
)

var _ payout.AdjustmentRepository = (*Repository)(nil)

func (r *Repository) InsertAdjustment(ctx context.Context, a payout.Adjustment) (bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_po_adjustments (key, sale_id, gross, commission, vat, net, reason, journal_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (key) DO NOTHING`,
		a.Key, a.SaleID, a.Gross, a.Commission, a.VAT, a.Net, a.Reason, a.Journal)
	return n == 1, err
}

func (r *Repository) AdjustmentByKey(ctx context.Context, key string) (payout.Adjustment, bool, error) {
	rows, err := postgres.Select[struct {
		Key        string     `db:"key"`
		SaleID     uuid.UUID  `db:"sale_id"`
		Gross      int64      `db:"gross"`
		Commission int64      `db:"commission"`
		VAT        int64      `db:"vat"`
		Net        int64      `db:"net"`
		Reason     string     `db:"reason"`
		Journal    *uuid.UUID `db:"journal_id"`
	}](ctx, r.db.Querier(ctx), `SELECT key, sale_id, gross, commission, vat, net, reason, journal_id FROM mz_po_adjustments WHERE key = $1`, key)
	if err != nil || len(rows) == 0 {
		return payout.Adjustment{}, false, err
	}
	x := rows[0]
	return payout.Adjustment{Key: x.Key, SaleID: x.SaleID, Gross: x.Gross, Commission: x.Commission, VAT: x.VAT, Net: x.Net, Reason: x.Reason, Journal: x.Journal}, true, nil
}

func (r *Repository) UpdateSaleFigures(ctx context.Context, s *payout.Sale) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_sales SET gross = $2, commission = $3, vat = $4, net = $5, updated_at = $6 WHERE id = $1`,
		s.ID, s.Gross.Amount(), s.Commission.Amount(), s.VAT.Amount(), s.Net.Amount(), s.UpdatedAt)
	return err
}

type clawbackRow struct {
	ID        uuid.UUID  `db:"id"`
	SaleID    uuid.UUID  `db:"sale_id"`
	SellerID  uuid.UUID  `db:"seller_id"`
	BatchID   *uuid.UUID `db:"batch_id"`
	Ref       string     `db:"ref"`
	Amount    int64      `db:"amount"`
	Recovered int64      `db:"recovered"`
	Reason    string     `db:"reason"`
	CreatedAt time.Time  `db:"created_at"`
}

const clawbackCols = `id, sale_id, seller_id, batch_id, ref, amount, recovered, reason, created_at`

// the books have one currency (the sale's): clawbacks carry none, so it is read from the sale
func (r *Repository) clawbackDomain(ctx context.Context, x clawbackRow) (payout.Clawback, error) {
	cur, err := postgres.Value[string](ctx, r.db.Querier(ctx), `SELECT currency FROM mz_po_sales WHERE id = $1`, x.SaleID)
	if err != nil {
		return payout.Clawback{}, err
	}
	return payout.Clawback{ID: x.ID, SaleID: x.SaleID, SellerID: x.SellerID, BatchID: x.BatchID, Ref: x.Ref, Amount: amount(x.Amount, cur), Recovered: amount(x.Recovered, cur), Reason: x.Reason, CreatedAt: x.CreatedAt}, nil
}

func (r *Repository) InsertClawback(ctx context.Context, c payout.Clawback) (payout.Clawback, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_po_clawbacks (id, sale_id, seller_id, batch_id, ref, amount, recovered, reason, created_at) VALUES ($1,$2,$3,$4,$5,$6,0,$7,$8) ON CONFLICT (sale_id, ref) DO NOTHING`,
		c.ID, c.SaleID, c.SellerID, c.BatchID, c.Ref, c.Amount.Amount(), c.Reason, c.CreatedAt)
	if err != nil {
		return c, false, err
	}
	if n == 1 {
		return c, true, nil
	}
	x, err := postgres.SelectOne[clawbackRow](ctx, q, `SELECT `+clawbackCols+` FROM mz_po_clawbacks WHERE sale_id = $1 AND ref = $2`, c.SaleID, c.Ref)
	if err != nil {
		return c, false, err
	}
	out, err := r.clawbackDomain(ctx, x)
	return out, false, err
}

func (r *Repository) Clawback(ctx context.Context, id uuid.UUID, forUpdate bool) (payout.Clawback, error) {
	sql := `SELECT ` + clawbackCols + ` FROM mz_po_clawbacks WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[clawbackRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return payout.Clawback{}, notFound(err, "clawback")
	}
	return r.clawbackDomain(ctx, x)
}

func (r *Repository) Clawbacks(ctx context.Context, seller uuid.UUID, openOnly bool) ([]payout.Clawback, error) {
	rows, err := postgres.Select[clawbackRow](ctx, r.db.Querier(ctx), `SELECT `+clawbackCols+` FROM mz_po_clawbacks WHERE seller_id = $1 AND (NOT $2 OR recovered < amount) ORDER BY created_at, id`, seller, openOnly)
	if err != nil {
		return nil, err
	}
	out := make([]payout.Clawback, 0, len(rows))
	for _, x := range rows {
		c, err := r.clawbackDomain(ctx, x)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *Repository) AddRecovered(ctx context.Context, id uuid.UUID, amt int64) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_clawbacks SET recovered = recovered + $2 WHERE id = $1`, id, amt)
	return err
}

func (r *Repository) InsertRecovery(ctx context.Context, v payout.Recovery) (bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_po_recoveries (key, clawback_id, method, amount, journal_id) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (key) DO NOTHING`,
		v.Key, v.ClawbackID, v.Method, v.Amount, v.Journal)
	return n == 1, err
}

func (r *Repository) RecoveryByKey(ctx context.Context, key string) (payout.Recovery, bool, error) {
	rows, err := postgres.Select[struct {
		Key     string     `db:"key"`
		CB      uuid.UUID  `db:"clawback_id"`
		Method  string     `db:"method"`
		Amount  int64      `db:"amount"`
		Journal *uuid.UUID `db:"journal_id"`
	}](ctx, r.db.Querier(ctx), `SELECT key, clawback_id, method, amount, journal_id FROM mz_po_recoveries WHERE key = $1`, key)
	if err != nil || len(rows) == 0 {
		return payout.Recovery{}, false, err
	}
	x := rows[0]
	return payout.Recovery{Key: x.Key, ClawbackID: x.CB, Method: x.Method, Amount: x.Amount, Journal: x.Journal}, true, nil
}

func (r *Repository) PaidSalesOf(ctx context.Context, batch, seller uuid.UUID) ([]payout.Sale, error) {
	rows, err := postgres.Select[saleRow](ctx, r.db.Querier(ctx), `SELECT `+saleCols+` FROM mz_po_sales s WHERE s.batch_id = $1 AND s.seller_id = $2 AND s.status = 'paid' ORDER BY s.id FOR UPDATE`, batch, seller)
	out := make([]payout.Sale, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) ReopenSales(ctx context.Context, ids []uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_sales SET status = 'held', batch_id = NULL, updated_at = now() WHERE id = ANY($1)`, ids)
	return err
}

func (r *Repository) RejectBatchItem(ctx context.Context, batch, seller uuid.UUID, reason string, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_po_batch_items SET rejected = true, reject_reason = $3, reject_journal = $4 WHERE batch_id = $1 AND seller_id = $2`, batch, seller, reason, journal)
	return err
}
