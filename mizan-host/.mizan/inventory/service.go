package inventory

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Accounts are the ledger accounts inventory posts to. The composition root creates them.
type Accounts struct {
	Inventory  string // asset: stock at carrying value
	COGS       string // expense: cost of goods sold (default of issues)
	Adjustment string // expense: count losses and write-downs; count gains are credited to it
	Payable    string // default counter account of receipts
	VATInput   string // asset: deductible input tax of purchases (required only when a receipt carries VAT)
	// Production costing (required only for orders): work in process, the overhead pool (a clearing account: Dr actual, Cr applied) and the expense of the variance.
	WIP                string
	Overhead           string
	ProductionVariance string
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
}

// Service is the inventory application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) amt(v int64) money.Money { return money.Must(v, s.cfg.Currency) }

func (s *Service) note(ctx context.Context, actor, action string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "inventory_item", EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "inventory_item", AggregateID: id.String(), Payload: details})
}

func (s *Service) day(t time.Time) (time.Time, error) {
	today := dateOnly(s.books.Today())
	if t.IsZero() {
		return today, nil
	}
	if t = dateOnly(t); t.After(today) {
		return time.Time{}, apperr.InvalidField("date", "must not be in the future")
	}
	return t, nil
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// CreateItem adds an item with no stock.
func (s *Service) CreateItem(ctx context.Context, in ItemInput) (Item, error) {
	in, err := in.validate()
	if err != nil {
		return Item{}, err
	}
	it := Item{ID: uuid.New(), Code: in.Code, Name: in.Name, Unit: in.Unit, Method: in.Method, FEFO: in.FEFO, Value: s.amt(0), Active: true, CreatedAt: s.clock()}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertItem(ctx, it); err != nil {
			return err
		}
		return s.note(ctx, in.Actor, "inventory.item_created", it.ID, "inventory.item.created", map[string]any{"code": it.Code, "method": string(it.Method)})
	})
	return it, err
}

// Item returns an item.
func (s *Service) Item(ctx context.Context, id uuid.UUID) (Item, error) {
	return s.repo.Item(ctx, id, false)
}

// Items lists the items.
func (s *Service) Items(ctx context.Context) ([]Item, error) { return s.repo.Items(ctx) }

// SetItemActive retires or restores an item for new receipts.
func (s *Service) SetItemActive(ctx context.Context, id uuid.UUID, active bool) error {
	return s.repo.SetItemActive(ctx, id, active)
}

// Movements lists the stock movements of an item.
func (s *Service) Movements(ctx context.Context, id uuid.UUID) ([]Movement, error) {
	if _, err := s.repo.Item(ctx, id, false); err != nil {
		return nil, err
	}
	return s.repo.Movements(ctx, id)
}

// Valuation reports the stock on hand per item. Its total equals the balance of the inventory account.
func (s *Service) Valuation(ctx context.Context) (Valuation, error) {
	items, err := s.repo.Items(ctx)
	if err != nil {
		return Valuation{}, err
	}
	v := Valuation{Items: items}
	for _, it := range items {
		v.TotalValue += it.Value.Amount()
	}
	return v, nil
}

// movement runs one stock movement under the item's row lock: a repeat of the same key returns the stored movement, otherwise
// apply changes the stock and returns the movement and its ledger lines (nil = no journal).
func (s *Service) movement(ctx context.Context, key string, itemID uuid.UUID, actor string, apply func(ctx context.Context, it *Item) (Movement, []books.Line, string, error)) (Movement, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 120 {
		return Movement{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	var out Movement
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		it, err := s.repo.Item(ctx, itemID, true)
		if err != nil {
			return err
		}
		if m, ok, err := s.repo.MovementByKey(ctx, key); err != nil {
			return err
		} else if ok {
			if m.ItemID != itemID {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different item")
			}
			out = m
			return nil
		}
		qtyBefore := it.Qty
		m, lines, desc, err := apply(ctx, &it)
		if err != nil {
			return err
		}
		if (m.Kind == Issue || m.Kind == CountLoss) && qtyBefore > 0 && it.Qty < qtyBefore {
			it.WritedownBalance = roundDiv(it.WritedownBalance, it.Qty, qtyBefore) // the written-down units that left take their share of the write-down with them
		}
		m.ItemID, m.IdemKey, m.Actor, m.CreatedAt = itemID, key, actor, s.clock()
		m.QtyAfter, m.ValueAfter = it.Qty, it.Value.Amount()
		if len(lines) > 0 {
			jid, err := s.books.Post(ctx, books.Entry{Key: "inventory:" + key, Date: m.Date, Description: desc, RefType: "inventory_item", RefID: itemID.String(), Actor: actor, Lines: lines})
			if err != nil {
				return err
			}
			m.Journal = &jid
		}
		if err := s.repo.UpdateBalance(ctx, itemID, it.Qty, it.Value.Amount(), it.WritedownBalance); err != nil {
			return err
		}
		if err := s.repo.InsertMovement(ctx, &m); err != nil {
			return err
		}
		out = m
		return s.note(ctx, actor, "inventory."+string(m.Kind), itemID, "inventory.stock.changed", map[string]any{"kind": string(m.Kind), "qty": m.Qty, "value": m.Value, "qty_after": m.QtyAfter})
	})
	return out, err
}

