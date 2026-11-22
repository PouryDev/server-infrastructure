// Package pgfixedasset is the PostgreSQL adapter of fixedasset.Repository.
package pgfixedasset

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Repository implements fixedasset.Repository.
type Repository struct{ db *postgres.DB }

var _ fixedasset.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

func amount(v int64, c string) money.Money { m, _ := money.New(v, money.Currency(c)); return m }

// ---- categories ----

type categoryRow struct {
	ID            uuid.UUID `db:"id"`
	Code          string    `db:"code"`
	Name          string    `db:"name"`
	Method        string    `db:"method"`
	LifeMonths    int       `db:"life_months"`
	RateBP        int       `db:"rate_bp"`
	SalvageBP     int       `db:"salvage_bp"`
	Source        string    `db:"source"`
	CostAccount   string    `db:"cost_account"`
	AccDepAccount string    `db:"accdep_account"`
	Active        bool      `db:"active"`
	CreatedAt     time.Time `db:"created_at"`
	IdleExempt    bool      `db:"idle_exempt"`
}

const categoryCols = `id, code, name, method, life_months, rate_bp, salvage_bp, source, cost_account, accdep_account, active, created_at, idle_exempt`

func (x categoryRow) domain() fixedasset.Category {
	return fixedasset.Category{ID: x.ID, Code: x.Code, Name: x.Name, Terms: fixedasset.Terms{Method: fixedasset.Method(x.Method), LifeMonths: x.LifeMonths, RateBP: x.RateBP},
		SalvageBP: x.SalvageBP, Source: x.Source, CostAccount: x.CostAccount, AccDepAccount: x.AccDepAccount, Active: x.Active, CreatedAt: x.CreatedAt, IdleExempt: x.IdleExempt}
}

func (r *Repository) InsertCategory(ctx context.Context, c fixedasset.Category) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_fa_categories (`+categoryCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		c.ID, c.Code, c.Name, string(c.Method), c.LifeMonths, c.RateBP, c.SalvageBP, c.Source, c.CostAccount, c.AccDepAccount, c.Active, c.CreatedAt, c.IdleExempt)
	if postgres.IsUniqueViolation(err, "mz_fa_categories_code_key") {
		return apperr.New(apperr.KindConflict, "category_exists", "a category with this code already exists")
	}
	return err
}

func (r *Repository) Category(ctx context.Context, id uuid.UUID) (fixedasset.Category, error) {
	x, err := postgres.SelectOne[categoryRow](ctx, r.db.Querier(ctx), `SELECT `+categoryCols+` FROM mz_fa_categories WHERE id = $1`, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return fixedasset.Category{}, apperr.NotFound("category")
	}
	return x.domain(), err
}

func (r *Repository) Categories(ctx context.Context) ([]fixedasset.Category, error) {
	rows, err := postgres.Select[categoryRow](ctx, r.db.Querier(ctx), `SELECT `+categoryCols+` FROM mz_fa_categories ORDER BY code`)
	out := make([]fixedasset.Category, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) SetCategoryActive(ctx context.Context, id uuid.UUID, active bool) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_fa_categories SET active = $2 WHERE id = $1`, id, active)
	if err == nil && n == 0 {
		return apperr.NotFound("category")
	}
	return err
}

// ---- assets ----

