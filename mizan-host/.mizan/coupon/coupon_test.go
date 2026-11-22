package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func irr(n int64) money.Money { return money.Must(n, money.IRR) }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestDiscountForRoundsHalfUpAndNeverExceedsTheAmount(t *testing.T) {
	pct := Coupon{Kind: Percent, PercentBP: 1250, Currency: money.IRR} // 12.5%
	for amount, want := range map[int64]int64{1000: 125, 1001: 125, 1004: 126, 8: 1, 3: 0} {
		if got, _ := pct.DiscountFor(irr(amount)); got != irr(want) {
			t.Errorf("12.5%% of %d = %v, want %d", amount, got, want)
		}
	}
	fixed := Coupon{Kind: Fixed, FixedAmount: 500, Currency: money.IRR}
	if got, _ := fixed.DiscountFor(irr(2000)); got != irr(500) {
		t.Errorf("%v", got)
	}
	if got, _ := fixed.DiscountFor(irr(300)); got != irr(300) {
		t.Errorf("a fixed coupon must not give more than the amount, gave %v", got)
	}
	full := Coupon{Kind: Percent, PercentBP: 10000, Currency: money.IRR}
	if got, _ := full.DiscountFor(irr(777)); got != irr(777) {
		t.Errorf("%v", got)
	}
}

func TestUsableExplainsWhyNot(t *testing.T) {
	two := 2
	from, until := day(2026, 11, 1), day(2026, 11, 30)
	base := Coupon{Kind: Percent, PercentBP: 1000, Currency: money.IRR, Active: true, ValidFrom: &from, ValidUntil: &until, MinAmount: 1000, MaxRedemptions: &two}
	today := day(2026, 11, 9)
	if err := base.Usable(today, irr(1000)); err != nil {
		t.Fatal(err)
	}
	if err := base.Usable(until, irr(1000)); err != nil {
		t.Fatalf("the last day is inclusive: %v", err)
	}
	for name, c := range map[string]struct {
		mut func(*Coupon)
		day time.Time
		amt money.Money
		why string
	}{
		"inactive":    {func(c *Coupon) { c.Active = false }, today, irr(1000), "coupon_inactive"},
		"not started": {func(c *Coupon) {}, day(2026, 10, 31), irr(1000), "coupon_not_started"},
		"expired":     {func(c *Coupon) {}, day(2026, 12, 1), irr(1000), "coupon_expired"},
		"below min":   {func(c *Coupon) {}, today, irr(999), "coupon_min_amount"},
		"exhausted":   {func(c *Coupon) { c.Redeemed = 2 }, today, irr(1000), "coupon_exhausted"},
		"currency":    {func(c *Coupon) {}, today, money.Must(1000, money.IRT), "currency_mismatch"},
	} {
		v := base
		c.mut(&v)
		err := v.Usable(c.day, c.amt)
		var ae *apperr.Error
		if !apperr.Is(err, apperr.KindConflict) || !asCode(err, &ae) || ae.Code != c.why {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func asCode(err error, out **apperr.Error) bool { return errors.As(err, out) }

func TestCouponValidation(t *testing.T) {
	ok := Input{Code: " spring-26 ", Name: "Spring", Kind: Percent, PercentBP: 1000, Currency: money.IRR}.coupon([16]byte{1}, time.Now())
	if err := ok.validate(); err != nil || ok.Code != "SPRING-26" {
		t.Fatalf("%v %s", err, ok.Code)
	}
	zero := 0
	for name, mut := range map[string]func(*Coupon){
		"short code":          func(c *Coupon) { c.Code = "AB" },
		"bad characters":      func(c *Coupon) { c.Code = "A B C" },
		"no name":             func(c *Coupon) { c.Name = " " },
		"percent over 100":    func(c *Coupon) { c.PercentBP = 10001 },
		"percent zero":        func(c *Coupon) { c.PercentBP = 0 },
		"percent with fixed":  func(c *Coupon) { c.FixedAmount = 5 },
		"fixed without value": func(c *Coupon) { c.Kind, c.PercentBP = Fixed, 0 },
		"unknown kind":        func(c *Coupon) { c.Kind = "bogus" },
		"zero total limit":    func(c *Coupon) { c.MaxRedemptions = &zero },
		"negative minimum":    func(c *Coupon) { c.MinAmount = -1 },
		"until before from": func(c *Coupon) {
			a, b := day(2026, 2, 1), day(2026, 1, 1)
			c.ValidFrom, c.ValidUntil = &a, &b
		},
	} {
		v := ok
		mut(&v)
		if err := v.validate(); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
}