// Receive books stock coming in: Dr inventory, Cr the counter account, for the whole cost of the receipt.
func (s *Service) Receive(ctx context.Context, in ReceiveInput) (Movement, error) {
	if in.Qty <= 0 {
		return Movement{}, apperr.InvalidField("quantity", "must be positive")
	}
	if in.TotalCost.Currency() != s.cfg.Currency || in.TotalCost.Amount() <= 0 {
		return Movement{}, apperr.InvalidField("total_cost", "must be a positive amount in "+string(s.cfg.Currency))
	}
	vat := int64(0)
	if in.VAT.Currency() != "" {
		if in.VAT.Currency() != s.cfg.Currency || in.VAT.Amount() < 0 {
			return Movement{}, apperr.InvalidField("vat", "must be zero or a positive amount in "+string(s.cfg.Currency))
		}
		vat = in.VAT.Amount()
	}
	if vat > 0 && s.cfg.Accounts.VATInput == "" {
		return Movement{}, apperr.Invalid("no input-VAT account is configured")
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	counter := in.CounterAccount
	if counter == "" {
		counter = s.cfg.Accounts.Payable
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		if !it.Active {
			return Movement{}, nil, "", apperr.Conflict("the item is not active")
		}
		if (strings.TrimSpace(in.LotNo) != "" || in.ExpiresOn != nil) && !it.Method.Layered() {
			return Movement{}, nil, "", apperr.InvalidField("lot_no", "lots and expiry dates need a layered method (fifo or specific_identification)")
		}
		wh, err := s.warehouse(ctx, in.WarehouseID)
		if err != nil {
			return Movement{}, nil, "", err
		}
		m := Movement{Kind: Receipt, Qty: in.Qty, Value: in.TotalCost.Amount(), Date: date, Ref: in.Ref, WarehouseID: &wh}
		if it.Method.Layered() {
			id, err := s.repo.InsertLayer(ctx, Layer{ItemID: it.ID, Qty: in.Qty, Remaining: in.Qty, RemainingValue: in.TotalCost.Amount(), WarehouseID: wh, LotNo: strings.TrimSpace(in.LotNo), ExpiresOn: in.ExpiresOn})
			if err != nil {
				return Movement{}, nil, "", err
			}
			m.LotID = &id
		}
		if err := s.repo.AddStock(ctx, it.ID, wh, in.Qty); err != nil {
			return Movement{}, nil, "", err
		}
		it.Qty += in.Qty
		it.Value = s.amt(it.Value.Amount() + in.TotalCost.Amount())
		lines := []books.Line{books.Dr(s.cfg.Accounts.Inventory, in.TotalCost)}
		if vat > 0 {
			lines = append(lines, books.Dr(s.cfg.Accounts.VATInput, s.amt(vat)))
		}
		lines = append(lines, books.Cr(counter, s.amt(in.TotalCost.Amount()+vat)))
		return m, lines, "Receipt of stock " + it.Code, nil
	})
}

