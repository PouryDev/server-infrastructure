package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type vpnAPIRunner struct{ changes, tests int }

func (v *vpnAPIRunner) Status(context.Context) (domain.VPNStatus, error) {
	return domain.VPNStatus{Revision: strings.Repeat("a", 64)}, nil
}
func (v *vpnAPIRunner) Change(context.Context, domain.VPNChange) (domain.VPNStatus, error) {
	v.changes++
	return domain.VPNStatus{Configured: true, Enabled: true, Running: true}, nil
}
func (v *vpnAPIRunner) Test(context.Context) error { v.tests++; return nil }

func TestVPNRequiresSessionOriginCSRFAndDoesNotAuditSecrets(t *testing.T) {
	runner := &vpnAPIRunner{}
	var repo *sqlite.Repository
	handler, _ := newConfiguredServer(t, func(s *Server, r *sqlite.Repository) { repo = r; s.VPN = &application.VPN{Runner: runner, Repo: r} })
	body := `{"link":"vless://very-private-credential@vpn.example:443","enabled":true,"revision":"` + strings.Repeat("a", 64) + `"}`
	if got := call(handler, "GET", "/api/vpn", "", nil, "", "http://manager.localhost").Code; got != 401 {
		t.Fatal(got)
	}
	if got := call(handler, "PUT", "/api/vpn", body, nil, "", "http://manager.localhost").Code; got != 401 {
		t.Fatal(got)
	}
	login := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	cookie := login.Result().Cookies()[0]
	var sess map[string]string
	json.Unmarshal(login.Body.Bytes(), &sess)
	for _, path := range []string{"/api/vpn", "/api/vpn/test"} {
		method := "PUT"
		if strings.HasSuffix(path, "test") {
			method = "POST"
		}
		if got := call(handler, method, path, body, cookie, "", "http://manager.localhost").Code; got != 403 {
			t.Fatal(got)
		}
		if got := call(handler, method, path, body, cookie, sess["csrf"], "https://other.example").Code; got != 403 {
			t.Fatal(got)
		}
	}
	if runner.changes != 0 || runner.tests != 0 {
		t.Fatal("unauthorized mutation")
	}
	if got := call(handler, "PUT", "/api/vpn", strings.Replace(body, strings.Repeat("a", 64), "", 1), cookie, sess["csrf"], "http://manager.localhost").Code; got != 422 {
		t.Fatal(got)
	}
	if got := call(handler, "PUT", "/api/vpn", body, cookie, sess["csrf"], "http://manager.localhost").Code; got != 200 || runner.changes != 1 {
		t.Fatal(got, runner.changes)
	}
	events, err := repo.Audit(context.Background(), "infra", 0, 50)
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	b, _ := json.Marshal(events)
	if strings.Contains(string(b), "credential") || strings.Contains(string(b), "vpn.example") {
		t.Fatal("VPN credentials leaked into audit")
	}
	if got := call(handler, "POST", "/api/vpn/test", "", cookie, sess["csrf"], "http://manager.localhost").Code; got != 200 || runner.tests != 1 {
		t.Fatal(got, runner.tests)
	}
}
