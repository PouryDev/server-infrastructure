package httpapi

import (
	"errors"
	"net/http"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// envRoutes serves the destination env store. Every route is behind login, exact
// Origin and CSRF (for mutations), strict JSON and a per-user rate limit. List
// responses never contain values; only the explicit reveal endpoint does.
func (s *Server) envRoutes(mux *http.ServeMux) {
	if s.Envs == nil {
		return
	}
	fail := func(w http.ResponseWriter, err error) {
		code := 422
		if errors.Is(err, application.ErrEnvConflict) || errors.Is(err, application.ErrConflict) {
			code = 409
		}
		problem(w, code, err.Error())
	}
	mux.HandleFunc("GET /api/env/projects", s.rated("env-read", 120, func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Envs.Projects(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("GET /api/env/{project}", s.rated("env-read", 120, func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Envs.State(r.Context(), r.PathValue("project"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("POST /api/env/{project}/reveal", s.rated("env-reveal", 30, func(w http.ResponseWriter, r *http.Request, sess session) {
		var req domain.EnvRevealRequest
		if !decode(w, r, &req) {
			return
		}
		value, err := s.Envs.Reveal(r.Context(), sess.User, r.PathValue("project"), req)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]string{"value": value})
	}))
	mux.HandleFunc("PUT /api/env/{project}", s.rated("env-write", 30, func(w http.ResponseWriter, r *http.Request, sess session) {
		var req domain.EnvWriteRequest
		if !decodeLimit(w, r, &req, 256<<10) {
			return
		}
		result, err := s.Envs.Write(r.Context(), sess.User, r.PathValue("project"), req)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
}