// issueCost computes the cost of taking qty out of a warehouse and updates the layers and the warehouse stock. lotID names the lot (required for specific_identification).
func (s *Service) issueCost(ctx context.Context, it *Item, qty int64, wh uuid.UUID, lotID int64) (int64, error) {
	if qty > it.Qty {
		return 0, errInsufficient()
	}
	here, err := s.repo.StockQty(ctx, it.ID, wh)
	if err != nil {
		return 0, err
	}
	if qty > here {
		return 0, apperr.New(apperr.KindConflict, "insufficient_stock", "the warehouse holds less than the quantity asked for")
	}
	if err := s.repo.AddStock(ctx, it.ID, wh, -qty); err != nil {
		return 0, err
	}
	if it.Method == WeightedAverage {
		return AverageIssue(it.Qty, it.Value.Amount(), qty)
	}
	layers, err := s.repo.Layers(ctx, it.ID, wh, it.FEFO)
	if err != nil {
		return 0, err
	}
	if it.Method == SpecificID {
		if lotID == 0 {
			return 0, apperr.InvalidField("lot_id", "an item costed by specific identification needs the lot it is taken from")
		}
		var one []Layer
		for _, l := range layers {
			if l.ID == lotID {
				one = append(one, l)
			}
		}
		if len(one) == 0 {
			return 0, apperr.New(apperr.KindConflict, "lot_not_in_warehouse", "that lot has no stock in the warehouse")
		}
		layers = one
	}
	cost, after, err := FIFOIssue(layers, qty)
	if err != nil {
		return 0, err
	}
	return cost, s.repo.UpdateLayers(ctx, after)
}

// Issue takes stock out at cost (sale, consumption): Dr the expense account (cost of goods sold by default), Cr inventory. An
// issue that exceeds the stock on hand is refused: stock never goes negative.
func (s *Service) Issue(ctx context.Context, in IssueInput) (Movement, error) {
	if in.Qty <= 0 {
		return Movement{}, apperr.InvalidField("quantity", "must be positive")
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	expense := in.ExpenseAccount
	if expense == "" {
		expense = s.cfg.Accounts.COGS
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		wh, err := s.warehouse(ctx, in.WarehouseID)
		if err != nil {
			return Movement{}, nil, "", err
		}
		cost, err := s.issueCost(ctx, it, in.Qty, wh, in.LotID)
		if err != nil {
			return Movement{}, nil, "", err
		}
		it.Qty -= in.Qty
		it.Value = s.amt(it.Value.Amount() - cost)
		m := Movement{Kind: Issue, Qty: -in.Qty, Value: -cost, Date: date, Ref: in.Ref, Note: in.Reason, WarehouseID: &wh}
		if in.LotID != 0 {
			m.LotID = &in.LotID
		}
		if cost == 0 {
			return m, nil, "", nil
		}
		return m, []books.Line{books.Dr(expense, s.amt(cost)), books.Cr(s.cfg.Accounts.Inventory, s.amt(cost))}, "Issue of stock " + it.Code, nil
	})
}

// Count corrects the quantity to what was counted. A shortage is issued at cost to the adjustment account; a surplus is received at
// the item's average cost (so it cannot create value out of nothing: an item with no stock has no cost to give a surplus).
func (s *Service) Count(ctx context.Context, in CountInput) (Movement, error) {
	if in.CountedQty < 0 {
		return Movement{}, apperr.InvalidField("counted_quantity", "must not be negative")
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		wh, err := s.warehouse(ctx, in.WarehouseID)
		if err != nil {
			return Movement{}, nil, "", err
		}
		here, err := s.repo.StockQty(ctx, it.ID, wh)
		if err != nil {
			return Movement{}, nil, "", err
		}
		diff := in.CountedQty - here
		switch {
		case diff < 0:
			if it.Method == SpecificID {
				return Movement{}, nil, "", apperr.New(apperr.KindConflict, "count_specific", "a shortage of a specifically identified item must be issued from its lot: issue it with the lot and reason")
			}
			cost, err := s.issueCost(ctx, it, -diff, wh, 0)
			if err != nil {
				return Movement{}, nil, "", err
			}
			it.Qty += diff
			it.Value = s.amt(it.Value.Amount() - cost)
			m := Movement{Kind: CountLoss, Qty: diff, Value: -cost, Date: date, Note: in.Reason, WarehouseID: &wh}
			if cost == 0 {
				return m, nil, "", nil
			}
			return m, []books.Line{books.Dr(s.cfg.Accounts.Adjustment, s.amt(cost)), books.Cr(s.cfg.Accounts.Inventory, s.amt(cost))}, "Stock count shortage " + it.Code, nil
		case diff > 0:
			if it.Qty == 0 || it.Value.Amount() == 0 {
				return Movement{}, nil, "", apperr.New(apperr.KindConflict, "no_cost_basis", "an item with no stock has no cost to value a surplus: receive it with its cost instead")
			}
			value := roundDiv(it.Value.Amount(), diff, it.Qty)
			if value == 0 {
				return Movement{}, nil, "", apperr.New(apperr.KindConflict, "no_cost_basis", "the surplus is worth nothing at the average cost")
			}
			if it.Method.Layered() {
				if _, err := s.repo.InsertLayer(ctx, Layer{ItemID: it.ID, Qty: diff, Remaining: diff, RemainingValue: value, WarehouseID: wh}); err != nil {
					return Movement{}, nil, "", err
				}
			}
			if err := s.repo.AddStock(ctx, it.ID, wh, diff); err != nil {
				return Movement{}, nil, "", err
			}
			it.Qty += diff
			it.Value = s.amt(it.Value.Amount() + value)
			m := Movement{Kind: CountGain, Qty: diff, Value: value, Date: date, Note: in.Reason, WarehouseID: &wh}
			return m, []books.Line{books.Dr(s.cfg.Accounts.Inventory, s.amt(value)), books.Cr(s.cfg.Accounts.Adjustment, s.amt(value))}, "Stock count surplus " + it.Code, nil
		}
		return Movement{Kind: CountGain, Date: date, Note: in.Reason}, nil, "", nil
	})
}

