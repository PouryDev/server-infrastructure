// Package money is the only way amounts are represented in mizan: an int64 count of the currency's
// smallest unit plus the currency. There is no float anywhere, and values of different currencies can
// never be added by accident.
//
// IRR (rial) is the legal accounting currency in Iran and the unit the ledger stores. IRT (toman) is a
// display convention (1 IRT = 10 IRR); convert explicitly with ToIRR / ToIRT.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// Currency is an upper-case code.
type Currency string

const (
	IRR Currency = "IRR" // rial: the unit stored in the ledger
	IRT Currency = "IRT" // toman: 10 rials
)

// Errors returned by arithmetic.
var (
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrOverflow         = errors.New("money: overflow")
	ErrUnknownCurrency  = errors.New("money: unknown currency")
	ErrNotConvertible   = errors.New("money: amount is not a whole number of tomans")
)

// Valid reports whether c is a supported currency.
func (c Currency) Valid() bool { return c == IRR || c == IRT }

// Money is an immutable amount. The zero value is invalid on purpose (no currency): use New or Zero.
type Money struct {
	amount   int64
	currency Currency
}

// New creates a Money. The amount may be negative (balances, reversals).
func New(amount int64, c Currency) (Money, error) {
	if !c.Valid() {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, c)
	}
	return Money{amount, c}, nil
}

// Must is New for constants and tests; it panics on an invalid currency.
func Must(amount int64, c Currency) Money {
	m, err := New(amount, c)
	if err != nil {
		panic(err)
	}
	return m
}

// Zero returns 0 in currency c.
func Zero(c Currency) Money { return Money{0, c} }

func (m Money) Amount() int64      { return m.amount }
func (m Money) Currency() Currency { return m.currency }
func (m Money) IsZero() bool       { return m.amount == 0 }
func (m Money) IsPositive() bool   { return m.amount > 0 }
func (m Money) IsNegative() bool   { return m.amount < 0 }
func (m Money) Valid() bool        { return m.currency.Valid() }

func (m Money) same(o Money) error {
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}

// Add returns m+o.
func (m Money) Add(o Money) (Money, error) {
	if err := m.same(o); err != nil {
		return Money{}, err
	}
	s := m.amount + o.amount
	if (o.amount > 0 && s < m.amount) || (o.amount < 0 && s > m.amount) {
		return Money{}, ErrOverflow
	}
	return Money{s, m.currency}, nil
}

// Sub returns m-o.
func (m Money) Sub(o Money) (Money, error) {
	if o.amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return m.Add(Money{-o.amount, o.currency})
}

// Neg returns -m.
func (m Money) Neg() (Money, error) {
	if m.amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{-m.amount, m.currency}, nil
}

// Cmp compares two amounts of the same currency: -1, 0 or 1.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.same(o); err != nil {
		return 0, err
	}
	switch {
	case m.amount < o.amount:
		return -1, nil
	case m.amount > o.amount:
		return 1, nil
	}
	return 0, nil
}

// Rounding decides how a fractional result is brought to a whole unit.
type Rounding uint8

const (
	HalfUp   Rounding = iota // 0.5 rounds away from zero: how Iranian invoices round tax
	Down                     // toward zero
	Up                       // away from zero
	HalfEven                 // banker's rounding
)

// MulRatio returns m * num / den rounded with r, using 128-bit intermediates so large amounts do not
// overflow before the division.
func (m Money) MulRatio(num, den int64, r Rounding) (Money, error) {
	if den <= 0 {
		return Money{}, errors.New("money: ratio denominator must be positive")
	}
	neg := (m.amount < 0) != (num < 0)
	a, n := absU(m.amount), absU(num)
	hi, lo := bits.Mul64(a, n)
	d := uint64(den) //nolint:gosec // den > 0 checked above
	if hi >= d {
		return Money{}, ErrOverflow
	}
	q, rem := bits.Div64(hi, lo, d)
	if rem != 0 {
		half := rem >= d-rem // rem/d >= 1/2
		exactHalf := rem == d-rem
		switch r {
		case Up:
			q++
		case HalfUp:
			if half {
				q++
			}
		case HalfEven:
			if half && (!exactHalf || q%2 == 1) {
				q++
			}
		case Down:
		}
	}
	if q > math.MaxInt64 {
		return Money{}, ErrOverflow
	}
	v := int64(q) //nolint:gosec // bounded above
	if neg {
		v = -v
	}
	return Money{v, m.currency}, nil
}

