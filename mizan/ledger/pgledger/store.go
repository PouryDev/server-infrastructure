// Package pgledger is the PostgreSQL adapter of ledger.Store. Statements are written by hand: the ledger
// needs row locks, window-free keyset paging and immutable tables, which an ORM would only hide.
package pgledger

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Store implements ledger.Store.
type Store struct {
	db      *postgres.DB
	journal *query.Schema
}

var _ ledger.Store = (*Store)(nil)

// New creates the store.
func New(db *postgres.DB) (*Store, error) {
	schema, err := query.NewSchemaFor[journalRow]("j",
		query.WithDefaultSort(query.Sort{Field: "id", Desc: true}),
		query.WithTieBreaker("j.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Store{db: db, journal: schema}, nil
}

type accountRow struct {
	ID          uuid.UUID `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Type        string    `db:"type"`
	Currency    string    `db:"currency"`
	NonNegative bool      `db:"non_negative"`
	CodingCode  string    `db:"coding_code"`
	OwnerKind   string    `db:"owner_kind"`
	OwnerID     string    `db:"owner_id"`
	Active      bool      `db:"active"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r accountRow) domain() ledger.Account {
	return ledger.Account{
		ID: r.ID, Code: r.Code, Name: r.Name, Type: ledger.AccountType(r.Type), Currency: money.Currency(r.Currency),
		NonNegative: r.NonNegative, CodingCode: r.CodingCode, Owner: ledger.Owner{Kind: r.OwnerKind, ID: r.OwnerID},
		Active: r.Active, CreatedAt: r.CreatedAt,
	}
}

const accountCols = `id, code, name, type, currency, non_negative, COALESCE(coding_code, '') AS coding_code, owner_kind, owner_id, active, created_at`

func (s *Store) CreateAccountIfMissing(ctx context.Context, sp ledger.AccountSpec) error {
	_, err := postgres.Exec(ctx, s.db.Querier(ctx), `
		INSERT INTO mz_accounts (code, name, type, currency, non_negative, coding_code, owner_kind, owner_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8) ON CONFLICT (code) DO NOTHING`,
		sp.Code, sp.Name, string(sp.Type), string(sp.Currency), sp.NonNegative, sp.CodingCode, sp.Owner.Kind, sp.Owner.ID)
	return err
}

func (s *Store) AccountsByCode(ctx context.Context, codes []string) (map[string]ledger.Account, error) {
	rows, err := postgres.Select[accountRow](ctx, s.db.Querier(ctx), `SELECT `+accountCols+` FROM mz_accounts WHERE code = ANY($1)`, codes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ledger.Account, len(rows))
	for _, r := range rows {
		out[r.Code] = r.domain()
	}
	return out, nil
}

func (s *Store) SetAccountCoding(ctx context.Context, code, coding string) error {
	n, err := postgres.Exec(ctx, s.db.Querier(ctx), `UPDATE mz_accounts SET coding_code = NULLIF($2, '') WHERE code = $1`, code, coding)
	if err != nil {
		if apperr.Is(err, apperr.KindConflict) {
			return apperr.Invalid("unknown coding code " + coding)
		}
		return err
	}
	if n == 0 {
		return apperr.NotFound("account")
	}
	return nil
}

func (s *Store) SetAccountActive(ctx context.Context, code string, active bool) error {
	n, err := postgres.Exec(ctx, s.db.Querier(ctx), `UPDATE mz_accounts SET active = $2 WHERE code = $1`, code, active)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("account")
	}
	return nil
}

func (s *Store) SetCodingForOwnerKind(ctx context.Context, kind, coding string) error {
	_, err := postgres.Exec(ctx, s.db.Querier(ctx),
		`UPDATE mz_accounts SET coding_code = $2 WHERE owner_kind = $1 AND coding_code IS NULL`, kind, coding)
	return err
}

func (s *Store) HasCoding(ctx context.Context, coding string) (bool, error) {
	return postgres.Value[bool](ctx, s.db.Querier(ctx), `SELECT EXISTS (SELECT 1 FROM mz_coding WHERE code = $1)`, coding)
}

func (s *Store) ImportCoding(ctx context.Context, nodes []ledger.CodingNode) error {
	sorted := slices.Clone(nodes)
	slices.SortStableFunc(sorted, func(a, b ledger.CodingNode) int { return a.Level - b.Level })
	for _, n := range sorted { // parents first
		if _, err := postgres.Exec(ctx, s.db.Querier(ctx), `
			INSERT INTO mz_coding (code, parent_code, level, title, type_hint) VALUES ($1, NULLIF($2, ''), $3, $4, $5)
			ON CONFLICT (code) DO UPDATE SET parent_code = EXCLUDED.parent_code, level = EXCLUDED.level,
			    title = EXCLUDED.title, type_hint = EXCLUDED.type_hint`,
			n.Code, n.Parent, n.Level, n.Title, string(n.TypeHint)); err != nil {
			return err
		}
	}
	return nil
}

type codingRow struct {
	Code     string `db:"code"`
	Parent   string `db:"parent"`
	Level    int    `db:"level"`
	Title    string `db:"title"`
	TypeHint string `db:"type_hint"`
}

func (s *Store) Coding(ctx context.Context) ([]ledger.CodingNode, error) {
	rows, err := postgres.Select[codingRow](ctx, s.db.Querier(ctx),
		`SELECT code, COALESCE(parent_code, '') AS parent, level, title, type_hint FROM mz_coding ORDER BY code`)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.CodingNode, len(rows))
	for i, r := range rows {
		out[i] = ledger.CodingNode{Code: r.Code, Parent: r.Parent, Level: r.Level, Title: r.Title, TypeHint: ledger.AccountType(r.TypeHint)}
	}
	return out, nil
}

