//nolint:staticcheck // the Persian text uses the zero-width non-joiner (U+200C), which is correct orthography
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

// Production costing, after accounting standard no. 8 (read from the official text, paras 9-20):
//   - the cost of conversion is the direct labour plus a systematic allocation of fixed and variable production overhead (paras 9-10);
//   - fixed overhead is allocated on the NORMAL capacity, variable overhead on the actual use; the cost of unused capacity is an expense of the period, not added to the stock
//     (para 11): the allocation RATE is therefore versioned data (OverheadRate) derived from a budget and a normal capacity, and what the pool of actual overhead holds beyond
//     what was applied to orders is expensed at settlement;
//   - abnormal waste, storage not needed for production, general administration and selling costs are not part of the cost (paras 17-19): they never enter an order;
//     abnormal losses of an order are expensed on completion.
//
// Flow: an order collects materials (issued from stock at their cost, Dr work in process) and labour (Dr WIP, Cr payable); on completion overhead is applied at the rate in force
// (Dr WIP, Cr overhead pool), the abnormal loss is expensed, and the rest becomes the cost of the finished goods lot (Dr inventory, Cr WIP). The actual overhead is recorded in
// the pool (Dr pool, Cr payable/bank); SettleOverhead expenses the difference between actual and applied.

// Basis is what the overhead rate is applied to.
type Basis string

const (
	BasisLaborCost    Basis = "direct_labor_cost"
	BasisMaterialCost Basis = "material_cost"
	BasisMachineUnits Basis = "machine_units" // machine hours (or any usage unit) given at completion
	BasisOutputUnits  Basis = "output_units"
)

// Valid reports whether b is known.
func (b Basis) Valid() bool {
	return b == BasisLaborCost || b == BasisMaterialCost || b == BasisMachineUnits || b == BasisOutputUnits
}

// Monetary reports whether the base is an amount (the rate is then in basis points of it) rather than a quantity (the rate is rials per unit).
func (b Basis) Monetary() bool { return b == BasisLaborCost || b == BasisMaterialCost }

// OverheadRate is a version of the overhead allocation rates, effective from a day.
type OverheadRate struct {
	ID            uuid.UUID
	EffectiveFrom time.Time
	Basis         Basis
	FixedRate     int64 // basis points of the base (monetary bases) or rials per unit
	VariableRate  int64
	BudgetFixed   int64 // informational: the budgeted fixed overhead the fixed rate was derived from
	NormalCap     int64 // informational: the normal capacity in base units (rials for monetary bases)
	Verified      bool
	Source        string
	CreatedAt     time.Time
}

// DeriveFixedRate is the fixed-overhead rate of a budget over the normal capacity (para 11): rials per unit for a quantity basis, basis points of the base for a monetary one.
func DeriveFixedRate(b Basis, budget, normalCapacity int64) (int64, error) {
	if budget < 0 || normalCapacity <= 0 {
		return 0, apperr.Invalid("the budget must not be negative and the normal capacity must be positive")
	}
	if b.Monetary() {
		return roundDiv(budget, 10000, normalCapacity), nil
	}
	return roundDiv(budget, 1, normalCapacity), nil
}

// Apply returns the fixed and variable overhead for an order's actual use.
func (r OverheadRate) Apply(materialCost, laborCost, machineUnits, outputQty int64) (fixed, variable int64) {
	var base int64
	switch r.Basis {
	case BasisLaborCost:
		base = laborCost
	case BasisMaterialCost:
		base = materialCost
	case BasisMachineUnits:
		base = machineUnits
	case BasisOutputUnits:
		base = outputQty
	}
	div := int64(1)
	if r.Basis.Monetary() {
		div = 10000
	}
	return roundDiv(base, r.FixedRate, div), roundDiv(base, r.VariableRate, div)
}

// OrderStatus of a production order.
type OrderStatus string

