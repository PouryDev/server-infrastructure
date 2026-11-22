package inventory

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Item is a stocked good or material.
type Item struct {
	ID     uuid.UUID
	Code   string
	Name   string
	Unit   string // base unit: quantities are whole numbers of it
	Method Method
	Qty    int64
	Value  money.Money // carrying value of the stock on hand
	// WritedownBalance is the write-down still standing (not yet reversed) on the stock on hand: what standard 8 allows to be reversed at most. It shrinks in proportion when stock leaves.
	WritedownBalance int64
	// FEFO issues the lot that expires first (first-expired first-out) instead of the oldest purchase: only for the fifo method.
	FEFO      bool
	Active    bool
	CreatedAt time.Time
}

// UnitCost is the average carrying cost per unit, rounded; 0 when there is no stock.
func (i Item) UnitCost() int64 {
	if i.Qty == 0 {
		return 0
	}
	return roundDiv(i.Value.Amount(), 1, i.Qty)
}

// ItemInput creates an item.
type ItemInput struct {
	Code   string
	Name   string
	Unit   string
	Method Method
	FEFO   bool
	Actor  string
}

func (in ItemInput) validate() (ItemInput, error) {
	in.Code, in.Name, in.Unit = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Unit)
	if in.Method == "" {
		in.Method = WeightedAverage
	}
	var fe []apperr.FieldError
	if in.Code == "" || len(in.Code) > 60 {
		fe = append(fe, apperr.FieldError{Field: "code", Message: "required, at most 60 characters"})
	}
	if in.Name == "" {
		fe = append(fe, apperr.FieldError{Field: "name", Message: "required"})
	}
	if in.Unit == "" {
		fe = append(fe, apperr.FieldError{Field: "unit", Message: "required (the base unit; quantities are whole numbers of it)"})
	}
	if !in.Method.Valid() {
		fe = append(fe, apperr.FieldError{Field: "method", Message: "must be fifo, weighted_average or specific_identification"})
	}
	if in.FEFO && in.Method != FIFO {
		fe = append(fe, apperr.FieldError{Field: "fefo", Message: "first-expired first-out needs the fifo method"})
	}
	if len(fe) > 0 {
		return in, apperr.Invalid("invalid item", fe...)
	}
	return in, nil
}

// Kind of a stock movement.
type Kind string

const (
	Receipt   Kind = "receipt"
	Issue     Kind = "issue"
	CountGain Kind = "count_gain"
	CountLoss Kind = "count_loss"
	WriteDown Kind = "write_down"
	// Transfer moves stock between warehouses: no journal, the carrying cost travels with it.
	Transfer Kind = "transfer"
	// ReverseWriteDown raises the carrying value again after the net realizable value rose, up to the write-down still standing (standard 8 para. 46).
	ReverseWriteDown Kind = "write_down_reversal"
)

// Movement is an append-only fact about an item's stock.
type Movement struct {
	ID         int64
	ItemID     uuid.UUID
	Kind       Kind
	Qty        int64 // signed effect on the quantity (issues and losses are negative)
	Value      int64 // signed effect on the value
	QtyAfter   int64
	ValueAfter int64
	Date       time.Time
	Journal    *uuid.UUID
	IdemKey    string
	Ref        string
	Note       string
	Actor      string
	CreatedAt  time.Time
	// WarehouseID is the warehouse of a receipt, issue or count (the one a transfer leaves); LotID the lot created or consumed (layered methods, single-lot movements).
	WarehouseID *uuid.UUID
	LotID       *int64
}

// ReceiveInput books a purchase or other receipt of stock.
type ReceiveInput struct {
	IdemKey   string
	ItemID    uuid.UUID
	Qty       int64
	TotalCost money.Money // the whole cost of this receipt: purchase price plus freight, duties and other costs of bringing it to its place
	Date      time.Time   // default today
	// VAT is the deductible input tax of the purchase (optional, zero when none): it is booked to the input-VAT account and is NOT part of the
	// stock's cost. The counter account is credited with the cost plus the tax.
	VAT money.Money
	// CounterAccount is credited (default the supplier payables account): pay from the bank by naming it.
	CounterAccount string
	Ref            string
	Actor          string
	// WarehouseID is where the stock goes (default: the main warehouse). LotNo and ExpiresOn describe the lot (fifo and specific_identification items only).
	WarehouseID uuid.UUID
	LotNo       string
	ExpiresOn   *time.Time
}

// IssueInput takes stock out at cost.
type IssueInput struct {
	IdemKey string
	ItemID  uuid.UUID
	Qty     int64
	Date    time.Time
	// ExpenseAccount is debited with the cost (default cost of goods sold).
	ExpenseAccount string
	Ref            string
	Reason         string
	Actor          string
	// WarehouseID is where the stock leaves from (default: the main warehouse). LotID names the lot: required for specific_identification items.
	WarehouseID uuid.UUID
	LotID       int64
}

// CountInput corrects the quantity after a stock count.
type CountInput struct {
	IdemKey    string
	ItemID     uuid.UUID
	CountedQty int64
	Date       time.Time
	Reason     string
	Actor      string
	// WarehouseID is the warehouse counted (default: the main warehouse): CountedQty is its quantity.
	WarehouseID uuid.UUID
}

// WriteDownInput lowers the carrying value of an item to its net realizable value.
type WriteDownInput struct {
	IdemKey string
	ItemID  uuid.UUID
	// NetRealizableValue is the total net realizable value of the whole stock of the item (what it can be sold for, less costs to sell).
	NetRealizableValue money.Money
	Date               time.Time
	Reason             string
	Actor              string
}

// Valuation is the stock valuation report.
type Valuation struct {
	Items      []Item
	TotalValue int64
}

// ReverseWriteDownInput raises the carrying value of an item toward its new net realizable value.
type ReverseWriteDownInput struct {
	IdemKey string
	ItemID  uuid.UUID
	// NetRealizableValue is the new total net realizable value of the whole stock; the carrying value rises to it, but never by more than the write-down still standing.
	NetRealizableValue money.Money
	Date               time.Time
	Reason             string
	Actor              string
}
