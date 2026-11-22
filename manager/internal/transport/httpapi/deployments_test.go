package httpapi

import (
	"context"
	"encoding/json"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"io"
	"testing"
)

type deploymentRunner struct{ called bool }

func (d *deploymentRunner) DeploymentCatalog(context.Context) (domain.DeploymentCatalog, error) {
	return domain.DeploymentCatalog{Projects: []domain.DeploymentSettings{}, Jobs: []domain.DeploymentJob{}}, nil
}
func (d *deploymentRunner) UploadDeployment(context.Context, string, io.Reader, int64) (domain.DeploymentPackage, error) {
	d.called = true
	return domain.DeploymentPackage{}, nil
}
func (d *deploymentRunner) ChangeDeployment(context.Context, string, domain.DeploymentRequest) (domain.DeploymentJob, error) {
	d.called = true
	return domain.DeploymentJob{Status: "queued"}, nil
}

func TestProjectDeploymentRequiresSessionOriginAndCSRF(t *testing.T) {
	runner := &deploymentRunner{}
	handler, _ := newConfiguredServer(t, func(s *Server, repo *sqlite.Repository) {
		s.Deployments = &application.Deployments{Runner: runner, Repo: repo}
	})
	if res := call(handler, "GET", "/api/deployments/catalog", "", nil, "", ""); res.Code != 401 {
		t.Fatal(res.Code)
	}
	login := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	cookie := login.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf"`
	}
	json.Unmarshal(login.Body.Bytes(), &session)
	body := `{"name":"myapp","key":"00000000-0000-0000-0000-000000000000","revision":"rev","domain":"app.example.com","service":"myapp-api","port":8080,"environment":{"SECRET":"private-value"}}`
	for _, tc := range []struct{ csrf, origin string }{{"", "http://manager.localhost"}, {session.CSRF, "http://foreign.example"}} {
		if res := call(handler, "POST", "/api/deployments/save", body, cookie, tc.csrf, tc.origin); res.Code != 403 {
			t.Fatal(res.Code)
		}
	}
	if runner.called {
		t.Fatal("rejected request reached destination")
	}
	if res := call(handler, "POST", "/api/deployments/save", body, cookie, session.CSRF, "http://manager.localhost"); res.Code != 202 {
		t.Fatal(res.Code, res.Body.String())
	}
}