const (
	OrderOpen      OrderStatus = "open"
	OrderCompleted OrderStatus = "completed"
)

// Order is a production order: the work in process of one product run.
type Order struct {
	ID            uuid.UUID
	Code          string
	ProductItemID uuid.UUID
	PlannedQty    int64
	Status        OrderStatus
	MaterialCost  int64
	LaborCost     int64
	FixedOverhead int64
	VarOverhead   int64
	MachineUnits  int64
	OutputQty     int64
	AbnormalLoss  int64
	FinishedCost  int64 // cost of the finished goods lot
	CompletedOn   *time.Time
	CompleteKey   string
	CreatedAt     time.Time
}

// WIP is the cost an order carries while it is open.
func (o Order) WIP() int64 {
	if o.Status != OrderOpen {
		return 0
	}
	return o.MaterialCost + o.LaborCost
}

// CostLine is one material issue or labour charge of an order (idempotency keyed).
type CostLine struct {
	ID      int64
	OrderID uuid.UUID
	Kind    string // "material" or "labor"
	Key     string
	ItemID  *uuid.UUID
	Qty     int64
	Amount  int64
	Ref     string
}

// PoolEvent moves the overhead pool: actual overhead recorded, or overhead applied to an order.
type PoolEvent struct {
	Kind   string // "actual" or "applied"
	Key    string
	Amount int64
	Date   time.Time
	Ref    string
}

// Settlement is the expensing of the pool's under- or over-absorbed overhead.
type Settlement struct {
	Key     string
	Date    time.Time
	Actual  int64
	Applied int64
	Journal *uuid.UUID
}

// ProductionRepository is the persistence port of production costing (implemented by the same adapter as Repository).
type ProductionRepository interface {
	InsertOverheadRate(ctx context.Context, r OverheadRate) error
	OverheadRates(ctx context.Context) ([]OverheadRate, error)
	InsertOrder(ctx context.Context, o Order) error
	Order(ctx context.Context, id uuid.UUID, forUpdate bool) (Order, error)
	Orders(ctx context.Context, status OrderStatus) ([]Order, error)
	UpdateOrder(ctx context.Context, o *Order) error
	// InsertCostLine stores a line; a taken key returns the stored line with created=false.
	InsertCostLine(ctx context.Context, l CostLine) (stored CostLine, created bool, err error)
	// InsertPoolEvent stores an event; a taken key returns created=false.
	InsertPoolEvent(ctx context.Context, e PoolEvent) (created bool, err error)
	// SettlePool marks every unsettled pool event as settled by the key and returns their sums.
	SettlePool(ctx context.Context, key string) (actual, applied int64, n int, err error)
	SettlementByKey(ctx context.Context, key string) (Settlement, bool, error)
	InsertSettlement(ctx context.Context, s Settlement) error
}

func (s *Service) prodRepo() (ProductionRepository, error) {
	p, ok := s.repo.(ProductionRepository)
	if !ok {
		return nil, apperr.New(apperr.KindConflict, "no_production", "production costing is not available with this repository")
	}
	return p, nil
}

func (s *Service) noteOrder(ctx context.Context, actor, action string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "production_order", EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "production_order", AggregateID: id.String(), Payload: details})
}

// AddOverheadRate stores a version of the overhead rates (immutable). When FixedRate is zero and a budget and normal capacity are given the fixed rate is derived from them (para 11).
func (s *Service) AddOverheadRate(ctx context.Context, r OverheadRate, actor string) (OverheadRate, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return OverheadRate{}, err
	}
	if r.EffectiveFrom.IsZero() || !r.Basis.Valid() || strings.TrimSpace(r.Source) == "" || r.VariableRate < 0 || r.FixedRate < 0 {
		return OverheadRate{}, apperr.Invalid("effective_from, a valid basis, a source and non-negative rates are required")
	}
	if r.FixedRate == 0 && r.BudgetFixed > 0 {
		if r.FixedRate, err = DeriveFixedRate(r.Basis, r.BudgetFixed, r.NormalCap); err != nil {
			return OverheadRate{}, err
		}
	}
	r.ID, r.EffectiveFrom, r.Source, r.CreatedAt = uuid.New(), dateOnly(r.EffectiveFrom), strings.TrimSpace(r.Source), s.clock()
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := pr.InsertOverheadRate(ctx, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "production.overhead_rate_added", EntityType: "production_overhead_rate", EntityID: r.ID.String(),
			Details: map[string]any{"basis": string(r.Basis), "fixed_rate": r.FixedRate, "variable_rate": r.VariableRate}})
	})
	return r, err
}