type assetRow struct {
	ID                   uuid.UUID  `db:"id"`
	Code                 string     `db:"code"`
	Name                 string     `db:"name"`
	CategoryID           uuid.UUID  `db:"category_id"`
	AcquiredOn           time.Time  `db:"acquired_on"`
	InServiceOn          time.Time  `db:"in_service_on"`
	Currency             string     `db:"currency"`
	Cost                 int64      `db:"cost"`
	Salvage              int64      `db:"salvage"`
	Method               string     `db:"method"`
	LifeMonths           int        `db:"life_months"`
	RateBP               int        `db:"rate_bp"`
	CostAccount          string     `db:"cost_account"`
	AccDepAccount        string     `db:"accdep_account"`
	StartIndex           int        `db:"start_index"`
	Accumulated          int64      `db:"accumulated"`
	MonthsDone           int        `db:"months_done"`
	ThroughIndex         int        `db:"through_index"`
	DepYear              int        `db:"dep_year"`
	YearStartAccumulated int64      `db:"year_start_accumulated"`
	MonthsInYear         int        `db:"months_in_year"`
	Status               string     `db:"status"`
	DisposedOn           *time.Time `db:"disposed_on"`
	AcquisitionJournal   *uuid.UUID `db:"acquisition_journal"`
	DisposalJournal      *uuid.UUID `db:"disposal_journal"`
	DisposalKey          string     `db:"disposal_key"`
	IdemKey              string     `db:"idem_key"`
	CreatedAt            time.Time  `db:"created_at"`
	IdleExempt           bool       `db:"idle_exempt"`
	ProgressMilli        int        `db:"progress_milli"`
}

const assetCols = `id, code, name, category_id, acquired_on, in_service_on, currency, cost, salvage, method, life_months, rate_bp, cost_account, accdep_account,
	start_index, accumulated, months_done, through_index, dep_year, year_start_accumulated, months_in_year, status, disposed_on, acquisition_journal,
	disposal_journal, disposal_key, idem_key, created_at, idle_exempt, progress_milli`

func (x assetRow) domain() fixedasset.Asset {
	return fixedasset.Asset{ID: x.ID, Code: x.Code, Name: x.Name, CategoryID: x.CategoryID, AcquiredOn: x.AcquiredOn, InServiceOn: x.InServiceOn,
		Cost: amount(x.Cost, x.Currency), Salvage: amount(x.Salvage, x.Currency),
		Terms:       fixedasset.Terms{Method: fixedasset.Method(x.Method), LifeMonths: x.LifeMonths, RateBP: x.RateBP},
		CostAccount: x.CostAccount, AccDepAccount: x.AccDepAccount, StartIndex: x.StartIndex, Accumulated: amount(x.Accumulated, x.Currency),
		MonthsDone: x.MonthsDone, ThroughIndex: x.ThroughIndex, DepYear: x.DepYear, YearStartAccumulated: amount(x.YearStartAccumulated, x.Currency),
		MonthsInYear: x.MonthsInYear, Status: fixedasset.Status(x.Status), DisposedOn: x.DisposedOn, AcquisitionJournal: x.AcquisitionJournal,
		DisposalJournal: x.DisposalJournal, DisposalKey: x.DisposalKey, IdemKey: x.IdemKey, CreatedAt: x.CreatedAt, IdleExempt: x.IdleExempt, ProgressMilli: x.ProgressMilli}
}

func (r *Repository) InsertAsset(ctx context.Context, a fixedasset.Asset) (fixedasset.Asset, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_fa_assets (`+assetCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
		ON CONFLICT (idem_key) DO NOTHING`,
		a.ID, a.Code, a.Name, a.CategoryID, a.AcquiredOn, a.InServiceOn, string(a.Cost.Currency()), a.Cost.Amount(), a.Salvage.Amount(), string(a.Method), a.LifeMonths, a.RateBP,
		a.CostAccount, a.AccDepAccount, a.StartIndex, a.Accumulated.Amount(), a.MonthsDone, a.ThroughIndex, a.DepYear, a.YearStartAccumulated.Amount(), a.MonthsInYear,
		string(a.Status), a.DisposedOn, a.AcquisitionJournal, a.DisposalJournal, a.DisposalKey, a.IdemKey, a.CreatedAt, a.IdleExempt, a.ProgressMilli)
	if postgres.IsUniqueViolation(err, "mz_fa_assets_code_key") {
		return a, false, apperr.New(apperr.KindConflict, "asset_exists", "an asset with this code already exists")
	}
	if err != nil {
		return a, false, err
	}
	if n == 1 {
		return a, true, nil
	}
	x, err := postgres.SelectOne[assetRow](ctx, q, `SELECT `+assetCols+` FROM mz_fa_assets WHERE idem_key = $1`, a.IdemKey)
	return x.domain(), false, err
}

