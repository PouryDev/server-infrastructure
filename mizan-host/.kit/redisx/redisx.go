// Package redisx connects to the shared Redis and enforces its contract: every key starts with the
// project's REDIS_KEY_PREFIX (the ACL rejects anything else) and every cache entry has a TTL (the
// eviction policy is volatile-lru, so a key without TTL is never evicted and can fill the memory).
package redisx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// ErrNoTTL is returned when a write has no positive TTL.
var ErrNoTTL = errors.New("redisx: a positive TTL is required (keys without TTL are never evicted)")

// Config configures Connect.
type Config struct {
	URL            string // REDIS_URL
	KeyPrefix      string // REDIS_KEY_PREFIX, e.g. "shop:"
	StartupTimeout time.Duration
	Logger         *slog.Logger
}

// Client is a Redis connection bound to a key prefix.
type Client struct {
	rdb    *redis.Client
	prefix string
	log    *slog.Logger
}

// Connect waits, with backoff, until Redis answers.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.KeyPrefix == "" {
		return nil, errors.New("redisx: REDIS_KEY_PREFIX is required")
	}
	opt, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, errors.New("redisx: REDIS_URL is not a valid URL") // never echo it, it holds the password
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	c := &Client{rdb: redis.NewClient(opt), prefix: normalizePrefix(cfg.KeyPrefix), log: log}

	timeout := cfg.StartupTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = retry.Do(wctx, retry.Policy{
		Initial: 500 * time.Millisecond, Max: 5 * time.Second,
		OnRetry: func(attempt int, err error, delay time.Duration) {
			log.Warn("redis not ready, retrying", "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		},
	}, c.Ping)
	if err != nil {
		_ = c.rdb.Close()
		return nil, fmt.Errorf("redisx: not reachable after %s: %w", timeout, err)
	}
	return c, nil
}

func normalizePrefix(p string) string {
	if strings.HasSuffix(p, ":") {
		return p
	}
	return p + ":"
}

// Ping checks connectivity (readiness probes).
func (c *Client) Ping(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.rdb.Ping(pctx).Err()
}

// Close releases the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }

// Raw exposes the go-redis client for features not wrapped here. Build keys with Key.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Key returns prefix + parts joined with ":".
func (c *Client) Key(parts ...string) string { return c.prefix + strings.Join(parts, ":") }

// Cache is a namespaced key/value cache with mandatory TTLs. See Remember, ScopedKey / Bump and ReadModel
// for the patterns built on it.
type Cache struct {
	c         *Client
	namespace string

	flight   singleflight.Group // Remember: one loader per key per process
	versions sync.Map           // scope -> versionEntry (short-lived local copy of the scope's version)
	verTTL   time.Duration
}

// CacheOption tunes NewCache.
type CacheOption func(*Cache)

// WithVersionCache sets how long a process remembers a scope's version before asking Redis again (default
// 1s). It bounds how long another process can keep serving data after Bump: the process that bumps sees it
// at once, the others within this time. Use 0 to read the version on every call.
func WithVersionCache(d time.Duration) CacheOption { return func(k *Cache) { k.verTTL = d } }

// NewCache creates a cache whose keys are <prefix><namespace>:<key>.
func (c *Client) NewCache(namespace string, opts ...CacheOption) *Cache {
	k := &Cache{c: c, namespace: namespace, verTTL: time.Second}
	for _, o := range opts {
		o(k)
	}
	return k
}

func (k *Cache) key(key string) string { return k.c.Key(k.namespace, key) }

// Get returns the value and whether it existed.
func (k *Cache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := k.c.rdb.Get(ctx, k.key(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// Set stores value for ttl (must be positive).
func (k *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrNoTTL
	}
	return k.c.rdb.Set(ctx, k.key(key), value, ttl).Err()
}

// Delete removes keys.
func (k *Cache) Delete(ctx context.Context, keys ...string) error {
	full := make([]string, len(keys))
	for i, key := range keys {
		full[i] = k.key(key)
	}
	return k.c.rdb.Del(ctx, full...).Err()
}

// GetJSON reads and decodes a JSON value.
func GetJSON[T any](ctx context.Context, k *Cache, key string) (v T, found bool, err error) {
	b, ok, err := k.Get(ctx, key)
	if err != nil || !ok {
		return v, false, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("redisx: decode %q: %w", key, err)
	}
	return v, true, nil
}

// SetJSON encodes and stores a JSON value.
func SetJSON[T any](ctx context.Context, k *Cache, key string, v T, ttl time.Duration) error {
	if ttl <= 0 {
		return ErrNoTTL
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("redisx: encode %q: %w", key, err)
	}
	return k.Set(ctx, key, b, ttl)
}
