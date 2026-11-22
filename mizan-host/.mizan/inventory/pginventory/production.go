package pginventory

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
)

var _ inventory.ProductionRepository = (*Repository)(nil)

// ---- warehouses and stock ----

type warehouseRow struct {
	ID        uuid.UUID `db:"id"`
	Code      string    `db:"code"`
	Name      string    `db:"name"`
	Active    bool      `db:"active"`
	CreatedAt time.Time `db:"created_at"`
}

func (x warehouseRow) domain() inventory.Warehouse {
	return inventory.Warehouse{ID: x.ID, Code: x.Code, Name: x.Name, Active: x.Active, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertWarehouse(ctx context.Context, w inventory.Warehouse) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_inv_warehouses (id, code, name, active, created_at) VALUES ($1,$2,$3,$4,$5)`, w.ID, w.Code, w.Name, w.Active, w.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_inv_warehouses_code_key") {
		return apperr.New(apperr.KindConflict, "warehouse_exists", "a warehouse with this code already exists")
	}
	return err
}

func (r *Repository) Warehouse(ctx context.Context, id uuid.UUID) (inventory.Warehouse, error) {
	x, err := postgres.SelectOne[warehouseRow](ctx, r.db.Querier(ctx), `SELECT id, code, name, active, created_at FROM mz_inv_warehouses WHERE id = $1`, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return inventory.Warehouse{}, apperr.NotFound("warehouse")
	}
	return x.domain(), err
}

func (r *Repository) Warehouses(ctx context.Context) ([]inventory.Warehouse, error) {
	rows, err := postgres.Select[warehouseRow](ctx, r.db.Querier(ctx), `SELECT id, code, name, active, created_at FROM mz_inv_warehouses ORDER BY code`)
	out := make([]inventory.Warehouse, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) StockQty(ctx context.Context, itemID, warehouse uuid.UUID) (int64, error) {
	return postgres.Value[int64](ctx, r.db.Querier(ctx), `SELECT COALESCE((SELECT qty FROM mz_inv_stock WHERE item_id = $1 AND warehouse_id = $2), 0)`, itemID, warehouse)
}

func (r *Repository) AddStock(ctx context.Context, itemID, warehouse uuid.UUID, delta int64) error {
	if delta < 0 {
		n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_inv_stock SET qty = qty + $3 WHERE item_id = $1 AND warehouse_id = $2 AND qty + $3 >= 0`, itemID, warehouse, delta)
		if err == nil && n == 0 {
			return apperr.New(apperr.KindConflict, "insufficient_stock", "the warehouse holds less than the quantity asked for")
		}
		return err
	}
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_inv_stock (item_id, warehouse_id, qty) VALUES ($1,$2,$3) ON CONFLICT (item_id, warehouse_id) DO UPDATE SET qty = mz_inv_stock.qty + EXCLUDED.qty`, itemID, warehouse, delta)
	return err
}

func (r *Repository) StockOf(ctx context.Context, itemID uuid.UUID) ([]inventory.StockRow, error) {
	rows, err := postgres.Select[struct {
		W uuid.UUID `db:"warehouse_id"`
		Q int64     `db:"qty"`
	}](ctx, r.db.Querier(ctx), `SELECT warehouse_id, qty FROM mz_inv_stock WHERE item_id = $1 AND qty > 0 ORDER BY warehouse_id`, itemID)
	out := make([]inventory.StockRow, len(rows))
	for i, x := range rows {
		out[i] = inventory.StockRow{WarehouseID: x.W, Qty: x.Q}
	}
	return out, err
}

func (r *Repository) LotsExpiring(ctx context.Context, onOrBefore time.Time) ([]inventory.LotInfo, error) {
	rows, err := postgres.Select[struct {
		ID   int64     `db:"id"`
		Item uuid.UUID `db:"item_id"`
		Code string    `db:"code"`
		W    uuid.UUID `db:"warehouse_id"`
		Lot  string    `db:"lot_no"`
		Exp  time.Time `db:"expires_on"`
		Rem  int64     `db:"remaining"`
		Val  int64     `db:"remaining_value"`
	}](ctx, r.db.Querier(ctx), `SELECT l.id, l.item_id, i.code, l.warehouse_id, l.lot_no, l.expires_on, l.remaining, l.remaining_value FROM mz_inv_layers l JOIN mz_inv_items i ON i.id = l.item_id
		WHERE l.remaining > 0 AND l.expires_on IS NOT NULL AND l.expires_on <= $1 ORDER BY l.expires_on, l.id`, onOrBefore)
	out := make([]inventory.LotInfo, len(rows))
	for i, x := range rows {
		out[i] = inventory.LotInfo{LotID: x.ID, ItemID: x.Item, ItemCode: x.Code, WarehouseID: x.W, LotNo: x.Lot, ExpiresOn: x.Exp, Remaining: x.Rem, Value: x.Val}
	}
	return out, err
}

// ---- production ----

type rateRow struct {
	ID            uuid.UUID `db:"id"`
	EffectiveFrom time.Time `db:"effective_from"`
	Basis         string    `db:"basis"`
	Fixed         int64     `db:"fixed_rate"`
	Variable      int64     `db:"variable_rate"`
	Budget        int64     `db:"budget_fixed"`
	NormalCap     int64     `db:"normal_cap"`
	Verified      bool      `db:"verified"`
	Source        string    `db:"source"`
	CreatedAt     time.Time `db:"created_at"`
}

func (r *Repository) InsertOverheadRate(ctx context.Context, v inventory.OverheadRate) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_prod_overhead_rates (id, effective_from, basis, fixed_rate, variable_rate, budget_fixed, normal_cap, verified, source, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, v.ID, v.EffectiveFrom, string(v.Basis), v.FixedRate, v.VariableRate, v.BudgetFixed, v.NormalCap, v.Verified, v.Source, v.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_prod_overhead_rates_effective_from_key") {
		return apperr.New(apperr.KindConflict, "rate_date_taken", "an overhead rate with this effective date already exists")
	}
	return err
}

func (r *Repository) OverheadRates(ctx context.Context) ([]inventory.OverheadRate, error) {
	rows, err := postgres.Select[rateRow](ctx, r.db.Querier(ctx), `SELECT id, effective_from, basis, fixed_rate, variable_rate, budget_fixed, normal_cap, verified, source, created_at FROM mz_prod_overhead_rates ORDER BY effective_from`)
	out := make([]inventory.OverheadRate, len(rows))
	for i, x := range rows {
		out[i] = inventory.OverheadRate{ID: x.ID, EffectiveFrom: x.EffectiveFrom, Basis: inventory.Basis(x.Basis), FixedRate: x.Fixed, VariableRate: x.Variable, BudgetFixed: x.Budget, NormalCap: x.NormalCap,
			Verified: x.Verified, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

type orderRow struct {
	ID          uuid.UUID  `db:"id"`
	Code        string     `db:"code"`
	Product     uuid.UUID  `db:"product_item"`
	Planned     int64      `db:"planned_qty"`
	Status      string     `db:"status"`
	Material    int64      `db:"material_cost"`
	Labor       int64      `db:"labor_cost"`
	Fixed       int64      `db:"fixed_overhead"`
	Var         int64      `db:"var_overhead"`
	Machine     int64      `db:"machine_units"`
	Output      int64      `db:"output_qty"`
	Loss        int64      `db:"abnormal_loss"`
	Finished    int64      `db:"finished_cost"`
	CompletedOn *time.Time `db:"completed_on"`
	CompleteKey string     `db:"complete_key"`
	CreatedAt   time.Time  `db:"created_at"`
}

const orderCols = `id, code, product_item, planned_qty, status, material_cost, labor_cost, fixed_overhead, var_overhead, machine_units, output_qty, abnormal_loss, finished_cost, completed_on, complete_key, created_at`

func (x orderRow) domain() inventory.Order {
	return inventory.Order{ID: x.ID, Code: x.Code, ProductItemID: x.Product, PlannedQty: x.Planned, Status: inventory.OrderStatus(x.Status), MaterialCost: x.Material, LaborCost: x.Labor,
		FixedOverhead: x.Fixed, VarOverhead: x.Var, MachineUnits: x.Machine, OutputQty: x.Output, AbnormalLoss: x.Loss, FinishedCost: x.Finished, CompletedOn: x.CompletedOn, CompleteKey: x.CompleteKey, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertOrder(ctx context.Context, o inventory.Order) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_prod_orders (id, code, product_item, planned_qty, status, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		o.ID, o.Code, o.ProductItemID, o.PlannedQty, string(o.Status), o.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_prod_orders_code_key") {
		return apperr.New(apperr.KindConflict, "order_exists", "a production order with this code already exists")
	}
	return err
}

func (r *Repository) Order(ctx context.Context, id uuid.UUID, forUpdate bool) (inventory.Order, error) {
	sql := `SELECT ` + orderCols + ` FROM mz_prod_orders WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[orderRow](ctx, r.db.Querier(ctx), sql, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return inventory.Order{}, apperr.NotFound("production order")
	}
	return x.domain(), err
}

