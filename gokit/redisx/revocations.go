package redisx

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

// Revocations makes stateless tokens revocable with two kinds of short-lived keys:
//
//	<prefix>auth:revoked:<jti>           one revoked token, kept until the token would have expired anyway
//	<prefix>auth:revoked-before:<sub>    "everything issued to sub up to this instant is void"
//
// Every key has a TTL, as the shared Redis requires, so the set never grows beyond the tokens that could
// still be presented. A check is one MGET (a single round trip).
type Revocations struct {
	c *Client
	// MaxTokenLifetime bounds how long a revoke-everything marker is kept: it must be at least the
	// longest token lifetime (the refresh token TTL). Default 30 days.
	MaxTokenLifetime time.Duration
	Now              func() time.Time
}

var (
	_ auth.RevocationChecker = (*Revocations)(nil)
	_ auth.Revoker           = (*Revocations)(nil)
)

// NewRevocations builds the store on a connected client.
func NewRevocations(c *Client) *Revocations {
	return &Revocations{c: c, MaxTokenLifetime: 30 * 24 * time.Hour, Now: time.Now}
}

func (r *Revocations) tokenKey(jti string) string   { return r.c.Key("auth", "revoked", jti) }
func (r *Revocations) subjectKey(sub string) string { return r.c.Key("auth", "revoked-before", sub) }

// Revoke invalidates one token until it expires.
func (r *Revocations) Revoke(ctx context.Context, c *auth.Claims) error {
	if c.ID == "" {
		return errors.New("redisx: cannot revoke a token without an id")
	}
	ttl := time.Until(c.ExpiresAt) + time.Minute // a minute of slack for clock skew
	if c.ExpiresAt.IsZero() {
		ttl = r.MaxTokenLifetime
	}
	if ttl <= 0 {
		return nil // already expired: nothing to remember
	}
	return r.c.rdb.Set(ctx, r.tokenKey(c.ID), "1", ttl).Err()
}

// RevokeSubject invalidates every token issued to subject up to now.
func (r *Revocations) RevokeSubject(ctx context.Context, subject string) error {
	return r.c.rdb.Set(ctx, r.subjectKey(subject), strconv.FormatInt(r.Now().Unix(), 10), r.MaxTokenLifetime).Err()
}

// IsRevoked implements auth.RevocationChecker.
func (r *Revocations) IsRevoked(ctx context.Context, c *auth.Claims) (bool, error) {
	vals, err := r.c.rdb.MGet(ctx, r.tokenKey(c.ID), r.subjectKey(c.Subject)).Result()
	if err != nil {
		return false, err
	}
	if vals[0] != nil {
		return true, nil
	}
	if s, ok := vals[1].(string); ok {
		before, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return false, err
		}
		return c.IssuedAt.Unix() <= before, nil
	}
	return false, nil
}
