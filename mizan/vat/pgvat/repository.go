// Package pgvat is the PostgreSQL adapter of vat.Repository.
package pgvat

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/vat"
)

// Repository implements vat.Repository.
type Repository struct{ db *postgres.DB }

var _ vat.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type ruleRow struct {
	ID             uuid.UUID `db:"id"`
	EffectiveFrom  time.Time `db:"effective_from"`
	PeriodMonths   int       `db:"period_months"`
	FilingDays     int       `db:"filing_days"`
	FilingMonthEnd bool      `db:"filing_month_end"`
	LatePenaltyBP  int       `db:"late_penalty_bp"`
	Source         string    `db:"source"`
	CreatedAt      time.Time `db:"created_at"`
}

func (r *Repository) InsertRule(ctx context.Context, x vat.Rule) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_vat_rules (id, effective_from, period_months, filing_days, filing_month_end, late_penalty_bp, source, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, x.ID, x.EffectiveFrom, x.PeriodMonths, x.FilingDays, x.FilingMonthEnd, x.LatePenaltyBP, x.Source, x.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_vat_rules_effective_from_key") {
		return apperr.New(apperr.KindConflict, "rule_date_taken", "a version with this effective date already exists")
	}
	return err
}

func (r *Repository) Rules(ctx context.Context) ([]vat.Rule, error) {
	rows, err := postgres.Select[ruleRow](ctx, r.db.Querier(ctx), `SELECT id, effective_from, period_months, filing_days, filing_month_end, late_penalty_bp, source, created_at FROM mz_vat_rules ORDER BY effective_from`)
	out := make([]vat.Rule, len(rows))
	for i, x := range rows {
		out[i] = vat.Rule{ID: x.ID, EffectiveFrom: x.EffectiveFrom, PeriodMonths: x.PeriodMonths, FilingDays: x.FilingDays, FilingMonthEnd: x.FilingMonthEnd, LatePenaltyBP: x.LatePenaltyBP, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

type returnRow struct {
	ID             uuid.UUID  `db:"id"`
	Year           int        `db:"year"`
	PeriodMonths   int        `db:"period_months"`
	PeriodNo       int        `db:"period_no"`
	FromDate       time.Time  `db:"from_date"`
	ToDate         time.Time  `db:"to_date"`
	Output         int64      `db:"output_tax"`
	Input          int64      `db:"input_tax"`
	Net            int64      `db:"net"`
	OpeningBalance int64      `db:"opening_balance"`
	DueOn          *time.Time `db:"due_on"`
	FiledOn        time.Time  `db:"filed_on"`
	JournalID      *uuid.UUID `db:"journal_id"`
	IdemKey        string     `db:"idem_key"`
	Actor          string     `db:"actor"`
	CreatedAt      time.Time  `db:"created_at"`
	Revision       int        `db:"revision"`
	AmendsID       *uuid.UUID `db:"amends_id"`
	DeltaOutput    int64      `db:"delta_output"`
	DeltaInput     int64      `db:"delta_input"`
}

const returnCols = `id, year, period_months, period_no, from_date, to_date, output_tax, input_tax, net, opening_balance, due_on, filed_on, journal_id, idem_key, actor, created_at, revision, amends_id, delta_output, delta_input`

func (x returnRow) domain() vat.Return {
	return vat.Return{ID: x.ID, Figures: vat.Figures{Year: x.Year, No: x.PeriodNo, Months: x.PeriodMonths, From: x.FromDate, To: x.ToDate, Output: x.Output, Input: x.Input, Net: x.Net,
		OpeningBalance: x.OpeningBalance, DueOn: x.DueOn}, FiledOn: x.FiledOn, Journal: x.JournalID, IdemKey: x.IdemKey, Actor: x.Actor, CreatedAt: x.CreatedAt,
		Revision: x.Revision, Amends: x.AmendsID, DeltaOut: x.DeltaOutput, DeltaIn: x.DeltaInput}
}

func (r *Repository) InsertReturn(ctx context.Context, x vat.Return) (vat.Return, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_vat_returns (`+returnCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) ON CONFLICT (idem_key) DO NOTHING`,
		x.ID, x.Year, x.Months, x.No, x.From, x.To, x.Output, x.Input, x.Net, x.OpeningBalance, x.DueOn, x.FiledOn, x.Journal, x.IdemKey, x.Actor, x.CreatedAt, x.Revision, x.Amends, x.DeltaOut, x.DeltaIn)
	if postgres.IsUniqueViolation(err, "mz_vat_returns_period_revision_key") {
		return x, false, apperr.New(apperr.KindConflict, "already_filed", "this tax period was already filed")
	}
	if err != nil {
		return x, false, err
	}
	if n == 1 {
		return x, true, nil
	}
	row, err := postgres.SelectOne[returnRow](ctx, q, `SELECT `+returnCols+` FROM mz_vat_returns WHERE idem_key = $1`, x.IdemKey)
	return row.domain(), false, err
}

func (r *Repository) SetReturnJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_vat_returns SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

func (r *Repository) Return(ctx context.Context, id uuid.UUID) (vat.Return, error) {
	x, err := postgres.SelectOne[returnRow](ctx, r.db.Querier(ctx), `SELECT `+returnCols+` FROM mz_vat_returns WHERE id = $1`, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return vat.Return{}, apperr.NotFound("return")
	}
	return x.domain(), err
}

func (r *Repository) Returns(ctx context.Context) ([]vat.Return, error) {
	rows, err := postgres.Select[returnRow](ctx, r.db.Querier(ctx), `SELECT `+returnCols+` FROM mz_vat_returns ORDER BY year DESC, period_no DESC, revision DESC`)
	out := make([]vat.Return, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) LockPeriod(ctx context.Context, year, months, no int) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, fmt.Sprintf("mizan:vat:%d:%d:%d", year, months, no))
	return err
}

func (r *Repository) LatestReturn(ctx context.Context, year, months, no int) (vat.Return, bool, error) {
	rows, err := postgres.Select[returnRow](ctx, r.db.Querier(ctx), `SELECT `+returnCols+` FROM mz_vat_returns WHERE year = $1 AND period_months = $2 AND period_no = $3 ORDER BY revision DESC LIMIT 1`, year, months, no)
	if err != nil || len(rows) == 0 {
		return vat.Return{}, false, err
	}
	return rows[0].domain(), true, nil
}

func (r *Repository) ReturnByKey(ctx context.Context, key string) (vat.Return, bool, error) {
	rows, err := postgres.Select[returnRow](ctx, r.db.Querier(ctx), `SELECT `+returnCols+` FROM mz_vat_returns WHERE idem_key = $1`, key)
	if err != nil || len(rows) == 0 {
		return vat.Return{}, false, err
	}
	return rows[0].domain(), true, nil
}

type rateRow struct {
	ID            uuid.UUID `db:"id"`
	Category      string    `db:"category"`
	EffectiveFrom time.Time `db:"effective_from"`
	RateBP        int       `db:"rate_bp"`
	LevyBP        *int      `db:"levy_bp"`
	Description   string    `db:"description"`
	Verified      bool      `db:"verified"`
	Source        string    `db:"source"`
	CreatedAt     time.Time `db:"created_at"`
}

func (r *Repository) InsertRate(ctx context.Context, x vat.RateVersion) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_vat_rates (id, category, effective_from, rate_bp, levy_bp, description, verified, source, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		x.ID, x.Category, x.EffectiveFrom, x.RateBP, x.LevyBP, x.Description, x.Verified, x.Source, x.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_vat_rates_category_effective_from_key") {
		return apperr.New(apperr.KindConflict, "rate_date_taken", "a version of this category with this effective date already exists")
	}
	return err
}

func (r *Repository) Rates(ctx context.Context) ([]vat.RateVersion, error) {
	rows, err := postgres.Select[rateRow](ctx, r.db.Querier(ctx), `SELECT id, category, effective_from, rate_bp, levy_bp, description, verified, source, created_at FROM mz_vat_rates ORDER BY category, effective_from`)
	out := make([]vat.RateVersion, len(rows))
	for i, x := range rows {
		out[i] = vat.RateVersion{ID: x.ID, Category: x.Category, EffectiveFrom: x.EffectiveFrom, RateBP: x.RateBP, LevyBP: x.LevyBP, Description: x.Description, Verified: x.Verified, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

type exemptionRow struct {
	ID            uuid.UUID  `db:"id"`
	Code          string     `db:"code"`
	Kind          string     `db:"kind"`
	Description   string     `db:"description"`
	Article       string     `db:"article"`
	EffectiveFrom time.Time  `db:"effective_from"`
	EffectiveTo   *time.Time `db:"effective_to"`
	Source        string     `db:"source"`
	CreatedAt     time.Time  `db:"created_at"`
}

func (r *Repository) InsertExemption(ctx context.Context, x vat.Exemption) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_vat_exemptions (id, code, kind, description, article, effective_from, effective_to, source, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		x.ID, x.Code, x.Kind, x.Description, x.Article, x.EffectiveFrom, x.EffectiveTo, x.Source, x.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_vat_exemptions_code_effective_from_key") {
		return apperr.New(apperr.KindConflict, "exemption_date_taken", "a version of this code with this effective date already exists")
	}
	return err
}

func (r *Repository) Exemptions(ctx context.Context) ([]vat.Exemption, error) {
	rows, err := postgres.Select[exemptionRow](ctx, r.db.Querier(ctx), `SELECT id, code, kind, description, article, effective_from, effective_to, source, created_at FROM mz_vat_exemptions ORDER BY code, effective_from`)
	out := make([]vat.Exemption, len(rows))
	for i, x := range rows {
		out[i] = vat.Exemption{ID: x.ID, Code: x.Code, Kind: x.Kind, Description: x.Description, Article: x.Article, EffectiveFrom: x.EffectiveFrom, EffectiveTo: x.EffectiveTo, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

type apportionRow struct {
	ID             uuid.UUID  `db:"id"`
	Year           int        `db:"year"`
	PeriodMonths   int        `db:"period_months"`
	PeriodNo       int        `db:"period_no"`
	InputTotal     int64      `db:"input_total"`
	DirectTaxable  int64      `db:"direct_taxable"`
	DirectExempt   int64      `db:"direct_exempt"`
	Machinery      int64      `db:"machinery"`
	Common         int64      `db:"common"`
	TaxableSales   int64      `db:"taxable_sales"`
	ExemptSales    int64      `db:"exempt_sales"`
	Recoverable    int64      `db:"recoverable"`
	NonRecoverable int64      `db:"non_recoverable"`
	JournalID      *uuid.UUID `db:"journal_id"`
	IdemKey        string     `db:"idem_key"`
	Actor          string     `db:"actor"`
	CreatedAt      time.Time  `db:"created_at"`
}

const apportionCols = `id, year, period_months, period_no, input_total, direct_taxable, direct_exempt, machinery, common, taxable_sales, exempt_sales, recoverable, non_recoverable, journal_id, idem_key, actor, created_at`

func (x apportionRow) domain() vat.Apportionment {
	return vat.Apportionment{ID: x.ID, Year: x.Year, No: x.PeriodNo, Mths: x.PeriodMonths, InputTotal: x.InputTotal, DirectTaxable: x.DirectTaxable, DirectExempt: x.DirectExempt,
		Machinery: x.Machinery, Common: x.Common, TaxableSales: x.TaxableSales, ExemptSales: x.ExemptSales, Recoverable: x.Recoverable, NonRecoverable: x.NonRecoverable,
		Journal: x.JournalID, IdemKey: x.IdemKey, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertApportionment(ctx context.Context, a vat.Apportionment) (vat.Apportionment, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_vat_apportionments (`+apportionCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) ON CONFLICT (idem_key) DO NOTHING`,
		a.ID, a.Year, a.Mths, a.No, a.InputTotal, a.DirectTaxable, a.DirectExempt, a.Machinery, a.Common, a.TaxableSales, a.ExemptSales, a.Recoverable, a.NonRecoverable, a.Journal, a.IdemKey, a.Actor, a.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_vat_apportionments_year_period_months_period_no_key") {
		return a, false, apperr.New(apperr.KindConflict, "already_apportioned", "this tax period was apportioned already")
	}
	if err != nil {
		return a, false, err
	}
	if n == 1 {
		return a, true, nil
	}
	row, err := postgres.SelectOne[apportionRow](ctx, q, `SELECT `+apportionCols+` FROM mz_vat_apportionments WHERE idem_key = $1`, a.IdemKey)
	return row.domain(), false, err
}

func (r *Repository) SetApportionmentJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_vat_apportionments SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

func (r *Repository) Apportionments(ctx context.Context) ([]vat.Apportionment, error) {
	rows, err := postgres.Select[apportionRow](ctx, r.db.Querier(ctx), `SELECT `+apportionCols+` FROM mz_vat_apportionments ORDER BY year DESC, period_no DESC`)
	out := make([]vat.Apportionment, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}
