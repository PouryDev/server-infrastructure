package httpapi

import (
	"net/http"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
)

// CSRFHeader must be present on every unsafe browser request. A cross-site page cannot add a custom header
// without a CORS preflight (which this service never allows), so together with SameSite=Strict cookies it
// closes cross-site request forgery.
const CSRFHeader = "X-Mizan-UI"

// gateway sits in front of the router and does three things:
//
//  1. /accounting/... is an alias of /v1/accounting/... (mizan's accounting group);
//  2. unsafe requests that rely on cookies (no Authorization header) need the CSRF header;
//  3. the access cookie becomes "Authorization: Bearer ..." so mizan's httpx.Authenticate sees an ordinary
//     bearer token. A client that sends its own Authorization header is passed through untouched.
func gateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, "/accounting"); ok && (rest == "" || rest[0] == '/') {
			r.URL.Path, r.URL.RawPath = "/v1/accounting"+rest, ""
		}
		explicit := r.Header.Get("Authorization") != ""
		if !explicit && unsafeMethod(r.Method) && csrfProtected(r.URL.Path) && r.Header.Get(CSRFHeader) == "" {
			httpx.WriteError(w, r, apperr.Forbidden("missing "+CSRFHeader+" header"))
			return
		}
		if !explicit {
			if c, err := r.Cookie(accessCookie); err == nil && c.Value != "" {
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// csrfProtected lists the cookie-authenticated API. Token endpoints use no cookies, and payment gateways call the
// callbacks server-to-server or by redirecting the payer.
func csrfProtected(path string) bool {
	switch {
	case path == "/auth/token" || path == "/auth/token/refresh":
		return false
	case strings.HasPrefix(path, "/auth/"):
		return true
	case strings.HasPrefix(path, "/v1/gateway-callbacks/"):
		return false
	case strings.HasPrefix(path, "/v1/"):
		return true
	}
	return false
}
