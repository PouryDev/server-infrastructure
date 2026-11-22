package web

import (
	"net/http"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Server) deliveryRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/seller/orders/{id}/shipment", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in shop.Shipment
		if err := decode(w, r, &in); err != nil {
			failure(w, err)
			return
		}
		if err := s.App.ShipOrder(r.Context(), u, r.PathValue("id"), in); err != nil {
			failure(w, err)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
}
