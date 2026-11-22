package shop

import (
	"testing"
	"time"
)

func TestIranPhones(t *testing.T) {
	for _, raw := range []string{"09121234567", "+989121234567", "۰۰۹۸۹۱۲۱۲۳۴۵۶۷", "۹۸۹۱۲۱۲۳۴۵۶۷"} {
		p, e := Phone(raw)
		if e != nil || p != "+989121234567" {
			t.Fatalf("%s: %s %v", raw, p, e)
		}
	}
	for _, raw := range []string{"+12025550123", "0989121234567", "0912123", "user@example.com"} {
		if _, e := Phone(raw); e == nil {
			t.Fatal(raw)
		}
	}
}
func TestBNPLVariantAndProductRules(t *testing.T) {
	p := BNPLPolicy{Mode: "blacklist", Variants: []string{"v1"}}
	if p.Allows("p", "v1") || !p.Allows("p", "v2") {
		t.Fatal("blacklist")
	}
	p = BNPLPolicy{Mode: "whitelist", Products: []string{"p"}}
	if !p.Allows("p", "any") || p.Allows("other", "any") {
		t.Fatal("whitelist")
	}
	if (BNPLPolicy{Mode: "unknown"}).Allows("p", "v") {
		t.Fatal("fail closed")
	}
}
func TestCampaignBounds(t *testing.T) {
	now := time.Now()
	q := Quote{Subtotal: 200000, Shipping: 50000, Lines: []OrderLine{{ProductID: "p", Price: 100000, Quantity: 2}}}
	d := Discount{Active: true, Kind: "percent", Value: 20, Cap: 30000, EndsAt: now.Add(time.Hour), PerCustomer: 1}
	if a := d.Amount(q, "", now, 0, 0, 0); a != 30000 {
		t.Fatal(a)
	}
	if d.Amount(q, "", now, 0, 1, 0) != 0 || d.Amount(q, "", d.EndsAt, 0, 0, 0) != 0 {
		t.Fatal("eligibility")
	}
	d.FirstOrder = true
	if d.Amount(q, "", now, 0, 0, 1) != 0 {
		t.Fatal("first order")
	}
	d.FirstOrder = false
	d.Variants = []string{"different"}
	if d.Amount(q, "", now, 0, 0, 0) != 0 {
		t.Fatal("scope")
	}
}
func TestSixteenDistinctStyles(t *testing.T) {
	seen := map[string]bool{}
	ts := Themes()
	if len(ts) != 16 {
		t.Fatal(len(ts))
	}
	for _, v := range ts {
		if seen[v.ID] {
			t.Fatal(v.ID)
		}
		seen[v.ID] = true
	}
}

func TestThemeDesignsAreDistinct(t *testing.T) {
	seen := map[Design]bool{}
	for _, theme := range Themes() {
		d := ThemeDesign(theme.ID)
		if seen[d] {
			t.Fatalf("duplicate composition %s", theme.ID)
		}
		seen[d] = true
	}
}
func TestBuyTwoGetOneUsesCheapestEligibleUnit(t *testing.T) {
	d := Discount{Active: true, Kind: "buy_x_get_y", BuyQuantity: 2, FreeQuantity: 1}
	q := Quote{Subtotal: 800000, Lines: []OrderLine{{ProductID: "a", Price: 300000, Quantity: 2}, {ProductID: "b", Price: 200000, Quantity: 1}}}
	if n := d.Amount(q, "", time.Now(), 0, 0, 0); n != 200000 {
		t.Fatal(n)
	}
}