func (r *Repository) Orders(ctx context.Context, status inventory.OrderStatus) ([]inventory.Order, error) {
	rows, err := postgres.Select[orderRow](ctx, r.db.Querier(ctx), `SELECT `+orderCols+` FROM mz_prod_orders WHERE ($1 = '' OR status = $1) ORDER BY code`, string(status))
	out := make([]inventory.Order, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) UpdateOrder(ctx context.Context, o *inventory.Order) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_prod_orders SET status = $2, material_cost = $3, labor_cost = $4, fixed_overhead = $5, var_overhead = $6, machine_units = $7,
		output_qty = $8, abnormal_loss = $9, finished_cost = $10, completed_on = $11, complete_key = $12 WHERE id = $1`,
		o.ID, string(o.Status), o.MaterialCost, o.LaborCost, o.FixedOverhead, o.VarOverhead, o.MachineUnits, o.OutputQty, o.AbnormalLoss, o.FinishedCost, o.CompletedOn, o.CompleteKey)
	return err
}

func (r *Repository) InsertCostLine(ctx context.Context, l inventory.CostLine) (inventory.CostLine, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_prod_cost_lines (order_id, kind, key, item_id, qty, amount, ref) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (key) DO NOTHING`,
		l.OrderID, l.Kind, l.Key, l.ItemID, l.Qty, l.Amount, l.Ref)
	return l, n == 1, err
}

