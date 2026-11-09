// Package auth implements stateless authentication: signed JWT access and refresh tokens, and
// argon2id password hashing. It has no HTTP or database dependency; httpx adds the middleware.
//
// Stateless means no server-side session store: a token is valid when its signature, issuer,
// audience, type and expiry check out. The price is that a token cannot be revoked before it expires,
// so keep access tokens short-lived (default 15 minutes). Every token carries a unique ID (jti) so a
// project that needs revocation can add a deny-list later without changing the token format.
package auth

import (
	"context"
	"slices"
	"time"
)

// TokenType separates access tokens from refresh tokens so one can never be used as the other.
type TokenType string

const (
	TypeAccess  TokenType = "access"
	TypeRefresh TokenType = "refresh"
)

// Claims is the verified content of a token.
type Claims struct {
	Subject   string
	Roles     []string
	Extra     map[string]any // application-specific, non-sensitive (a JWT is signed, not encrypted)
	Type      TokenType
	ID        string
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// HasAnyRole reports whether the subject has at least one of roles.
func (c *Claims) HasAnyRole(roles ...string) bool {
	return slices.ContainsFunc(roles, func(r string) bool { return slices.Contains(c.Roles, r) })
}

// TokenPair is returned on login and refresh.
type TokenPair struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"` // always "Bearer"
	ExpiresIn        int       `json:"expires_in"` // access token lifetime in seconds
	AccessExpiresAt  time.Time `json:"-"`
	RefreshExpiresAt time.Time `json:"-"`
}

// TokenVerifier is what request authentication needs (and nothing more).
type TokenVerifier interface {
	Verify(ctx context.Context, token string, want TokenType) (*Claims, error)
}

// RevocationChecker is the optional hook that turns stateless tokens into revocable ones: Verify asks it
// about every token that passed the signature checks. Implementations must be fast (one Redis round
// trip); redisx.Revocations is one.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, c *Claims) (bool, error)
}

// Revoker records revocations for a RevocationChecker to find.
type Revoker interface {
	// Revoke invalidates one token (by its id) until it would have expired anyway.
	Revoke(ctx context.Context, c *Claims) error
	// RevokeSubject invalidates every token issued to subject up to now ("log out everywhere").
	RevokeSubject(ctx context.Context, subject string) error
}

// SessionIssuer is what login and refresh use cases need (and nothing more).
type SessionIssuer interface {
	IssuePair(subject string, roles []string, extra map[string]any) (TokenPair, error)
}

type ctxKey struct{}

// WithClaims stores verified claims in the context.
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// ClaimsFrom returns the claims set by the authentication middleware.
func ClaimsFrom(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok && c != nil
}
