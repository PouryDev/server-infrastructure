package httpx

import (
	"net/http"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

// Authenticate requires a valid access token. It puts the claims in the request context (see
// auth.ClaimsFrom) and marks the response private and uncacheable, as the CDN contract requires for
// authenticated responses.
func Authenticate(v auth.TokenVerifier) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				WriteError(w, r, apperr.Unauthorized("missing bearer token"))
				return
			}
			claims, err := v.Verify(r.Context(), token, auth.TypeAccess)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
		})
	}
}

// RequireRoles allows the request only when the authenticated subject has at least one of roles.
// Put it after Authenticate.
func RequireRoles(roles ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFrom(r.Context())
			if !ok {
				WriteError(w, r, apperr.Unauthorized("authentication required"))
				return
			}
			if !claims.HasAnyRole(roles...) {
				WriteError(w, r, apperr.Forbidden("insufficient permissions"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
