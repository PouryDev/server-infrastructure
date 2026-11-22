package web

import (
	"github.com/PouryDev/storefront/internal/domain/shop"
	"net/http"
)

func (s *Server) careRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/reviews", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		var in shop.Review
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.Care.Review(r.Context(), u, in); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 201, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("GET /api/seller/reviews", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		out, e := s.Care.Reviews(r.Context(), "", u)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, out)
	}, true))
	m.HandleFunc("POST /api/seller/reviews/{id}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		var in struct {
			Status string `json:"status"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.Care.ModerateReview(r.Context(), u, r.PathValue("id"), in.Status); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("POST /api/customer/returns", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		var in struct {
			OrderID string `json:"orderId"`
			Reason  string `json:"reason"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.Care.RequestReturn(r.Context(), u, in.OrderID, in.Reason); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 201, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("GET /api/seller/returns", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		out, e := s.Care.Returns(r.Context(), u)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, out)
	}, true))
	m.HandleFunc("POST /api/seller/returns/{order}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if s.Care == nil {
			failure(w, shop.ErrNotFound)
			return
		}
		var in struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.Care.DecideReturn(r.Context(), u, r.PathValue("order"), in.Status, in.Note); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
}
