package httpapi

import (
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"net/http"
	"time"
)

func (s *Server) deploymentRoutes(m *http.ServeMux) {
	if s.Deployments == nil {
		return
	}
	m.HandleFunc("GET /api/deployments/catalog", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		out, err := s.Deployments.Catalog(r.Context())
		if err != nil {
			problem(w, 503, err.Error())
			return
		}
		respond(w, 200, out)
	}))
	m.HandleFunc("POST /api/deployments/upload", s.rated("deployment-upload", 6, func(w http.ResponseWriter, r *http.Request, sess session) {
		controller := http.NewResponseController(w)
		controller.SetReadDeadline(time.Now().Add(30 * time.Minute))
		controller.SetWriteDeadline(time.Now().Add(31 * time.Minute))
		if r.ContentLength <= 0 || r.ContentLength > application.MaxDeploymentUpload {
			problem(w, 413, "Package exceeds the 2 GiB upload limit")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, application.MaxDeploymentUpload)
		out, err := s.Deployments.Upload(r.Context(), sess.User, r.URL.Query().Get("project"), r.Body, r.ContentLength)
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		respond(w, 200, out)
	}))
	for _, action := range []string{"save", "deploy"} {
		m.HandleFunc("POST /api/deployments/"+action, s.rated("deployment-"+action, 20, func(w http.ResponseWriter, r *http.Request, sess session) {
			var body domain.DeploymentRequest
			if !decodeLimit(w, r, &body, 128<<10) {
				return
			}
			out, err := s.Deployments.Change(r.Context(), sess.User, action, body)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			respond(w, 202, out)
		}))
	}
}
