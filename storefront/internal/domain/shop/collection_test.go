package shop

import "testing"

func TestManualCollectionPreservesOwnerOrderAndHidesInactive(t *testing.T) {
	products := []Product{{ID: "a", Active: true}, {ID: "b", Active: true}, {ID: "hidden", Active: false}}
	c := Collection{Mode: "manual", ProductIDs: []string{"b", "hidden", "a", "b", "missing"}}
	got := c.Select(products, 0)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("manual selection: %+v", got)
	}
	if len(c.Select(products, 1)) != 1 {
		t.Fatal("row limit ignored")
	}
}
func TestAutomaticCollectionUsesAvailableModelsPriceAndRules(t *testing.T) {
	products := []Product{
		{ID: "a", Category: "home", Active: true, Badge: "pick", Variants: []Variant{{Price: 10, Stock: 0}, {Price: 200, Stock: 2}}},
		{ID: "b", Category: "home", Active: true, Badge: "pick", Variants: []Variant{{Price: 150, Stock: 3}}},
		{ID: "c", Category: "other", Active: true, Badge: "pick", Variants: []Variant{{Price: 100, Stock: 3}}},
		{ID: "d", Category: "home", Active: true, Variants: []Variant{{Price: 120, Stock: 3}}},
	}
	c := Collection{Mode: "automatic", Category: "home", InStock: true, WithBadge: true, MinPrice: 100, MaxPrice: 210, Sort: "price-asc"}
	got := c.Select(products, 8)
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "a" {
		t.Fatalf("rules: %+v", got)
	}
	products[1].Variants[0].Stock = 0
	if len(c.Select(products, 8)) != 1 {
		t.Fatal("stock changes did not update selection")
	}
}
func TestAddressNormalizationAndValidation(t *testing.T) {
	address := Address{Name: "  مشتری نمونه ", Phone: "۰۹۱۲۱۲۳۴۵۶۷", Province: " تهران ", City: "تهران", Street: "خیابان نمونه، کوچهٔ آزمایشی، پلاک ۱۰", Postal: "۱۲۳۴۵۶۷۸۹۰"}
	got, err := address.Normalize()
	if err != nil || got.Phone != "+989121234567" || got.Postal != "1234567890" || got.Name != "مشتری نمونه" {
		t.Fatalf("address: %+v %v", got, err)
	}
	address.Postal = "123"
	if _, err = address.Normalize(); err == nil {
		t.Fatal("short postal code accepted")
	}
}
