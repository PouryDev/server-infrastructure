package redisx

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Thin wrappers for the two Redis structures read models are usually made of. They exist to make the
// shared-Redis rules impossible to forget: every key is under the project prefix and every write refreshes
// the TTL in the same transaction.

func isNil(err error) bool { return err == redis.Nil } //nolint:errorlint // go-redis documents comparing with redis.Nil

// Hash is a Redis hash: a small record or a set of counters under one key.
type Hash struct {
	c   *Client
	key string
	ttl time.Duration
}

// Hash returns the hash stored at <prefix>rm:<name>. ttl must be positive.
func (c *Client) Hash(name string, ttl time.Duration) (*Hash, error) {
	if ttl <= 0 {
		return nil, ErrNoTTL
	}
	return &Hash{c: c, key: c.Key("rm", name), ttl: ttl}, nil
}

// Set writes fields and refreshes the TTL.
func (h *Hash) Set(ctx context.Context, fields map[string]any) error {
	_, err := h.c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, h.key, fields)
		p.Expire(ctx, h.key, h.ttl)
		return nil
	})
	return err
}

// Incr adds by to a numeric field (creating it) and refreshes the TTL; it returns the new value.
func (h *Hash) Incr(ctx context.Context, field string, by int64) (int64, error) {
	var incr *redis.IntCmd
	_, err := h.c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		incr = p.HIncrBy(ctx, h.key, field, by)
		p.Expire(ctx, h.key, h.ttl)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

// Get reads one field.
func (h *Hash) Get(ctx context.Context, field string) (string, bool, error) {
	v, err := h.c.rdb.HGet(ctx, h.key, field).Result()
	if isNil(err) {
		return "", false, nil
	}
	return v, err == nil, err
}

// GetAll reads every field (an empty map when the key does not exist).
func (h *Hash) GetAll(ctx context.Context) (map[string]string, error) {
	return h.c.rdb.HGetAll(ctx, h.key).Result()
}

// Delete removes the hash.
func (h *Hash) Delete(ctx context.Context) error { return h.c.rdb.Del(ctx, h.key).Err() }

// Scored is a ranking entry.
type Scored struct {
	Member string
	Score  float64
}

// Ranking is a Redis sorted set: a leaderboard, "top N", most recent items. A maximum size keeps its
// memory bounded.
type Ranking struct {
	c       *Client
	key     string
	ttl     time.Duration
	maxSize int64
}

// Ranking returns the sorted set stored at <prefix>rm:<name>. ttl must be positive; maxSize > 0 keeps only
// the best maxSize members (the lowest scores are dropped after each write).
func (c *Client) Ranking(name string, ttl time.Duration, maxSize int) (*Ranking, error) {
	if ttl <= 0 {
		return nil, ErrNoTTL
	}
	return &Ranking{c: c, key: c.Key("rm", name), ttl: ttl, maxSize: int64(maxSize)}, nil
}

func (r *Ranking) write(ctx context.Context, op func(p redis.Pipeliner)) error {
	_, err := r.c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		op(p)
		if r.maxSize > 0 {
			p.ZRemRangeByRank(ctx, r.key, 0, -(r.maxSize + 1))
		}
		p.Expire(ctx, r.key, r.ttl)
		return nil
	})
	return err
}

// Add sets a member's score.
func (r *Ranking) Add(ctx context.Context, member string, score float64) error {
	return r.write(ctx, func(p redis.Pipeliner) { p.ZAdd(ctx, r.key, redis.Z{Score: score, Member: member}) })
}

// Incr adds delta to a member's score (creating it).
func (r *Ranking) Incr(ctx context.Context, member string, delta float64) error {
	return r.write(ctx, func(p redis.Pipeliner) { p.ZIncrBy(ctx, r.key, delta, member) })
}

// Top returns the n best members, highest score first.
func (r *Ranking) Top(ctx context.Context, n int) ([]Scored, error) {
	zs, err := r.c.rdb.ZRevRangeWithScores(ctx, r.key, 0, int64(n)-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Scored, len(zs))
	for i, z := range zs {
		out[i] = Scored{Member: z.Member.(string), Score: z.Score}
	}
	return out, nil
}

// Rank returns the 0-based position of member (0 = best) and whether it is in the ranking.
func (r *Ranking) Rank(ctx context.Context, member string) (int64, bool, error) {
	pos, err := r.c.rdb.ZRevRank(ctx, r.key, member).Result()
	if isNil(err) {
		return 0, false, nil
	}
	return pos, err == nil, err
}

// Remove deletes members.
func (r *Ranking) Remove(ctx context.Context, members ...string) error {
	args := make([]any, len(members))
	for i, m := range members {
		args[i] = m
	}
	return r.c.rdb.ZRem(ctx, r.key, args...).Err()
}

// Len returns the number of members.
func (r *Ranking) Len(ctx context.Context) (int64, error) { return r.c.rdb.ZCard(ctx, r.key).Result() }
