package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"

	"github.com/PouryDev/mizan-host/internal/domain/user"
	"github.com/PouryDev/mizan-host/internal/usecase/account"
)

// fakeAccounts signs in one user with real tokens.
type fakeAccounts struct {
	jwt  *kitauth.JWT
	user user.User
}

func (f fakeAccounts) session() account.Session {
	pair, _ := f.jwt.IssuePair(f.user.ID.String(), []string{string(f.user.Role)}, nil)
	return account.Session{User: f.user, Tokens: pair}
}

func (f fakeAccounts) Login(_ context.Context, in account.LoginInput) (account.Session, error) {
	if in.Username != f.user.Username || in.Password != "right-password" {
		return account.Session{}, apperr.Unauthorized("invalid username or password")
	}
	return f.session(), nil
}

func (f fakeAccounts) Refresh(ctx context.Context, tok string) (account.Session, error) {
	if _, err := f.jwt.Verify(ctx, tok, kitauth.TypeRefresh); err != nil {
		return account.Session{}, err
	}
	return f.session(), nil
}
func (f fakeAccounts) Me(context.Context, uuid.UUID) (user.User, error) { return f.user, nil }
func (fakeAccounts) Logout(context.Context, string, string)             {}

type env struct {
	h   http.Handler
	jwt *kitauth.JWT
}

func setup(t *testing.T, role user.Role, webDir string) env {
	t.Helper()
	keys, _ := kitauth.KeyConfig{Secret: strings.Repeat("k", 32)}.KeySet()
	jwt, err := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "t", Audience: "t"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	u := user.User{ID: uuid.New(), Username: "m.ahmadi", Name: "مریم", Role: role, CreatedAt: time.Now()}
	h := New(Deps{
		Accounts: fakeAccounts{jwt, u}, Verifier: jwt, Company: "شرکت نمونه", WebDir: webDir,
		FiscalYears: func(context.Context) ([]FiscalYear, error) { return []FiscalYear{{1, "1405", "open"}}, nil },
		Mount: func(r *httpx.Router, authn httpx.Middleware) {
			// stands in for mizan/api.Mount: finance routes behind Authenticate + RequireRoles("finance", "admin")
			fin := r.Group(authn, httpx.RequireRoles("finance", "admin"))
			fin.Route("GET /v1/accounting/journals", func(w http.ResponseWriter, _ *http.Request) error {
				httpx.JSON(w, http.StatusOK, map[string]any{"items": []any{}})
				return nil
			})
			fin.Route("POST /v1/accounting/journals", func(w http.ResponseWriter, _ *http.Request) error {
				httpx.JSON(w, http.StatusCreated, map[string]any{"ok": true})
				return nil
			})
		},
	})
	return env{h, jwt}
}

type opt func(*http.Request)

func bearer(tok string) opt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}
func csrf() opt { return func(r *http.Request) { r.Header.Set(CSRFHeader, "1") } }
func cookie(n, v string) opt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: n, Value: v}) }
}

func (e env) do(method, target, body string, opts ...opt) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// pair issues tokens as if now were age ago (a negative-looking ttl is not accepted by the kit).
func (e env) pair(t *testing.T, role string, age time.Duration) kitauth.TokenPair {
	t.Helper()
	keys, _ := kitauth.KeyConfig{Secret: strings.Repeat("k", 32)}.KeySet()
	j, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "t", Audience: "t", Now: func() time.Time { return time.Now().Add(-age) }}, keys)
	p, err := j.IssuePair(uuid.NewString(), []string{role}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cookiesOf(rec *httptest.ResponseRecorder) map[string]*http.Cookie {
	m := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		m[c.Name] = c
	}
	return m
}

