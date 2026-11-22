package web

import (
	"net/http"
	"net/url"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
)

func (s *Server) merchandisingRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/seller/collections", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Collection shop.Collection `json:"collection"`
			Revision   int             `json:"revision"`
			Position   int             `json:"position"`
		}
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		revision, err := s.App.SaveCollection(r.Context(), u, in.Collection, in.Revision, in.Position)
		if err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]int{"revision": revision})
	}, true))
	m.HandleFunc("DELETE /api/seller/collections/{id}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Revision int `json:"revision"`
		}
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		revision, err := s.App.DeleteCollection(r.Context(), u, r.PathValue("id"), in.Revision)
		if err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]int{"revision": revision})
	}, true))
	m.HandleFunc("GET /fragments/rail", func(w http.ResponseWriter, r *http.Request) {
		row, err := s.App.HomeRail(r.Context(), r.URL.Query().Get("kind"), r.URL.Query().Get("key"))
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Fragment contains escaped public product cards only, never boot/session data.
		for _, p := range row.Products {
			if err = s.templates.ExecuteTemplate(w, "card", p); err != nil {
				return
			}
		}
	})
}
func (s *Server) homeRows(p *page) {
	p.CollectionRows = map[string]commerce.HomeRail{}
	for _, c := range p.Settings.Collections {
		products := c.Select(p.Products, c.Limit)
		title := c.Name
		for _, section := range p.Settings.Sections {
			if section.Kind == "collection" && section.CollectionID == c.ID && section.Title != "" {
				title = section.Title
				break
			}
		}
		p.CollectionRows[c.ID] = commerce.HomeRail{Kind: "collection", Key: c.ID, Title: title, Description: c.Description, Href: "/collections/" + c.ID, Products: products, Lazy: c.Lazy, Enabled: c.Enabled && len(products) > 0}
	}
	for _, category := range p.Categories {
		p.CategoryRows = append(p.CategoryRows, commerce.HomeRail{Kind: "category", Key: category.Name, Title: category.Name, Description: "کالاهای این دسته را یک‌جا ببینید و انتخاب کنید.", Href: "/shop?category=" + url.QueryEscape(category.Name), Lazy: true, Enabled: true})
	}
}
