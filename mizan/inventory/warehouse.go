package inventory

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
)

// MainWarehouseID is the default warehouse every item starts in (created by the migration); stock that existed before warehouses were introduced sits in it.
var MainWarehouseID = uuid.MustParse("00000000-0000-4000-8000-000000003001")

// Warehouse is a storage location. Stock is tracked per item and warehouse; the cost stays with the item (average) or the lot (fifo, specific identification), so there is one
// inventory account whatever the number of warehouses.
type Warehouse struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Active    bool
	CreatedAt time.Time
}

// StockRow is the quantity of an item in a warehouse.
type StockRow struct {
	WarehouseID uuid.UUID
	Qty         int64
}

// LotInfo is an open lot with its expiry.
type LotInfo struct {
	LotID       int64
	ItemID      uuid.UUID
	ItemCode    string
	WarehouseID uuid.UUID
	LotNo       string
	ExpiresOn   time.Time
	Remaining   int64
	Value       int64
}

// warehouse resolves the default.
func (s *Service) warehouse(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	if id == uuid.Nil {
		return MainWarehouseID, nil
	}
	w, err := s.repo.Warehouse(ctx, id)
	if err != nil {
		return uuid.Nil, err
	}
	if !w.Active {
		return uuid.Nil, apperr.Conflict("the warehouse is not active")
	}
	return w.ID, nil
}

// CreateWarehouse adds a warehouse.
func (s *Service) CreateWarehouse(ctx context.Context, code, name, actor string) (Warehouse, error) {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	if code == "" || len(code) > 40 || name == "" {
		return Warehouse{}, apperr.Invalid("code (at most 40 characters) and name are required")
	}
	w := Warehouse{ID: uuid.New(), Code: code, Name: name, Active: true, CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertWarehouse(ctx, w); err != nil {
			return err
		}
		return s.note(ctx, actor, "inventory.warehouse_created", w.ID, "inventory.warehouse.created", map[string]any{"code": code})
	})
	return w, err
}

// Warehouses lists them.
func (s *Service) Warehouses(ctx context.Context) ([]Warehouse, error) { return s.repo.Warehouses(ctx) }

// StockOf is the quantity of an item per warehouse.
func (s *Service) StockOf(ctx context.Context, itemID uuid.UUID) ([]StockRow, error) {
	if _, err := s.repo.Item(ctx, itemID, false); err != nil {
		return nil, err
	}
	return s.repo.StockOf(ctx, itemID)
}

// ExpiringLots lists the open lots that expire on or before a day (the stock to sell, write down or scrap first).
func (s *Service) ExpiringLots(ctx context.Context, onOrBefore time.Time) ([]LotInfo, error) {
	return s.repo.LotsExpiring(ctx, dateOnly(onOrBefore))
}

// TransferInput moves stock between warehouses.
type TransferInput struct {
	IdemKey string
	ItemID  uuid.UUID
	From    uuid.UUID
	To      uuid.UUID
	Qty     int64
	// LotID names the lot: required for specific_identification items.
	LotID int64
	Date  time.Time
	Note  string
	Actor string
}

// Transfer moves qty of an item from one warehouse to another. There is no journal: the cost travels with the stock (average items keep the item's average, lots keep their cost
// and expiry; the moved part of a lot becomes a new lot in the destination, so FIFO order in the destination follows the transfer order).
func (s *Service) Transfer(ctx context.Context, in TransferInput) (Movement, error) {
	if in.Qty <= 0 {
		return Movement{}, apperr.InvalidField("quantity", "must be positive")
	}
	if in.From == uuid.Nil || in.To == uuid.Nil || in.From == in.To {
		return Movement{}, apperr.InvalidField("to", "from and to must be two different warehouses")
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		from, err := s.warehouse(ctx, in.From)
		if err != nil {
			return Movement{}, nil, "", err
		}
		to, err := s.warehouse(ctx, in.To)
		if err != nil {
			return Movement{}, nil, "", err
		}
		here, err := s.repo.StockQty(ctx, it.ID, from)
		if err != nil {
			return Movement{}, nil, "", err
		}
		if in.Qty > here {
			return Movement{}, nil, "", apperr.New(apperr.KindConflict, "insufficient_stock", "the warehouse holds less than the quantity asked for")
		}
		if it.Method.Layered() {
			layers, err := s.repo.Layers(ctx, it.ID, from, it.FEFO)
			if err != nil {
				return Movement{}, nil, "", err
			}
			if it.Method == SpecificID {
				if in.LotID == 0 {
					return Movement{}, nil, "", apperr.InvalidField("lot_id", "an item costed by specific identification needs the lot to move")
				}
				var one []Layer
				for _, l := range layers {
					if l.ID == in.LotID {
						one = append(one, l)
					}
				}
				if len(one) == 0 {
					return Movement{}, nil, "", apperr.New(apperr.KindConflict, "lot_not_in_warehouse", "that lot has no stock in the warehouse")
				}
				layers = one
			}
			takes, after, err := TakeLayers(layers, in.Qty)
			if err != nil {
				return Movement{}, nil, "", err
			}
			if err := s.repo.UpdateLayers(ctx, after); err != nil {
				return Movement{}, nil, "", err
			}
			for _, t := range takes {
				src := layers[t.Index]
				if _, err := s.repo.InsertLayer(ctx, Layer{ItemID: it.ID, Qty: t.Qty, Remaining: t.Qty, RemainingValue: t.Cost, WarehouseID: to, LotNo: src.LotNo, ExpiresOn: src.ExpiresOn}); err != nil {
					return Movement{}, nil, "", err
				}
			}
		}
		if err := s.repo.AddStock(ctx, it.ID, from, -in.Qty); err != nil {
			return Movement{}, nil, "", err
		}
		if err := s.repo.AddStock(ctx, it.ID, to, in.Qty); err != nil {
			return Movement{}, nil, "", err
		}
		note := strings.TrimSpace(in.Note)
		if note == "" {
			note = "transfer of " + strconv.FormatInt(in.Qty, 10) + " to warehouse " + to.String()
		}
		return Movement{Kind: Transfer, Date: date, Note: note, WarehouseID: &from, Ref: "to:" + to.String()}, nil, "", nil
	})
}
