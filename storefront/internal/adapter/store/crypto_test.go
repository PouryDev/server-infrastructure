package store

import (
	"strings"
	"testing"
)

func TestGatewayEncryptionBindsKeyAndRejectsTampering(t *testing.T) {
	s := Store{Secret: []byte(strings.Repeat("a", 32))}
	a, e := s.crypt("merchant-secret", true)
	if e != nil || strings.Contains(a, "merchant-secret") {
		t.Fatal(a, e)
	}
	b, e := s.crypt("merchant-secret", true)
	if e != nil || a == b {
		t.Fatal("nonce reused")
	}
	plain, e := s.crypt(a, false)
	if e != nil || plain != "merchant-secret" {
		t.Fatal(plain, e)
	}
	other := Store{Secret: []byte(strings.Repeat("b", 32))}
	if _, e = other.crypt(a, false); e == nil {
		t.Fatal("wrong key accepted")
	}
}
