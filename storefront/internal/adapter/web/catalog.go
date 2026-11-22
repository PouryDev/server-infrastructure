package web

import (
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func searchText(s string) string {
	return strings.ToLower(strings.NewReplacer("ي", "ی", "ك", "ک", "‌", " ").Replace(s))
}

// Pick the lowest-priced available model for the card and initial detail view.
func defaultVariant(p shop.Product) shop.Variant {
	var best shop.Variant
	found, available := false, false
	for _, v := range p.Variants {
		if available && v.Stock <= 0 {
			continue
		}
		if !found || (!available && v.Stock > 0) || v.Price < best.Price {
			best = v
			found = true
		}
		if v.Stock > 0 {
			available = true
		}
	}
	return best
}
func productPrice(p shop.Product) int64 { return defaultVariant(p).Price }
func priceFilter(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 || n > 100_000_000_000_000 {
		return 0
	}
	return n
}
func (p *page) filterProducts(query url.Values) {
	p.MinPrice = priceFilter(query.Get("min")) * 10
	p.MaxPrice = priceFilter(query.Get("max")) * 10
	p.InStock = query.Get("stock") == "1"
	p.Sort = query.Get("sort")
	filtered := []shop.Product{}
	for _, pr := range p.Products {
		if !pr.Active || (p.Query != "" && !strings.Contains(searchText(pr.Name+" "+pr.Description+" "+pr.Category), searchText(p.Query))) || (p.Category != "" && pr.Category != p.Category) {
			continue
		}
		price := productPrice(pr)
		if (p.MinPrice > 0 && price < p.MinPrice) || (p.MaxPrice > 0 && price > p.MaxPrice) {
			continue
		}
		if p.InStock && !slices.ContainsFunc(pr.Variants, func(v shop.Variant) bool { return v.Stock > 0 }) {
			continue
		}
		filtered = append(filtered, pr)
	}
	switch p.Sort {
	case "price-asc":
		slices.SortStableFunc(filtered, func(a, b shop.Product) int {
			if productPrice(a) < productPrice(b) {
				return -1
			}
			if productPrice(a) > productPrice(b) {
				return 1
			}
			return 0
		})
	case "price-desc":
		slices.SortStableFunc(filtered, func(a, b shop.Product) int {
			if productPrice(a) > productPrice(b) {
				return -1
			}
			if productPrice(a) < productPrice(b) {
				return 1
			}
			return 0
		})
	case "name":
		slices.SortStableFunc(filtered, func(a, b shop.Product) int { return strings.Compare(a.Name, b.Name) })
	}
	p.Products = filtered
}