// OverheadRates lists the versions.
func (s *Service) OverheadRates(ctx context.Context) ([]OverheadRate, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return nil, err
	}
	return pr.OverheadRates(ctx)
}

// rateAt is the version in force on a day.
func (s *Service) rateAt(ctx context.Context, pr ProductionRepository, day time.Time) (OverheadRate, bool, error) {
	rs, err := pr.OverheadRates(ctx)
	if err != nil {
		return OverheadRate{}, false, err
	}
	var best OverheadRate
	found := false
	for _, r := range rs {
		if !r.EffectiveFrom.After(day) && (!found || r.EffectiveFrom.After(best.EffectiveFrom)) {
			best, found = r, true
		}
	}
	return best, found, nil
}

// OpenOrder starts a production order for a product item.
func (s *Service) OpenOrder(ctx context.Context, code string, product uuid.UUID, plannedQty int64, actor string) (Order, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return Order{}, err
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 60 || plannedQty <= 0 {
		return Order{}, apperr.Invalid("code (at most 60 characters) and a positive planned quantity are required")
	}
	if s.cfg.Accounts.WIP == "" {
		return Order{}, apperr.New(apperr.KindConflict, "no_wip_account", "no work-in-process account configured")
	}
	o := Order{ID: uuid.New(), Code: code, ProductItemID: product, PlannedQty: plannedQty, Status: OrderOpen, CreatedAt: s.clock()}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.Item(ctx, product, false); err != nil {
			return err
		}
		if err := pr.InsertOrder(ctx, o); err != nil {
			return err
		}
		return s.noteOrder(ctx, actor, "production.order_opened", o.ID, "production.order.opened", map[string]any{"code": code, "planned_qty": plannedQty})
	})
	return o, err
}

// Order returns an order.
func (s *Service) Order(ctx context.Context, id uuid.UUID) (Order, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return Order{}, err
	}
	return pr.Order(ctx, id, false)
}

// Orders lists orders (all when status is empty).
func (s *Service) Orders(ctx context.Context, status OrderStatus) ([]Order, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return nil, err
	}
	return pr.Orders(ctx, status)
}

// WorkInProcess is the total carried by the open orders: it equals the balance of the work-in-process account.
func (s *Service) WorkInProcess(ctx context.Context) (int64, error) {
	os, err := s.Orders(ctx, OrderOpen)
	if err != nil {
		return 0, err
	}
	var t int64
	for _, o := range os {
		t += o.WIP()
	}
	return t, nil
}

// IssueMaterialInput takes stock into an order.
type IssueMaterialInput struct {
	IdemKey     string
	ItemID      uuid.UUID
	Qty         int64
	WarehouseID uuid.UUID
	LotID       int64
	Date        time.Time
	Actor       string
}

