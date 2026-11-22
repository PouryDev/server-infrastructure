//go:build integration

// Package integration tests the assembled host against a real PostgreSQL: users, login, JWT, the mounted
// mizan API and its schema. Run with `make test-integration` (or scripts/mizan-host.sh integration for a
// throw-away database).
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/logging"
	"github.com/PouryDev/server-infrastructure/gokit/migrate"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"

	"github.com/PouryDev/mizan-host/internal/adapter/httpapi"
	"github.com/PouryDev/mizan-host/internal/adapter/mizanmod"
	pgadapter "github.com/PouryDev/mizan-host/internal/adapter/postgres"
	"github.com/PouryDev/mizan-host/internal/domain/user"
	"github.com/PouryDev/mizan-host/internal/usecase/account"
	"github.com/PouryDev/mizan-host/internal/usecase/users"
	"github.com/PouryDev/mizan-host/migrations"
)

const password = "a-long-test-password"

type host struct {
	srv  *httptest.Server
	keys *kitauth.KeySet
	db   *kitpg.DB
}

func newHost(t *testing.T) *host {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	if _, err := (migrate.Runner{URL: url, FS: migrations.FS}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mizanmod.Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	db, err := kitpg.Connect(ctx, kitpg.Config{URL: url, StartupTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	keys, err := kitauth.KeyConfig{Secret: strings.Repeat("i", 32)}.KeySet()
	if err != nil {
		t.Fatal(err)
	}
	jwt, err := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "mizan-host", Audience: "mizan-host"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := pgadapter.NewUserRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := account.New(repo, kitauth.NewArgon2id(), jwt)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := mizanmod.New(ctx, db, logging.New(logging.Config{Level: "error", Format: "text"}))
	if err != nil {
		t.Fatal(err)
	}
	h := httpapi.New(httpapi.Deps{
		Accounts: accounts, Verifier: jwt, Company: "test co", Mount: mod.Mount,
		FiscalYears: func(ctx context.Context) ([]httpapi.FiscalYear, error) {
			ys, err := mod.FiscalYears(ctx)
			out := make([]httpapi.FiscalYear, len(ys))
			for i, y := range ys {
				out[i] = httpapi.FiscalYear{ID: y.ID, Name: y.Name, Status: y.Status}
			}
			return out, err
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return &host{srv: srv, keys: keys, db: db}
}

func (h *host) createUser(t *testing.T, role user.Role) string {
	t.Helper()
	repo, _ := pgadapter.NewUserRepository(h.db)
	name := "u" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := users.New(repo, kitauth.NewArgon2id()).Create(context.Background(),
		users.CreateInput{Username: name, Name: "Test " + name, Password: password, Role: role}); err != nil {
		t.Fatal(err)
	}
	return name
}

func (h *host) call(t *testing.T, method, path, token string, body any, hdr map[string]string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

type tokens struct {
	Tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	} `json:"tokens"`
}

func (h *host) login(t *testing.T, username string) tokens {
	t.Helper()
	code, b := h.call(t, "POST", "/auth/token", "", map[string]string{"username": username, "password": password}, nil)
	if code != 200 {
		t.Fatalf("login %d %s", code, b)
	}
	var tk tokens
	if err := json.Unmarshal(b, &tk); err != nil || tk.Tokens.Access == "" {
		t.Fatalf("%s %v", b, err)
	}
	return tk
}

func journalBody() map[string]any {
	amt := map[string]any{"amount": 1_500_000, "currency": "IRR"}
	return map[string]any{
		"date": time.Now().UTC().Format("2006-01-02"), "description": "integration test",
		"lines": []map[string]any{
			{"account": "bank.main", "side": "debit", "amount": amt},
			{"account": "equity.capital", "side": "credit", "amount": amt},
		},
	}
}

func TestLoginThenPostJournalThroughTheMountedAPI(t *testing.T) {
	h := newHost(t)
	tk := h.login(t, h.createUser(t, user.RoleFinance))

	// the accounting group answers under /accounting (alias) and /v1/accounting
	for _, p := range []string{"/accounting/journals", "/v1/accounting/journals"} {
		if code, b := h.call(t, "GET", p, tk.Tokens.Access, nil, nil); code != 200 {
			t.Fatalf("GET %s: %d %s", p, code, b)
		}
	}
	idem := map[string]string{"Idempotency-Key": uuid.NewString()}
	code, b := h.call(t, "POST", "/v1/accounting/journals", tk.Tokens.Access, journalBody(), idem)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("post journal: %d %s", code, b)
	}
	// the same Idempotency-Key does not post twice
	code2, b2 := h.call(t, "POST", "/v1/accounting/journals", tk.Tokens.Access, journalBody(), idem)
	if code2 != code {
		t.Fatalf("replay: %d %s", code2, b2)
	}
	code, b = h.call(t, "GET", "/accounting/journals", tk.Tokens.Access, nil, nil)
	if code != 200 || !strings.Contains(string(b), "integration test") {
		t.Fatalf("journal not listed: %d %s", code, b)
	}
	if code, b = h.call(t, "GET", "/auth/me", tk.Tokens.Access, nil, nil); code != 200 || !strings.Contains(string(b), `"finance"`) {
		t.Fatalf("me: %d %s", code, b)
	}
	if code, b = h.call(t, "GET", "/auth/options", "", nil, nil); code != 200 || !strings.Contains(string(b), "fiscal_years") {
		t.Fatalf("options: %d %s", code, b)
	}
}

func TestWrongExpiredAndWrongTypeTokensAre401(t *testing.T) {
	h := newHost(t)
	tk := h.login(t, h.createUser(t, user.RoleAdmin))
	other, _ := kitauth.KeyConfig{Secret: strings.Repeat("x", 32)}.KeySet()
	forgedJWT, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "mizan-host", Audience: "mizan-host"}, other)
	forged, _ := forgedJWT.IssuePair(uuid.NewString(), []string{"admin"}, nil)
	past, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "mizan-host", Audience: "mizan-host",
		Now: func() time.Time { return time.Now().Add(-2 * time.Hour) }}, h.keys)
	old, _ := past.IssuePair(uuid.NewString(), []string{"admin"}, nil)
	wrongAud, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "mizan-host", Audience: "someone-else"}, h.keys)
	aud, _ := wrongAud.IssuePair(uuid.NewString(), []string{"admin"}, nil)

	for name, token := range map[string]string{
		"none": "", "garbage": "abc.def.ghi", "forged signature": forged.AccessToken, "expired": old.AccessToken,
		"other audience": aud.AccessToken, "refresh token as access token": tk.Tokens.Refresh,
	} {
		if code, _ := h.call(t, "GET", "/v1/accounting/journals", token, nil, nil); code != 401 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := h.call(t, "POST", "/auth/token/refresh", "", map[string]string{"refresh_token": tk.Tokens.Access}, nil); code != 401 {
		t.Errorf("access token as refresh token: %d", code)
	}
}

