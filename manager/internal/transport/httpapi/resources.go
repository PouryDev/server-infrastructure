package httpapi

import (
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"net/http"
)

func (s *Server) resourceRoutes(mux *http.ServeMux) {
	if s.Resources == nil {
		return
	}
	for _, action := range []string{"preview", "apply"} {
		mux.HandleFunc("POST /api/resources/"+action, s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
			var in domain.ResourceRequest
			if !decode(w, r, &in) {
				return
			}
			var out any
			var err error
			if action == "preview" {
				out, err = s.Resources.Preview(r.Context(), in)
			} else {
				out, err = s.Resources.Apply(r.Context(), sess.User, in)
			}
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			if action == "apply" && s.Wake != nil {
				s.Wake()
			}
			respond(w, 200, out)
		}))
	}
}
