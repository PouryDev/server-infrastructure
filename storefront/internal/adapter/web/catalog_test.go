package web

import (
	"net/url"
	"testing"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func TestShopSearchFiltersAndSort(t *testing.T) {
	products := []shop.Product{
		{ID: "a", Name: "كوسن", Category: "خانه", Active: true, Variants: []shop.Variant{{Price: 900000, Stock: 3}}},
		{ID: "b", Name: "چراغ", Category: "خانه", Active: true, Variants: []shop.Variant{{Price: 2000000, Stock: 0}}},
		{ID: "c", Name: "ماگ", Category: "آشپزخانه", Active: true, Variants: []shop.Variant{{Price: 600000, Stock: 2}}},
		{ID: "hidden", Active: false, Variants: []shop.Variant{{Price: 1, Stock: 1}}},
	}
	for _, tt := range []struct{ query, search, category, want string }{
		{query: "sort=price-asc", want: "cab"},
		{query: "stock=1&sort=price-desc", want: "ac"},
		{query: "min=70000&max=100000", want: "a"},
		{search: "کوسن", want: "a"},
		{category: "آشپزخانه", want: "c"},
		{query: "max=-1&min=9223372036854775807", want: "abc"},
	} {
		t.Run(tt.query+tt.search+tt.category, func(t *testing.T) {
			p := page{Products: products, Query: tt.search, Category: tt.category}
			q, _ := url.ParseQuery(tt.query)
			p.filterProducts(q)
			ids := ""
			for _, product := range p.Products {
				ids += product.ID
			}
			want := tt.want
			if ids != want {
				t.Fatalf("got %s want %s", ids, want)
			}
		})
	}
}

func TestInitialVariantSkipsSoldOutModels(t *testing.T) {
	p := shop.Product{Variants: []shop.Variant{{ID: "sold-out", Price: 10, Stock: 0}, {ID: "available", Price: 30, Stock: 2}, {ID: "lower-available", Price: 20, Stock: 1}}}
	if got := defaultVariant(p); got.ID != "lower-available" {
		t.Fatalf("wrong initial model: %+v", got)
	}
}
