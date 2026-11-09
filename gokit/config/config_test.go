package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoaderCollectsAllErrors(t *testing.T) {
	l := FromMap(map[string]string{"PORT": "abc", "TTL": "15m", "DEBUG": "yes"})
	_ = l.Int("PORT", 8080)
	_ = l.Required("DATABASE_URL")
	if got := l.Duration("TTL", time.Second); got != 15*time.Minute {
		t.Fatalf("TTL = %v", got)
	}
	_ = l.Bool("DEBUG", false)

	err := l.Err()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"PORT", "DATABASE_URL", "DEBUG"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestDefaultsAndLists(t *testing.T) {
	l := FromMap(map[string]string{"ORIGINS": " a.com, ,b.com "})
	if got := l.List("ORIGINS", nil); len(got) != 2 || got[1] != "b.com" {
		t.Fatalf("List = %v", got)
	}
	if l.String("MISSING", "x") != "x" || l.Int("N", 3) != 3 || l.Err() != nil {
		t.Fatal("defaults must apply without errors")
	}
}
