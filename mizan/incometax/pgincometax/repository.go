// Package pgincometax is the PostgreSQL adapter of incometax.Repository.
package pgincometax

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/incometax"
)

// Repository implements incometax.Repository.
type Repository struct{ db *postgres.DB }

var _ incometax.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) *Repository { return &Repository{db: db} }

type ruleRow struct {
	ID                    uuid.UUID `db:"id"`
	EffectiveFrom         time.Time `db:"effective_from"`
	RateBP                int       `db:"rate_bp"`
	CooperativeDiscountBP int       `db:"cooperative_discount_bp"`
	LossCarryYears        *int      `db:"loss_carry_years"`
	FilingMonths          int       `db:"filing_months"`
	Source                string    `db:"source"`
	CreatedAt             time.Time `db:"created_at"`
}

func (r *Repository) InsertRule(ctx context.Context, x incometax.Rule) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_it_rules (id, effective_from, rate_bp, cooperative_discount_bp, loss_carry_years, filing_months, source, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		x.ID, x.EffectiveFrom, x.RateBP, x.CooperativeDiscountBP, x.LossCarryYears, x.FilingMonths, x.Source, x.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_it_rules_effective_from_key") {
		return apperr.New(apperr.KindConflict, "rule_date_taken", "a version with this effective date already exists")
	}
	return err
}

func (r *Repository) Rules(ctx context.Context) ([]incometax.Rule, error) {
	rows, err := postgres.Select[ruleRow](ctx, r.db.Querier(ctx), `SELECT id, effective_from, rate_bp, cooperative_discount_bp, loss_carry_years, filing_months, source, created_at FROM mz_it_rules ORDER BY effective_from`)
	out := make([]incometax.Rule, len(rows))
	for i, x := range rows {
		out[i] = incometax.Rule{ID: x.ID, EffectiveFrom: x.EffectiveFrom, RateBP: x.RateBP, CooperativeDiscountBP: x.CooperativeDiscountBP, LossCarryYears: x.LossCarryYears,
			FilingMonths: x.FilingMonths, Source: x.Source, CreatedAt: x.CreatedAt}
	}
	return out, err
}

type assessRow struct {
	ID                uuid.UUID  `db:"id"`
	FiscalYear        string     `db:"fiscal_year"`
	YearFrom          time.Time  `db:"year_from"`
	YearTo            time.Time  `db:"year_to"`
	BookOn            *time.Time `db:"book_on"`
	Profit            int64      `db:"profit"`
	Adjustments       []byte     `db:"adjustments"`
	AddBackTotal      int64      `db:"add_back_total"`
	DeductionTotal    int64      `db:"deduction_total"`
	DepreciationAdj   int64      `db:"depreciation_adj"`
	TaxableBeforeLoss int64      `db:"taxable_before_loss"`
	LossUsed          int64      `db:"loss_used"`
	NewLoss           int64      `db:"new_loss"`
	Taxable           int64      `db:"taxable"`
	RateBP            int        `db:"rate_bp"`
	Tax               int64      `db:"tax"`
	Credits           int64      `db:"credits"`
	Payable           int64      `db:"payable"`
	Paid              int64      `db:"paid"`
	DueOn             time.Time  `db:"due_on"`
	RuleID            uuid.UUID  `db:"rule_id"`
	JournalID         *uuid.UUID `db:"journal_id"`
	IdemKey           string     `db:"idem_key"`
	Actor             string     `db:"actor"`
	CreatedAt         time.Time  `db:"created_at"`
}

const assessCols = `id, fiscal_year, year_from, year_to, book_on, profit, adjustments, add_back_total, deduction_total, depreciation_adj, taxable_before_loss, loss_used, new_loss, taxable, rate_bp, tax, credits, payable, paid, due_on, rule_id, journal_id, idem_key, actor, created_at`

func (x assessRow) domain() incometax.Assessment {
	var adj struct {
		AddBacks        []incometax.Item `json:"add_backs"`
		Deductions      []incometax.Item `json:"deductions"`
		DepreciationAdj int64            `json:"depreciation_adj"`
		Cooperative     bool             `json:"cooperative"`
		RateReductionBP int              `json:"rate_reduction_bp"`
		Credits         int64            `json:"credits"`
	}
	_ = json.Unmarshal(x.Adjustments, &adj)
	a := incometax.Assessment{ID: x.ID, FiscalYear: x.FiscalYear, From: x.YearFrom, To: x.YearTo, RuleID: x.RuleID, Credits: x.Credits, Payable: x.Payable, Paid: x.Paid, DueOn: x.DueOn,
		Journal: x.JournalID, IdemKey: x.IdemKey, Actor: x.Actor, CreatedAt: x.CreatedAt}
	a.Facts = incometax.Facts{FiscalYear: x.FiscalYear, From: x.YearFrom, To: x.YearTo, Profit: x.Profit}
	if x.BookOn != nil {
		a.Facts.BookOn = *x.BookOn
	}
	a.Adj = incometax.Adjustments{AddBacks: adj.AddBacks, Deductions: adj.Deductions, DepreciationAdj: adj.DepreciationAdj, Cooperative: adj.Cooperative, RateReductionBP: adj.RateReductionBP, Credits: adj.Credits}
	a.Result = incometax.Result{Profit: x.Profit, AddBackTotal: x.AddBackTotal, DeductionTotal: x.DeductionTotal, DepreciationAdj: x.DepreciationAdj, TaxableBeforeLoss: x.TaxableBeforeLoss,
		LossUsedTotal: x.LossUsed, NewLoss: x.NewLoss, Taxable: x.Taxable, RateBP: x.RateBP, Tax: x.Tax}
	return a
}