type fyRow struct {
	ID         int64     `db:"id"`
	Name       string    `db:"name"`
	StartsOn   time.Time `db:"starts_on"`
	EndsBefore time.Time `db:"ends_before"`
	Status     string    `db:"status"`
}

type periodRow struct {
	ID           int64     `db:"id"`
	FiscalYearID int64     `db:"fiscal_year_id"`
	Number       int       `db:"number"`
	StartsOn     time.Time `db:"starts_on"`
	EndsBefore   time.Time `db:"ends_before"`
	Status       string    `db:"status"`
}

func (r periodRow) domain() ledger.Period {
	return ledger.Period{ID: r.ID, FiscalYearID: r.FiscalYearID, Number: r.Number, StartsOn: r.StartsOn, EndsBefore: r.EndsBefore, Status: ledger.Status(r.Status)}
}

func (s *Store) CreateFiscalYear(ctx context.Context, fy ledger.FiscalYear) (ledger.FiscalYear, error) {
	q := s.db.Querier(ctx)
	overlap, err := postgres.Value[bool](ctx, q, `
		SELECT EXISTS (SELECT 1 FROM mz_fiscal_years WHERE starts_on < $2 AND $1 < ends_before AND name <> $3)`,
		fy.StartsOn, fy.EndsBefore, fy.Name)
	if err != nil {
		return fy, err
	}
	if overlap {
		return fy, apperr.Conflict("the fiscal year overlaps another one")
	}
	rows, err := postgres.Select[fyRow](ctx, q, `
		INSERT INTO mz_fiscal_years (name, starts_on, ends_before) VALUES ($1, $2, $3)
		ON CONFLICT (name) DO NOTHING RETURNING id, name, starts_on, ends_before, status`, fy.Name, fy.StartsOn, fy.EndsBefore)
	if err != nil {
		return fy, err
	}
	if len(rows) == 0 { // already there: return it as it is
		all, err := s.FiscalYears(ctx)
		if err != nil {
			return fy, err
		}
		for _, f := range all {
			if f.Name == fy.Name {
				return f, nil
			}
		}
		return fy, apperr.Internal(fmt.Errorf("fiscal year %s vanished", fy.Name))
	}
	fy.ID, fy.Status = rows[0].ID, ledger.Status(rows[0].Status)
	for i, p := range fy.Periods {
		pr, err := postgres.SelectOne[periodRow](ctx, q, `
			INSERT INTO mz_periods (fiscal_year_id, number, starts_on, ends_before) VALUES ($1, $2, $3, $4)
			RETURNING id, fiscal_year_id, number, starts_on, ends_before, status`, fy.ID, p.Number, p.StartsOn, p.EndsBefore)
		if err != nil {
			return fy, err
		}
		fy.Periods[i] = pr.domain()
	}
	return fy, nil
}

