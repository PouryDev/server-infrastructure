package config

import (
	"strings"
	"testing"
	"time"

	kitconfig "github.com/PouryDev/server-infrastructure/gokit/config"
)

func TestLoadDefaults(t *testing.T) {
	c, err := LoadFrom(kitconfig.FromMap(map[string]string{
		"DATABASE_URL": "postgres://u:p@db/x", "JWT_SECRET": strings.Repeat("s", 32),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != "production" || c.HTTP.Addr != ":8080" || c.JWT.AccessTTL != 15*time.Minute || c.LogFormat != "json" || !c.App.SecureCookies {
		t.Fatalf("%+v", c)
	}
}

func TestLoadReportsEverything(t *testing.T) {
	_, err := LoadFrom(kitconfig.FromMap(map[string]string{"APP_ENV": "staging", "JWT_SECRET": "short"}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "JWT_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q not reported in %q", want, err)
		}
	}
}

func TestCookiesAreNotSecureInDevelopment(t *testing.T) {
	m := map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://u:p@db/x", "JWT_SECRET": strings.Repeat("s", 32)}
	c, err := LoadFrom(kitconfig.FromMap(m))
	if err != nil || c.App.SecureCookies {
		t.Fatalf("%+v %v", c.App, err)
	}
	m["COOKIE_SECURE"] = "true"
	if c, _ = LoadFrom(kitconfig.FromMap(m)); !c.App.SecureCookies {
		t.Fatal("COOKIE_SECURE must win")
	}
}