func (r *Repository) InsertAssessment(ctx context.Context, a incometax.Assessment) (incometax.Assessment, bool, error) {
	q := r.db.Querier(ctx)
	adj, err := incometax.AdjustmentsJSON(a.Adj)
	if err != nil {
		return a, false, err
	}
	var bookOn *time.Time
	if !a.Facts.BookOn.IsZero() {
		bookOn = &a.Facts.BookOn
	}
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_it_assessments (`+assessCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25) ON CONFLICT (idem_key) DO NOTHING`,
		a.ID, a.FiscalYear, a.From, a.To, bookOn, a.Result.Profit, adj, a.Result.AddBackTotal, a.Result.DeductionTotal, a.Result.DepreciationAdj, a.Result.TaxableBeforeLoss,
		a.Result.LossUsedTotal, a.Result.NewLoss, a.Result.Taxable, a.Result.RateBP, a.Result.Tax, a.Credits, a.Payable, a.Paid, a.DueOn, a.RuleID, a.Journal, a.IdemKey, a.Actor, a.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_it_assessments_fiscal_year_key") {
		return a, false, apperr.New(apperr.KindConflict, "already_declared", "the income tax of this fiscal year was declared already")
	}
	if err != nil {
		return a, false, err
	}
	if n == 1 {
		return a, true, nil
	}
	row, err := postgres.SelectOne[assessRow](ctx, q, `SELECT `+assessCols+` FROM mz_it_assessments WHERE idem_key = $1`, a.IdemKey)
	return row.domain(), false, err
}

func (r *Repository) Assessment(ctx context.Context, id uuid.UUID, forUpdate bool) (incometax.Assessment, error) {
	q := `SELECT ` + assessCols + ` FROM mz_it_assessments WHERE id = $1`
	if forUpdate {
		q += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[assessRow](ctx, r.db.Querier(ctx), q, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return incometax.Assessment{}, apperr.NotFound("assessment")
	}
	return x.domain(), err
}

func (r *Repository) Assessments(ctx context.Context) ([]incometax.Assessment, error) {
	rows, err := postgres.Select[assessRow](ctx, r.db.Querier(ctx), `SELECT `+assessCols+` FROM mz_it_assessments ORDER BY year_from DESC`)
	out := make([]incometax.Assessment, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, err
}

func (r *Repository) SetJournal(ctx context.Context, id, journal uuid.UUID) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_it_assessments SET journal_id = $2 WHERE id = $1`, id, journal)
	return err
}

type lossRow struct {
	ID         uuid.UUID `db:"id"`
	FiscalYear string    `db:"fiscal_year"`
	YearFrom   time.Time `db:"year_from"`
	Remaining  int64     `db:"remaining"`
}

func (r *Repository) OpenLosses(ctx context.Context, before time.Time) ([]incometax.Loss, error) {
	rows, err := postgres.Select[lossRow](ctx, r.db.Querier(ctx), `SELECT a.id, a.fiscal_year, a.year_from, a.new_loss - COALESCE((SELECT SUM(u.amount) FROM mz_it_loss_uses u WHERE u.loss_id = a.id), 0) AS remaining
		FROM mz_it_assessments a WHERE a.new_loss > 0 AND a.year_from < $1 ORDER BY a.year_from`, before)
	var out []incometax.Loss
	for _, x := range rows {
		if x.Remaining > 0 {
			out = append(out, incometax.Loss{AssessmentID: x.ID, FiscalYear: x.FiscalYear, YearStart: x.YearFrom, Remaining: x.Remaining})
		}
	}
	return out, err
}

func (r *Repository) AddLossUses(ctx context.Context, assessment uuid.UUID, uses []incometax.LossUse) error {
	for _, u := range uses {
		if _, err := postgres.Exec(ctx, r.db.Querier(ctx), `INSERT INTO mz_it_loss_uses (loss_id, used_by, amount) VALUES ($1,$2,$3)`, u.AssessmentID, assessment, u.Amount); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) AddPayment(ctx context.Context, id uuid.UUID, key string, amount int64, journal uuid.UUID, date time.Time) (bool, error) {
	q := r.db.Querier(ctx)
	n, err := postgres.Exec(ctx, q, `INSERT INTO mz_it_payments (idem_key, assessment_id, amount, journal_id, paid_on) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (idem_key) DO NOTHING`, key, id, amount, journal, date)
	if err != nil || n == 0 {
		return false, err
	}
	_, err = postgres.Exec(ctx, q, `UPDATE mz_it_assessments SET paid = paid + $2 WHERE id = $1`, id, amount)
	return true, err
}

func (r *Repository) HasPayment(ctx context.Context, id uuid.UUID, key string) (bool, error) {
	rows, err := postgres.Select[struct {
		Key string `db:"idem_key"`
	}](ctx, r.db.Querier(ctx), `SELECT idem_key FROM mz_it_payments WHERE idem_key = $1 AND assessment_id = $2`, key, id)
	return len(rows) > 0, err
}

func (r *Repository) LockAssessments(ctx context.Context) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `SELECT pg_advisory_xact_lock(hashtextextended('mizan:incometax', 0))`)
	return err
}