func (s *Store) FiscalYears(ctx context.Context) ([]ledger.FiscalYear, error) {
	q := s.db.Querier(ctx)
	years, err := postgres.Select[fyRow](ctx, q, `SELECT id, name, starts_on, ends_before, status FROM mz_fiscal_years ORDER BY starts_on`)
	if err != nil {
		return nil, err
	}
	periods, err := postgres.Select[periodRow](ctx, q,
		`SELECT id, fiscal_year_id, number, starts_on, ends_before, status FROM mz_periods ORDER BY fiscal_year_id, number`)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.FiscalYear, len(years))
	for i, y := range years {
		out[i] = ledger.FiscalYear{ID: y.ID, Name: y.Name, StartsOn: y.StartsOn, EndsBefore: y.EndsBefore, Status: ledger.Status(y.Status)}
		for _, p := range periods {
			if p.FiscalYearID == y.ID {
				out[i].Periods = append(out[i].Periods, p.domain())
			}
		}
	}
	return out, nil
}

func (s *Store) PeriodFor(ctx context.Context, date time.Time) (ledger.Period, error) {
	r, err := postgres.SelectOne[periodRow](ctx, s.db.Querier(ctx), `
		SELECT p.id, p.fiscal_year_id, p.number, p.starts_on, p.ends_before,
		       CASE WHEN fy.status = 'closed' THEN 'closed' ELSE p.status END AS status
		  FROM mz_periods p JOIN mz_fiscal_years fy ON fy.id = p.fiscal_year_id
		 WHERE p.starts_on <= $1 AND $1 < p.ends_before`, date)
	if err != nil {
		return ledger.Period{}, err
	}
	return r.domain(), nil
}

func (s *Store) SetPeriodStatus(ctx context.Context, id int64, st ledger.Status) error {
	n, err := postgres.Exec(ctx, s.db.Querier(ctx), `UPDATE mz_periods SET status = $2 WHERE id = $1`, id, string(st))
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("period")
	}
	return nil
}

func (s *Store) LockFiscalYear(ctx context.Context, id int64) (ledger.FiscalYear, error) {
	q := s.db.Querier(ctx)
	// NO KEY UPDATE does not conflict with the key-share lock a posting takes on the year through its foreign key.
	r, err := postgres.SelectOne[fyRow](ctx, q, `SELECT id, name, starts_on, ends_before, status FROM mz_fiscal_years WHERE id = $1 FOR NO KEY UPDATE`, id)
	if err != nil {
		return ledger.FiscalYear{}, err
	}
	seq := "journal:" + fmt.Sprint(id)
	if _, err := postgres.Exec(ctx, q, `INSERT INTO mz_sequences (name, last) VALUES ($1, 0) ON CONFLICT (name) DO NOTHING`, seq); err != nil {
		return ledger.FiscalYear{}, err
	}
	if _, err := postgres.Exec(ctx, q, `SELECT 1 FROM mz_sequences WHERE name = $1 FOR UPDATE`, seq); err != nil {
		return ledger.FiscalYear{}, err
	}
	return ledger.FiscalYear{ID: r.ID, Name: r.Name, StartsOn: r.StartsOn, EndsBefore: r.EndsBefore, Status: ledger.Status(r.Status)}, nil
}

func (s *Store) SetFiscalYearStatus(ctx context.Context, id int64, st ledger.Status) error {
	n, err := postgres.Exec(ctx, s.db.Querier(ctx), `UPDATE mz_fiscal_years SET status = $2 WHERE id = $1`, id, string(st))
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.NotFound("fiscal year")
	}
	return nil
}

