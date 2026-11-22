package httpapi

import (
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"net/http"
)

func (s *Server) storefrontRoutes(m *http.ServeMux) {
	if s.Storefront == nil {
		return
	}
	m.HandleFunc("GET /api/storefront/catalog", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		out, e := s.Storefront.Catalog(r.Context())
		if e != nil {
			problem(w, 503, e.Error())
			return
		}
		respond(w, 200, out)
	}))
	m.HandleFunc("POST /api/storefront/create", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		var in domain.StorefrontSpec
		if !decode(w, r, &in) {
			return
		}
		out, e := s.Storefront.Create(r.Context(), sess.User, in)
		if e != nil {
			problem(w, 422, e.Error())
			return
		}
		respond(w, 202, out)
	}))
}
