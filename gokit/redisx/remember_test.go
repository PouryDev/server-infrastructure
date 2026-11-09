package redisx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

type card struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// deadClient points at a port nobody listens on, to prove the cache fails open.
func deadClient() *Client {
	return &Client{
		rdb:    redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 30 * time.Millisecond, MaxRetries: -1}),
		prefix: "t:", log: quietLog(),
	}
}

func TestJitterTTL(t *testing.T) {
	for range 200 {
		got := jitterTTL(time.Minute, 0.1)
		if got > time.Minute || got < 54*time.Second {
			t.Fatalf("jittered ttl %v outside [54s, 60s]", got)
		}
	}
	if jitterTTL(time.Minute, 0) != time.Minute {
		t.Fatal("no jitter must keep the ttl")
	}
}

func TestRememberFailsOpenWhenRedisIsDown(t *testing.T) {
	k := deadClient().NewCache("users")
	calls := 0
	for range 3 {
		v, err := Remember(context.Background(), k, "1", time.Minute, func(context.Context) (card, error) {
			calls++
			return card{ID: "1", Name: "ada"}, nil
		})
		if err != nil || v.Name != "ada" {
			t.Fatalf("a Redis outage must not fail the request: %v %v", v, err)
		}
	}
	if calls != 3 {
		t.Fatalf("without Redis every call loads, got %d loads", calls)
	}
	// the loader's own errors still come back
	boom := errors.New("db down")
	if _, err := Remember(context.Background(), k, "2", time.Minute, func(context.Context) (card, error) { return card{}, boom }); !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	if got := k.ScopedKey(context.Background(), "user", "42"); got != "user:v0:42" {
		t.Fatalf("scoped key falls back to version 0: %q", got)
	}
	if _, err := Remember(context.Background(), k, "x", 0, func(context.Context) (card, error) { return card{}, nil }); !errors.Is(err, ErrNoTTL) {
		t.Fatalf("ttl is mandatory: %v", err)
	}
	if _, err := deadClient().Hash("h", 0); !errors.Is(err, ErrNoTTL) {
		t.Fatal("hash needs a ttl")
	}
	if _, err := deadClient().Ranking("r", 0, 10); !errors.Is(err, ErrNoTTL) {
		t.Fatal("ranking needs a ttl")
	}
}

func liveClient(t testing.TB) *Client {
	t.Helper()
	url := os.Getenv("GOKIT_TEST_REDIS_URL")
	if url == "" {
		t.Skip("GOKIT_TEST_REDIS_URL not set")
	}
	prefix := "gk" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "") + ":"
	c, err := Connect(context.Background(), Config{URL: url, KeyPrefix: prefix, StartupTimeout: 5 * time.Second, Logger: quietLog()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestRememberAgainstRedis(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	k := c.NewCache("users")

	t.Run("stampede: many concurrent misses, one load", func(t *testing.T) {
		var loads atomic.Int32
		load := func(context.Context) (card, error) {
			loads.Add(1)
			time.Sleep(60 * time.Millisecond)
			return card{ID: "1", Name: "ada"}, nil
		}
		var wg sync.WaitGroup
		for range 50 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if v, err := Remember(ctx, k, "1", time.Minute, load); err != nil || v.Name != "ada" {
					t.Errorf("%v %v", v, err)
				}
			}()
		}
		wg.Wait()
		if loads.Load() != 1 {
			t.Fatalf("loader ran %d times, want 1", loads.Load())
		}
		if _, err := Remember(ctx, k, "1", time.Minute, load); err != nil || loads.Load() != 1 {
			t.Fatalf("second round must be a cache hit: loads=%d err=%v", loads.Load(), err)
		}
		if ttl := c.Raw().TTL(ctx, c.Key("users", "1")).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("every key needs a TTL within the requested one, got %v", ttl)
		}
	})

	t.Run("loader errors are not cached", func(t *testing.T) {
		calls := 0
		boom := errors.New("db down")
		for range 2 {
			if _, err := Remember(ctx, k, "err", time.Minute, func(context.Context) (card, error) { calls++; return card{}, boom }); !errors.Is(err, boom) {
				t.Fatal(err)
			}
		}
		if calls != 2 {
			t.Fatalf("calls = %d", calls)
		}
	})

	t.Run("negative caching is opt-in", func(t *testing.T) {
		calls := 0
		missing := func(context.Context) (card, error) { calls++; return card{}, apperr.NotFound("card") }
		for range 3 {
			if _, err := Remember(ctx, k, "nf-off", time.Minute, missing); !apperr.Is(err, apperr.KindNotFound) {
				t.Fatal(err)
			}
		}
		if calls != 3 {
			t.Fatalf("without WithNegative every miss loads: %d", calls)
		}
		calls = 0
		for range 3 {
			_, err := Remember(ctx, k, "nf-on", time.Minute, missing, WithNegative(30*time.Second, func() error { return apperr.NotFound("card") }))
			if !apperr.Is(err, apperr.KindNotFound) || !strings.Contains(err.Error(), "card") {
				t.Fatal(err)
			}
		}
		if calls != 1 {
			t.Fatalf("a cached 'not found' must stop the loads: %d", calls)
		}
		if ttl := c.Raw().TTL(ctx, c.Key("users", "nf-on")).Val(); ttl <= 0 || ttl > 30*time.Second {
			t.Fatalf("negative entries are short-lived: %v", ttl)
		}
	})

	t.Run("an undecodable cached value is overwritten", func(t *testing.T) {
		if err := k.Set(ctx, "old-schema", []byte(`{"id": 5}`), time.Minute); err != nil { // id is a string now
			t.Fatal(err)
		}
		v, err := Remember(ctx, k, "old-schema", time.Minute, func(context.Context) (card, error) { return card{ID: "5", Name: "new"}, nil })
		if err != nil || v.Name != "new" {
			t.Fatalf("%v %v", v, err)
		}
		if got, _, _ := GetJSON[card](ctx, k, "old-schema"); got.Name != "new" {
			t.Fatalf("stored value was not replaced: %+v", got)
		}
	})
}

