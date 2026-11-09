package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fast() Policy { return Policy{Initial: time.Millisecond, Max: 2 * time.Millisecond} }

func TestDoRetriesUntilSuccess(t *testing.T) {
	calls := 0
	err := Do(context.Background(), fast(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDoStopsOnPermanentAndMaxAttempts(t *testing.T) {
	boom := errors.New("bad config")
	calls := 0
	err := Do(context.Background(), fast(), func(context.Context) error { calls++; return Permanent(boom) })
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}

	p := fast()
	p.MaxAttempts = 2
	calls = 0
	err = Do(context.Background(), p, func(context.Context) error { calls++; return boom })
	if !errors.Is(err, boom) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestDoHonoursContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := Do(ctx, Policy{Initial: time.Second}, func(context.Context) error { return errors.New("down") })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
