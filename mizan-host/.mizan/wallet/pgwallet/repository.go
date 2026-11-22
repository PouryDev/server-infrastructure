// Package pgwallet is the PostgreSQL adapter of wallet.Repository.
package pgwallet

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

// Repository implements wallet.Repository.
type Repository struct{ db *postgres.DB }

var _ wallet.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type row struct {
	ID        uuid.UUID `db:"id"`
	OwnerRef  string    `db:"owner_ref"`
	Currency  string    `db:"currency"`
	Status    string    `db:"status"`
	Account   string    `db:"account_code"`
	CreatedAt time.Time `db:"created_at"`
}

func (r row) domain() wallet.Wallet {
	return wallet.Wallet{ID: r.ID, OwnerRef: r.OwnerRef, Currency: money.Currency(r.Currency), Status: wallet.Status(r.Status), AccountCode: r.Account, CreatedAt: r.CreatedAt}
}

const cols = `w.id, w.owner_ref, w.currency, w.status, a.code AS account_code, w.created_at`
const from = ` FROM mz_wallets w JOIN mz_accounts a ON a.id = w.account_id `

func (r *Repository) LockOwner(ctx context.Context, owner string, cur money.Currency) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "wallet:"+owner+":"+string(cur))
	return err
}

func (r *Repository) ByOwnerCurrency(ctx context.Context, owner string, cur money.Currency) (wallet.Wallet, error) {
	v, err := postgres.SelectOne[row](ctx, r.db.Querier(ctx), `SELECT `+cols+from+`WHERE w.owner_ref = $1 AND w.currency = $2`, owner, string(cur))
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return wallet.Wallet{}, apperr.NotFound("wallet")
		}
		return wallet.Wallet{}, err
	}
	return v.domain(), nil
}

// Insert links the wallet to its ledger account, which the service created just before in the same transaction.
func (r *Repository) Insert(ctx context.Context, w wallet.Wallet) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_wallets (id, owner_ref, currency, status, account_id)
		SELECT $1, $2, $3, $4, id FROM mz_accounts WHERE code = $5`, w.ID, w.OwnerRef, string(w.Currency), string(w.Status), w.AccountCode)
	if err == nil && n == 0 {
		return apperr.Internal(nil)
	}
	return err
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID, forUpdate bool) (wallet.Wallet, error) {
	sql := `SELECT ` + cols + from + `WHERE w.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF w`
	}
	v, err := postgres.SelectOne[row](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return wallet.Wallet{}, apperr.NotFound("wallet")
		}
		return wallet.Wallet{}, err
	}
	return v.domain(), nil
}

func (r *Repository) ByOwner(ctx context.Context, owner string) ([]wallet.Wallet, error) {
	rows, err := postgres.Select[row](ctx, r.db.Querier(ctx), `SELECT `+cols+from+`WHERE w.owner_ref = $1 ORDER BY w.created_at`, owner)
	if err != nil {
		return nil, err
	}
	out := make([]wallet.Wallet, len(rows))
	for i, v := range rows {
		out[i] = v.domain()
	}
	return out, nil
}

func (r *Repository) SetStatus(ctx context.Context, id uuid.UUID, st wallet.Status) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_wallets SET status = $2 WHERE id = $1`, id, string(st))
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("wallet")
	}
	return nil
}
