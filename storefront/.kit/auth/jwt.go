package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// JWTConfig configures the token service.
type JWTConfig struct {
	Issuer     string        // "iss" claim, required
	Audience   string        // "aud" claim, required
	AccessTTL  time.Duration // default 15m
	RefreshTTL time.Duration // default 30d
	Leeway     time.Duration // clock skew tolerated when verifying, default 30s
	Now        func() time.Time
	// Revocations, when set, is consulted for every token that is otherwise valid. Without it tokens
	// cannot be revoked before they expire. A failing checker fails the request closed (503).
	Revocations RevocationChecker
}

// JWT issues and verifies tokens. It implements TokenVerifier and SessionIssuer.
type JWT struct {
	cfg    JWTConfig
	keys   *KeySet
	parser *jwt.Parser
}

var (
	_ TokenVerifier = (*JWT)(nil)
	_ SessionIssuer = (*JWT)(nil)
)

type tokenClaims struct {
	jwt.RegisteredClaims
	Type  TokenType      `json:"typ"`
	Roles []string       `json:"roles,omitempty"`
	Extra map[string]any `json:"ext,omitempty"`
}

// NewJWT validates the configuration.
func NewJWT(cfg JWTConfig, keys *KeySet) (*JWT, error) {
	if cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("auth: issuer and audience are required")
	}
	if keys == nil {
		return nil, errors.New("auth: keys are required")
	}
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = 15 * time.Minute
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = 30 * 24 * time.Hour
	}
	if cfg.Leeway <= 0 {
		cfg.Leeway = 30 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	j := &JWT{cfg: cfg, keys: keys}
	j.parser = jwt.NewParser(
		jwt.WithValidMethods([]string{algHS256, algEdDSA}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(cfg.Leeway),
		jwt.WithTimeFunc(cfg.Now),
	)
	return j, nil
}

// IssuePair creates a short-lived access token and a long-lived refresh token for subject.
func (j *JWT) IssuePair(subject string, roles []string, extra map[string]any) (TokenPair, error) {
	now := j.cfg.Now()
	access, accessExp, err := j.sign(TypeAccess, subject, roles, extra, now, j.cfg.AccessTTL)
	if err != nil {
		return TokenPair{}, err
	}
	// The refresh token carries identity only: roles are re-read from the source of truth when it is
	// exchanged, so a role change takes effect at the next refresh.
	refresh, refreshExp, err := j.sign(TypeRefresh, subject, nil, nil, now, j.cfg.RefreshTTL)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: access, RefreshToken: refresh, TokenType: "Bearer",
		ExpiresIn: int(j.cfg.AccessTTL.Seconds()), AccessExpiresAt: accessExp, RefreshExpiresAt: refreshExp,
	}, nil
}

func (j *JWT) sign(typ TokenType, subject string, roles []string, extra map[string]any,
	now time.Time, ttl time.Duration) (string, time.Time, error) {

	k := j.keys.signer
	if !k.CanSign() {
		return "", time.Time{}, errors.New("auth: this key set cannot sign tokens (verify-only)")
	}
	jti, err := newID()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := now.Add(ttl)
	claims := tokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: j.cfg.Issuer, Subject: subject, Audience: jwt.ClaimStrings{j.cfg.Audience},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp), ID: jti,
		},
		Type: typ, Roles: roles, Extra: extra,
	}
	var (
		tok *jwt.Token
		key any
	)
	switch k.alg {
	case algEdDSA:
		tok, key = jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims), k.priv
	default:
		tok, key = jwt.NewWithClaims(jwt.SigningMethodHS256, claims), k.secret
	}
	tok.Header["kid"] = k.ID
	s, err := tok.SignedString(key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: sign token: %w", err)
	}
	return s, exp, nil
}

// Verify checks signature, issuer, audience, expiry and that the token is of the wanted type.
// Failures are apperr.KindUnauthorized; an expired token has code "token_expired" so clients know to
// refresh instead of asking the user to log in again.
func (j *JWT) Verify(ctx context.Context, token string, want TokenType) (*Claims, error) {
	var tc tokenClaims
	_, err := j.parser.ParseWithClaims(token, &tc, j.keyFunc)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, apperr.Wrap(apperr.KindUnauthorized, "token_expired", "token expired", err)
		}
		return nil, apperr.Wrap(apperr.KindUnauthorized, "invalid_token", "invalid token", err)
	}
	if tc.Type != want {
		return nil, apperr.Wrap(apperr.KindUnauthorized, "invalid_token", "invalid token",
			fmt.Errorf("token type %q, want %q", tc.Type, want))
	}
	if tc.Subject == "" {
		return nil, apperr.Wrap(apperr.KindUnauthorized, "invalid_token", "invalid token", errors.New("empty subject"))
	}
	c := &Claims{Subject: tc.Subject, Roles: tc.Roles, Extra: tc.Extra, Type: tc.Type, ID: tc.ID}
	if tc.IssuedAt != nil {
		c.IssuedAt = tc.IssuedAt.Time
	}
	if tc.ExpiresAt != nil {
		c.ExpiresAt = tc.ExpiresAt.Time
	}
	if j.cfg.Revocations != nil {
		revoked, err := j.cfg.Revocations.IsRevoked(ctx, c)
		if err != nil {
			return nil, apperr.Wrap(apperr.KindUnavailable, "revocation_check_failed", "cannot verify the token status right now", err)
		}
		if revoked {
			return nil, apperr.New(apperr.KindUnauthorized, "token_revoked", "token revoked")
		}
	}
	return c, nil
}

// keyFunc selects the verification key by "kid" and pins the algorithm to the one that key was
// created for, which blocks algorithm-confusion attacks (e.g. an HS256 token "signed" with a public key).
func (j *JWT) keyFunc(t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)
	k, ok := j.keys.byID[kid]
	if !ok {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	if t.Method.Alg() != k.alg {
		return nil, fmt.Errorf("algorithm %s does not match key %q", t.Method.Alg(), kid)
	}
	if k.alg == algEdDSA {
		return k.pub, nil
	}
	return k.secret, nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