type closingRow struct {
	FiscalYearID    int64  `db:"fiscal_year_id"`
	Generation      int    `db:"generation"`
	State           string `db:"state"`
	RetainedAccount string `db:"retained_account"`
	Actor           string `db:"actor"`
}

func (s *Store) YearClosing(ctx context.Context, fyID int64) (ledger.YearClosing, bool, error) {
	rows, err := postgres.Select[closingRow](ctx, s.db.Querier(ctx),
		`SELECT fiscal_year_id, generation, state, retained_account, actor FROM mz_year_closings WHERE fiscal_year_id = $1`, fyID)
	if err != nil || len(rows) == 0 {
		return ledger.YearClosing{}, false, err
	}
	r := rows[0]
	return ledger.YearClosing{FiscalYearID: r.FiscalYearID, Generation: r.Generation, State: ledger.ClosingState(r.State), RetainedAccount: r.RetainedAccount, Actor: r.Actor}, true, nil
}

func (s *Store) SaveYearClosing(ctx context.Context, c ledger.YearClosing) error {
	_, err := postgres.Exec(ctx, s.db.Querier(ctx), `
		INSERT INTO mz_year_closings (fiscal_year_id, generation, state, retained_account, actor) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (fiscal_year_id) DO UPDATE SET generation = EXCLUDED.generation, state = EXCLUDED.state,
		       retained_account = EXCLUDED.retained_account, actor = EXCLUDED.actor, updated_at = now()`,
		c.FiscalYearID, c.Generation, string(c.State), c.RetainedAccount, c.Actor)
	return err
}

func (s *Store) CountJournals(ctx context.Context, fyID int64, except []uuid.UUID) (int, error) {
	if except == nil {
		except = []uuid.UUID{}
	}
	return postgres.Value[int](ctx, s.db.Querier(ctx), `SELECT count(*) FROM mz_journals WHERE fiscal_year_id = $1 AND NOT (id = ANY($2))`, fyID, except)
}

