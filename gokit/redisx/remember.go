package redisx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// A cache must never be the reason a request fails: every Redis error on the read or write path of
// Remember is logged and the data is simply loaded from the source. Only errors of the loader itself are
// returned.

type rememberCfg struct {
	jitter      float64
	negTTL      time.Duration
	negErr      func() error
	loadTimeout time.Duration
}

// RememberOption tunes Remember.
type RememberOption func(*rememberCfg)

// WithJitter shortens each stored TTL by a random 0..frac of itself (default 0.1), so keys written
// together do not all expire, and get reloaded, at the same moment. frac must be in [0, 0.5].
func WithJitter(frac float64) RememberOption {
	return func(c *rememberCfg) { c.jitter = min(max(frac, 0), 0.5) }
}

// WithNegative also caches "not found" (an apperr.KindNotFound error from the loader) for ttl, so a flood of
// lookups for ids that do not exist cannot reach the database. On a hit, errFn builds the error returned to
// the caller (nil errFn: a generic apperr.NotFound). Keep ttl short: a row created right after the miss stays
// invisible until it ends, unless the writer deletes the key.
func WithNegative(ttl time.Duration, errFn func() error) RememberOption {
	return func(c *rememberCfg) { c.negTTL, c.negErr = ttl, errFn }
}

// WithLoadTimeout bounds the loader (default 10s). The loader runs detached from the first caller's
// cancellation, because other callers share its result.
func WithLoadTimeout(d time.Duration) RememberOption {
	return func(c *rememberCfg) { c.loadTimeout = d }
}

var notFoundMarker = []byte("\x00gokit:notfound")

// Remember returns the cached value of key, or loads it, stores it for ttl (minus jitter) and returns it.
//
//   - Stampede protection: concurrent misses for the same key in one process run the loader once.
//   - Fail-open: a Redis outage degrades to "always load", never to errors.
//   - A cached value that no longer decodes (the type changed in a deploy) is a miss and is overwritten.
//   - The value is stored as JSON; callers get their own decoded copy.
//
// Singleflight is per process: N instances can still load once each after a miss, which is what the
// database sees in the worst case.
func Remember[T any](ctx context.Context, k *Cache, key string, ttl time.Duration,
	load func(ctx context.Context) (T, error), opts ...RememberOption) (T, error) {

	var zero T
	if ttl <= 0 {
		return zero, ErrNoTTL
	}
	cfg := rememberCfg{jitter: 0.1, loadTimeout: 10 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}

	if b, found, err := k.Get(ctx, key); err != nil {
		k.c.log.Warn("cache read failed, loading from the source", "namespace", k.namespace, "error", err.Error())
	} else if found {
		if bytes.Equal(b, notFoundMarker) {
			if cfg.negErr != nil {
				return zero, cfg.negErr()
			}
			return zero, apperr.NotFound("resource")
		}
		var v T
		if json.Unmarshal(b, &v) == nil {
			return v, nil
		}
	}

	raw, err, _ := k.flight.Do(k.key(key), func() (any, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.loadTimeout)
		defer cancel()
		v, err := load(lctx)
		if err != nil {
			if cfg.negTTL > 0 && apperr.Is(err, apperr.KindNotFound) {
				k.store(lctx, key, notFoundMarker, cfg.negTTL, cfg.jitter)
			}
			return nil, err
		}
		b, merr := json.Marshal(v)
		if merr != nil {
			return nil, fmt.Errorf("redisx: %T is not JSON-serialisable: %w", v, merr)
		}
		k.store(lctx, key, b, ttl, cfg.jitter)
		return b, nil
	})
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(raw.([]byte), &v); err != nil {
		return zero, fmt.Errorf("redisx: decode %q: %w", key, err)
	}
	return v, nil
}

// store writes best-effort.
func (k *Cache) store(ctx context.Context, key string, value []byte, ttl time.Duration, jitter float64) {
	if err := k.Set(ctx, key, value, jitterTTL(ttl, jitter)); err != nil {
		k.c.log.Warn("cache write failed", "namespace", k.namespace, "error", err.Error())
	}
}

// jitterTTL returns ttl reduced by a random 0..frac of itself (never below 1ms).
func jitterTTL(ttl time.Duration, frac float64) time.Duration {
	if frac <= 0 {
		return ttl
	}
	return max(ttl-time.Duration(float64(ttl)*frac*rand.Float64()), time.Millisecond) //nolint:gosec // spreading expiries needs no crypto randomness
}

// --- invalidation by version ------------------------------------------------------------------------
//
// KEYS and pattern deletes are not allowed on the shared Redis, so "delete everything about users" is done
// the other way round: keys embed the current version of a scope ("users:v7:42"), and Bump increments it.
// Old keys are never read again and expire on their own TTL. Data TTLs must be well below versionTTL.

const versionTTL = 30 * 24 * time.Hour

type versionEntry struct {
	v     int64
	until time.Time
}

func (k *Cache) versionKey(scope string) string { return k.c.Key(k.namespace, "ver", scope) }

