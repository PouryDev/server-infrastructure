package inventory

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertItem stores an item; a taken code fails with a conflict "item_exists".
	InsertItem(ctx context.Context, i Item) error
	// Item returns an item; forUpdate locks the row until the transaction ends (every movement does).
	Item(ctx context.Context, id uuid.UUID, forUpdate bool) (Item, error)
	Items(ctx context.Context) ([]Item, error)
	SetItemActive(ctx context.Context, id uuid.UUID, active bool) error
	UpdateBalance(ctx context.Context, id uuid.UUID, qty, value, writedown int64) error

	// Layers returns the open purchase layers (lots) of a layered item, oldest first, or earliest expiry first when fefo (lots without an expiry last). A nil warehouse means all.
	Layers(ctx context.Context, itemID uuid.UUID, warehouse uuid.UUID, fefo bool) ([]Layer, error)
	// InsertLayer stores a layer and returns its id.
	InsertLayer(ctx context.Context, l Layer) (int64, error)
	UpdateLayers(ctx context.Context, layers []Layer) error

	// Warehouses and the quantity of an item per warehouse (rows are created on first use; the quantity never goes below zero).
	InsertWarehouse(ctx context.Context, w Warehouse) error
	Warehouse(ctx context.Context, id uuid.UUID) (Warehouse, error)
	Warehouses(ctx context.Context) ([]Warehouse, error)
	StockQty(ctx context.Context, itemID, warehouse uuid.UUID) (int64, error)
	AddStock(ctx context.Context, itemID, warehouse uuid.UUID, delta int64) error
	StockOf(ctx context.Context, itemID uuid.UUID) ([]StockRow, error)
	// LotsExpiring lists open lots expiring on or before a day (those without an expiry are never listed), earliest first.
	LotsExpiring(ctx context.Context, onOrBefore time.Time) ([]LotInfo, error)

	MovementByKey(ctx context.Context, key string) (Movement, bool, error)
	InsertMovement(ctx context.Context, m *Movement) error
	Movements(ctx context.Context, itemID uuid.UUID) ([]Movement, error)
}