func (r *Repository) Asset(ctx context.Context, id uuid.UUID, forUpdate bool) (fixedasset.Asset, error) {
	sql := `SELECT ` + assetCols + ` FROM mz_fa_assets WHERE id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[assetRow](ctx, r.db.Querier(ctx), sql, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return fixedasset.Asset{}, apperr.NotFound("asset")
	}
	return x.domain(), err
}

func (r *Repository) Assets(ctx context.Context, status fixedasset.Status) ([]fixedasset.Asset, error) {
	rows, err := postgres.Select[assetRow](ctx, r.db.Querier(ctx), `SELECT `+assetCols+` FROM mz_fa_assets WHERE ($1 = '' OR status = $1) ORDER BY code`, string(status))
	out := make([]fixedasset.Asset, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) UpdateAsset(ctx context.Context, a *fixedasset.Asset) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_fa_assets SET accumulated = $2, months_done = $3, through_index = $4, dep_year = $5, year_start_accumulated = $6, months_in_year = $7, status = $8,
		       disposed_on = $9, acquisition_journal = $10, disposal_journal = $11, disposal_key = $12, progress_milli = $13 WHERE id = $1`,
		a.ID, a.Accumulated.Amount(), a.MonthsDone, a.ThroughIndex, a.DepYear, a.YearStartAccumulated.Amount(), a.MonthsInYear, string(a.Status), a.DisposedOn,
		a.AcquisitionJournal, a.DisposalJournal, a.DisposalKey, a.ProgressMilli)
	return err
}

