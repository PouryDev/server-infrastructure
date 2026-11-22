package shop

import (
	"testing"
	"time"
)

func TestDeliveryTariffsAndDestination(t *testing.T) {
	now := time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)
	s := Settings{DeliveryMethods: []DeliveryMethod{
		{ID: "post", Name: "پست", Kind: "shipping", Enabled: true, Fee: 800000, FreeAbove: 15000000, MinDays: 2, MaxDays: 5},
		{ID: "courier", Name: "پیک", Kind: "courier", Enabled: true, Fee: 1200000, Cities: []string{"تهران"}, MinDays: 0, MaxDays: 1, MaxWeightGrams: 1000},
		{ID: "pickup", Name: "دریافت", Kind: "pickup", Enabled: true, PickupAddress: "خیابان نمونه", MinDays: 1, MaxDays: 2},
		{ID: "off", Name: "غیرفعال", Kind: "shipping", Enabled: false},
	}}
	quotes, e := s.DeliveryQuotes(DeliverySelection{City: "تهران"}, 15000000, 500, now)
	if e != nil || len(quotes) != 3 || quotes[0].Fee != 0 || quotes[1].Fee != 1200000 || quotes[2].PickupAddress == "" {
		t.Fatalf("%+v %v", quotes, e)
	}
	if quotes[0].From != "2026-11-22" {
		t.Fatalf("Tehran calendar date: %s", quotes[0].From)
	}
	quotes, e = s.DeliveryQuotes(DeliverySelection{City: "شیراز"}, 100000, 1500, now)
	if e != nil || len(quotes) != 2 || quotes[0].Fee != 800000 {
		t.Fatalf("%+v %v", quotes, e)
	}
	s.DeliveryMethods = s.DeliveryMethods[3:]
	if _, e = s.DeliveryQuotes(DeliverySelection{}, 100000, 0, now); e == nil {
		t.Fatal("accepted all disabled methods")
	}
}
func TestLegacyDeliveryAndValidation(t *testing.T) {
	quotes, e := (Settings{Shipping: 50000, FreeShipping: 100000}).DeliveryQuotes(DeliverySelection{}, 100000, 0, time.Now())
	if e != nil || quotes[0].Fee != 0 || quotes[0].MethodID != "standard" {
		t.Fatal(quotes, e)
	}
	for _, m := range []DeliveryMethod{{ID: "x", Name: "x", Kind: "pickup"}, {ID: "x", Name: "x", Kind: "shipping", MinDays: 5, MaxDays: 2}, {ID: "x", Name: "x", Kind: "shipping", Fee: -1}} {
		if m.Validate() == nil {
			t.Fatal(m)
		}
	}
}
func TestProductDetailsRejectUnsafeAndInconsistentValues(t *testing.T) {
	base := Product{Variants: []Variant{{Price: 100000, OldPrice: 120000, Color: "#123456", Image: "/media/photo"}}, Images: []string{"/media/photo"}, Specs: map[string]string{"جنس": "پنبه"}}
	if e := base.ValidateDetails(); e != nil {
		t.Fatal(e)
	}
	for _, variant := range []Variant{{Price: 100000, OldPrice: 90000}, {Price: 100001}, {Price: 100000, Color: "red;display:none"}, {Price: 100000, Image: "//evil.test/x"}} {
		p := base
		p.Variants = []Variant{variant}
		if p.ValidateDetails() == nil {
			t.Fatal(variant)
		}
	}
	if LocalLink("//evil.test") || LocalLink("/\\evil.test") || !LocalLink("/pages/faq") {
		t.Fatal("local URL validation")
	}
}
