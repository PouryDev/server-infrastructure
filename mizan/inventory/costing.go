// Package inventory keeps stock of goods and materials (موجودی مواد و کالا) with their cost, and books purchases, issues
// (cost of goods sold), count differences and write-downs to the ledger.
//
// Cost formulas follow Iranian accounting standard no. 8: first-in first-out and weighted average (LIFO is not allowed and is not
// offered), and inventory is carried at the lower of cost and net realizable value, item by item. Quantities are whole numbers of
// the item's base unit (use grams rather than kilograms when you need fractions); amounts are int64 rials.
package inventory

import (
	"math/big"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Method is the cost formula of an item.
type Method string

const (
	// FIFO issues the oldest purchase layers first (اولین صادره از اولین وارده).
	FIFO Method = "fifo"
	// WeightedAverage issues at the running average cost of the stock on hand (میانگین موزون متحرک): after each receipt the
	// average is recomputed, an issue takes its share of the total value.
	WeightedAverage Method = "weighted_average"
	// SpecificID attributes the cost of a named lot to the issue (شناسایی ویژه, standard 8 para. 25): every issue names the lot it takes from. It suits items that are
	// bought or made for a specific order and can be told apart; para. 25 says it is not suitable for interchangeable items.
	SpecificID Method = "specific_identification"
)

// Valid reports whether m is known.
func (m Method) Valid() bool { return m == FIFO || m == WeightedAverage || m == SpecificID }

// Layered reports whether the method keeps purchase layers (lots).
func (m Method) Layered() bool { return m == FIFO || m == SpecificID }

// Layer is a purchase layer of a FIFO item.
type Layer struct {
	ID             int64
	ItemID         uuid.UUID
	Seq            int
	Qty            int64 // quantity received
	Remaining      int64
	RemainingValue int64
	WarehouseID    uuid.UUID
	LotNo          string
	ExpiresOn      *time.Time
}

// roundDiv returns round-half-up(a*b/c) for non-negative inputs.
func roundDiv(a, b, c int64) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	n.Mul(n, big.NewInt(2))
	n.Add(n, big.NewInt(c))
	n.Div(n, new(big.Int).Mul(big.NewInt(c), big.NewInt(2)))
	return n.Int64()
}

// ErrInsufficientStock is the conflict returned when an issue exceeds the stock on hand.
func errInsufficient() error {
	return apperr.New(apperr.KindConflict, "insufficient_stock", "the stock on hand is less than the quantity asked for")
}

// AverageIssue is the cost of issuing qty from stock holding onHand units worth value. The last unit takes the remainder, so the
// stock value never drifts.
func AverageIssue(onHand, value, qty int64) (int64, error) {
	switch {
	case qty <= 0:
		return 0, apperr.InvalidField("quantity", "must be positive")
	case qty > onHand:
		return 0, errInsufficient()
	case qty == onHand:
		return value, nil
	}
	return roundDiv(value, qty, onHand), nil
}

// Take is a quantity taken from one layer and the cost it carries.
type Take struct {
	Index int // position in the layers slice
	Qty   int64
	Cost  int64
}

// TakeLayers takes qty from the layers in the order given and returns what was taken from each and the layers after.
func TakeLayers(layers []Layer, qty int64) (takes []Take, after []Layer, err error) {
	if qty <= 0 {
		return nil, nil, apperr.InvalidField("quantity", "must be positive")
	}
	after = append([]Layer(nil), layers...)
	left := qty
	for i := range after {
		if left == 0 {
			break
		}
		l := &after[i]
		if l.Remaining == 0 {
			continue
		}
		take := min(left, l.Remaining)
		c := l.RemainingValue
		if take < l.Remaining {
			c = roundDiv(l.RemainingValue, take, l.Remaining)
		}
		takes = append(takes, Take{Index: i, Qty: take, Cost: c})
		l.Remaining -= take
		l.RemainingValue -= c
		left -= take
	}
	if left > 0 {
		return nil, nil, errInsufficient()
	}
	return takes, after, nil
}

// FIFOIssue takes qty from the oldest layers and returns the cost and the layers after the issue (same order). A layer that is
// used up gives all its remaining value, a partly used one its rounded share.
func FIFOIssue(layers []Layer, qty int64) (cost int64, after []Layer, err error) {
	takes, after, err := TakeLayers(layers, qty)
	if err != nil {
		return 0, nil, err
	}
	for _, t := range takes {
		cost += t.Cost
	}
	return cost, after, nil
}

// CutValue lowers the total value of layers to newTotal, spreading the cut in proportion to each layer's value (the last layer
// takes the rounding remainder). It is used by write-downs of FIFO items.
func CutValue(layers []Layer, newTotal int64) []Layer {
	after := append([]Layer(nil), layers...)
	var total int64
	for _, l := range after {
		total += l.RemainingValue
	}
	if total == 0 || newTotal >= total {
		return after
	}
	var assigned int64
	last := -1
	for i := range after {
		if after[i].Remaining > 0 {
			last = i
		}
	}
	for i := range after {
		if after[i].Remaining == 0 {
			continue
		}
		if i == last {
			after[i].RemainingValue = newTotal - assigned
			continue
		}
		v := roundDiv(after[i].RemainingValue, newTotal, total)
		after[i].RemainingValue = v
		assigned += v
	}
	return after
}

// RaiseValue adds raise to the total value of the open layers, spread in proportion to each layer's remaining quantity (the last layer takes the rounding remainder).
func RaiseValue(layers []Layer, raise int64) []Layer {
	after := append([]Layer(nil), layers...)
	var qty int64
	last := -1
	for i, l := range after {
		if l.Remaining > 0 {
			qty += l.Remaining
			last = i
		}
	}
	if qty == 0 || raise <= 0 {
		return after
	}
	var assigned int64
	for i := range after {
		if after[i].Remaining == 0 {
			continue
		}
		part := raise - assigned
		if i != last {
			part = raise * after[i].Remaining / qty
		}
		after[i].RemainingValue += part
		assigned += part
	}
	return after
}
