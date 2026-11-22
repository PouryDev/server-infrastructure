package web

import (
	"github.com/PouryDev/storefront/internal/domain/shop"
	"net/url"
)

func productSchema(origin string, p shop.Product) map[string]any {
	variants := []any{}
	varies := []string{}
	color, size := false, false
	for _, v := range p.Variants {
		availability := "https://schema.org/InStock"
		if v.Stock <= 0 {
			availability = "https://schema.org/OutOfStock"
		}
		image := p.Image
		if v.Image != "" {
			image = v.Image
		}
		product := map[string]any{"@type": "Product", "name": p.Name + " — " + v.Name, "description": p.Description, "image": origin + image, "sku": v.SKU, "url": origin + "/products/" + p.Slug + "?variant=" + url.QueryEscape(v.ID), "offers": map[string]any{"@type": "Offer", "price": v.Price, "priceCurrency": "IRR", "availability": availability, "url": origin + "/products/" + p.Slug + "?variant=" + url.QueryEscape(v.ID)}}
		if v.ColorName != "" {
			product["color"] = v.ColorName
			color = true
		}
		if v.Size != "" {
			product["size"] = v.Size
			size = true
		}
		variants = append(variants, product)
	}
	if color {
		varies = append(varies, "https://schema.org/color")
	}
	if size {
		varies = append(varies, "https://schema.org/size")
	}
	out := map[string]any{"@context": "https://schema.org", "@type": "ProductGroup", "productGroupID": p.ID, "name": p.Name, "description": p.Description, "url": origin + "/products/" + p.Slug, "hasVariant": variants}
	if len(varies) > 0 {
		out["variesBy"] = varies
	}
	if p.Brand != "" {
		out["brand"] = map[string]string{"@type": "Brand", "name": p.Brand}
	}
	return out
}
