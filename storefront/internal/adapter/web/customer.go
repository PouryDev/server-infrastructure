package web

import (
	"net/http"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Server) customerRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/account/addresses", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		items, err := s.App.Addresses(r.Context(), u)
		if err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, items)
	}, false))
	m.HandleFunc("POST /api/account/addresses", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in shop.SavedAddress
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		out, err := s.App.SaveAddress(r.Context(), u, in)
		if err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, out)
	}, false))
	m.HandleFunc("DELETE /api/account/addresses/{id}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if err := s.App.DeleteAddress(r.Context(), u, r.PathValue("id")); err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("POST /api/account/profile", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Name string `json:"name"`
		}
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		if err := s.App.UpdateProfile(r.Context(), u, in.Name); err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, false))
}
