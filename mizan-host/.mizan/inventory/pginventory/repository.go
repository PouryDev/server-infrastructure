// Package pginventory is the PostgreSQL adapter of inventory.Repository.
package pginventory

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Repository implements inventory.Repository.
type Repository struct{ db *postgres.DB }

var _ inventory.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type itemRow struct {
	ID        uuid.UUID `db:"id"`
	Code      string    `db:"code"`
	Name      string    `db:"name"`
	Unit      string    `db:"unit"`
	Method    string    `db:"method"`
	Qty       int64     `db:"qty"`
	Value     int64     `db:"value"`
	Currency  string    `db:"currency"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
	Writedown int64     `db:"writedown_balance"`
	FEFO      bool      `db:"fefo"`
}

const itemCols = `id, code, name, unit, method, qty, value, currency, active, created_at, writedown_balance, fefo`

func (x itemRow) domain() inventory.Item {
	v, _ := money.New(x.Value, money.Currency(x.Currency))
	return inventory.Item{ID: x.ID, Code: x.Code, Name: x.Name, Unit: x.Unit, Method: inventory.Method(x.Method), Qty: x.Qty, Value: v, Active: x.Active, CreatedAt: x.CreatedAt, WritedownBalance: x.Writedown, FEFO: x.FEFO}
}

func (r *Repository) InsertItem(ctx context.Context, i inventory.Item) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_inv_items (`+itemCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		i.ID, i.Code, i.Name, i.Unit, string(i.Method), i.Qty, i.Value.Amount(), string(i.Value.Currency()), i.Active, i.CreatedAt, i.WritedownBalance, i.FEFO)
	if postgres.IsUniqueViolation(err, "mz_inv_items_code_key") {
		return apperr.New(apperr.KindConflict, "item_exists", "an item with this code already exists")
	}
	return err
}

func (r *Repository) Item(ctx context.Context, id uuid.UUID, forUpdate bool) (inventory.Item, error) {
	sql := `SELECT ` + itemCols + ` FROM mz_inv_items WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[itemRow](ctx, r.db.Querier(ctx), sql, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return inventory.Item{}, apperr.NotFound("item")
	}
	return x.domain(), err
}

func (r *Repository) Items(ctx context.Context) ([]inventory.Item, error) {
	rows, err := postgres.Select[itemRow](ctx, r.db.Querier(ctx), `SELECT `+itemCols+` FROM mz_inv_items ORDER BY code`)
	out := make([]inventory.Item, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) SetItemActive(ctx context.Context, id uuid.UUID, active bool) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_inv_items SET active = $2 WHERE id = $1`, id, active)
	if err == nil && n == 0 {
		return apperr.NotFound("item")
	}
	return err
}

func (r *Repository) UpdateBalance(ctx context.Context, id uuid.UUID, qty, value, writedown int64) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_inv_items SET qty = $2, value = $3, writedown_balance = $4 WHERE id = $1`, id, qty, value, writedown)
	return err
}

type layerRow struct {
	ID             int64      `db:"id"`
	ItemID         uuid.UUID  `db:"item_id"`
	Seq            int        `db:"seq"`
	Qty            int64      `db:"qty"`
	Remaining      int64      `db:"remaining"`
	RemainingValue int64      `db:"remaining_value"`
	WarehouseID    uuid.UUID  `db:"warehouse_id"`
	LotNo          string     `db:"lot_no"`
	ExpiresOn      *time.Time `db:"expires_on"`
}

func (r *Repository) Layers(ctx context.Context, itemID, warehouse uuid.UUID, fefo bool) ([]inventory.Layer, error) {
	order := "seq"
	if fefo {
		order = "expires_on NULLS LAST, seq"
	}
	rows, err := postgres.Select[layerRow](ctx, r.db.Querier(ctx),
		`SELECT id, item_id, seq, qty, remaining, remaining_value, warehouse_id, lot_no, expires_on FROM mz_inv_layers
		 WHERE item_id = $1 AND remaining > 0 AND ($2 = '00000000-0000-0000-0000-000000000000'::uuid OR warehouse_id = $2) ORDER BY `+order, itemID, warehouse)
	out := make([]inventory.Layer, len(rows))
	for i, x := range rows {
		out[i] = inventory.Layer{ID: x.ID, ItemID: x.ItemID, Seq: x.Seq, Qty: x.Qty, Remaining: x.Remaining, RemainingValue: x.RemainingValue, WarehouseID: x.WarehouseID, LotNo: x.LotNo, ExpiresOn: x.ExpiresOn}
	}
	return out, err
}

func (r *Repository) InsertLayer(ctx context.Context, l inventory.Layer) (int64, error) {
	wh := l.WarehouseID
	if wh == uuid.Nil {
		wh = inventory.MainWarehouseID
	}
	// Sequence numbers keep growing even when old layers are used up: take the next one after every layer, open or not.
	return postgres.Value[int64](ctx, r.db.Querier(ctx), `
		INSERT INTO mz_inv_layers (item_id, seq, qty, remaining, remaining_value, warehouse_id, lot_no, expires_on)
		VALUES ($1, (SELECT COALESCE(max(seq), 0) + 1 FROM mz_inv_layers WHERE item_id = $1), $2, $3, $4, $5, $6, $7) RETURNING id`,
		l.ItemID, l.Qty, l.Remaining, l.RemainingValue, wh, l.LotNo, l.ExpiresOn)
}

func (r *Repository) UpdateLayers(ctx context.Context, layers []inventory.Layer) error {
	for _, l := range layers {
		if _, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_inv_layers SET remaining = $2, remaining_value = $3 WHERE id = $1`, l.ID, l.Remaining, l.RemainingValue); err != nil {
			return err
		}
	}
	return nil
}

