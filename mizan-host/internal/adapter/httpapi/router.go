// Package httpapi is the HTTP delivery adapter: it translates requests into use-case calls and results
// into JSON. It holds no business rules; errors come back classified (apperr) and the kit's httpx
// turns them into status codes.
//
// Routes:
//
//	GET  /healthz /readyz            probes
//	GET  /auth/options               company and fiscal years for the sign-in page (public)
//	POST /auth/login                 browser sign-in: tokens go into httpOnly cookies, the body is the profile
//	POST /auth/refresh               browser: new cookies from the refresh cookie
//	POST /auth/logout                browser: drops the cookies (and revokes the tokens when a store is configured)
//	GET  /auth/me                    the signed-in user
//	POST /auth/token                 API clients: username + password -> {user, tokens} (no cookies)
//	POST /auth/token/refresh         API clients: {refresh_token} -> {user, tokens}
//	...  /v1/... and /accounting/... mizan's API (mounted by Deps.Mount)
//	GET  /*                          the built accounting UI, when Deps.WebDir is set
package httpapi

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"

	"github.com/PouryDev/mizan-host/internal/domain/user"
	"github.com/PouryDev/mizan-host/internal/usecase/account"
)

// Accounts is what the handlers need from the account use cases (defined here, by the consumer).
type Accounts interface {
	Login(ctx context.Context, in account.LoginInput) (account.Session, error)
	Refresh(ctx context.Context, refreshToken string) (account.Session, error)
	Me(ctx context.Context, id uuid.UUID) (user.User, error)
	Logout(ctx context.Context, accessToken, refreshToken string)
}

// FiscalYear is one entry of the sign-in page's select.
type FiscalYear struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Deps are the collaborators of the API.
type Deps struct {
	Accounts Accounts
	Verifier kitauth.TokenVerifier
	Checks   []httpx.Check // readiness probes
	Company  string
	// FiscalYears feeds GET /auth/options.
	FiscalYears func(ctx context.Context) ([]FiscalYear, error)
	// Mount registers the protected modules (mizan); authn is httpx.Authenticate(Verifier).
	Mount func(r *httpx.Router, authn httpx.Middleware)
	// SecureCookies marks the cookies Secure (everything except plain-http development).
	SecureCookies bool
	// WebDir is a directory with the built UI (index.html + assets); empty = API only.
	WebDir string
}

type handler struct {
	accounts Accounts
	deps     Deps
}

// New registers every route and wraps them in the cookie gateway.
func New(d Deps) http.Handler {
	r := httpx.NewRouter()
	httpx.RegisterHealth(r, d.Checks...)

	h := &handler{accounts: d.Accounts, deps: d}
	authenticated := httpx.Authenticate(d.Verifier)

	r.Route("GET /auth/options", h.options)
	r.Route("POST /auth/login", h.login)
	r.Route("POST /auth/refresh", h.refresh)
	r.Route("POST /auth/logout", h.logout)
	r.Route("GET /auth/me", h.me, authenticated)
	r.Route("POST /auth/token", h.token)
	r.Route("POST /auth/token/refresh", h.tokenRefresh)

	if d.Mount != nil {
		d.Mount(r, authenticated)
	}
	if d.WebDir != "" {
		r.Handle("GET /", spa(d.WebDir))
	}
	return gateway(r)
}