// IssueMaterial issues stock to an open order at its cost: Dr work in process, Cr inventory. Idempotent by key.
func (s *Service) IssueMaterial(ctx context.Context, orderID uuid.UUID, in IssueMaterialInput) (Movement, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return Movement{}, err
	}
	var out Movement
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		o, err := pr.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if o.Status != OrderOpen {
			return apperr.Conflict("the order is completed")
		}
		mv, err := s.Issue(ctx, IssueInput{IdemKey: in.IdemKey, ItemID: in.ItemID, Qty: in.Qty, Date: in.Date, ExpenseAccount: s.cfg.Accounts.WIP, Ref: "order:" + o.Code,
			Reason: "production", Actor: in.Actor, WarehouseID: in.WarehouseID, LotID: in.LotID})
		if err != nil {
			return err
		}
		out = mv
		item := in.ItemID
		_, created, err := pr.InsertCostLine(ctx, CostLine{OrderID: o.ID, Kind: "material", Key: "issue:" + in.IdemKey, ItemID: &item, Qty: in.Qty, Amount: -mv.Value, Ref: o.Code})
		if err != nil || !created {
			return err // a retry: the cost is already in the order
		}
		o.MaterialCost += -mv.Value
		if err := pr.UpdateOrder(ctx, &o); err != nil {
			return err
		}
		return s.noteOrder(ctx, in.Actor, "production.material_issued", o.ID, "production.order.material_issued", map[string]any{"item_id": item.String(), "qty": in.Qty, "cost": -mv.Value})
	})
	return out, err
}

// AddLaborInput charges direct labour to an order.
type AddLaborInput struct {
	IdemKey        string
	Amount         money.Money
	CounterAccount string // credited (default the payables account); the payroll module's accounts are the usual ones
	Date           time.Time
	Actor          string
}

// AddLabor charges direct labour to an open order: Dr work in process, Cr the counter account. Idempotent by key.
func (s *Service) AddLabor(ctx context.Context, orderID uuid.UUID, in AddLaborInput) error {
	pr, err := s.prodRepo()
	if err != nil {
		return err
	}
	key := strings.TrimSpace(in.IdemKey)
	if key == "" || len(key) > 120 {
		return apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if in.Amount.Currency() != s.cfg.Currency || !in.Amount.IsPositive() {
		return apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date)
	if err != nil {
		return err
	}
	counter := in.CounterAccount
	if counter == "" {
		counter = s.cfg.Accounts.Payable
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		o, err := pr.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if o.Status != OrderOpen {
			return apperr.Conflict("the order is completed")
		}
		_, created, err := pr.InsertCostLine(ctx, CostLine{OrderID: o.ID, Kind: "labor", Key: "labor:" + key, Amount: in.Amount.Amount(), Ref: o.Code})
		if err != nil || !created {
			return err
		}
		if _, err := s.books.Post(ctx, books.Entry{Key: "production:labor:" + key, Date: date, Description: "Direct labour to production order " + o.Code, RefType: "production_order", RefID: o.ID.String(),
			Actor: in.Actor, Lines: []books.Line{books.Dr(s.cfg.Accounts.WIP, in.Amount), books.Cr(counter, in.Amount)}}); err != nil {
			return err
		}
		o.LaborCost += in.Amount.Amount()
		if err := pr.UpdateOrder(ctx, &o); err != nil {
			return err
		}
		return s.noteOrder(ctx, in.Actor, "production.labor_added", o.ID, "production.order.labor_added", map[string]any{"amount": in.Amount.Amount()})
	})
}

// CompleteInput finishes an order.
type CompleteInput struct {
	IdemKey      string
	OutputQty    int64
	MachineUnits int64 // for the machine_units basis
	// AbnormalLoss is the part of the accumulated cost that is an abnormal loss (waste beyond the normal, para 18): expensed, not put into the finished goods.
	AbnormalLoss int64
	Date         time.Time
	WarehouseID  uuid.UUID
	LotNo        string
	ExpiresOn    *time.Time
	// NoOverhead completes the order without overhead even though no rate applies (otherwise a missing rate is refused: overhead must not be forgotten silently).
	NoOverhead bool
	Actor      string
}