type movementRow struct {
	ID         int64      `db:"id"`
	ItemID     uuid.UUID  `db:"item_id"`
	Kind       string     `db:"kind"`
	Qty        int64      `db:"qty"`
	Value      int64      `db:"value"`
	QtyAfter   int64      `db:"qty_after"`
	ValueAfter int64      `db:"value_after"`
	EntryDate  time.Time  `db:"entry_date"`
	JournalID  *uuid.UUID `db:"journal_id"`
	IdemKey    string     `db:"idem_key"`
	Ref        string     `db:"ref"`
	Note       string     `db:"note"`
	Actor      string     `db:"actor"`
	CreatedAt  time.Time  `db:"created_at"`
	Warehouse  *uuid.UUID `db:"warehouse_id"`
	LotID      *int64     `db:"lot_id"`
}

const movementCols = `id, item_id, kind, qty, value, qty_after, value_after, entry_date, journal_id, idem_key, ref, note, actor, created_at, warehouse_id, lot_id`

func (x movementRow) domain() inventory.Movement {
	return inventory.Movement{ID: x.ID, ItemID: x.ItemID, Kind: inventory.Kind(x.Kind), Qty: x.Qty, Value: x.Value, QtyAfter: x.QtyAfter, ValueAfter: x.ValueAfter,
		Date: x.EntryDate, Journal: x.JournalID, IdemKey: x.IdemKey, Ref: x.Ref, Note: x.Note, Actor: x.Actor, CreatedAt: x.CreatedAt, WarehouseID: x.Warehouse, LotID: x.LotID}
}

func (r *Repository) MovementByKey(ctx context.Context, key string) (inventory.Movement, bool, error) {
	rows, err := postgres.Select[movementRow](ctx, r.db.Querier(ctx), `SELECT `+movementCols+` FROM mz_inv_movements WHERE idem_key = $1`, key)
	if err != nil || len(rows) == 0 {
		return inventory.Movement{}, false, err
	}
	return rows[0].domain(), true, nil
}

func (r *Repository) InsertMovement(ctx context.Context, m *inventory.Movement) error {
	id, err := postgres.Value[int64](ctx, r.db.Querier(ctx), `
		INSERT INTO mz_inv_movements (item_id, kind, qty, value, qty_after, value_after, entry_date, journal_id, idem_key, ref, note, actor, created_at, warehouse_id, lot_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`,
		m.ItemID, string(m.Kind), m.Qty, m.Value, m.QtyAfter, m.ValueAfter, m.Date, m.Journal, m.IdemKey, m.Ref, m.Note, m.Actor, m.CreatedAt, m.WarehouseID, m.LotID)
	m.ID = id
	return err
}

func (r *Repository) Movements(ctx context.Context, itemID uuid.UUID) ([]inventory.Movement, error) {
	rows, err := postgres.Select[movementRow](ctx, r.db.Querier(ctx), `SELECT `+movementCols+` FROM mz_inv_movements WHERE item_id = $1 ORDER BY id`, itemID)
	out := make([]inventory.Movement, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}
