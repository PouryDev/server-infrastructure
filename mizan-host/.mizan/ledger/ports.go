package ledger

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in one database transaction; stores called with the context join it.
// gokit's postgres.DB satisfies it.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Store is the persistence port. Methods used by Post must be called inside a transaction.
type Store interface {
	// CreateAccountIfMissing inserts the account and does nothing when the code exists.
	CreateAccountIfMissing(ctx context.Context, spec AccountSpec) error
	// AccountsByCode returns the accounts found; missing codes are absent from the map.
	AccountsByCode(ctx context.Context, codes []string) (map[string]Account, error)
	SetAccountCoding(ctx context.Context, code, codingCode string) error
	SetAccountActive(ctx context.Context, code string, active bool) error
	// SetCodingForOwnerKind maps every account of an owner kind (for example "wallet") that has no coding yet.
	SetCodingForOwnerKind(ctx context.Context, ownerKind, codingCode string) error
	HasCoding(ctx context.Context, codingCode string) (bool, error)

	ImportCoding(ctx context.Context, nodes []CodingNode) error
	Coding(ctx context.Context) ([]CodingNode, error)

	CreateFiscalYear(ctx context.Context, fy FiscalYear) (FiscalYear, error)
	FiscalYears(ctx context.Context) ([]FiscalYear, error)
	// PeriodFor returns the period containing date, or an apperr.NotFound error.
	PeriodFor(ctx context.Context, date time.Time) (Period, error)
	SetPeriodStatus(ctx context.Context, periodID int64, st Status) error

	// JournalByKey returns apperr.NotFound when the key is unused.
	JournalByKey(ctx context.Context, key string) (Journal, error)
	JournalByID(ctx context.Context, id uuid.UUID) (Journal, error)
	JournalsByRef(ctx context.Context, ref Ref) ([]Journal, error)
	// NextNumber returns the next value of a gap-free sequence. It must be called as late as possible in the
	// transaction: the sequence row stays locked until commit.
	NextNumber(ctx context.Context, sequence string) (int64, error)
	// InsertJournal stores the journal and its lines. It returns ErrDuplicateKey or ErrAlreadyReversed.
	InsertJournal(ctx context.Context, j Journal) error
	// ApplyBalance adds delta to the tracked balance of the account and returns the new balance.
	ApplyBalance(ctx context.Context, accountID uuid.UUID, delta int64) (int64, error)
	TrackedBalance(ctx context.Context, accountID uuid.UUID) (int64, error)

	// LockFiscalYear locks the year (and waits for postings in flight into it) until the transaction ends and
	// returns it without its periods. Postings take the year's number sequence late, so once this returns no
	// journal of the year can appear until commit.
	LockFiscalYear(ctx context.Context, id int64) (FiscalYear, error)
	SetFiscalYearStatus(ctx context.Context, id int64, st Status) error
	// YearClosing returns the closing record of a year; the bool is false when the year was never closed.
	YearClosing(ctx context.Context, fiscalYearID int64) (YearClosing, bool, error)
	SaveYearClosing(ctx context.Context, c YearClosing) error
	// CountJournals counts the journals of a fiscal year, not counting the given ones.
	CountJournals(ctx context.Context, fiscalYearID int64, except []uuid.UUID) (int, error)

	TrialBalance(ctx context.Context, q TrialBalanceQuery) ([]TrialBalanceRow, error)
	Statement(ctx context.Context, acct Account, q StatementQuery) (Statement, error)
	ListJournals(ctx context.Context, params query.Params) (query.Page[JournalSummary], error)
}

// TrialBalanceQuery selects the period of a trial balance; nil bounds are open. Dates are inclusive.
type TrialBalanceQuery struct {
	From, To    *time.Time
	IncludeZero bool
	// ExcludeClosing leaves out the year-end closing journals and their reversals, which is what an income
	// statement and the year's final balance sheet need.
	ExcludeClosing bool
	// ExcludeClosingYear leaves out the closing journals (and their reversals) of that one fiscal year id only. A balance sheet
	// dated inside a year needs this: the year's own closing documents would zero its balances, while the closing and opening
	// documents of earlier years cancel each other and must stay.
	ExcludeClosingYear int64
}

// TrialBalanceRow is one account of a trial balance.
type TrialBalanceRow struct {
	Code       string
	Name       string
	Type       AccountType
	CodingCode string
	Debit      int64
	Credit     int64
}

// Balance is the natural-direction balance.
func (r TrialBalanceRow) Balance() int64 { return Natural(r.Type, r.Debit, r.Credit) }

// StatementQuery pages through an account's lines (keyset on the line id).
type StatementQuery struct {
	From, To    *time.Time
	AfterLineID int64
	Limit       int
}

// StatementRow is one line with the running balance after it.
type StatementRow struct {
	LineID      int64
	JournalID   uuid.UUID
	Number      int64
	FiscalYear  string
	Date        time.Time
	Description string
	Memo        string
	Ref         Ref
	Debit       int64
	Credit      int64
	Running     int64
}

// Statement is a page of an account statement (صورت‌حساب / دفتر معین).
type Statement struct {
	Account   Account
	Opening   int64 // balance before the first row of the requested range
	Rows      []StatementRow
	NextAfter int64 // pass as AfterLineID for the next page; 0 when there is none
}

// JournalSummary is a journal row for listings.
type JournalSummary struct {
	ID           uuid.UUID `json:"id"`
	FiscalYearID int64     `json:"fiscal_year_id"`
	Number       int64     `json:"number"`
	Kind         Kind      `json:"kind"`
	Date         time.Time `json:"date"`
	Description  string    `json:"description"`
	Currency     string    `json:"currency"`
	Total        int64     `json:"total"`
	RefType      string    `json:"ref_type"`
	RefID        string    `json:"ref_id"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}
