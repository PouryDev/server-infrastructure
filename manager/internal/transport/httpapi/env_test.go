package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type fakeEnvRemote struct {
	mu      sync.Mutex
	writes  [][]domain.EnvChange
	revised string
}

func (f *fakeEnvRemote) EnvProjects(context.Context, domain.ShipTarget) ([]domain.EnvProject, error) {
	return []domain.EnvProject{{Name: "shop", Services: []string{"api"}}}, nil
}
func (f *fakeEnvRemote) EnvState(_ context.Context, _ domain.ShipTarget, project string) (domain.EnvStateRaw, error) {
	return domain.EnvStateRaw{Project: project, Revision: strings.Repeat("a", 64), Services: []domain.EnvServiceInfo{{Name: "api", UsesProjectEnv: true}}, Files: []domain.EnvFileRaw{{Exists: true, Variables: []domain.EnvPair{{Name: "DB_PASSWORD", Value: "hunter2-value"}, {Name: "APP_ENV", Value: "production"}}}}}, nil
}
func (f *fakeEnvRemote) EnvWrite(_ context.Context, _ domain.ShipTarget, _, revision string, changes []domain.EnvChange) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if revision != strings.Repeat("a", 64) {
		return application.ErrEnvConflict
	}
	f.writes = append(f.writes, changes)
	return nil
}
func (f *fakeEnvRemote) BuildArgs(context.Context, domain.ShipPlan) (map[string]string, error) {
	return nil, nil
}

type envHarness struct {
	handler http.Handler
	remote  *fakeEnvRemote
	repo    *sqlite.Repository
	cookie  *http.Cookie
	csrf    string
}

