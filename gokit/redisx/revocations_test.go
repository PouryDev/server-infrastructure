package redisx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

// Needs Redis: GOKIT_TEST_REDIS_URL (make gokit-integration starts one).
func TestRevocationsAndCache(t *testing.T) {
	url := os.Getenv("GOKIT_TEST_REDIS_URL")
	if url == "" {
		t.Skip("GOKIT_TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	prefix := "gokittest" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "") + ":"
	c, err := Connect(ctx, Config{URL: url, KeyPrefix: prefix, StartupTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// cache with TTL
	k := c.NewCache("users")
	if err := SetJSON(ctx, k, "1", map[string]string{"n": "ada"}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	v, ok, err := GetJSON[map[string]string](ctx, k, "1")
	if err != nil || !ok || v["n"] != "ada" {
		t.Fatalf("%v %v %v", v, ok, err)
	}
	if ttl := c.Raw().TTL(ctx, c.Key("users", "1")).Val(); ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("every key must carry a TTL, got %v", ttl)
	}
	if _, ok, _ := k.Get(ctx, "missing"); ok {
		t.Fatal("missing key reported as found")
	}
	_ = k.Delete(ctx, "1")

	// revocations
	rev := NewRevocations(c)
	now := time.Now()
	claims := &auth.Claims{Subject: "u1", ID: "jti-1", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if revoked, err := rev.IsRevoked(ctx, claims); err != nil || revoked {
		t.Fatalf("fresh token: %v %v", revoked, err)
	}
	if err := rev.Revoke(ctx, claims); err != nil {
		t.Fatal(err)
	}
	if revoked, _ := rev.IsRevoked(ctx, claims); !revoked {
		t.Fatal("revoked token must be reported")
	}
	if ttl := c.Raw().TTL(ctx, c.Key("auth", "revoked", "jti-1")).Val(); ttl <= 0 || ttl > time.Hour+2*time.Minute {
		t.Fatalf("revocation must expire with the token, ttl=%v", ttl)
	}
	other := &auth.Claims{Subject: "u1", ID: "jti-2", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
	if revoked, _ := rev.IsRevoked(ctx, other); revoked {
		t.Fatal("other tokens are not affected")
	}

	// "log out everywhere": tokens issued before now are void, later ones are fine
	rev.Now = func() time.Time { return now }
	if err := rev.RevokeSubject(ctx, "u1"); err != nil {
		t.Fatal(err)
	}
	if revoked, _ := rev.IsRevoked(ctx, other); !revoked {
		t.Fatal("token issued before the revoke-all must be void")
	}
	later := &auth.Claims{Subject: "u1", ID: "jti-3", IssuedAt: now.Add(2 * time.Second), ExpiresAt: now.Add(time.Hour)}
	if revoked, _ := rev.IsRevoked(ctx, later); revoked {
		t.Fatal("a token issued after the revoke-all must work")
	}
	if err := rev.Revoke(ctx, &auth.Claims{Subject: "u1", ID: "gone", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal("revoking an already expired token is a no-op")
	}
}
