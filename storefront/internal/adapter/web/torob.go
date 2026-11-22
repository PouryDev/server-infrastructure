package web

import (
	"net/http"

	"github.com/PouryDev/storefront/internal/adapter/torob"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/syndication"
)

const torobPath = "/torob_api/v3/products"

func (s *Server) torobRoutes(m *http.ServeMux) {
	m.HandleFunc("POST "+torobPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if s.TorobVerify == nil || s.TorobVerify(r.Header.Get("X-Torob-Token"), r.Header.Get("X-Torob-Token-Version")) != nil {
			jsonOut(w, 401, map[string]string{"error": "invalid Torob token"})
			return
		}
		if s.Feed == nil {
			jsonOut(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		enabled, err := s.Feed.Enabled(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		if !enabled {
			http.NotFound(w, r)
			return
		}
		var in syndication.Request
		if err = decode(w, r, &in); err != nil {
			jsonOut(w, 400, map[string]string{"error": "invalid request parameters"})
			return
		}
		result, err := s.Feed.Read(r.Context(), in)
		if err != nil {
			if err == shop.ErrInvalid {
				jsonOut(w, 400, map[string]string{"error": "invalid request parameters"})
			} else {
				failure(w, err)
			}
			return
		}
		products := []torob.Product{}
		for _, p := range result.Products {
			products = append(products, torob.MapProduct(s.Origin, p))
		}
		var cursor *string
		if in.Sort == "product_id_desc" && len(products) == 100 {
			last := products[len(products)-1].ID
			cursor = &last
		}
		page := 1
		if in.Page != nil {
			page = *in.Page
		}
		jsonOut(w, 200, map[string]any{"api_version": "torob_api_v3", "current_page": page, "total": result.Total, "max_pages": max(1, (result.Total+99)/100), "next_cursor": cursor, "products": products})
	})
	m.HandleFunc("GET /api/seller/torob", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Feed == nil {
			jsonOut(w, 503, map[string]string{"error": "catalog unavailable"})
			return
		}
		status, err := s.Feed.Status(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]any{"endpoint": s.Origin + torobPath, "sitemap": s.Origin + "/sitemap.xml", "sync": status})
	}, true))
}
