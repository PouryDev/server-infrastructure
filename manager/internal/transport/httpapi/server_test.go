package httpapi

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type fakeRuntime struct{}

func (fakeRuntime) List(context.Context) ([]domain.Container, error) {
	return []domain.Container{{ID: "api", Name: "shop-api", Project: "shop", Service: "api", Managed: true, State: "running"}}, nil
}
func (fakeRuntime) Stop(context.Context, string) error  { return nil }
func (fakeRuntime) Start(context.Context, string) error { return nil }

type fakeMetrics struct{}

func (fakeMetrics) Collect(context.Context) ([]domain.Metric, error) { return nil, nil }

func newServer(t *testing.T, resources ...*application.Resources) (http.Handler, *Auth) {
	return newConfiguredServer(t, func(s *Server, _ *sqlite.Repository) {
		if len(resources) > 0 {
			s.Resources = resources[0]
		}
	})
}

func newConfiguredServer(t *testing.T, configure func(*Server, *sqlite.Repository)) (http.Handler, *Auth) {
	t.Helper()
	dir := t.TempDir()
	repo, err := sqlite.Open(filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	salt := []byte("0123456789abcdef")
	hash, err := pbkdf2.Key(sha256.New, "test-password", salt, 600000, 32)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuth("admin", hex.EncodeToString(salt)+":"+hex.EncodeToString(hash), false)
	if err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(dir, "web")
	os.Mkdir(web, 0700)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>Manager</html>"), 0600)
	server := &Server{Manager: application.New(fakeRuntime{}, repo, fakeMetrics{}), Auth: auth, Origin: "http://manager.localhost", WebDir: web}
	configure(server, repo)
	return server.Handler(), auth
}

func call(handler http.Handler, method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://manager.localhost"+path, bytes.NewBufferString(body))
	req.Header.Set("Origin", origin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestAuthenticationCSRFAndLogoutRevocation(t *testing.T) {
	handler, _ := newServer(t)
	if got := call(handler, "GET", "/api/snapshot", "", nil, "", "").Code; got != 401 {
		t.Fatalf("unauthenticated snapshot: %d", got)
	}
	if got := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "https://foreign.example").Code; got != 403 {
		t.Fatalf("cross-origin login: %d", got)
	}
	login := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 28800 {
		t.Fatal("insecure cookie settings")
	}
	var sess map[string]string
	json.Unmarshal(login.Body.Bytes(), &sess)
	if got := call(handler, "POST", "/api/projects/shop/suspend", `{"revision":0,"expiresAt":null}`, cookie, "", "http://manager.localhost").Code; got != 403 {
		t.Fatalf("missing csrf: %d", got)
	}
	if got := call(handler, "POST", "/api/projects/shop/suspend", `{"revision":0,"expiresAt":null}`, cookie, sess["csrf"], "http://manager.localhost").Code; got != 202 {
		t.Fatalf("valid policy change: %d", got)
	}
	if got := call(handler, "POST", "/api/projects/shop/renew", `{"revision":0,"expiresAt":null}`, cookie, sess["csrf"], "http://manager.localhost").Code; got != 409 {
		t.Fatalf("concurrent stale write: %d", got)
	}
	if got := call(handler, "POST", "/api/logout", "", cookie, sess["csrf"], "http://manager.localhost").Code; got != 204 {
		t.Fatalf("logout: %d", got)
	}
	if got := call(handler, "GET", "/api/session", "", cookie, "", "").Code; got != 401 {
		t.Fatalf("revoked session still works: %d", got)
	}
}

func TestLoginRateLimitAndNoIndexHeaders(t *testing.T) {
	handler, _ := newServer(t)
	for i := range 6 {
		response := call(handler, "POST", "/api/login", `{"username":"admin","password":"wrong"}`, nil, "", "http://manager.localhost")
		expected := 401
		if i == 5 {
			expected = 429
		}
		if response.Code != expected {
			t.Fatalf("attempt %d: %d", i, response.Code)
		}
	}
	for _, path := range []string{"/", "/robots.txt", "/api/snapshot", "/missing"} {
		response := call(handler, "GET", path, "", nil, "", "")
		if response.Header().Get("X-Robots-Tag") == "" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing security headers on %s", path)
		}
	}
}

func TestExpiredSessionsAndUnexpectedHostAreRejected(t *testing.T) {
	handler, auth := newServer(t)
	login := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	cookie := login.Result().Cookies()[0]
	auth.mu.Lock()
	for key, s := range auth.sessions {
		s.Expires = time.Now().Add(-time.Second)
		auth.sessions[key] = s
	}
	auth.mu.Unlock()
	if call(handler, "GET", "/api/session", "", cookie, "", "").Code != 401 {
		t.Fatal("expired session accepted")
	}
	req := httptest.NewRequest("GET", "http://foreign.example/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 403 {
		t.Fatal("unexpected host accepted")
	}
}