func (r *Repository) InsertPoolEvent(ctx context.Context, e inventory.PoolEvent) (bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_prod_pool_events (kind, key, amount, entry_date, ref) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (key) DO NOTHING`, e.Kind, e.Key, e.Amount, e.Date, e.Ref)
	return n == 1, err
}

func (r *Repository) SettlePool(ctx context.Context, key string) (int64, int64, int, error) {
	rows, err := postgres.Select[struct {
		Kind   string `db:"kind"`
		Amount int64  `db:"amount"`
	}](ctx, r.db.Querier(ctx), `UPDATE mz_prod_pool_events SET settled_in = $1 WHERE settled_in IS NULL RETURNING kind, amount`, key)
	var actual, applied int64
	for _, x := range rows {
		if x.Kind == "actual" {
			actual += x.Amount
		} else {
			applied += x.Amount
		}
	}
	return actual, applied, len(rows), err
}

func (r *Repository) SettlementByKey(ctx context.Context, key string) (inventory.Settlement, bool, error) {
	rows, err := postgres.Select[struct {
		Date    time.Time  `db:"entry_date"`
		Actual  int64      `db:"actual"`
		Applied int64      `db:"applied"`
		Journal *uuid.UUID `db:"journal_id"`
	}](ctx, r.db.Querier(ctx), `SELECT entry_date, actual, applied, journal_id FROM mz_prod_settlements WHERE key = $1`, key)
	if err != nil || len(rows) == 0 {
		return inventory.Settlement{}, false, err
	}
	x := rows[0]
	return inventory.Settlement{Key: key, Date: x.Date, Actual: x.Actual, Applied: x.Applied, Journal: x.Journal}, true, nil
}

func (r *Repository) InsertSettlement(ctx context.Context, s inventory.Settlement) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_prod_settlements (key, entry_date, actual, applied, journal_id) VALUES ($1,$2,$3,$4,$5)`, s.Key, s.Date, s.Actual, s.Applied, s.Journal)
	return err
}