// Percent returns m * basisPoints / 10_000 (100 bp = 1%), the form tax and discount rates use.
func (m Money) Percent(basisPoints int64, r Rounding) (Money, error) {
	return m.MulRatio(basisPoints, 10_000, r)
}

func absU(v int64) uint64 {
	if v < 0 {
		return uint64(-(v + 1)) + 1
	}
	return uint64(v)
}

// Allocate splits m in proportion to ratios without losing a unit: the remainder goes one unit at a time
// to the largest fractional parts (earlier entries first on ties). Ratios must be non-negative with a
// positive sum, m must not be negative.
func (m Money) Allocate(ratios ...int64) ([]Money, error) {
	if len(ratios) == 0 {
		return nil, errors.New("money: no ratios")
	}
	if m.amount < 0 {
		return nil, errors.New("money: cannot allocate a negative amount")
	}
	var total int64
	for _, r := range ratios {
		if r < 0 {
			return nil, errors.New("money: negative ratio")
		}
		if total += r; total < 0 {
			return nil, ErrOverflow
		}
	}
	if total == 0 {
		return nil, errors.New("money: ratios sum to zero")
	}
	type part struct {
		idx  int
		frac uint64
	}
	out := make([]Money, len(ratios))
	parts := make([]part, len(ratios))
	var given int64
	for i, r := range ratios {
		hi, lo := bits.Mul64(uint64(m.amount), uint64(r))
		q, rem := bits.Div64(hi, lo, uint64(total))
		out[i] = Money{int64(q), m.currency} //nolint:gosec // q <= amount
		parts[i] = part{i, rem}
		given += int64(q) //nolint:gosec // q <= amount
	}
	left := m.amount - given
	for ; left > 0; left-- {
		best := 0
		for i := range parts {
			if parts[i].frac > parts[best].frac {
				best = i
			}
		}
		out[parts[best].idx].amount++
		parts[best].frac = 0
	}
	return out, nil
}

// ToIRR converts toman to rial; IRR is returned unchanged.
func (m Money) ToIRR() (Money, error) {
	if m.currency != IRT {
		return m, nil
	}
	if m.amount > math.MaxInt64/10 || m.amount < math.MinInt64/10 {
		return Money{}, ErrOverflow
	}
	return Money{m.amount * 10, IRR}, nil
}

// ToIRT converts rial to toman and fails when the amount is not a whole number of tomans.
func (m Money) ToIRT() (Money, error) {
	if m.currency != IRR {
		return m, nil
	}
	if m.amount%10 != 0 {
		return Money{}, ErrNotConvertible
	}
	return Money{m.amount / 10, IRT}, nil
}

// String formats as "1,250,000 IRR".
func (m Money) String() string {
	s := strconv.FormatInt(m.amount, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if neg {
		out = "-" + out
	}
	return out + " " + string(m.currency)
}

type wire struct {
	Amount   int64    `json:"amount"`
	Currency Currency `json:"currency"`
}

// MarshalJSON writes {"amount": 1000, "currency": "IRR"}.
func (m Money) MarshalJSON() ([]byte, error) { return json.Marshal(wire{m.amount, m.currency}) }

// UnmarshalJSON reads the form MarshalJSON writes and rejects an unknown currency.
func (m *Money) UnmarshalJSON(b []byte) error {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	v, err := New(w.Amount, w.Currency)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
