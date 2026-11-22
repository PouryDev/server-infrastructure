package main

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
)

// DEV-ONLY sign-in. This repository has no identity service, so the UI needs something to log in against while it is
// developed. This file is the smallest honest stand-in:
//
//   - one user from the environment (DEV_USER / DEV_PASSWORD), the password hashed with argon2id;
//   - gokit's stateless JWT pair; the tokens live in httpOnly, SameSite=Strict cookies, so the page's JavaScript never
//     sees them (nothing in localStorage);
//   - a gateway that turns the access cookie into the Authorization header mizan/api expects, so the UI and the API share
//     one origin and need no CORS;
//   - a CSRF guard on top of SameSite: every unsafe request must carry the custom header X-Mizan-UI.
//
// A production deployment replaces all of it with the project's identity provider.

const (
	accessCookie  = "mz_at"
	refreshCookie = "mz_rt"
	csrfHeader    = "X-Mizan-UI"
	maxFailures   = 5
	lockFor       = 15 * time.Minute
)

type devUser struct{ Username, Password, Name, Title, Company string }

type devAuth struct {
	jwt     *auth.JWT
	m       *mizan.Mizan
	user    devUser
	hash    string
	hasher  auth.PasswordHasher
	secure  bool
	mu      sync.Mutex
	fails   int
	lockEnd time.Time
}

func newDevAuth(jwt *auth.JWT, m *mizan.Mizan, u devUser, secure bool) (*devAuth, error) {
	if len(u.Password) < 8 {
		return nil, errors.New("DEV_PASSWORD (at least 8 characters) is required: there is no default password")
	}
	h := auth.NewArgon2id()
	hash, err := h.Hash(u.Password)
	if err != nil {
		return nil, err
	}
	u.Password = ""
	return &devAuth{jwt: jwt, m: m, user: u, hash: hash, hasher: h, secure: secure}, nil
}

func (d *devAuth) mount(r *httpx.Router) {
	r.Route("GET /auth/options", d.options)
	r.Route("POST /auth/login", d.login)
	r.Route("POST /auth/refresh", d.refresh)
	r.Route("POST /auth/logout", d.logout)
	r.Route("GET /auth/me", d.me)
}

type profile struct {
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Company  string   `json:"company"`
	Roles    []string `json:"roles"`
}

func (d *devAuth) profile() profile {
	return profile{Username: d.user.Username, Name: d.user.Name, Title: d.user.Title, Company: d.user.Company, Roles: []string{"finance", "admin"}}
}

// options feeds the "company and fiscal year" select of the sign-in page (dev-only: public).
func (d *devAuth) options(w http.ResponseWriter, r *http.Request) error {
	fys, err := d.m.Ledger.FiscalYears(r.Context())
	if err != nil {
		return err
	}
	type year struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	years := make([]year, len(fys))
	for i, f := range fys {
		years[i] = year{f.ID, f.Name, string(f.Status)}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"company": d.user.Company, "fiscal_years": years})
	return nil
}

func (d *devAuth) login(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}](r)
	if err != nil {
		return err
	}
	if !d.allow() {
		return apperr.New(apperr.KindForbidden, "account_locked", "too many failed attempts; try again in 15 minutes")
	}
	ok, _, verr := d.hasher.Verify(req.Password, d.hash)
	if verr != nil || !ok || strings.TrimSpace(req.Username) != d.user.Username {
		d.fail()
		return apperr.Unauthorized("invalid credentials")
	}
	d.reset()
	return d.issue(w, d.profile())
}

func (d *devAuth) refresh(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(refreshCookie)
	if err != nil {
		return apperr.Unauthorized("no session")
	}
	if _, err := d.jwt.Verify(r.Context(), c.Value, auth.TypeRefresh); err != nil {
		clearCookies(w, d.secure)
		return err
	}
	return d.issue(w, d.profile())
}

func (d *devAuth) logout(w http.ResponseWriter, _ *http.Request) error {
	clearCookies(w, d.secure)
	httpx.NoContent(w)
	return nil
}

func (d *devAuth) me(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(accessCookie)
	if err != nil {
		return apperr.Unauthorized("no session")
	}
	if _, err := d.jwt.Verify(r.Context(), c.Value, auth.TypeAccess); err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, d.profile())
	return nil
}

func (d *devAuth) issue(w http.ResponseWriter, p profile) error {
	pair, err := d.jwt.IssuePair(p.Username, p.Roles, map[string]any{"name": p.Name})
	if err != nil {
		return err
	}
	set := func(name, value, path string, exp time.Time) {
		//nolint:gosec // dev-only: Secure follows COOKIE_SECURE because the local stack is plain http on localhost
		http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, Expires: exp, HttpOnly: true, Secure: d.secure, SameSite: http.SameSiteStrictMode})
	}
	set(accessCookie, pair.AccessToken, "/", pair.AccessExpiresAt)
	set(refreshCookie, pair.RefreshToken, "/auth", pair.RefreshExpiresAt)
	httpx.JSON(w, http.StatusOK, p)
	return nil
}

func clearCookies(w http.ResponseWriter, secure bool) {
	for _, c := range []struct{ name, path string }{{accessCookie, "/"}, {refreshCookie, "/auth"}} {
		//nolint:gosec // dev-only: see issue()
		http.SetCookie(w, &http.Cookie{Name: c.name, Value: "", Path: c.path, MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode})
	}
}

func (d *devAuth) allow() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fails < maxFailures || time.Now().After(d.lockEnd)
}

func (d *devAuth) fail() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().After(d.lockEnd) && d.fails >= maxFailures {
		d.fails = 0
	}
	d.fails++
	if d.fails >= maxFailures {
		d.lockEnd = time.Now().Add(lockFor)
	}
}

func (d *devAuth) reset() {
	d.mu.Lock()
	d.fails = 0
	d.mu.Unlock()
}

// cookieGateway guards unsafe requests with the CSRF header and lets the API read the access cookie as a bearer token.
func (d *devAuth) cookieGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		if unsafe && r.URL.Path != "/healthz" && strings.HasPrefix(r.URL.Path, "/auth/") && r.Header.Get(csrfHeader) == "" ||
			unsafe && strings.HasPrefix(r.URL.Path, "/v1/") && !strings.HasPrefix(r.URL.Path, "/v1/gateway-callbacks/") && r.Header.Get(csrfHeader) == "" {
			httpx.WriteError(w, r, apperr.Forbidden("missing "+csrfHeader+" header"))
			return
		}
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(accessCookie); err == nil && c.Value != "" {
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
}