func TestLoginSetsHttpOnlyCookiesAndNoTokenInBody(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	rec := e.do("POST", "/auth/login", `{"username":"m.ahmadi","password":"right-password"}`, csrf())
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	cs := cookiesOf(rec)
	for _, n := range []string{accessCookie, refreshCookie} {
		c := cs[n]
		if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Value == "" {
			t.Fatalf("cookie %s: %+v", n, c)
		}
	}
	if cs[refreshCookie].Path != "/auth" {
		t.Fatalf("refresh cookie must only travel to /auth: %q", cs[refreshCookie].Path)
	}
	if strings.Contains(rec.Body.String(), cs[accessCookie].Value) || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("tokens leaked into the body: %s", rec.Body)
	}
	var p profileResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Username != "m.ahmadi" || p.Company != "شرکت نمونه" || p.Roles[0] != "finance" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestLoginRejectsBadPasswordAndMissingCSRFHeader(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	if rec := e.do("POST", "/auth/login", `{"username":"m.ahmadi","password":"nope"}`, csrf()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
	if rec := e.do("POST", "/auth/login", `{"username":"m.ahmadi","password":"right-password"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("login without %s: %d", CSRFHeader, rec.Code)
	}
	if rec := e.do("POST", "/auth/login", `{"username":"m.ahmadi","password":"right-password","admin":true}`, csrf()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown fields must be rejected: %d", rec.Code)
	}
}

func TestCookieSessionReachesTheFinanceAPIAndNeedsCSRFHeaderToWrite(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	tok := e.pair(t, "finance", 0).AccessToken
	if rec := e.do("GET", "/v1/accounting/journals", "", cookie(accessCookie, tok)); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/v1/accounting/journals", `{}`, cookie(accessCookie, tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("cookie write without CSRF header: %d", rec.Code)
	}
	if rec := e.do("POST", "/v1/accounting/journals", `{}`, cookie(accessCookie, tok), csrf()); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestBearerClientsNeedNoCSRFHeader(t *testing.T) {
	e := setup(t, user.RoleAdmin, "")
	tok := e.pair(t, "admin", 0).AccessToken
	if rec := e.do("POST", "/v1/accounting/journals", `{}`, bearer(tok)); rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAuthenticationFailuresAre401(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	pair := e.pair(t, "finance", 0)
	expired := e.pair(t, "finance", time.Hour)
	otherKeys, _ := kitauth.KeyConfig{Secret: strings.Repeat("z", 32)}.KeySet()
	other, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "t", Audience: "t"}, otherKeys)
	forged, _ := other.IssuePair(uuid.NewString(), []string{"admin"}, nil)

	for name, o := range map[string]opt{
		"no token":                 nil,
		"garbage":                  bearer("not.a.jwt"),
		"expired access token":     bearer(expired.AccessToken),
		"refresh token as access":  bearer(pair.RefreshToken),
		"wrong signing key":        bearer(forged.AccessToken),
		"refresh cookie as access": cookie(accessCookie, pair.RefreshToken),
	} {
		var opts []opt
		if o != nil {
			opts = append(opts, o)
		}
		if rec := e.do("GET", "/v1/accounting/journals", "", opts...); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestRoleWithoutFinanceIs403(t *testing.T) {
	e := setup(t, user.RoleUser, "")
	tok := e.pair(t, "user", 0).AccessToken
	if rec := e.do("GET", "/v1/accounting/journals", "", bearer(tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("%d", rec.Code)
	}
	// ...but /auth/me works for any signed-in user
	if rec := e.do("GET", "/auth/me", "", bearer(tok)); rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
}

func TestAccountingAlias(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	tok := e.pair(t, "finance", 0).AccessToken
	if rec := e.do("GET", "/accounting/journals", "", bearer(tok)); rec.Code != http.StatusOK {
		t.Fatalf("alias: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/accounting/journals", `{}`, cookie(accessCookie, tok)); rec.Code != http.StatusForbidden {
		t.Fatalf("alias must keep the CSRF rule: %d", rec.Code)
	}
	if rec := e.do("GET", "/accountingx/journals", "", bearer(tok)); rec.Code == http.StatusOK {
		t.Fatal("only the /accounting path segment is aliased")
	}
}

func TestRefreshFromCookie(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	pair := e.pair(t, "finance", 0)
	rec := e.do("POST", "/auth/refresh", "", cookie(refreshCookie, pair.RefreshToken), csrf())
	if rec.Code != http.StatusOK || cookiesOf(rec)[accessCookie] == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	// an access token in the refresh cookie is refused and the cookies are cleared
	rec = e.do("POST", "/auth/refresh", "", cookie(refreshCookie, pair.AccessToken), csrf())
	if rec.Code != http.StatusUnauthorized || cookiesOf(rec)[accessCookie].MaxAge >= 0 {
		t.Fatalf("%d %v", rec.Code, cookiesOf(rec))
	}
	if rec := e.do("POST", "/auth/refresh", "", csrf()); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", rec.Code)
	}
}

func TestLogoutClearsCookiesEvenWithAnExpiredSession(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	rec := e.do("POST", "/auth/logout", "", cookie(accessCookie, "expired"), csrf())
	cs := cookiesOf(rec)
	if rec.Code != http.StatusNoContent || cs[accessCookie] == nil || cs[accessCookie].MaxAge >= 0 || cs[refreshCookie].MaxAge >= 0 {
		t.Fatalf("%d %v", rec.Code, cs)
	}
	if rec := e.do("POST", "/auth/logout", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("logout is CSRF-protected too: %d", rec.Code)
	}
}

func TestTokenEndpointsForAPIClients(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	rec := e.do("POST", "/auth/token", `{"username":"m.ahmadi","password":"right-password"}`) // no CSRF header, no cookies
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("%d cookies=%v", rec.Code, rec.Result().Cookies())
	}
	var tr tokenResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	if tr.Tokens.AccessToken == "" || tr.Tokens.RefreshToken == "" {
		t.Fatalf("%s", rec.Body)
	}
	if rec := e.do("GET", "/v1/accounting/journals", "", bearer(tr.Tokens.AccessToken)); rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
	rec = e.do("POST", "/auth/token/refresh", `{"refresh_token":"`+tr.Tokens.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/auth/token/refresh", `{"refresh_token":"`+tr.Tokens.AccessToken+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("access token accepted as refresh token: %d", rec.Code)
	}
	if rec := e.do("POST", "/auth/token", `{"username":"m.ahmadi","password":"bad"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("%d", rec.Code)
	}
}

func TestOptionsAreCompanyAndFiscalYears(t *testing.T) {
	e := setup(t, user.RoleFinance, "")
	rec := e.do("GET", "/auth/options", "")
	var o struct {
		Company     string       `json:"company"`
		FiscalYears []FiscalYear `json:"fiscal_years"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &o); err != nil || o.Company != "شرکت نمونه" || len(o.FiscalYears) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestServesTheUIWithSPAFallbackButNotForAPIPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ui</html>"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "assets"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("js"), 0o600)
	e := setup(t, user.RoleFinance, dir)

	if rec := e.do("GET", "/", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ui") {
		t.Fatalf("%d", rec.Code)
	}
	if rec := e.do("GET", "/journals/new", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "ui") {
		t.Fatalf("client route: %d", rec.Code)
	}
	rec := e.do("GET", "/assets/app.js", "")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if csp := e.do("GET", "/", "").Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src") && !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("the UI needs a CSP that allows its own scripts: %q", csp)
	}
	if rec := e.do("GET", "/v1/nothing", ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "ui") {
		t.Fatalf("API paths must 404, not return the page: %d", rec.Code)
	}
}
