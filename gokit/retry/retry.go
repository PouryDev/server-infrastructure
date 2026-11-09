// Package retry runs an operation with exponential backoff and jitter.
//
// Shared infrastructure may start after a project, so every connection to a dependency is made
// through Do instead of failing on the first refused connection.
package retry

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// Policy configures Do. The zero value is replaced by sensible defaults.
type Policy struct {
	MaxAttempts int           // 0 = retry until the context ends
	Initial     time.Duration // first delay (default 200ms)
	Max         time.Duration // delay ceiling (default 10s)
	Multiplier  float64       // growth factor (default 2)
	// OnRetry is called before sleeping, with the failed attempt number (1-based).
	OnRetry func(attempt int, err error, delay time.Duration)
}

func (p Policy) withDefaults() Policy {
	if p.Initial <= 0 {
		p.Initial = 200 * time.Millisecond
	}
	if p.Max <= 0 {
		p.Max = 10 * time.Second
	}
	if p.Multiplier < 1 {
		p.Multiplier = 2
	}
	return p
}

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks err as not worth retrying; Do returns the wrapped error immediately.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// IsPermanent reports whether err was marked with Permanent (Do itself returns the unwrapped cause, so
// classify inside fn when the distinction matters afterwards).
func IsPermanent(err error) bool {
	var perm permanent
	return errors.As(err, &perm)
}

// Do calls fn until it succeeds, returns a Permanent error, runs out of attempts, or ctx ends.
func Do(ctx context.Context, p Policy, fn func(ctx context.Context) error) error {
	p = p.withDefaults()
	delay := p.Initial
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		var perm permanent
		if errors.As(err, &perm) {
			return perm.err
		}
		if p.MaxAttempts > 0 && attempt >= p.MaxAttempts {
			return err
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), err)
		}
		// Full jitter between delay/2 and delay avoids thundering herds after an infra restart.
		sleep := delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1)) //nolint:gosec // jitter needs no crypto randomness
		if p.OnRetry != nil {
			p.OnRetry(attempt, err, sleep)
		}
		t := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(ctx.Err(), err)
		case <-t.C:
		}
		delay = min(time.Duration(float64(delay)*p.Multiplier), p.Max)
	}
}