// CompleteOrder applies the overhead, expenses an abnormal loss and receives the finished goods at the order's cost. Idempotent by key: a retry returns the completed order.
func (s *Service) CompleteOrder(ctx context.Context, orderID uuid.UUID, in CompleteInput) (Order, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return Order{}, err
	}
	key := strings.TrimSpace(in.IdemKey)
	if key == "" || len(key) > 120 {
		return Order{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if in.OutputQty <= 0 || in.MachineUnits < 0 || in.AbnormalLoss < 0 {
		return Order{}, apperr.Invalid("a positive output quantity and non-negative machine units and loss are required")
	}
	date, err := s.day(in.Date)
	if err != nil {
		return Order{}, err
	}
	var out Order
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		o, err := pr.Order(ctx, orderID, true)
		if err != nil {
			return err
		}
		if o.Status == OrderCompleted {
			if o.CompleteKey == key {
				out = o
				return nil
			}
			return apperr.Conflict("the order is already completed")
		}
		rate, found, err := s.rateAt(ctx, pr, date)
		if err != nil {
			return err
		}
		var fixed, variable int64
		switch {
		case found:
			fixed, variable = rate.Apply(o.MaterialCost, o.LaborCost, in.MachineUnits, in.OutputQty)
		case !in.NoOverhead:
			return apperr.New(apperr.KindConflict, "no_overhead_rate", "no overhead rate applies on that date: add one, or complete with no_overhead")
		}
		overhead := fixed + variable
		total := o.MaterialCost + o.LaborCost + overhead
		if in.AbnormalLoss > total {
			return apperr.InvalidField("abnormal_loss", "more than the cost of the order")
		}
		finished := total - in.AbnormalLoss
		if finished <= 0 {
			return apperr.Conflict("the order carries no cost to put into the finished goods")
		}
		if overhead > 0 {
			if _, err := s.books.Post(ctx, books.Entry{Key: "production:" + o.ID.String() + ":overhead", Date: date, Description: "Overhead applied to production order " + o.Code, RefType: "production_order",
				RefID: o.ID.String(), Actor: in.Actor, Lines: []books.Line{books.Dr(s.cfg.Accounts.WIP, s.amt(overhead)), books.Cr(s.cfg.Accounts.Overhead, s.amt(overhead))}}); err != nil {
				return err
			}
			if _, err := pr.InsertPoolEvent(ctx, PoolEvent{Kind: "applied", Key: "applied:" + o.ID.String(), Amount: overhead, Date: date, Ref: o.Code}); err != nil {
				return err
			}
		}
		if in.AbnormalLoss > 0 {
			if _, err := s.books.Post(ctx, books.Entry{Key: "production:" + o.ID.String() + ":loss", Date: date, Description: "Abnormal loss of production order " + o.Code, RefType: "production_order",
				RefID: o.ID.String(), Actor: in.Actor, Lines: []books.Line{books.Dr(s.cfg.Accounts.Adjustment, s.amt(in.AbnormalLoss)), books.Cr(s.cfg.Accounts.WIP, s.amt(in.AbnormalLoss))}}); err != nil {
				return err
			}
		}
		if _, err := s.Receive(ctx, ReceiveInput{IdemKey: "production:" + o.ID.String() + ":output", ItemID: o.ProductItemID, Qty: in.OutputQty, TotalCost: s.amt(finished), Date: date,
			CounterAccount: s.cfg.Accounts.WIP, Ref: "order:" + o.Code, Actor: in.Actor, WarehouseID: in.WarehouseID, LotNo: in.LotNo, ExpiresOn: in.ExpiresOn}); err != nil {
			return err
		}
		o.Status, o.FixedOverhead, o.VarOverhead, o.MachineUnits, o.OutputQty, o.AbnormalLoss, o.FinishedCost = OrderCompleted, fixed, variable, in.MachineUnits, in.OutputQty, in.AbnormalLoss, finished
		o.CompletedOn, o.CompleteKey = &date, key
		if err := pr.UpdateOrder(ctx, &o); err != nil {
			return err
		}
		out = o
		return s.noteOrder(ctx, in.Actor, "production.order_completed", o.ID, "production.order.completed", map[string]any{"code": o.Code, "output_qty": in.OutputQty, "finished_cost": finished, "overhead": overhead, "abnormal_loss": in.AbnormalLoss})
	})
	return out, err
}

