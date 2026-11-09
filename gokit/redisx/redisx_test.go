package redisx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKeysArePrefixed(t *testing.T) {
	for _, p := range []string{"shop", "shop:"} {
		c := &Client{prefix: normalizePrefix(p)}
		if got := c.NewCache("session").key("abc"); got != "shop:session:abc" {
			t.Fatalf("prefix %q: key = %s", p, got)
		}
	}
}

func TestWritesRequireTTL(t *testing.T) {
	k := (&Client{prefix: "shop:"}).NewCache("x") // no connection: validation happens before any I/O
	if err := k.Set(context.Background(), "a", []byte("v"), 0); !errors.Is(err, ErrNoTTL) {
		t.Fatalf("err = %v", err)
	}
	if err := SetJSON(context.Background(), k, "a", 1, -time.Second); !errors.Is(err, ErrNoTTL) {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectNeedsPrefix(t *testing.T) {
	if _, err := Connect(context.Background(), Config{URL: "redis://x"}); err == nil {
		t.Fatal("prefix is required")
	}
}
