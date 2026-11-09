package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func benchJWT(b *testing.B, key Key) (*JWT, TokenPair) {
	b.Helper()
	ks, _ := NewKeySet(key)
	j, err := NewJWT(JWTConfig{Issuer: "svc", Audience: "api", Now: time.Now}, ks)
	if err != nil {
		b.Fatal(err)
	}
	pair, err := j.IssuePair("user-1", []string{"admin", "user"}, nil)
	if err != nil {
		b.Fatal(err)
	}
	return j, pair
}

func BenchmarkVerifyHS256(b *testing.B) {
	k, _ := HMACKey("k1", []byte(strings.Repeat("s", 40)))
	j, pair := benchJWT(b, k)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := j.Verify(context.Background(), pair.AccessToken, TypeAccess); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyEd25519(b *testing.B) {
	priv, _, _ := GenerateEd25519()
	k, _ := Ed25519Key("e1", priv)
	j, pair := benchJWT(b, k)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := j.Verify(context.Background(), pair.AccessToken, TypeAccess); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIssuePairHS256(b *testing.B) {
	k, _ := HMACKey("k1", []byte(strings.Repeat("s", 40)))
	j, _ := benchJWT(b, k)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := j.IssuePair("user-1", []string{"admin"}, nil); err != nil {
			b.Fatal(err)
		}
	}
}
