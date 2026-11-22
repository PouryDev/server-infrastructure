// Package pgtreasury is the PostgreSQL adapter of treasury.Repository.
package pgtreasury

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/treasury"
)

// Repository implements treasury.Repository.
type Repository struct {
	db                                  *postgres.DB
	chequeSchema, lineSchema, setSchema *query.Schema
}

var _ treasury.Repository = (*Repository)(nil)

// New creates the repository.
func New(db *postgres.DB) (*Repository, error) {
	cs, err := query.NewSchemaFor[chequeRow]("c", query.WithDefaultSort(query.Sort{Field: "due_date"}), query.WithTieBreaker("c.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	ls, err := query.NewSchemaFor[lineRow]("l", query.WithDefaultSort(query.Sort{Field: "line_date"}), query.WithTieBreaker("l.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	ss, err := query.NewSchemaFor[settlementRow]("s", query.WithDefaultSort(query.Sort{Field: "settled_on", Desc: true}), query.WithTieBreaker("s.id"), query.WithPageSize(50, 200))
	if err != nil {
		return nil, err
	}
	return &Repository{db: db, chequeSchema: cs, lineSchema: ls, setSchema: ss}, nil
}

func cur(s string) money.Currency { return money.Currency(s) }

func amount(v int64, c string) money.Money { m, _ := money.New(v, cur(c)); return m }

// ---- bank accounts ----

type accountRow struct {
	ID         uuid.UUID `db:"id"`
	Name       string    `db:"name"`
	BankName   string    `db:"bank_name"`
	IBAN       string    `db:"iban"`
	AccountNo  string    `db:"account_no"`
	Currency   string    `db:"currency"`
	LedgerCode string    `db:"ledger_code"`
	Active     bool      `db:"active"`
	CreatedAt  time.Time `db:"created_at"`
}

const accountCols = `id, name, bank_name, iban, account_no, currency, ledger_code, active, created_at`

func (x accountRow) domain() treasury.BankAccount {
	return treasury.BankAccount{ID: x.ID, Name: x.Name, BankName: x.BankName, IBAN: x.IBAN, AccountNo: x.AccountNo, Currency: cur(x.Currency),
		LedgerCode: x.LedgerCode, Active: x.Active, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertBankAccount(ctx context.Context, a treasury.BankAccount) (treasury.BankAccount, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_bank_accounts (`+accountCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT (ledger_code) DO NOTHING`,
		a.ID, a.Name, a.BankName, a.IBAN, a.AccountNo, string(a.Currency), a.LedgerCode, a.Active, a.CreatedAt)
	if err != nil {
		return a, false, err
	}
	if n == 1 {
		return a, true, nil
	}
	x, err := postgres.SelectOne[accountRow](ctx, r.db.Querier(ctx), `SELECT `+accountCols+` FROM mz_bank_accounts WHERE ledger_code = $1`, a.LedgerCode)
	return x.domain(), false, err
}

func (r *Repository) BankAccount(ctx context.Context, id uuid.UUID) (treasury.BankAccount, error) {
	x, err := postgres.SelectOne[accountRow](ctx, r.db.Querier(ctx), `SELECT `+accountCols+` FROM mz_bank_accounts WHERE id = $1`, id)
	if err != nil {
		return treasury.BankAccount{}, notFound(err, "bank account")
	}
	return x.domain(), nil
}

func (r *Repository) BankAccounts(ctx context.Context) ([]treasury.BankAccount, error) {
	rows, err := postgres.Select[accountRow](ctx, r.db.Querier(ctx), `SELECT `+accountCols+` FROM mz_bank_accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	out := make([]treasury.BankAccount, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func notFound(err error, what string) error {
	if apperr.Is(err, apperr.KindNotFound) {
		return apperr.NotFound(what)
	}
	return err
}

// ---- cheques ----

type chequeRow struct {
	ID              uuid.UUID  `db:"id" json:"id"`
	IdemKey         string     `db:"idem_key" json:"-" query:"-"`
	Direction       string     `db:"direction" json:"direction"`
	Status          string     `db:"status" json:"status"`
	SayadID         string     `db:"sayad_id" json:"sayad_id"`
	Number          string     `db:"number" json:"number"`
	Amount          int64      `db:"amount" json:"amount"`
	Currency        string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	DueDate         time.Time  `db:"due_date" json:"due_date"`
	CounterpartyRef string     `db:"counterparty_ref" json:"counterparty_ref"`
	DrawerBank      string     `db:"drawer_bank" json:"drawer_bank"`
	Note            string     `db:"note" json:"-" query:"-"`
	BankAccountID   *uuid.UUID `db:"bank_account_id" json:"bank_account_id"`
	CounterAccount  string     `db:"counter_account" json:"-" query:"-"`
	OpenJournal     *uuid.UUID `db:"open_journal" json:"-" query:"-"`
	DepositJournal  *uuid.UUID `db:"deposit_journal" json:"-" query:"-"`
	FinalJournal    *uuid.UUID `db:"final_journal" json:"-" query:"-"`
	Reason          string     `db:"reason" json:"-" query:"-"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"-" query:"-"`
}

const chequeCols = `c.id, c.idem_key, c.direction, c.status, c.sayad_id, c.number, c.amount, c.currency, c.due_date, c.counterparty_ref, c.drawer_bank,
	c.note, c.bank_account_id, c.counter_account, c.open_journal, c.deposit_journal, c.final_journal, c.reason, c.created_at, c.updated_at`

func (x chequeRow) domain() treasury.Cheque {
	return treasury.Cheque{ID: x.ID, IdemKey: x.IdemKey, Direction: treasury.Direction(x.Direction), Status: treasury.ChequeStatus(x.Status), SayadID: x.SayadID,
		Number: x.Number, Amount: amount(x.Amount, x.Currency), DueDate: x.DueDate, CounterpartyRef: x.CounterpartyRef, DrawerBank: x.DrawerBank, Note: x.Note,
		BankAccountID: x.BankAccountID, CounterAccount: x.CounterAccount, OpenJournal: x.OpenJournal, DepositJournal: x.DepositJournal,
		FinalJournal: x.FinalJournal, Reason: x.Reason, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}

func (r *Repository) cheque(ctx context.Context, where string, forUpdate bool, args ...any) (treasury.Cheque, error) {
	sql := `SELECT ` + chequeCols + ` FROM mz_cheques c WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[chequeRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		return treasury.Cheque{}, notFound(err, "cheque")
	}
	return x.domain(), nil
}

func (r *Repository) InsertCheque(ctx context.Context, c treasury.Cheque) (treasury.Cheque, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_cheques (id, idem_key, direction, status, sayad_id, number, amount, currency, due_date, counterparty_ref, drawer_bank, note,
		                        bank_account_id, counter_account, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15) ON CONFLICT (idem_key) DO NOTHING`,
		c.ID, c.IdemKey, string(c.Direction), string(c.Status), c.SayadID, c.Number, c.Amount.Amount(), string(c.Amount.Currency()), c.DueDate,
		c.CounterpartyRef, c.DrawerBank, c.Note, c.BankAccountID, c.CounterAccount, c.CreatedAt)
	if postgres.IsUniqueViolation(err, "mz_cheques_sayad") {
		return c, false, apperr.New(apperr.KindConflict, "cheque_exists", "a cheque with this Sayad id is already registered")
	}
	if err != nil {
		return c, false, err
	}
	if n == 1 {
		return c, true, nil
	}
	stored, err := r.cheque(ctx, "c.idem_key = $1", false, c.IdemKey)
	return stored, false, err
}

func (r *Repository) Cheque(ctx context.Context, id uuid.UUID, forUpdate bool) (treasury.Cheque, error) {
	return r.cheque(ctx, "c.id = $1", forUpdate, id)
}

func (r *Repository) UpdateCheque(ctx context.Context, c *treasury.Cheque) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_cheques SET status = $2, bank_account_id = $3, open_journal = $4, deposit_journal = $5, final_journal = $6, reason = $7, updated_at = now()
		 WHERE id = $1`, c.ID, string(c.Status), c.BankAccountID, c.OpenJournal, c.DepositJournal, c.FinalJournal, c.Reason)
	return err
}

func (r *Repository) AddChequeEvent(ctx context.Context, id uuid.UUID, e treasury.ChequeEvent) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_cheque_events (cheque_id, from_status, to_status, actor, note, journal_id, at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, string(e.From), string(e.To), e.Actor, e.Note, e.Journal, e.At)
	return err
}

type eventRow struct {
	From    string     `db:"from_status"`
	To      string     `db:"to_status"`
	Actor   string     `db:"actor"`
	Note    string     `db:"note"`
	Journal *uuid.UUID `db:"journal_id"`
	At      time.Time  `db:"at"`
}

func (r *Repository) ChequeEvents(ctx context.Context, id uuid.UUID) ([]treasury.ChequeEvent, error) {
	rows, err := postgres.Select[eventRow](ctx, r.db.Querier(ctx),
		`SELECT from_status, to_status, actor, note, journal_id, at FROM mz_cheque_events WHERE cheque_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	out := make([]treasury.ChequeEvent, len(rows))
	for i, e := range rows {
		out[i] = treasury.ChequeEvent{From: treasury.ChequeStatus(e.From), To: treasury.ChequeStatus(e.To), Actor: e.Actor, Note: e.Note, Journal: e.Journal, At: e.At}
	}
	return out, nil
}

func (r *Repository) ListCheques(ctx context.Context, p query.Params) (query.Page[treasury.Cheque], error) {
	page, err := postgres.Paginate[chequeRow](ctx, r.db.Querier(ctx), r.chequeSchema,
		postgres.ListQuery{Select: "SELECT " + chequeCols, From: "FROM mz_cheques c"}, p)
	if err != nil {
		return query.Page[treasury.Cheque]{}, err
	}
	return query.MapPage(page, chequeRow.domain), nil
}

type sumRow struct {
	Direction string `db:"direction"`
	Status    string `db:"status"`
	Currency  string `db:"currency"`
	N         int    `db:"n"`
	Total     int64  `db:"total"`
}

func (r *Repository) ChequeSums(ctx context.Context) ([]treasury.ChequeSum, error) {
	rows, err := postgres.Select[sumRow](ctx, r.db.Querier(ctx), `
		SELECT direction, status, currency, count(*)::int AS n, COALESCE(sum(amount), 0)::bigint AS total
		  FROM mz_cheques WHERE status NOT IN ('cleared', 'returned') GROUP BY direction, status, currency ORDER BY direction, status`)
	if err != nil {
		return nil, err
	}
	out := make([]treasury.ChequeSum, len(rows))
	for i, x := range rows {
		out[i] = treasury.ChequeSum{Direction: treasury.Direction(x.Direction), Status: treasury.ChequeStatus(x.Status), Count: x.N, Total: amount(x.Total, x.Currency)}
	}
	return out, nil
}

func (r *Repository) ChequeCandidates(ctx context.Context, account uuid.UUID, d treasury.Direction, statuses []treasury.ChequeStatus, a money.Money) ([]treasury.Cheque, error) {
	st := make([]string, len(statuses))
	for i, s := range statuses {
		st[i] = string(s)
	}
	rows, err := postgres.Select[chequeRow](ctx, r.db.Querier(ctx), `SELECT `+chequeCols+` FROM mz_cheques c
		WHERE c.bank_account_id = $1 AND c.direction = $2 AND c.status = ANY($3) AND c.amount = $4 AND c.currency = $5 ORDER BY c.due_date, c.id`,
		account, string(d), st, a.Amount(), string(a.Currency()))
	if err != nil {
		return nil, err
	}
	out := make([]treasury.Cheque, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

// ---- statement lines ----

type lineRow struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	AccountID   uuid.UUID  `db:"bank_account_id" json:"bank_account_id"`
	Date        time.Time  `db:"line_date" json:"line_date"`
	Amount      int64      `db:"amount" json:"amount"`
	Description string     `db:"description" json:"-" query:"-"`
	Reference   string     `db:"reference" json:"reference"`
	Balance     *int64     `db:"balance" json:"-" query:"-"`
	Fingerprint string     `db:"fingerprint" json:"-" query:"-"`
	Status      string     `db:"status" json:"status"`
	MatchKind   string     `db:"match_kind" json:"match_kind"`
	MatchID     string     `db:"match_id" json:"-" query:"-"`
	MatchedBy   string     `db:"matched_by" json:"-" query:"-"`
	MatchedAt   *time.Time `db:"matched_at" json:"-" query:"-"`
	JournalID   *uuid.UUID `db:"journal_id" json:"-" query:"-"`
	ImportedBy  string     `db:"imported_by" json:"-" query:"-"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
}

const lineCols = `l.id, l.bank_account_id, l.line_date, l.amount, l.description, l.reference, l.balance, l.fingerprint, l.status, l.match_kind, l.match_id,
	l.matched_by, l.matched_at, l.journal_id, l.imported_by, l.created_at`

func (x lineRow) domain() treasury.Line {
	return treasury.Line{ID: x.ID, AccountID: x.AccountID, Date: x.Date, Amount: x.Amount, Description: x.Description, Reference: x.Reference, Balance: x.Balance,
		Fingerprint: x.Fingerprint, Status: treasury.LineStatus(x.Status), MatchKind: treasury.MatchKind(x.MatchKind), MatchID: x.MatchID, MatchedBy: x.MatchedBy,
		MatchedAt: x.MatchedAt, JournalID: x.JournalID, ImportedBy: x.ImportedBy, CreatedAt: x.CreatedAt}
}

func (r *Repository) InsertLines(ctx context.Context, lines []treasury.Line) (int, error) {
	inserted := 0
	for _, l := range lines {
		n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
			INSERT INTO mz_bank_statement_lines (id, bank_account_id, line_date, amount, description, reference, balance, fingerprint, imported_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT (bank_account_id, fingerprint) DO NOTHING`,
			l.ID, l.AccountID, l.Date, l.Amount, l.Description, l.Reference, l.Balance, l.Fingerprint, l.ImportedBy, l.CreatedAt)
		if err != nil {
			return inserted, err
		}
		inserted += int(n)
	}
	return inserted, nil
}

func (r *Repository) Line(ctx context.Context, id uuid.UUID, forUpdate bool) (treasury.Line, error) {
	sql := `SELECT ` + lineCols + ` FROM mz_bank_statement_lines l WHERE l.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	x, err := postgres.SelectOne[lineRow](ctx, r.db.Querier(ctx), sql, id)
	if err != nil {
		return treasury.Line{}, notFound(err, "statement line")
	}
	return x.domain(), nil
}

func (r *Repository) UpdateLine(ctx context.Context, l *treasury.Line) error {
	_, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		UPDATE mz_bank_statement_lines SET status = $2, match_kind = $3, match_id = $4, matched_by = $5, matched_at = $6, journal_id = $7 WHERE id = $1`,
		l.ID, string(l.Status), string(l.MatchKind), l.MatchID, l.MatchedBy, l.MatchedAt, l.JournalID)
	return err
}

func (r *Repository) ListLines(ctx context.Context, p query.Params) (query.Page[treasury.Line], error) {
	page, err := postgres.Paginate[lineRow](ctx, r.db.Querier(ctx), r.lineSchema,
		postgres.ListQuery{Select: "SELECT " + lineCols, From: "FROM mz_bank_statement_lines l"}, p)
	if err != nil {
		return query.Page[treasury.Line]{}, err
	}
	return query.MapPage(page, lineRow.domain), nil
}

func (r *Repository) UnmatchedLines(ctx context.Context, account uuid.UUID, limit int) ([]treasury.Line, error) {
	rows, err := postgres.Select[lineRow](ctx, r.db.Querier(ctx), `SELECT `+lineCols+` FROM mz_bank_statement_lines l
		WHERE l.bank_account_id = $1 AND l.status = 'unmatched' ORDER BY l.line_date, l.id LIMIT $2`, account, limit)
	if err != nil {
		return nil, err
	}
	out := make([]treasury.Line, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

// ---- gateway settlements ----

type settlementRow struct {
	ID        uuid.UUID  `db:"id" json:"id"`
	Gateway   string     `db:"gateway" json:"gateway"`
	Ref       string     `db:"ref" json:"ref"`
	AccountID uuid.UUID  `db:"bank_account_id" json:"bank_account_id"`
	Gross     int64      `db:"gross" json:"gross"`
	Fee       int64      `db:"fee" json:"fee"`
	Net       int64      `db:"net" json:"net"`
	Currency  string     `db:"currency" json:"currency" query:"nofilter,nosort"`
	SettledOn time.Time  `db:"settled_on" json:"settled_on"`
	JournalID uuid.UUID  `db:"journal_id" json:"-" query:"-"`
	LineID    *uuid.UUID `db:"line_id" json:"line_id"`
	Note      string     `db:"note" json:"-" query:"-"`
	Actor     string     `db:"actor" json:"-" query:"-"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
}

const settlementCols = `s.id, s.gateway, s.ref, s.bank_account_id, s.gross, s.fee, s.net, a.currency, s.settled_on, s.journal_id, s.line_id, s.note, s.actor, s.created_at`
const settlementFrom = ` FROM mz_gateway_settlements s JOIN mz_bank_accounts a ON a.id = s.bank_account_id `

func (x settlementRow) domain() treasury.Settlement {
	return treasury.Settlement{ID: x.ID, Gateway: x.Gateway, Ref: x.Ref, AccountID: x.AccountID, Gross: amount(x.Gross, x.Currency), Fee: amount(x.Fee, x.Currency),
		Net: amount(x.Net, x.Currency), SettledOn: x.SettledOn, JournalID: x.JournalID, LineID: x.LineID, Note: x.Note, Actor: x.Actor, CreatedAt: x.CreatedAt}
}

func (r *Repository) settlement(ctx context.Context, where string, forUpdate bool, args ...any) (treasury.Settlement, error) {
	sql := `SELECT ` + settlementCols + settlementFrom + `WHERE ` + where
	if forUpdate {
		sql += ` FOR UPDATE OF s`
	}
	x, err := postgres.SelectOne[settlementRow](ctx, r.db.Querier(ctx), sql, args...)
	if err != nil {
		return treasury.Settlement{}, notFound(err, "settlement")
	}
	return x.domain(), nil
}

func (r *Repository) InsertSettlement(ctx context.Context, s treasury.Settlement) (treasury.Settlement, bool, error) {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `
		INSERT INTO mz_gateway_settlements (id, gateway, ref, bank_account_id, gross, fee, net, settled_on, journal_id, note, actor, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) ON CONFLICT (gateway, ref) DO NOTHING`,
		s.ID, s.Gateway, s.Ref, s.AccountID, s.Gross.Amount(), s.Fee.Amount(), s.Net.Amount(), s.SettledOn, s.JournalID, s.Note, s.Actor, s.CreatedAt)
	if err != nil {
		return s, false, err
	}
	if n == 1 {
		return s, true, nil
	}
	stored, err := r.SettlementByRef(ctx, s.Gateway, s.Ref)
	return stored, false, err
}

func (r *Repository) Settlement(ctx context.Context, id uuid.UUID, forUpdate bool) (treasury.Settlement, error) {
	return r.settlement(ctx, "s.id = $1", forUpdate, id)
}

func (r *Repository) SettlementByRef(ctx context.Context, gateway, ref string) (treasury.Settlement, error) {
	return r.settlement(ctx, "s.gateway = $1 AND s.ref = $2", false, gateway, ref)
}

func (r *Repository) ListSettlements(ctx context.Context, p query.Params) (query.Page[treasury.Settlement], error) {
	page, err := postgres.Paginate[settlementRow](ctx, r.db.Querier(ctx), r.setSchema,
		postgres.ListQuery{Select: "SELECT " + settlementCols, From: "FROM mz_gateway_settlements s JOIN mz_bank_accounts a ON a.id = s.bank_account_id"}, p)
	if err != nil {
		return query.Page[treasury.Settlement]{}, err
	}
	return query.MapPage(page, settlementRow.domain), nil
}

func (r *Repository) UnlinkedSettlements(ctx context.Context, account uuid.UUID, net money.Money) ([]treasury.Settlement, error) {
	rows, err := postgres.Select[settlementRow](ctx, r.db.Querier(ctx), `SELECT `+settlementCols+settlementFrom+
		`WHERE s.bank_account_id = $1 AND s.line_id IS NULL AND s.net = $2 AND a.currency = $3 ORDER BY s.settled_on, s.id`, account, net.Amount(), string(net.Currency()))
	if err != nil {
		return nil, err
	}
	out := make([]treasury.Settlement, len(rows))
	for i, x := range rows {
		out[i] = x.domain()
	}
	return out, nil
}

func (r *Repository) LinkSettlement(ctx context.Context, id, line uuid.UUID) error {
	n, err := postgres.Exec(ctx, r.db.Querier(ctx), `UPDATE mz_gateway_settlements SET line_id = $2 WHERE id = $1 AND line_id IS NULL`, id, line)
	if err != nil {
		return err
	}
	if n == 0 {
		return apperr.New(apperr.KindConflict, "settlement_confirmed", "the payout is already confirmed by another line")
	}
	return nil
}
