// Package pgappropriation is the PostgreSQL adapter of appropriation.Repository.
package pgappropriation

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
)

// Repository implements appropriation.Repository.
type Repository struct{ db *postgres.DB }

var _ appropriation.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type row struct {
	ID                 uuid.UUID  `db:"id"`
	FiscalYear         string     `db:"fiscal_year"`
	DecidedOn          time.Time  `db:"decided_on"`
	PayBy              time.Time  `db:"pay_by"`
	NetProfit          int64      `db:"net_profit"`
	RetainedAtStart    int64      `db:"retained_at_start"`
	Capital            int64      `db:"capital"`
	LegalReserveBefore int64      `db:"legal_reserve_before"`
	PublicCompany      bool       `db:"public_company"`
	LegalReserve       int64      `db:"legal_reserve"`
	OptionalReserve    int64      `db:"optional_reserve"`
	Dividend           int64      `db:"dividend"`
	Paid               int64      `db:"paid"`
	JournalID          *uuid.UUID `db:"journal_id"`
	IdemKey            string     `db:"idem_key"`
	Actor              string     `db:"actor"`
	CreatedAt          time.Time  `db:"created_at"`
}

const cols = `id, fiscal_year, decided_on, pay_by, net_profit, retained_at_start, capital, legal_reserve_before, public_company, legal_reserve, optional_reserve,
	dividend, paid, journal_id, idem_key, actor, created_at`

func (x row) domain() appropriation.Record {
	return appropriation.Record{ID: x.ID, FiscalYear: x.FiscalYear, DecidedOn: x.DecidedOn, PayBy: x.PayBy,
		Facts:        appropriation.Facts{NetProfit: x.NetProfit, RetainedAtStart: x.RetainedAtStart, Capital: x.Capital, LegalReserve: x.LegalReserveBefore, PublicCompany: x.PublicCompany},
		LegalReserve: x.LegalReserve, OptionalReserve: x.OptionalReserve, Dividend: x.Dividend, Paid: x.Paid, Journal: x.JournalID, IdemKey: x.IdemKey, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) Insert(ctx context.Context, rec appropriation.Record) (appropriation.Record, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_appr_decisions (`+cols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT (idem_key) DO NOTHING`,
		rec.ID, rec.FiscalYear, rec.DecidedOn, rec.PayBy, rec.Facts.NetProfit, rec.Facts.RetainedAtStart, rec.Facts.Capital, rec.Facts.LegalReserve, rec.Facts.PublicCompany,
		rec.LegalReserve, rec.OptionalReserve, rec.Dividend, rec.Paid, rec.Journal, rec.IdemKey, rec.Actor, rec.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_appr_decisions_fiscal_year_key") {
		return rec, false, apperr.New(apperr.KindConflict, "already_declared", "the profit of this fiscal year was already appropriated")
	}
	if err != nil {
		return rec, false, err
	}
	if n == 1 {
		return rec, true, nil
	}
	x, err := postgres.SelectOne[row](ctx, q, `SELECT `+cols+` FROM mz_appr_decisions WHERE idem_key = $1`, rec.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) Record(ctx context.Context, id uuid.UUID, forUpdate bool) (appropriation.Record, error) {
	sql := `SELECT ` + cols + ` FROM mz_appr_decisions WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[row](ctx, r.db.Querier(ctx), sql, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return appropriation.Record{}, apperr.NotFound("appropriation")
	}
	return x.domain(), err
}

func (r *Repository) Records(ctx context.Context) ([]appropriation.Record, error) {
	rows, err := postgres.Select[row](ctx, r.db.Querier(ctx), `SELECT `+cols+` FROM mz_appr_decisions ORDER BY decided_on DESC, id`)
	out := make([]appropriation.Record, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) UpdateJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_appr_decisions SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

func (r *Repository) AddPayment(ctx context.Context, id uuid.UUID, key string, amount int64, journal uuid.UUID, date time.Time) (bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_appr_payments (idem_key, decision_id, amount, journal_id, paid_on) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (idem_key) DO NOTHING`,
		key, id, amount, journal, date)
	if err != nil || n == 0 {
		return false, err
	}
	_, err = postgres.Exec(ctx, q, `UPDATE mz_appr_decisions SET paid = paid + $2 WHERE id = $1`, id, amount)
	return err == nil, err
}

func (r *Repository) HasPayment(ctx context.Context, id uuid.UUID, key string) (bool, error) {
	return postgres.Value[bool](ctx, r.db.Querier(ctx), `SELECT EXISTS (SELECT 1 FROM mz_appr_payments WHERE idem_key = $1 AND decision_id = $2)`, key, id)
}
