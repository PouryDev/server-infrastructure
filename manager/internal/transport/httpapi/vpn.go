package httpapi

import (
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"net/http"
)

func (s *Server) vpnRoutes(mux *http.ServeMux) {
	if s.VPN == nil {
		return
	}
	mux.HandleFunc("GET /api/vpn", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		out, err := s.VPN.Runner.Status(r.Context())
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		respond(w, 200, out)
	}))
	mux.HandleFunc("PUT /api/vpn", s.rated("vpn.change", 10, func(w http.ResponseWriter, r *http.Request, sess session) {
		var in domain.VPNChange
		if !decodeLimit(w, r, &in, 20<<10) {
			return
		}
		out, err := s.VPN.Change(r.Context(), sess.User, in)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		respond(w, 200, out)
	}))
	mux.HandleFunc("POST /api/vpn/test", s.rated("vpn.test", 6, func(w http.ResponseWriter, r *http.Request, _ session) {
		if err := s.VPN.Runner.Test(r.Context()); err != nil {
			problem(w, 422, err.Error())
			return
		}
		respond(w, 200, map[string]bool{"reachable": true})
	}))
}