type journalRow struct {
	ID           uuid.UUID  `db:"id" json:"id"`
	FiscalYearID int64      `db:"fiscal_year_id" json:"fiscal_year_id"`
	PeriodID     int64      `db:"period_id" json:"-" query:"-"`
	Number       int64      `db:"number" json:"number"`
	Kind         string     `db:"kind" json:"kind"`
	EntryDate    time.Time  `db:"entry_date" json:"date"`
	Description  string     `db:"description" json:"description"`
	Currency     string     `db:"currency" json:"currency"`
	Total        int64      `db:"total" json:"total"`
	RefType      string     `db:"ref_type" json:"ref_type"`
	RefID        string     `db:"ref_id" json:"ref_id"`
	IdemKey      string     `db:"idem_key" json:"-" query:"-"`
	Fingerprint  string     `db:"fingerprint" json:"-" query:"-"`
	ReversesID   *uuid.UUID `db:"reverses_id" json:"-" query:"-"`
	CreatedBy    string     `db:"created_by" json:"created_by"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
}

const journalCols = `j.id, j.fiscal_year_id, j.period_id, j.number, j.kind, j.entry_date, j.description, j.currency, j.total,
	j.ref_type, j.ref_id, j.idem_key, j.fingerprint, j.reverses_id, j.created_by, j.created_at`

type lineRow struct {
	JournalID   uuid.UUID `db:"journal_id"`
	LineNo      int       `db:"line_no"`
	AccountID   uuid.UUID `db:"account_id"`
	AccountCode string    `db:"account_code"`
	Debit       int64     `db:"debit"`
	Credit      int64     `db:"credit"`
	Memo        string    `db:"memo"`
}

func (s *Store) loadJournals(ctx context.Context, where string, args ...any) ([]ledger.Journal, error) {
	q := s.db.Querier(ctx)
	rows, err := postgres.Select[journalRow](ctx, q, `SELECT `+journalCols+` FROM mz_journals j WHERE `+where+` ORDER BY j.id`, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	lines, err := postgres.Select[lineRow](ctx, q, `
		SELECT l.journal_id, l.line_no, l.account_id, a.code AS account_code, l.debit, l.credit, l.memo
		  FROM mz_journal_lines l JOIN mz_accounts a ON a.id = l.account_id
		 WHERE l.journal_id = ANY($1) ORDER BY l.journal_id, l.line_no`, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.Journal, len(rows))
	for i, r := range rows {
		cur := money.Currency(r.Currency)
		total, _ := money.New(r.Total, cur)
		j := ledger.Journal{
			ID: r.ID, FiscalYearID: r.FiscalYearID, PeriodID: r.PeriodID, Number: r.Number, Kind: ledger.Kind(r.Kind),
			Date: r.EntryDate, Description: r.Description, Total: total, Ref: ledger.Ref{Type: r.RefType, ID: r.RefID},
			Key: r.IdemKey, Fingerprint: r.Fingerprint, ReversesID: r.ReversesID, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		}
		for _, l := range lines {
			if l.JournalID != r.ID {
				continue
			}
			side, amt := ledger.Debit, l.Debit
			if l.Credit > 0 {
				side, amt = ledger.Credit, l.Credit
			}
			a, _ := money.New(amt, cur)
			j.Lines = append(j.Lines, ledger.PostedLine{No: l.LineNo, AccountID: l.AccountID, AccountCode: l.AccountCode, Side: side, Amount: a, Memo: l.Memo})
		}
		out[i] = j
	}
	return out, nil
}

func (s *Store) one(ctx context.Context, where string, arg any) (ledger.Journal, error) {
	js, err := s.loadJournals(ctx, where, arg)
	if err != nil {
		return ledger.Journal{}, err
	}
	if len(js) == 0 {
		return ledger.Journal{}, apperr.NotFound("journal")
	}
	return js[0], nil
}

func (s *Store) JournalByKey(ctx context.Context, key string) (ledger.Journal, error) {
	return s.one(ctx, "j.idem_key = $1", key)
}

func (s *Store) JournalByID(ctx context.Context, id uuid.UUID) (ledger.Journal, error) {
	return s.one(ctx, "j.id = $1", id)
}

func (s *Store) JournalsByRef(ctx context.Context, ref ledger.Ref) ([]ledger.Journal, error) {
	return s.loadJournals(ctx, "j.ref_type = $1 AND j.ref_id = $2", ref.Type, ref.ID)
}

func (s *Store) NextNumber(ctx context.Context, seq string) (int64, error) {
	return postgres.Value[int64](ctx, s.db.Querier(ctx), `
		INSERT INTO mz_sequences (name, last) VALUES ($1, 1)
		ON CONFLICT (name) DO UPDATE SET last = mz_sequences.last + 1 RETURNING last`, seq)
}

func (s *Store) InsertJournal(ctx context.Context, j ledger.Journal) error {
	q := s.db.Querier(ctx)
	// ON CONFLICT DO NOTHING keeps the transaction usable after a duplicate (no aborted state).
	n, err := postgres.Exec(ctx, q, `
		INSERT INTO mz_journals (id, fiscal_year_id, period_id, number, kind, entry_date, description, currency, total,
		                         ref_type, ref_id, idem_key, fingerprint, reverses_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) ON CONFLICT DO NOTHING`,
		j.ID, j.FiscalYearID, j.PeriodID, j.Number, string(j.Kind), j.Date, j.Description, string(j.Total.Currency()), j.Total.Amount(),
		j.Ref.Type, j.Ref.ID, j.Key, j.Fingerprint, j.ReversesID, j.CreatedBy)
	if err != nil {
		return err
	}
	if n == 0 {
		used, err := postgres.Value[bool](ctx, q, `SELECT EXISTS (SELECT 1 FROM mz_journals WHERE idem_key = $1)`, j.Key)
		if err != nil {
			return err
		}
		if used {
			return ledger.ErrDuplicateKey
		}
		return ledger.ErrAlreadyReversed
	}
	var (
		sb   strings.Builder
		args []any
	)
	sb.WriteString(`INSERT INTO mz_journal_lines (journal_id, line_no, account_id, debit, credit, memo) VALUES `)
	for i, l := range j.Lines {
		if i > 0 {
			sb.WriteByte(',')
		}
		var d, c int64
		if l.Side == ledger.Debit {
			d = l.Amount.Amount()
		} else {
			c = l.Amount.Amount()
		}
		b := len(args)
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d)", b+1, b+2, b+3, b+4, b+5, b+6)
		args = append(args, j.ID, l.No, l.AccountID, d, c, l.Memo)
	}
	_, err = postgres.Exec(ctx, q, sb.String(), args...)
	return err
}

func (s *Store) ApplyBalance(ctx context.Context, id uuid.UUID, delta int64) (int64, error) {
	return postgres.Value[int64](ctx, s.db.Querier(ctx), `
		INSERT INTO mz_balances (account_id, balance) VALUES ($1, $2)
		ON CONFLICT (account_id) DO UPDATE SET balance = mz_balances.balance + EXCLUDED.balance, updated_at = now()
		RETURNING balance`, id, delta)
}

func (s *Store) TrackedBalance(ctx context.Context, id uuid.UUID) (int64, error) {
	v, err := postgres.Value[int64](ctx, s.db.Querier(ctx), `SELECT balance FROM mz_balances WHERE account_id = $1`, id)
	if apperr.Is(err, apperr.KindNotFound) {
		return 0, nil
	}
	return v, err
}

type tbRow struct {
	Code       string `db:"code"`
	Name       string `db:"name"`
	Type       string `db:"type"`
	CodingCode string `db:"coding_code"`
	Debit      int64  `db:"debit"`
	Credit     int64  `db:"credit"`
}

func dateArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Store) TrialBalance(ctx context.Context, q ledger.TrialBalanceQuery) ([]ledger.TrialBalanceRow, error) {
	having := "HAVING COALESCE(sum(l.debit), 0) + COALESCE(sum(l.credit), 0) > 0"
	if q.IncludeZero {
		having = ""
	}
	rows, err := postgres.Select[tbRow](ctx, s.db.Querier(ctx), `
		SELECT a.code, a.name, a.type, COALESCE(a.coding_code, '') AS coding_code,
		       COALESCE(sum(l.debit), 0)::bigint AS debit, COALESCE(sum(l.credit), 0)::bigint AS credit
		  FROM mz_accounts a
		  LEFT JOIN (SELECT l.account_id, l.debit, l.credit
		               FROM mz_journal_lines l JOIN mz_journals j ON j.id = l.journal_id
		              WHERE ($1::date IS NULL OR j.entry_date >= $1) AND ($2::date IS NULL OR j.entry_date <= $2)
		                AND ($3::boolean = false OR (j.kind <> 'closing' AND NOT EXISTS (
		                      SELECT 1 FROM mz_journals o WHERE o.id = j.reverses_id AND o.kind = 'closing')))
		                AND ($4::bigint = 0 OR NOT ((j.kind = 'closing' AND j.fiscal_year_id = $4) OR EXISTS (
		                      SELECT 1 FROM mz_journals o WHERE o.id = j.reverses_id AND o.kind = 'closing' AND o.fiscal_year_id = $4)))) l
		         ON l.account_id = a.id
		 GROUP BY a.id `+having+` ORDER BY a.code`, dateArg(q.From), dateArg(q.To), q.ExcludeClosing, q.ExcludeClosingYear)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.TrialBalanceRow, len(rows))
	for i, r := range rows {
		out[i] = ledger.TrialBalanceRow{Code: r.Code, Name: r.Name, Type: ledger.AccountType(r.Type), CodingCode: r.CodingCode, Debit: r.Debit, Credit: r.Credit}
	}
	return out, nil
}

type stmtRow struct {
	LineID      int64     `db:"line_id"`
	JournalID   uuid.UUID `db:"journal_id"`
	Number      int64     `db:"number"`
	FiscalYear  string    `db:"fiscal_year"`
	EntryDate   time.Time `db:"entry_date"`
	Description string    `db:"description"`
	Memo        string    `db:"memo"`
	RefType     string    `db:"ref_type"`
	RefID       string    `db:"ref_id"`
	Debit       int64     `db:"debit"`
	Credit      int64     `db:"credit"`
}

type sums struct {
	Debit  int64 `db:"d"`
	Credit int64 `db:"c"`
}

func (s *Store) Statement(ctx context.Context, a ledger.Account, q ledger.StatementQuery) (ledger.Statement, error) {
	db := s.db.Querier(ctx)
	from, to := dateArg(q.From), dateArg(q.To)
	opening, err := postgres.SelectOne[sums](ctx, db, `
		SELECT COALESCE(sum(l.debit), 0)::bigint AS d, COALESCE(sum(l.credit), 0)::bigint AS c
		  FROM mz_journal_lines l JOIN mz_journals j ON j.id = l.journal_id
		 WHERE l.account_id = $1 AND $2::date IS NOT NULL AND j.entry_date < $2`, a.ID, from)
	if err != nil {
		return ledger.Statement{}, err
	}
	skipped := sums{}
	if q.AfterLineID > 0 {
		if skipped, err = postgres.SelectOne[sums](ctx, db, `
			SELECT COALESCE(sum(l.debit), 0)::bigint AS d, COALESCE(sum(l.credit), 0)::bigint AS c
			  FROM mz_journal_lines l JOIN mz_journals j ON j.id = l.journal_id
			 WHERE l.account_id = $1 AND l.id <= $4 AND ($2::date IS NULL OR j.entry_date >= $2) AND ($3::date IS NULL OR j.entry_date <= $3)`,
			a.ID, from, to, q.AfterLineID); err != nil {
			return ledger.Statement{}, err
		}
	}
	rows, err := postgres.Select[stmtRow](ctx, db, `
		SELECT l.id AS line_id, l.journal_id, j.number, fy.name AS fiscal_year, j.entry_date, j.description, l.memo,
		       j.ref_type, j.ref_id, l.debit, l.credit
		  FROM mz_journal_lines l JOIN mz_journals j ON j.id = l.journal_id JOIN mz_fiscal_years fy ON fy.id = j.fiscal_year_id
		 WHERE l.account_id = $1 AND l.id > $4 AND ($2::date IS NULL OR j.entry_date >= $2) AND ($3::date IS NULL OR j.entry_date <= $3)
		 ORDER BY l.id LIMIT $5`, a.ID, from, to, q.AfterLineID, q.Limit+1)
	if err != nil {
		return ledger.Statement{}, err
	}
	st := ledger.Statement{Account: a, Opening: ledger.Natural(a.Type, opening.Debit, opening.Credit)}
	if len(rows) > q.Limit {
		rows = rows[:q.Limit]
		st.NextAfter = rows[len(rows)-1].LineID
	}
	run := ledger.Natural(a.Type, opening.Debit+skipped.Debit, opening.Credit+skipped.Credit)
	for _, r := range rows {
		run += ledger.Natural(a.Type, r.Debit, r.Credit)
		st.Rows = append(st.Rows, ledger.StatementRow{
			LineID: r.LineID, JournalID: r.JournalID, Number: r.Number, FiscalYear: r.FiscalYear, Date: r.EntryDate,
			Description: r.Description, Memo: r.Memo, Ref: ledger.Ref{Type: r.RefType, ID: r.RefID},
			Debit: r.Debit, Credit: r.Credit, Running: run,
		})
	}
	return st, nil
}

func (s *Store) ListJournals(ctx context.Context, p query.Params) (query.Page[ledger.JournalSummary], error) {
	page, err := postgres.Paginate[journalRow](ctx, s.db.Querier(ctx), s.journal,
		postgres.ListQuery{Select: "SELECT " + journalCols, From: "FROM mz_journals j"}, p)
	if err != nil {
		return query.Page[ledger.JournalSummary]{}, err
	}
	return query.MapPage(page, func(r journalRow) ledger.JournalSummary {
		return ledger.JournalSummary{
			ID: r.ID, FiscalYearID: r.FiscalYearID, Number: r.Number, Kind: ledger.Kind(r.Kind), Date: r.EntryDate,
			Description: r.Description, Currency: r.Currency, Total: r.Total, RefType: r.RefType, RefID: r.RefID,
			CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		}
	}), nil
}