// WriteDown lowers the carrying value of the stock to its net realizable value (standard no. 8: lower of cost and net realizable
// value, item by item): Dr the adjustment account, Cr inventory. A value that is not below the carrying value is refused; ReverseWriteDown undoes it later.
func (s *Service) WriteDown(ctx context.Context, in WriteDownInput) (Movement, error) {
	if in.NetRealizableValue.Currency() != s.cfg.Currency || in.NetRealizableValue.Amount() < 0 {
		return Movement{}, apperr.InvalidField("net_realizable_value", "must be zero or a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		cut := it.Value.Amount() - in.NetRealizableValue.Amount()
		if cut <= 0 {
			return Movement{}, nil, "", apperr.New(apperr.KindConflict, "no_write_down_needed", "the net realizable value is not below the carrying value")
		}
		if it.Method.Layered() {
			layers, err := s.repo.Layers(ctx, it.ID, uuid.Nil, false)
			if err != nil {
				return Movement{}, nil, "", err
			}
			if err := s.repo.UpdateLayers(ctx, CutValue(layers, in.NetRealizableValue.Amount())); err != nil {
				return Movement{}, nil, "", err
			}
		}
		it.Value = in.NetRealizableValue
		it.WritedownBalance += cut
		m := Movement{Kind: WriteDown, Value: -cut, Date: date, Note: in.Reason}
		return m, []books.Line{books.Dr(s.cfg.Accounts.Adjustment, s.amt(cut)), books.Cr(s.cfg.Accounts.Inventory, s.amt(cut))}, "Write-down of stock to net realizable value " + it.Code, nil
	})
}

// ReverseWriteDown raises the carrying value of the stock toward a higher net realizable value (standard no. 8 para. 46: the increase is recognised as income or a
// reduction of the expense, at most up to the earlier reduction): Dr inventory, Cr the adjustment account. The raise is the smaller of the new value minus the
// carrying value and the write-down still standing; nothing to reverse is a conflict. For FIFO items the raise is spread over the open layers by quantity (the cap is on the
// item's total, layer costs are not tracked separately: a documented approximation).
func (s *Service) ReverseWriteDown(ctx context.Context, in ReverseWriteDownInput) (Movement, error) {
	if in.NetRealizableValue.Currency() != s.cfg.Currency || in.NetRealizableValue.Amount() < 0 {
		return Movement{}, apperr.InvalidField("net_realizable_value", "must be zero or a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Movement{}, err
	}
	return s.movement(ctx, in.IdemKey, in.ItemID, in.Actor, func(ctx context.Context, it *Item) (Movement, []books.Line, string, error) {
		raise := min(in.NetRealizableValue.Amount()-it.Value.Amount(), it.WritedownBalance)
		if raise <= 0 {
			return Movement{}, nil, "", apperr.New(apperr.KindConflict, "nothing_to_reverse", "the net realizable value is not above the carrying value, or no write-down stands")
		}
		if it.Method.Layered() {
			layers, err := s.repo.Layers(ctx, it.ID, uuid.Nil, false)
			if err != nil {
				return Movement{}, nil, "", err
			}
			if err := s.repo.UpdateLayers(ctx, RaiseValue(layers, raise)); err != nil {
				return Movement{}, nil, "", err
			}
		}
		it.Value = s.amt(it.Value.Amount() + raise)
		it.WritedownBalance -= raise
		m := Movement{Kind: ReverseWriteDown, Value: raise, Date: date, Note: in.Reason}
		return m, []books.Line{books.Dr(s.cfg.Accounts.Inventory, s.amt(raise)), books.Cr(s.cfg.Accounts.Adjustment, s.amt(raise))}, "Reversal of a stock write-down " + it.Code, nil
	})
}