func TestUserWithoutFinanceRoleIs403(t *testing.T) {
	h := newHost(t)
	tk := h.login(t, h.createUser(t, user.RoleUser))
	if code, _ := h.call(t, "GET", "/v1/accounting/journals", tk.Tokens.Access, nil, nil); code != 403 {
		t.Fatalf("%d", code)
	}
	if code, _ := h.call(t, "POST", "/v1/accounting/journals", tk.Tokens.Access, journalBody(), map[string]string{"Idempotency-Key": uuid.NewString()}); code != 403 {
		t.Fatalf("%d", code)
	}
	if code, _ := h.call(t, "GET", "/auth/me", tk.Tokens.Access, nil, nil); code != 200 {
		t.Fatalf("%d", code)
	}
}

func TestRefreshRereadsRoleFromTheDatabase(t *testing.T) {
	h := newHost(t)
	name := h.createUser(t, user.RoleFinance)
	tk := h.login(t, name)
	if _, err := h.db.Pool().Exec(context.Background(), `UPDATE users SET role = 'user' WHERE username = $1`, name); err != nil {
		t.Fatal(err)
	}
	// the old access token still carries the old role until it expires (stateless JWT)...
	if code, _ := h.call(t, "GET", "/v1/accounting/journals", tk.Tokens.Access, nil, nil); code != 200 {
		t.Fatalf("%d", code)
	}
	// ...but a refresh picks up the demotion
	code, b := h.call(t, "POST", "/auth/token/refresh", "", map[string]string{"refresh_token": tk.Tokens.Refresh}, nil)
	var fresh tokens
	if code != 200 || json.Unmarshal(b, &fresh) != nil {
		t.Fatalf("%d %s", code, b)
	}
	if code, _ := h.call(t, "GET", "/v1/accounting/journals", fresh.Tokens.Access, nil, nil); code != 403 {
		t.Fatalf("demoted user: %d", code)
	}
}

func TestLockoutAfterFiveWrongPasswords(t *testing.T) {
	h := newHost(t)
	name := h.createUser(t, user.RoleAdmin)
	bad := map[string]string{"username": name, "password": "definitely wrong"}
	for i := 1; i <= 4; i++ {
		if code, _ := h.call(t, "POST", "/auth/token", "", bad, nil); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code, _ := h.call(t, "POST", "/auth/token", "", bad, nil); code != 403 {
		t.Fatalf("fifth failure must lock: %d", code)
	}
	good := map[string]string{"username": name, "password": password}
	if code, b := h.call(t, "POST", "/auth/token", "", good, nil); code != 403 || !strings.Contains(string(b), "account_locked") {
		t.Fatalf("right password during the lock: %d %s", code, b)
	}
	// the lock is stored, not in memory: lift it in the database as an expired one
	if _, err := h.db.Pool().Exec(context.Background(), `UPDATE users SET locked_until = now() - interval '1 second' WHERE username = $1`, name); err != nil {
		t.Fatal(err)
	}
	if code, b := h.call(t, "POST", "/auth/token", "", good, nil); code != 200 {
		t.Fatalf("after the lock: %d %s", code, b)
	}
}

// The browser flow end to end: httpOnly cookies, CSRF header, refresh and logout.
func TestBrowserCookieFlow(t *testing.T) {
	h := newHost(t)
	name := h.createUser(t, user.RoleFinance)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	do := func(method, path string, body any, csrf bool) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequestWithContext(context.Background(), method, h.srv.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		if csrf {
			req.Header.Set(httpapi.CSRFHeader, "1")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	if code, _ := do("GET", "/auth/me", nil, false); code != 401 {
		t.Fatalf("anonymous /auth/me: %d", code)
	}
	if code, b := do("POST", "/auth/login", map[string]string{"username": name, "password": password}, true); code != 200 || strings.Contains(string(b), "token") {
		t.Fatalf("login: %d %s", code, b)
	}
	if code, _ := do("GET", "/auth/me", nil, false); code != 200 {
		t.Fatalf("/auth/me with cookie: %d", code)
	}
	if code, _ := do("POST", "/v1/accounting/journals", journalBody(), false); code != 403 {
		t.Fatalf("write without CSRF header: %d", code)
	}
	if code, _ := do("POST", "/auth/refresh", nil, true); code != 200 {
		t.Fatalf("refresh: %d", code)
	}
	if code, _ := do("POST", "/auth/logout", nil, true); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := do("GET", "/auth/me", nil, false); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}