func (r *Repository) DepreciableAssets(ctx context.Context) ([]fixedasset.Asset, error) {
	rows, err := postgres.Select[assetRow](ctx, r.db.Querier(ctx),
		`SELECT `+assetCols+` FROM mz_fa_assets WHERE status = 'active' AND accumulated < cost - salvage ORDER BY id FOR UPDATE`)
	out := make([]fixedasset.Asset, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

// ---- runs ----

func (r *Repository) InsertItems(ctx context.Context, period, seq int, items []fixedasset.Item) error {
	for _, it := range items {
		if _, err := postgres.Exec(ctx, r.db.Querier(ctx),
			`INSERT INTO mz_fa_depreciations (asset_id, period_index, run_seq, amount, currency, idle, progress_milli) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			it.AssetID, period, seq, it.Amount.Amount(), string(it.Amount.Currency()), it.Idle, it.ProgressMilli); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) InsertRun(ctx context.Context, run fixedasset.Run) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_fa_runs (period_index, seq, year, month, journal_id, total, currency, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		run.PeriodIndex, run.Seq, run.Year, run.Month, run.Journal, run.Total.Amount(), string(run.Total.Currency()), run.CreatedAt)
	return err
}

type runRow struct {
	PeriodIndex int        `db:"period_index"`
	Seq         int        `db:"seq"`
	Year        int        `db:"year"`
	Month       int        `db:"month"`
	JournalID   *uuid.UUID `db:"journal_id"`
	Total       int64      `db:"total"`
	Currency    string     `db:"currency"`
	CreatedAt   time.Time  `db:"created_at"`
}

type itemRow struct {
	AssetID     uuid.UUID `db:"asset_id"`
	AssetCode   string    `db:"code"`
	PeriodIndex int       `db:"period_index"`
	Amount      int64     `db:"amount"`
	Currency    string    `db:"currency"`
	Idle        bool      `db:"idle"`
	Progress    int       `db:"progress_milli"`
}

func (x itemRow) domain() fixedasset.Item {
	return fixedasset.Item{AssetID: x.AssetID, AssetCode: x.AssetCode, PeriodIndex: x.PeriodIndex, Amount: amount(x.Amount, x.Currency), Idle: x.Idle, ProgressMilli: x.Progress}
}

func (r *Repository) LatestRun(ctx context.Context, period int) (fixedasset.Run, bool, error) {
	q := r.db.Querier(ctx)
	rows, err := postgres.Select[runRow](ctx, q, `SELECT period_index, seq, year, month, journal_id, total, currency, created_at FROM mz_fa_runs WHERE period_index = $1 AND voided_at IS NULL ORDER BY seq DESC LIMIT 1`, period)
	if err != nil || len(rows) == 0 {
		return fixedasset.Run{}, false, err
	}
	x := rows[0]
	items, err := postgres.Select[itemRow](ctx, q, `SELECT d.asset_id, a.code, d.period_index, d.amount, d.currency, d.idle, d.progress_milli FROM mz_fa_depreciations d JOIN mz_fa_assets a ON a.id = d.asset_id
		WHERE d.period_index = $1 AND d.run_seq = $2 ORDER BY a.code`, period, x.Seq)
	if err != nil {
		return fixedasset.Run{}, false, err
	}
	run := fixedasset.Run{PeriodIndex: x.PeriodIndex, Seq: x.Seq, Year: x.Year, Month: x.Month, Journal: x.JournalID, Total: amount(x.Total, x.Currency), CreatedAt: x.CreatedAt}
	for _, it := range items {
		run.Items = append(run.Items, it.domain())
	}
	return run, true, nil
}

func (r *Repository) RunCount(ctx context.Context, period int) (int, error) {
	return postgres.Value[int](ctx, r.db.Querier(ctx), `SELECT count(*) FROM mz_fa_runs WHERE period_index = $1`, period)
}

func (r *Repository) Depreciations(ctx context.Context, assetID uuid.UUID) ([]fixedasset.Item, error) {
	rows, err := postgres.Select[itemRow](ctx, r.db.Querier(ctx), `SELECT d.asset_id, a.code, d.period_index, d.amount, d.currency, d.idle, d.progress_milli FROM mz_fa_depreciations d JOIN mz_fa_assets a ON a.id = d.asset_id
		WHERE d.asset_id = $1 AND NOT d.voided ORDER BY d.period_index`, assetID)
	out := make([]fixedasset.Item, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) MarkRunVoided(ctx context.Context, period, seq int, journal uuid.UUID, at time.Time) error {
	q := r.db.Querier(ctx)
	if _, err := postgres.Exec(ctx, q, `UPDATE mz_fa_runs SET voided_journal = $3, voided_at = $4 WHERE period_index = $1 AND seq = $2`, period, seq, journal, at); err != nil {
		return err
	}
	_, err := postgres.Exec(ctx, q, `UPDATE mz_fa_depreciations SET voided = true WHERE period_index = $1 AND run_seq = $2`, period, seq)
	return err
}

func (r *Repository) SetUsage(ctx context.Context, assetID uuid.UUID, period int, units int64, actor string) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_fa_usage (asset_id, period_index, units, actor) VALUES ($1,$2,$3,$4)
		ON CONFLICT (asset_id, period_index) DO UPDATE SET units = EXCLUDED.units, actor = EXCLUDED.actor`, assetID, period, units, actor)
	return err
}

func (r *Repository) UnitsUpTo(ctx context.Context, assetID uuid.UUID, period int) (int64, error) {
	return postgres.Value[int64](ctx, r.db.Querier(ctx), `SELECT COALESCE(SUM(units), 0)::bigint FROM mz_fa_usage WHERE asset_id = $1 AND period_index <= $2`, assetID, period)
}

func (r *Repository) InsertPartial(ctx context.Context, key string, assetID uuid.UUID, bp int, journal uuid.UUID) (uuid.UUID, bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_fa_partials (idem_key, asset_id, portion_bp, journal_id) VALUES ($1,$2,$3,$4) ON CONFLICT (idem_key) DO NOTHING`, key, assetID, bp, journal)
	if err != nil {
		return uuid.Nil, false, err
	}
	if n == 1 {
		return assetID, true, nil
	}
	id, err := postgres.Value[uuid.UUID](ctx, q, `SELECT asset_id FROM mz_fa_partials WHERE idem_key = $1`, key)
	return id, false, err
}

type thresholdRow struct {
	ID            uuid.UUID `db:"id"`
	EffectiveFrom time.Time `db:"effective_from"`
	SmallLimit    int64     `db:"small_limit"`
	Verified      bool      `db:"verified"`
	Source        string    `db:"source"`
	CreatedAt     time.Time `db:"created_at"`
}

func (r *Repository) InsertThreshold(ctx context.Context, t fixedasset.Threshold) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_fa_thresholds (id, effective_from, small_limit, verified, source, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.EffectiveFrom, t.SmallLimit, t.Verified, t.Source, t.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_fa_thresholds_effective_from_key") {
		return apperr.New(apperr.KindConflict, "threshold_date_taken", "a limit with this effective date already exists")
	}
	return err
}

func (r *Repository) Thresholds(ctx context.Context) ([]fixedasset.Threshold, error) {
	rows, err := postgres.Select[thresholdRow](ctx, r.db.Querier(ctx), `SELECT id, effective_from, small_limit, verified, source, created_at FROM mz_fa_thresholds ORDER BY effective_from`)
	out := make([]fixedasset.Threshold, len(rows))
	for i, x := range rows {
		out[i] = fixedasset.Threshold{ID: x.ID, EffectiveFrom: x.EffectiveFrom, SmallLimit: x.SmallLimit, Verified: x.Verified, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

func (r *Repository) ReduceAsset(ctx context.Context, a *fixedasset.Asset) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_fa_assets SET cost = $2, salvage = $3, accumulated = $4, year_start_accumulated = $5 WHERE id = $1`,
		a.ID, a.Cost.Amount(), a.Salvage.Amount(), a.Accumulated.Amount(), a.YearStartAccumulated.Amount())
	return err
}

func (r *Repository) PartialByKey(ctx context.Context, key string) (uuid.UUID, bool, error) {
	rows, err := postgres.Select[struct {
		ID uuid.UUID `db:"asset_id"`
	}](ctx, r.db.Querier(ctx), `SELECT asset_id FROM mz_fa_partials WHERE idem_key = $1`, key)
	if err != nil || len(rows) == 0 {
		return uuid.Nil, false, err
	}
	return rows[0].ID, true, nil
}

type spanRow struct {
	ID        uuid.UUID `db:"id"`
	AssetID   uuid.UUID `db:"asset_id"`
	From      int       `db:"from_index"`
	To        int       `db:"to_index"`
	Note      string    `db:"note"`
	CreatedAt time.Time `db:"created_at"`
}

func (r *Repository) InsertIdleSpan(ctx context.Context, sp fixedasset.IdleSpan) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_fa_idle_spans (id, asset_id, from_index, to_index, note, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		sp.ID, sp.AssetID, sp.From, sp.To, sp.Note, sp.CreatedAt)
	return err
}

func (r *Repository) IdleSpans(ctx context.Context, assetID uuid.UUID) ([]fixedasset.IdleSpan, error) {
	rows, err := postgres.Select[spanRow](ctx, r.db.Querier(ctx), `SELECT id, asset_id, from_index, to_index, note, created_at FROM mz_fa_idle_spans WHERE asset_id = $1 ORDER BY from_index`, assetID)
	out := make([]fixedasset.IdleSpan, len(rows))
	for i, x := range rows {
		out[i] = fixedasset.IdleSpan{ID: x.ID, AssetID: x.AssetID, From: x.From, To: x.To, Note: x.Note, CreatedAt: x.CreatedAt}
	}
	return out, err
}

func (r *Repository) DeleteIdleSpan(ctx context.Context, id uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `DELETE FROM mz_fa_idle_spans WHERE id = $1`, id)
	return err
}
