package httpapi

import (
	"errors"
	"net/http"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func (s *Server) shippingRoutes(mux *http.ServeMux) {
	if s.Shipping == nil {
		return
	}
	fail := func(w http.ResponseWriter, err error) {
		code := 422
		if errors.Is(err, application.ErrConflict) || errors.Is(err, application.ErrEnvConflict) {
			code = 409
		}
		problem(w, code, err.Error())
	}
	mux.HandleFunc("GET /api/shipping/target", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Shipping.Target(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("GET /api/shipping/deployment", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Shipping.NativeState(r.Context(), r.URL.Query().Get("source"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("PUT /api/shipping/target", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		var target domain.ShipTarget
		if !decode(w, r, &target) {
			return
		}
		if err := s.Shipping.SaveTarget(r.Context(), sess.User, target); err != nil {
			fail(w, err)
			return
		}
		result, err := s.Shipping.Target(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("GET /api/shipping/catalog", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Shipping.Catalog(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("POST /api/shipping/probe", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		result, err := s.Shipping.Probe(r.Context(), sess.User)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("POST /api/shipping/plans", s.rated("plans", 30, func(w http.ResponseWriter, r *http.Request, sess session) {
		var req domain.ShipRequest
		if !decodeLimit(w, r, &req, 256<<10) {
			return
		}
		result, err := s.Shipping.Prepare(r.Context(), sess.User, req)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 201, result)
	}))
	mux.HandleFunc("POST /api/shipping/jobs", s.rated("jobs", 20, func(w http.ResponseWriter, r *http.Request, sess session) {
		var req struct {
			PlanID string `json:"planId"`
		}
		if !decode(w, r, &req) {
			return
		}
		result, err := s.Shipping.Commit(r.Context(), sess.User, req.PlanID)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 202, result)
	}))
	mux.HandleFunc("GET /api/shipping/sources", s.rated("sources", 60, func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Shipping.BuildSources(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
	mux.HandleFunc("GET /api/shipping/jobs", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		result, err := s.Shipping.Jobs(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, result)
	}))
}
