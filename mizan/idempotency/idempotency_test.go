package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/idempotency"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
)

func TestRunsOnceAndReplays(t *testing.T) {
	s := idempotency.New(testdb.New(t))
	ctx := context.Background()
	var calls atomic.Int32
	fn := func(context.Context) (int, []byte, error) {
		calls.Add(1)
		return 201, []byte(`{"id":"x"}`), nil
	}
	var wg sync.WaitGroup
	results := make(chan idempotency.Result, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Do(ctx, "inv", "k1", "h1", fn)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	replays := 0
	for r := range results {
		if r.Status != 201 || string(r.Body) != `{"id":"x"}` {
			t.Fatalf("bad result %+v %s", r, r.Body)
		}
		if r.Replayed {
			replays++
		}
	}
	if calls.Load() != 1 || replays != 7 {
		t.Fatalf("calls=%d replays=%d", calls.Load(), replays)
	}
	if _, err := s.Do(ctx, "inv", "k1", "other-hash", fn); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("different request with the same key must conflict: %v", err)
	}
}

func TestFailureIsNotCached(t *testing.T) {
	s := idempotency.New(testdb.New(t))
	ctx := context.Background()
	if _, err := s.Do(ctx, "s", "k", "h", func(context.Context) (int, []byte, error) { return 0, nil, errors.New("db down") }); err == nil {
		t.Fatal("expected error")
	}
	r, err := s.Do(ctx, "s", "k", "h", func(context.Context) (int, []byte, error) { return 200, []byte(`1`), nil })
	if err != nil || r.Replayed || r.Status != 200 {
		t.Fatalf("a failed first attempt must not block the retry: %+v %v", r, err)
	}
}