func newEnvHarness(t *testing.T) *envHarness {
	t.Helper()
	h := &envHarness{remote: &fakeEnvRemote{}}
	h.handler, _ = newConfiguredServer(t, func(s *Server, repo *sqlite.Repository) {
		h.repo = repo
		s.Envs = application.NewEnvs(h.remote, repo, repo)
	})
	login := call(h.handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	h.cookie = login.Result().Cookies()[0]
	var sess map[string]string
	json.Unmarshal(login.Body.Bytes(), &sess)
	h.csrf = sess["csrf"]
	return h
}
func (h *envHarness) do(method, path, body string) *httptest.ResponseRecorder {
	return call(h.handler, method, path, body, h.cookie, h.csrf, "http://manager.localhost")
}
func (h *envHarness) audit(t *testing.T) string {
	events, err := h.repo.Audit(context.Background(), "", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(events)
	return string(data)
}

func TestEnvEndpointsRequireSessionOriginAndCSRF(t *testing.T) {
	h := newEnvHarness(t)
	write := `{"revision":"` + strings.Repeat("a", 64) + `","changes":[{"scope":"","name":"A","action":"set","value":"v"}]}`
	if got := call(h.handler, "GET", "/api/env/shop", "", nil, "", "").Code; got != 401 {
		t.Fatal(got)
	}
	if got := call(h.handler, "PUT", "/api/env/shop", write, h.cookie, "", "http://manager.localhost").Code; got != 403 {
		t.Fatalf("missing CSRF accepted: %d", got)
	}
	if got := call(h.handler, "PUT", "/api/env/shop", write, h.cookie, h.csrf, "https://evil.example").Code; got != 403 {
		t.Fatalf("foreign origin accepted: %d", got)
	}
	if got := call(h.handler, "POST", "/api/env/shop/reveal", `{"scope":"","name":"A"}`, h.cookie, "", "http://manager.localhost").Code; got != 403 {
		t.Fatal(got)
	}
	if len(h.remote.writes) != 0 {
		t.Fatal("rejected request reached the destination")
	}
}
func TestEnvListsAreMaskedAndRevealAndWritesAuditOnlyNames(t *testing.T) {
	h := newEnvHarness(t)
	list := h.do("GET", "/api/env/shop", "")
	if list.Code != 200 || strings.Contains(list.Body.String(), "hunter2-value") || strings.Contains(list.Body.String(), "production") || !strings.Contains(list.Body.String(), `"sensitive":true`) {
		t.Fatalf("%d %s", list.Code, list.Body.String())
	}
	reveal := h.do("POST", "/api/env/shop/reveal", `{"scope":"","name":"DB_PASSWORD"}`)
	if reveal.Code != 200 || !strings.Contains(reveal.Body.String(), "hunter2-value") {
		t.Fatalf("%d %s", reveal.Code, reveal.Body.String())
	}
	if h.do("POST", "/api/env/shop/reveal", `{"scope":"","name":"MISSING"}`).Code != 422 {
		t.Fatal("unknown variable revealed")
	}
	save := h.do("PUT", "/api/env/shop", `{"revision":"`+strings.Repeat("a", 64)+`","changes":[{"scope":"","name":"NEW_SECRET","action":"set","value":"never-in-audit-123"},{"scope":"api","name":"OLD","action":"delete"}]}`)
	if save.Code != 200 || strings.Contains(save.Body.String(), "never-in-audit-123") || len(h.remote.writes) != 1 {
		t.Fatalf("%d %s", save.Code, save.Body.String())
	}
	log := h.audit(t)
	for _, secret := range []string{"hunter2-value", "never-in-audit-123", "production"} {
		if strings.Contains(log, secret) {
			t.Fatalf("audit contains a value: %s", secret)
		}
	}
	for _, want := range []string{"env_reveal", "env_change", "NEW_SECRET", "DB_PASSWORD", "api delete=OLD"} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit lacks %q: %s", want, log)
		}
	}
}
func TestEnvWriteValidationAndConflict(t *testing.T) {
	h := newEnvHarness(t)
	rev := strings.Repeat("a", 64)
	for name, body := range map[string]string{
		"lowercase name": `{"revision":"` + rev + `","changes":[{"scope":"","name":"bad","action":"set","value":"v"}]}`,
		"newline":        `{"revision":"` + rev + `","changes":[{"scope":"","name":"A","action":"set","value":"a\nb"}]}`,
		"unknown action": `{"revision":"` + rev + `","changes":[{"scope":"","name":"A","action":"wipe"}]}`,
		"empty":          `{"revision":"` + rev + `","changes":[]}`,
		"no revision":    `{"changes":[{"scope":"","name":"A","action":"set"}]}`,
		"duplicate":      `{"revision":"` + rev + `","changes":[{"scope":"","name":"A","action":"set"},{"scope":"","name":"A","action":"delete"}]}`,
	} {
		if got := h.do("PUT", "/api/env/shop", body).Code; got != 422 {
			t.Errorf("%s: %d", name, got)
		}
	}
	if got := h.do("PUT", "/api/env/shop", `{"revision":"`+rev+`","changes":[],"extra":1}`).Code; got != 400 {
		t.Errorf("unknown field: %d", got)
	}
	if got := h.do("PUT", "/api/env/infra", `{"revision":"`+rev+`","changes":[{"scope":"","name":"A","action":"set"}]}`).Code; got != 422 {
		t.Errorf("infra project accepted: %d", got)
	}
	if got := h.do("GET", "/api/env/Bad_Name", "").Code; got != 422 {
		t.Errorf("project name not validated: %d", got)
	}
	if got := h.do("PUT", "/api/env/shop", `{"revision":"`+strings.Repeat("b", 64)+`","changes":[{"scope":"","name":"A","action":"set","value":"v"}]}`).Code; got != 409 {
		t.Errorf("stale revision: %d", got)
	}
	if len(h.remote.writes) != 0 {
		t.Fatal("invalid request reached the destination")
	}
}
func TestEnvRevealIsRateLimited(t *testing.T) {
	h := newEnvHarness(t)
	last := 0
	for i := 0; i < 40; i++ {
		last = h.do("POST", "/api/env/shop/reveal", `{"scope":"","name":"APP_ENV"}`).Code
	}
	if last != 429 {
		t.Fatalf("reveal was not rate limited: %d", last)
	}
}
