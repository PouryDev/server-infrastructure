package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func TestArithmetic(t *testing.T) {
	a, b := money.Must(1000, money.IRR), money.Must(250, money.IRR)
	s, err := a.Add(b)
	if err != nil || s.Amount() != 1250 {
		t.Fatalf("add: %v %v", s, err)
	}
	d, _ := a.Sub(b)
	if d.Amount() != 750 {
		t.Fatalf("sub: %v", d)
	}
	if _, err := a.Add(money.Must(1, money.IRT)); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if _, err := money.Must(math.MaxInt64, money.IRR).Add(b); !errors.Is(err, money.ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	if _, err := money.New(1, "USD"); !errors.Is(err, money.ErrUnknownCurrency) {
		t.Fatalf("expected unknown currency, got %v", err)
	}
}

func TestPercentRounding(t *testing.T) {
	cases := []struct {
		amount, bp int64
		r          money.Rounding
		want       int64
	}{
		{1005, 1000, money.HalfUp, 101},   // 100.5 -> 101
		{1005, 1000, money.HalfEven, 100}, // 100.5 -> 100
		{1015, 1000, money.HalfEven, 102}, // 101.5 -> 102
		{1004, 1000, money.HalfUp, 100},
		{1001, 1000, money.Up, 101},
		{1009, 1000, money.Down, 100},
		{-1005, 1000, money.HalfUp, -101},
		{1_000_000_000_000_000, 1000, money.HalfUp, 100_000_000_000_000}, // no overflow in the intermediate product
	}
	for _, c := range cases {
		got, err := money.Must(c.amount, money.IRR).Percent(c.bp, c.r)
		if err != nil || got.Amount() != c.want {
			t.Errorf("%d@%dbp r=%d: got %v (%v), want %d", c.amount, c.bp, c.r, got, err, c.want)
		}
	}
}

func TestAllocateNeverLosesAUnit(t *testing.T) {
	parts, err := money.Must(100, money.IRR).Allocate(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, p := range parts {
		sum += p.Amount()
	}
	if sum != 100 || parts[0].Amount() != 34 || parts[1].Amount() != 33 {
		t.Fatalf("parts %v", parts)
	}
	for _, n := range []int64{0, 1, 7, 999_999_937} {
		ps, _ := money.Must(n, money.IRR).Allocate(3, 5, 11, 0, 2)
		var s int64
		for _, p := range ps {
			s += p.Amount()
		}
		if s != n {
			t.Fatalf("allocate %d lost units: %d", n, s)
		}
	}
}

func TestConvertAndFormat(t *testing.T) {
	irr, _ := money.Must(1500, money.IRT).ToIRR()
	if irr.Amount() != 15000 || irr.Currency() != money.IRR {
		t.Fatal(irr)
	}
	if _, err := money.Must(15, money.IRR).ToIRT(); !errors.Is(err, money.ErrNotConvertible) {
		t.Fatal(err)
	}
	if got := money.Must(-1234567, money.IRR).String(); got != "-1,234,567 IRR" {
		t.Fatal(got)
	}
}

func TestJSON(t *testing.T) {
	b, _ := json.Marshal(money.Must(5, money.IRR))
	if string(b) != `{"amount":5,"currency":"IRR"}` {
		t.Fatal(string(b))
	}
	var m money.Money
	if err := json.Unmarshal([]byte(`{"amount":5,"currency":"XXX"}`), &m); err == nil {
		t.Fatal("expected error")
	}
}
