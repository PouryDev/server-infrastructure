// Package config loads every setting from environment variables (twelve-factor): the same image runs
// anywhere. All problems are reported together.
package config

import (
	"time"

	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	kitconfig "github.com/PouryDev/server-infrastructure/gokit/config"
)

// Config is the typed configuration of the service.
type Config struct {
	Env         string // development | production
	ServiceName string // OTEL_SERVICE_NAME
	LogLevel    string
	LogFormat   string
	HTTP        HTTP
	Database    Database
	Redis       Redis
	JWT         JWT
	App         App
}

// App holds the host application's own settings.
type App struct {
	Company       string // shown on the sign-in page
	WebDir        string // built UI served at /, empty = API only
	SecureCookies bool   // Secure flag of the session cookies; default: on in production
}

type HTTP struct {
	Addr            string
	ShutdownTimeout time.Duration
	CORSOrigins     []string
	MaxBodyBytes    int64
}

type Database struct {
	URL                string
	MaxConns           int32
	PreparedStatements bool          // named server-side prepared statements (PgBouncer >= 1.21 with max_prepared_statements)
	SlowQuery          time.Duration // log statements slower than this, 0 = off
}

// Redis is optional: when URL is set, tokens become revocable (logout revokes the tokens) and Redis joins the
// readiness probe.
type Redis struct {
	URL       string
	KeyPrefix string
}

type JWT struct {
	Issuer     string
	Audience   string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Keys       kitauth.KeyConfig
}

// Load reads the process environment.
func Load() (Config, error) { return LoadFrom(kitconfig.FromEnv()) }

// LoadFrom reads from any loader (tests use kitconfig.FromMap).
func LoadFrom(l *kitconfig.Loader) (Config, error) {
	c := Config{
		ServiceName: l.String("OTEL_SERVICE_NAME", "mizan-host-api"),
		Env:         l.OneOf("APP_ENV", "production", "development", "production"),
		LogLevel:    l.OneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
		LogFormat:   l.OneOf("LOG_FORMAT", "json", "json", "text"),
		HTTP: HTTP{
			Addr:            l.String("HTTP_ADDR", ":8080"),
			ShutdownTimeout: l.Duration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
			CORSOrigins:     l.List("CORS_ALLOWED_ORIGINS", nil),
			MaxBodyBytes:    int64(l.Int("HTTP_MAX_BODY_BYTES", 8<<20)),
		},
		Database: Database{
			URL:                l.Required("DATABASE_URL"),
			MaxConns:           int32(l.Int("DATABASE_MAX_CONNS", 10)),
			PreparedStatements: l.Bool("DATABASE_PREPARED_STATEMENTS", true),
			SlowQuery:          l.Duration("DATABASE_SLOW_QUERY", 250*time.Millisecond),
		},
		App: App{
			Company: l.String("COMPANY_NAME", "شرکت نمونه"),
			WebDir:  l.String("WEB_DIR", ""),
		},
		Redis: Redis{URL: l.String("REDIS_URL", ""), KeyPrefix: l.String("REDIS_KEY_PREFIX", "")},
		JWT: JWT{
			Issuer:     l.String("JWT_ISSUER", "mizan-host"),
			Audience:   l.String("JWT_AUDIENCE", "mizan-host"),
			AccessTTL:  l.Duration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTTL: l.Duration("JWT_REFRESH_TTL", 30*24*time.Hour),
			Keys: kitauth.KeyConfig{
				KeyID:         l.String("JWT_KEY_ID", "k1"),
				PrivateKeyPEM: l.String("JWT_PRIVATE_KEY", ""),
				PublicKeyPEM:  l.String("JWT_PUBLIC_KEY", ""),
				Secret:        l.String("JWT_SECRET", ""),
			},
		},
	}
	c.App.SecureCookies = l.Bool("COOKIE_SECURE", c.Env == "production")
	// Key material is validated for real when the key set is built at start-up; here we only make
	// sure something was provided, and that a shared secret is long enough.
	k := c.JWT.Keys
	switch {
	case k.PrivateKeyPEM != "" || k.PublicKeyPEM != "":
	case k.Secret == "":
		_ = l.Required("JWT_SECRET") // reports "JWT_SECRET: is required"
	default:
		_ = l.MinLen("JWT_SECRET", k.Secret, 32)
	}
	if c.Redis.URL != "" && c.Redis.KeyPrefix == "" {
		_ = l.Required("REDIS_KEY_PREFIX") // the shared Redis only accepts keys under the project prefix
	}
	return c, l.Err()
}
