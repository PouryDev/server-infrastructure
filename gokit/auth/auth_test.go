package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

var ctx = context.Background()

var secret = []byte(strings.Repeat("s3cret-", 8))

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newJWT(t *testing.T, clk *clock, keys ...Key) *JWT {
	t.Helper()
	ks, err := NewKeySet(keys...)
	if err != nil {
		t.Fatal(err)
	}
	j, err := NewJWT(JWTConfig{Issuer: "svc", Audience: "api", Now: clk.now}, ks)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func hmac(t *testing.T, id string) Key {
	k, err := HMACKey(id, append([]byte(id), secret...))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestRoundTripAndTypes(t *testing.T) {
	clk := &clock{time.Now()}
	j := newJWT(t, clk, hmac(t, "k1"))

	pair, err := j.IssuePair("user-1", []string{"admin"}, map[string]any{"tenant": "t1"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := j.Verify(ctx, pair.AccessToken, TypeAccess)
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != "user-1" || !c.HasAnyRole("admin") || c.HasAnyRole("root") || c.Extra["tenant"] != "t1" || c.ID == "" {
		t.Fatalf("claims = %+v", c)
	}
	if pair.TokenType != "Bearer" || pair.ExpiresIn != 900 {
		t.Fatalf("pair = %+v", pair)
	}

	// Types must not be interchangeable.
	if _, err := j.Verify(ctx, pair.RefreshToken, TypeAccess); code(err) != "invalid_token" {
		t.Fatalf("refresh used as access: %v", err)
	}
	if _, err := j.Verify(ctx, pair.AccessToken, TypeRefresh); code(err) != "invalid_token" {
		t.Fatalf("access used as refresh: %v", err)
	}
	r, err := j.Verify(ctx, pair.RefreshToken, TypeRefresh)
	if err != nil || len(r.Roles) != 0 {
		t.Fatalf("refresh token must carry identity only: %+v %v", r, err)
	}
}

func TestExpiryAndTamper(t *testing.T) {
	clk := &clock{time.Now()}
	j := newJWT(t, clk, hmac(t, "k1"))
	pair, _ := j.IssuePair("u", nil, nil)

	clk.t = clk.t.Add(16 * time.Minute)
	_, err := j.Verify(ctx, pair.AccessToken, TypeAccess)
	if apperr.KindOf(err) != apperr.KindUnauthorized || code(err) != "token_expired" {
		t.Fatalf("expired token: %v", err)
	}
	if _, err := j.Verify(ctx, pair.RefreshToken, TypeRefresh); err != nil {
		t.Fatalf("refresh outlives access: %v", err)
	}

	clk.t = time.Now()
	tampered := pair.AccessToken[:len(pair.AccessToken)-2] + "xx"
	if _, err := j.Verify(ctx, tampered, TypeAccess); code(err) != "invalid_token" {
		t.Fatalf("tampered: %v", err)
	}
	if _, err := j.Verify(ctx, "not.a.jwt", TypeAccess); code(err) != "invalid_token" {
		t.Fatalf("garbage: %v", err)
	}
}

func TestIssuerAudienceMismatch(t *testing.T) {
	clk := &clock{time.Now()}
	ks, _ := NewKeySet(hmac(t, "k1"))
	a, _ := NewJWT(JWTConfig{Issuer: "svc", Audience: "api", Now: clk.now}, ks)
	b, _ := NewJWT(JWTConfig{Issuer: "svc", Audience: "other", Now: clk.now}, ks)
	pair, _ := a.IssuePair("u", nil, nil)
	if _, err := b.Verify(ctx, pair.AccessToken, TypeAccess); err == nil {
		t.Fatal("a token for another audience must be rejected")
	}
}

func TestKeyRotation(t *testing.T) {
	clk := &clock{time.Now()}
	oldKey, newKey := hmac(t, "old"), hmac(t, "new")

	issuedByOld, _ := newJWT(t, clk, oldKey).IssuePair("u", nil, nil)
	rotated := newJWT(t, clk, newKey, oldKey) // new key signs, old key still verifies
	if _, err := rotated.Verify(ctx, issuedByOld.AccessToken, TypeAccess); err != nil {
		t.Fatalf("old token must stay valid during rotation: %v", err)
	}
	fresh, _ := rotated.IssuePair("u", nil, nil)
	if _, err := newJWT(t, clk, oldKey).Verify(ctx, fresh.AccessToken, TypeAccess); err == nil {
		t.Fatal("new token must not verify with the old key only")
	}
	if _, err := newJWT(t, clk, newKey).Verify(ctx, issuedByOld.AccessToken, TypeAccess); err == nil {
		t.Fatal("old token must fail once the old key is retired")
	}
}

func TestEd25519AndVerifyOnlyService(t *testing.T) {
	privPEM, pubPEM, err := GenerateEd25519()
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{time.Now()}
	signer, err := Ed25519Key("e1", privPEM)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := newJWT(t, clk, signer).IssuePair("u", []string{"user"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := Ed25519PublicKey("e1", pubPEM)
	if err != nil {
		t.Fatal(err)
	}
	verifier := newJWT(t, clk, pub)
	if _, err := verifier.Verify(ctx, pair.AccessToken, TypeAccess); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.IssuePair("u", nil, nil); err == nil {
		t.Fatal("a verify-only service must not be able to issue tokens")
	}
	// Single-line env var form with literal \n.
	oneLine := strings.ReplaceAll(string(privPEM), "\n", `\n`)
	if _, err := Ed25519Key("e1", []byte(oneLine)); err != nil {
		t.Fatalf("escaped PEM: %v", err)
	}
}

func TestAlgorithmConfusionIsRejected(t *testing.T) {
	_, pubPEM, _ := GenerateEd25519()
	pub, _ := Ed25519PublicKey("e1", pubPEM)
	clk := &clock{time.Now()}
	j := newJWT(t, clk, pub)

	// Attacker signs an HS256 token using the (public) key bytes as the HMAC secret.
	claims := tokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "svc", Subject: "admin", Audience: jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(clk.t.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(clk.t),
		},
		Type: TypeAccess,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = "e1"
	forged, _ := tok.SignedString([]byte(pub.pub))
	if _, err := j.Verify(ctx, forged, TypeAccess); err == nil {
		t.Fatal("algorithm confusion must be rejected")
	}

	// alg=none.
	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = "e1"
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := j.Verify(ctx, s, TypeAccess); err == nil {
		t.Fatal("alg=none must be rejected")
	}

	// A token signed by an unknown key.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	et := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	et.Header["kid"] = "e1"
	forged2, _ := et.SignedString(other)
	if _, err := j.Verify(ctx, forged2, TypeAccess); err == nil {
		t.Fatal("foreign signature must be rejected")
	}
}

func TestKeyValidation(t *testing.T) {
	if _, err := HMACKey("k", []byte("short")); err == nil {
		t.Fatal("short secrets must be rejected")
	}
	k := hmac(t, "k1")
	if _, err := NewKeySet(k, k); err == nil {
		t.Fatal("duplicate ids must be rejected")
	}
	if _, err := NewKeySet(); err == nil {
		t.Fatal("empty key set must be rejected")
	}
	if _, err := (KeyConfig{}).KeySet(); err == nil {
		t.Fatal("empty config must be rejected")
	}
	if _, err := (KeyConfig{Secret: string(secret)}).KeySet(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJWT(JWTConfig{}, nil); err == nil {
		t.Fatal("issuer/audience required")
	}
}

func TestArgon2id(t *testing.T) {
	h := &Argon2id{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32} // fast for tests
	enc, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("format: %s", enc)
	}
	enc2, _ := h.Hash("correct horse")
	if enc == enc2 {
		t.Fatal("salt must differ per hash")
	}

	ok, rehash, err := h.Verify("correct horse", enc)
	if err != nil || !ok || rehash {
		t.Fatalf("ok=%v rehash=%v err=%v", ok, rehash, err)
	}
	if ok, _, _ := h.Verify("wrong", enc); ok {
		t.Fatal("wrong password accepted")
	}

	stronger := &Argon2id{MemoryKiB: 128, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	if ok, rehash, _ := stronger.Verify("correct horse", enc); !ok || !rehash {
		t.Fatalf("weaker hash must verify and ask for a rehash (ok=%v rehash=%v)", ok, rehash)
	}

	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=64,t=1,p=1$x", "$argon2i$v=19$m=64,t=1,p=1$YQ$YQ",
		"$argon2id$v=19$m=99999999,t=1,p=1$YQ$YQ"} {
		if _, _, err := h.Verify("x", bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	_ = errors.New
}

func TestDefaultsAreOWASPMinimum(t *testing.T) {
	a := NewArgon2id()
	if a.MemoryKiB < 19*1024 || a.Iterations < 2 {
		t.Fatalf("%+v", a)
	}
}

type fakeRevocations struct {
	revoked map[string]bool
	err     error
}

func (f fakeRevocations) IsRevoked(_ context.Context, c *Claims) (bool, error) {
	return f.revoked[c.ID], f.err
}

func TestRevocation(t *testing.T) {
	clk := &clock{time.Now()}
	ks, _ := NewKeySet(hmac(t, "k1"))
	rev := fakeRevocations{revoked: map[string]bool{}}
	j, _ := NewJWT(JWTConfig{Issuer: "svc", Audience: "api", Now: clk.now, Revocations: rev}, ks)

	pair, _ := j.IssuePair("u", nil, nil)
	c, err := j.Verify(ctx, pair.AccessToken, TypeAccess)
	if err != nil {
		t.Fatal(err)
	}
	rev.revoked[c.ID] = true
	if _, err := j.Verify(ctx, pair.AccessToken, TypeAccess); code(err) != "token_revoked" || apperr.KindOf(err) != apperr.KindUnauthorized {
		t.Fatalf("revoked token: %v", err)
	}

	// a broken checker fails closed, with a retryable status rather than a bogus "invalid token"
	broken, _ := NewJWT(JWTConfig{Issuer: "svc", Audience: "api", Now: clk.now, Revocations: fakeRevocations{err: errors.New("redis down")}}, ks)
	if _, err := broken.Verify(ctx, pair.AccessToken, TypeAccess); apperr.KindOf(err) != apperr.KindUnavailable {
		t.Fatalf("checker failure: %v", err)
	}
}