func TestScopedInvalidation(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	writer := c.NewCache("catalog", WithVersionCache(0)) // reads the version on every call
	reader := c.NewCache("catalog", WithVersionCache(0))

	loads := 0
	get := func(k *Cache) string {
		key := k.ScopedKey(ctx, "product", "42")
		v, err := Remember(ctx, k, key, time.Minute, func(context.Context) (string, error) { loads++; return "v" + string(rune('0'+loads)), nil })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if a, b := get(reader), get(reader); a != "v1" || b != "v1" || loads != 1 {
		t.Fatalf("%s %s loads=%d", a, b, loads)
	}
	if err := writer.Bump(ctx, "product"); err != nil {
		t.Fatal(err)
	}
	if got := get(reader); got != "v2" || loads != 2 {
		t.Fatalf("after Bump another cache object must reload: %s loads=%d", got, loads)
	}
	if ttl := c.Raw().TTL(ctx, c.Key("catalog", "ver", "product")).Val(); ttl <= 0 {
		t.Fatalf("the version key needs a TTL too: %v", ttl)
	}
	// other scopes are untouched
	other := writer.ScopedKey(ctx, "category", "1")
	if !strings.HasPrefix(other, "category:v0:") {
		t.Fatalf("%s", other)
	}

	// the local version cache bounds staleness: a process with a 1s cache sees a Bump from elsewhere within 1s
	slow := c.NewCache("catalog", WithVersionCache(200*time.Millisecond))
	before := slow.ScopedKey(ctx, "product", "9")
	_ = writer.Bump(ctx, "product")
	if slow.ScopedKey(ctx, "product", "9") != before {
		t.Fatal("within the cache window the old version is still used")
	}
	time.Sleep(250 * time.Millisecond)
	if slow.ScopedKey(ctx, "product", "9") == before {
		t.Fatal("after the cache window the new version must be used")
	}
}

func TestReadModel(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()
	k := c.NewCache("views")
	source := map[string]card{"1": {"1", "ada"}, "2": {"2", "bob"}, "3": {"3", "cy"}, "4": {"4", "dee"}}
	rebuilds := 0
	rm := NewReadModel(k, "card", time.Minute, func(_ context.Context, id string) (card, error) {
		rebuilds++
		v, ok := source[id]
		if !ok {
			return card{}, apperr.NotFound("card")
		}
		return v, nil
	})

	if v, err := rm.Get(ctx, "1"); err != nil || v.Name != "ada" {
		t.Fatalf("%v %v", v, err)
	}
	_, _ = rm.Get(ctx, "1")
	if rebuilds != 1 {
		t.Fatalf("rebuilds = %d", rebuilds)
	}
	// write-through, then invalidation
	if err := rm.Put(ctx, "1", card{"1", "ada v2"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := rm.Get(ctx, "1"); v.Name != "ada v2" || rebuilds != 1 {
		t.Fatalf("Put must be served without a rebuild: %+v rebuilds=%d", v, rebuilds)
	}
	if err := rm.Invalidate(ctx, "1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := rm.Get(ctx, "1"); v.Name != "ada" || rebuilds != 2 {
		t.Fatalf("after Invalidate the next read rebuilds: %+v rebuilds=%d", v, rebuilds)
	}
	if _, err := rm.Get(ctx, "nope"); !apperr.Is(err, apperr.KindNotFound) {
		t.Fatalf("%v", err)
	}

	// GetMany: one MGET, ONE batched load for all misses, then everything is cached
	_ = rm.Put(ctx, "2", source["2"])
	batches := 0
	var asked []string
	loadMany := func(_ context.Context, missing []string) (map[string]card, error) {
		batches++
		asked = append(asked, missing...)
		out := map[string]card{}
		for _, id := range missing {
			if v, ok := source[id]; ok {
				out[id] = v
			}
		}
		return out, nil
	}
	got, err := rm.GetMany(ctx, []string{"1", "2", "3", "4", "ghost"}, loadMany)
	if err != nil || len(got) != 4 || got["3"].Name != "cy" {
		t.Fatalf("%v %v", got, err)
	}
	if batches != 1 || len(asked) != 3 { // 1 and 2 were cached; 3, 4, ghost were not
		t.Fatalf("batches=%d asked=%v", batches, asked)
	}
	if _, ok := got["ghost"]; ok {
		t.Fatal("ids the source does not have are absent from the result")
	}
	if _, err := rm.GetMany(ctx, []string{"1", "2", "3", "4"}, loadMany); err != nil || batches != 1 {
		t.Fatalf("all cached now: batches=%d err=%v", batches, err)
	}
	if m, err := rm.GetMany(ctx, nil, loadMany); err != nil || len(m) != 0 {
		t.Fatal("empty input")
	}
	if ttl := c.Raw().TTL(ctx, c.Key("views", "rm:card:3")).Val(); ttl <= 0 {
		t.Fatalf("batch-written keys need a TTL: %v", ttl)
	}
}

func TestHashAndRanking(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	h, _ := c.Hash("product-stats", time.Minute)
	if err := h.Set(ctx, map[string]any{"name": "tea", "price": 12}); err != nil {
		t.Fatal(err)
	}
	if n, err := h.Incr(ctx, "views", 3); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := h.Incr(ctx, "views", 2); n != 5 {
		t.Fatalf("%d", n)
	}
	all, _ := h.GetAll(ctx)
	if all["name"] != "tea" || all["views"] != "5" || all["price"] != "12" {
		t.Fatalf("%v", all)
	}
	if v, ok, _ := h.Get(ctx, "name"); !ok || v != "tea" {
		t.Fatal("get")
	}
	if _, ok, _ := h.Get(ctx, "missing"); ok {
		t.Fatal("missing field")
	}
	if ttl := c.Raw().TTL(ctx, c.Key("rm", "product-stats")).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("hash writes must refresh the TTL: %v", ttl)
	}
	_ = h.Delete(ctx)
	if m, _ := h.GetAll(ctx); len(m) != 0 {
		t.Fatal("deleted")
	}

	r, _ := c.Ranking("top-products", time.Minute, 3) // keep the best 3
	for member, score := range map[string]float64{"a": 10, "b": 50, "c": 30, "d": 20, "e": 40} {
		if err := r.Add(ctx, member, score); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := r.Len(ctx); n != 3 {
		t.Fatalf("max size must bound memory: %d", n)
	}
	top, _ := r.Top(ctx, 10)
	if len(top) != 3 || top[0].Member != "b" || top[1].Member != "e" || top[2].Member != "c" {
		t.Fatalf("%v", top)
	}
	_ = r.Incr(ctx, "c", 100) // c becomes the leader
	if pos, ok, _ := r.Rank(ctx, "c"); !ok || pos != 0 {
		t.Fatalf("rank %d %v", pos, ok)
	}
	if _, ok, _ := r.Rank(ctx, "a"); ok {
		t.Fatal("a was trimmed")
	}
	_ = r.Remove(ctx, "c")
	if n, _ := r.Len(ctx); n != 2 {
		t.Fatalf("%d", n)
	}
	if ttl := c.Raw().TTL(ctx, c.Key("rm", "top-products")).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ranking writes must refresh the TTL: %v", ttl)
	}
}

func BenchmarkRememberHit(b *testing.B) {
	c := liveClient(b)
	ctx := context.Background()
	k := c.NewCache("bench")
	load := func(context.Context) (card, error) { return card{ID: "1", Name: "ada"}, nil }
	_, _ = Remember(ctx, k, "1", time.Hour, load)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Remember(ctx, k, "1", time.Hour, load); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetMany100(b *testing.B) {
	c := liveClient(b)
	ctx := context.Background()
	k := c.NewCache("bench")
	rm := NewReadModel(k, "card", time.Hour, func(_ context.Context, id string) (card, error) { return card{ID: id}, nil })
	ids := make([]string, 100)
	src := map[string]card{}
	for i := range ids {
		ids[i] = strings.Repeat("x", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		src[ids[i]] = card{ID: ids[i], Name: "n"}
	}
	loadMany := func(_ context.Context, missing []string) (map[string]card, error) { return src, nil }
	_, _ = rm.GetMany(ctx, ids, loadMany)
	b.Run("GetMany-one-MGET", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := rm.GetMany(ctx, ids, loadMany); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Get-loop-100-round-trips", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, id := range ids {
				if _, err := rm.Get(ctx, id); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