// RecordOverhead books actual production overhead into the pool: Dr overhead pool, Cr the counter account (payables by default). Idempotent by key.
func (s *Service) RecordOverhead(ctx context.Context, key string, amount money.Money, counter, ref string, date time.Time, actor string) error {
	pr, err := s.prodRepo()
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 120 {
		return apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if amount.Currency() != s.cfg.Currency || !amount.IsPositive() {
		return apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	day, err := s.day(date)
	if err != nil {
		return err
	}
	if counter == "" {
		counter = s.cfg.Accounts.Payable
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		created, err := pr.InsertPoolEvent(ctx, PoolEvent{Kind: "actual", Key: "actual:" + key, Amount: amount.Amount(), Date: day, Ref: ref})
		if err != nil || !created {
			return err
		}
		if _, err := s.books.Post(ctx, books.Entry{Key: "production:overhead:" + key, Date: day, Description: "Production overhead incurred " + ref, RefType: "production_overhead", RefID: key, Actor: actor,
			Lines: []books.Line{books.Dr(s.cfg.Accounts.Overhead, amount), books.Cr(counter, amount)}}); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "production.overhead_recorded", EntityType: "production_overhead", EntityID: key, Details: map[string]any{"amount": amount.Amount()}})
	})
}

// SettleOverhead expenses what the pool holds: actual overhead beyond what was applied to orders (the cost of unused capacity, para 11) is debited to the production-variance
// expense; applied beyond actual is credited back to it (an over-absorption an accountant may prefer to spread over the stock and cost of goods sold: flagged in the README).
// Idempotent by key; nothing to settle returns a zero settlement without a journal.
func (s *Service) SettleOverhead(ctx context.Context, key string, date time.Time, actor string) (Settlement, error) {
	pr, err := s.prodRepo()
	if err != nil {
		return Settlement{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 120 {
		return Settlement{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	day, err := s.day(date)
	if err != nil {
		return Settlement{}, err
	}
	if s.cfg.Accounts.ProductionVariance == "" {
		return Settlement{}, apperr.New(apperr.KindConflict, "no_variance_account", "no production-variance account configured")
	}
	var out Settlement
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if st, ok, err := pr.SettlementByKey(ctx, key); err != nil {
			return err
		} else if ok {
			out = st
			return nil
		}
		actual, applied, n, err := pr.SettlePool(ctx, key)
		if err != nil {
			return err
		}
		out = Settlement{Key: key, Date: day, Actual: actual, Applied: applied}
		diff := actual - applied
		if n == 0 || diff == 0 {
			if n > 0 {
				return pr.InsertSettlement(ctx, out)
			}
			return nil
		}
		var lines []books.Line
		if diff > 0 {
			lines = []books.Line{books.Dr(s.cfg.Accounts.ProductionVariance, s.amt(diff)), books.Cr(s.cfg.Accounts.Overhead, s.amt(diff))}
		} else {
			lines = []books.Line{books.Dr(s.cfg.Accounts.Overhead, s.amt(-diff)), books.Cr(s.cfg.Accounts.ProductionVariance, s.amt(-diff))}
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "production:settle:" + key, Date: day, Description: "Settlement of production overhead variance", RefType: "production_overhead", RefID: key, Actor: actor, Lines: lines})
		if err != nil {
			return err
		}
		out.Journal = &jid
		if err := pr.InsertSettlement(ctx, out); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "production.overhead_settled", EntityType: "production_overhead", EntityID: key, Details: map[string]any{"actual": actual, "applied": applied}})
	})
	return out, err
}
