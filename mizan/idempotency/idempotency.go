// Package idempotency makes unsafe HTTP commands (POST) safe to retry. The key's row is written in the
// same transaction as the business change, so "the change happened" and "the key is used" are one fact:
// a retry either replays the stored response or, when the first attempt rolled back, runs again.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
)

// Result is the response of a command, fresh or replayed.
type Result struct {
	Status   int
	Body     []byte
	Replayed bool
}

// Store keeps idempotency keys in mz_idempotency.
type Store struct{ db *postgres.DB }

// New creates the store.
func New(db *postgres.DB) *Store { return &Store{db: db} }

// Hash fingerprints a request (method, path, body, caller...) so a key reused for a different request is
// detected instead of silently replaying the wrong response.
func Hash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type stored struct {
	Hash   string `db:"request_hash"`
	Status int    `db:"status_code"`
	Body   []byte `db:"response"`
}

// Do runs fn at most once per (scope, key). fn executes inside a transaction that also stores its
// response; an error from fn rolls everything back, including the key, so failures are retryable.
func (s *Store) Do(ctx context.Context, scope, key, requestHash string, fn func(ctx context.Context) (int, []byte, error)) (Result, error) {
	if key == "" || len(key) > 200 {
		return Result{}, apperr.Invalid("Idempotency-Key must be 1-200 characters")
	}
	for range 3 {
		var res Result
		retry := false
		err := s.db.WithTx(ctx, func(ctx context.Context) error {
			q := s.db.Querier(ctx)
			n, err := postgres.Exec(ctx, q, `
				INSERT INTO mz_idempotency (scope, key, request_hash, status_code, response) VALUES ($1, $2, $3, 0, ''::bytea)
				ON CONFLICT DO NOTHING`, scope, key, requestHash)
			if err != nil {
				return err
			}
			if n == 0 { // somebody has the key: wait for their transaction, then look at the outcome
				rows, err := postgres.Select[stored](ctx, q,
					`SELECT request_hash, status_code, response FROM mz_idempotency WHERE scope = $1 AND key = $2 FOR UPDATE`, scope, key)
				if err != nil {
					return err
				}
				if len(rows) == 0 { // the first attempt rolled back
					retry = true
					return nil
				}
				if rows[0].Hash != requestHash {
					return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the Idempotency-Key was used with a different request")
				}
				res = Result{Status: rows[0].Status, Body: rows[0].Body, Replayed: true}
				return nil
			}
			status, body, err := fn(ctx)
			if err != nil {
				return err
			}
			res = Result{Status: status, Body: body}
			_, err = postgres.Exec(ctx, q, `UPDATE mz_idempotency SET status_code = $3, response = $4 WHERE scope = $1 AND key = $2`,
				scope, key, status, body)
			return err
		})
		if err != nil {
			return Result{}, err
		}
		if !retry {
			return res, nil
		}
	}
	return Result{}, apperr.New(apperr.KindConflict, "idempotency_busy", "the request is being processed, retry shortly")
}

// Purge deletes keys older than days; call it from a daily job.
func (s *Store) Purge(ctx context.Context, days int) (int64, error) {
	return postgres.Exec(ctx, s.db.Querier(ctx), `DELETE FROM mz_idempotency WHERE created_at < now() - make_interval(days => $1)`, days)
}
