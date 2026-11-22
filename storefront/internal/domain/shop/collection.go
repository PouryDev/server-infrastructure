package shop

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

var collectionIDRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

type Collection struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        string   `json:"mode"`
	ProductIDs  []string `json:"productIds"`
	Category    string   `json:"category"`
	InStock     bool     `json:"inStock"`
	WithBadge   bool     `json:"withBadge"`
	MinPrice    int64    `json:"minPrice"`
	MaxPrice    int64    `json:"maxPrice"`
	Sort        string   `json:"sort"`
	Limit       int      `json:"limit"`
	Enabled     bool     `json:"enabled"`
	Lazy        bool     `json:"lazy"`
}

func (c Collection) Validate() error {
	if !collectionIDRE.MatchString(c.ID) || strings.TrimSpace(c.Name) == "" || len(c.Name) > 250 || len(c.Description) > 1500 || len(c.Category) > 200 || !slices.Contains([]string{"manual", "automatic"}, c.Mode) || c.Limit < 1 || c.Limit > 24 || len(c.ProductIDs) > 100 || c.MinPrice < 0 || c.MaxPrice < 0 || (c.MaxPrice > 0 && c.MaxPrice < c.MinPrice) || !slices.Contains([]string{"catalog", "price-asc", "price-desc", "name"}, c.Sort) {
		return ErrInvalid
	}
	if c.Mode == "manual" && c.Enabled && len(c.ProductIDs) == 0 {
		return ErrInvalid
	}
	return nil
}
func ProductAvailable(p Product) bool {
	return slices.ContainsFunc(p.Variants, func(v Variant) bool { return v.Stock > 0 })
}
func MinimumPrice(p Product) int64 {
	var price int64
	found := false
	available := ProductAvailable(p)
	for _, v := range p.Variants {
		if available && v.Stock <= 0 {
			continue
		}
		if !found || v.Price < price {
			price = v.Price
			found = true
		}
	}
	return price
}

// Select is deterministic and preserves the owner's order for manual selections.
// A zero limit returns the whole collection for its SSR catalog page.
func (c Collection) Select(products []Product, limit int) []Product {
	out := []Product{}
	if c.Mode == "manual" {
		byID := map[string]Product{}
		for _, p := range products {
			if p.Active {
				byID[p.ID] = p
			}
		}
		seen := map[string]bool{}
		for _, id := range c.ProductIDs {
			if p, ok := byID[id]; ok && !seen[id] {
				out = append(out, p)
				seen[id] = true
			}
		}
	} else {
		for _, p := range products {
			price := MinimumPrice(p)
			if !p.Active || (c.Category != "" && p.Category != c.Category) || (c.InStock && !ProductAvailable(p)) || (c.WithBadge && p.Badge == "") || price < c.MinPrice || (c.MaxPrice > 0 && price > c.MaxPrice) {
				continue
			}
			out = append(out, p)
		}
		switch c.Sort {
		case "price-asc":
			slices.SortStableFunc(out, func(a, b Product) int { return cmp.Compare(MinimumPrice(a), MinimumPrice(b)) })
		case "price-desc":
			slices.SortStableFunc(out, func(a, b Product) int { return cmp.Compare(MinimumPrice(b), MinimumPrice(a)) })
		case "name":
			slices.SortStableFunc(out, func(a, b Product) int { return strings.Compare(a.Name, b.Name) })
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