// version returns the scope's current version (0 when it was never bumped or Redis is unreachable).
func (k *Cache) version(ctx context.Context, scope string) int64 {
	if k.verTTL > 0 {
		if e, ok := k.versions.Load(scope); ok && time.Now().Before(e.(versionEntry).until) {
			return e.(versionEntry).v
		}
	}
	s, err := k.c.rdb.Get(ctx, k.versionKey(scope)).Result()
	if err != nil {
		if !isNil(err) {
			k.c.log.Warn("cache version read failed", "namespace", k.namespace, "scope", scope, "error", err.Error())
		}
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	if k.verTTL > 0 {
		k.versions.Store(scope, versionEntry{v: v, until: time.Now().Add(k.verTTL)})
	}
	return v
}

// ScopedKey builds a key that belongs to an invalidation scope: <scope>:v<version>:<parts...>. Pass it to
// Remember; after any write that makes the scope's data stale call Bump(scope).
func (k *Cache) ScopedKey(ctx context.Context, scope string, parts ...string) string {
	return scope + ":v" + strconv.FormatInt(k.version(ctx, scope), 10) + ":" + strings.Join(parts, ":")
}

// Bump invalidates every key built with ScopedKey for the scopes. This process sees the new version at once;
// others within the version cache time (see WithVersionCache).
func (k *Cache) Bump(ctx context.Context, scopes ...string) error {
	for _, scope := range scopes {
		pipe := k.c.rdb.TxPipeline()
		incr := pipe.Incr(ctx, k.versionKey(scope))
		pipe.Expire(ctx, k.versionKey(scope), versionTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		if k.verTTL > 0 {
			k.versions.Store(scope, versionEntry{v: incr.Val(), until: time.Now().Add(k.verTTL)})
		}
	}
	return nil
}

// --- read models ------------------------------------------------------------------------------------

// ReadModel keeps a denormalised, ready-to-serve view of one kind of thing in Redis, keyed by id. Reads go
// to Redis first and rebuild from the source of truth on a miss; writers either Put the fresh view right
// after their database commit (write-through) or Invalidate the id and let the next read rebuild it.
//
// It is a performance structure, not storage: the shared Redis evicts and every key has a TTL, so the view
// must always be rebuildable from the database.
type ReadModel[T any] struct {
	cache   *Cache
	name    string
	ttl     time.Duration
	rebuild func(ctx context.Context, id string) (T, error)
	opts    []RememberOption
}

// NewReadModel creates a read model. name keys it ("user-card"); rebuild loads one id from the database.
func NewReadModel[T any](c *Cache, name string, ttl time.Duration,
	rebuild func(ctx context.Context, id string) (T, error), opts ...RememberOption) *ReadModel[T] {
	return &ReadModel[T]{cache: c, name: name, ttl: ttl, rebuild: rebuild, opts: opts}
}

func (m *ReadModel[T]) key(id string) string { return "rm:" + m.name + ":" + id }

// Get returns the view of id (Redis, else rebuilt and stored).
func (m *ReadModel[T]) Get(ctx context.Context, id string) (T, error) {
	return Remember(ctx, m.cache, m.key(id), m.ttl, func(ctx context.Context) (T, error) { return m.rebuild(ctx, id) }, m.opts...)
}

// Put stores a fresh view (write-through after a successful commit).
func (m *ReadModel[T]) Put(ctx context.Context, id string, v T) error {
	return SetJSON(ctx, m.cache, m.key(id), v, jitterTTL(m.ttl, 0.1))
}

// Invalidate drops the views of the ids; the next read rebuilds them.
func (m *ReadModel[T]) Invalidate(ctx context.Context, ids ...string) error {
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = m.key(id)
	}
	return m.cache.Delete(ctx, keys...)
}

// GetMany fetches many views with one MGET and rebuilds only the misses with ONE call of loadMany (a single
// batched database query, never one query per id), then stores them in one pipeline. Ids missing from
// loadMany's result are absent from the returned map. A Redis failure degrades to loading everything.
func (m *ReadModel[T]) GetMany(ctx context.Context, ids []string,
	loadMany func(ctx context.Context, missing []string) (map[string]T, error)) (map[string]T, error) {

	out := make(map[string]T, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	missing := ids
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = m.cache.key(m.key(id))
	}
	if vals, err := m.cache.c.rdb.MGet(ctx, keys...).Result(); err != nil {
		m.cache.c.log.Warn("cache read failed, loading from the source", "namespace", m.cache.namespace, "error", err.Error())
	} else {
		missing = missing[:0:0]
		for i, v := range vals {
			s, ok := v.(string)
			var t T
			if ok && json.Unmarshal([]byte(s), &t) == nil && s != string(notFoundMarker) {
				out[ids[i]] = t
				continue
			}
			missing = append(missing, ids[i])
		}
	}
	if len(missing) == 0 {
		return out, nil
	}

	loaded, err := loadMany(ctx, missing)
	if err != nil {
		return nil, err
	}
	pipe := m.cache.c.rdb.Pipeline()
	for id, v := range loaded {
		out[id] = v
		if b, err := json.Marshal(v); err == nil {
			pipe.Set(ctx, m.cache.key(m.key(id)), b, jitterTTL(m.ttl, 0.1))
		}
	}
	if _, err := pipe.Exec(ctx); err != nil && pipe.Len() > 0 {
		m.cache.c.log.Warn("cache write failed", "namespace", m.cache.namespace, "error", err.Error())
	}
	return out, nil
}
