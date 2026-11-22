package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Sentinel errors a Store returns from InsertJournal.
var (
	ErrDuplicateKey    = errors.New("ledger: idempotency key already used")
	ErrAlreadyReversed = errors.New("ledger: journal already reversed")
)

// Tehran is the default accounting location.
var Tehran = mustLoad("Asia/Tehran")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("IRST", 3*3600+1800)
	}
	return loc
}

// Service is the ledger's application layer.
type Service struct {
	store Store
	tx    Transactor
	clock func() time.Time
	loc   *time.Location

	summaryAcct, retainedAcct      string // year-end accounts, see WithYearEnd
	closingTransit, openingTransit string
	chunk                          int
	audit                          trail.Recorder
	events                         trail.Emitter
}

// Option customises a Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// WithLocation sets the location that defines "today" (default Asia/Tehran).
func WithLocation(l *time.Location) Option { return func(s *Service) { s.loc = l } }

// NewService creates the service.
func NewService(store Store, tx Transactor, opts ...Option) *Service {
	s := &Service{store: store, tx: tx, clock: time.Now, loc: Tehran, audit: trail.Discard{}, events: trail.Discard{}}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Today is the current accounting date.
func (s *Service) Today() time.Time { return s.DateOf(s.clock()) }

// DateOf converts an instant to its accounting date: midnight UTC of the calendar day in the service location.
func (s *Service) DateOf(t time.Time) time.Time {
	y, m, d := t.In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Ensure creates the accounts that do not exist yet and fails when an existing account has a different
// type, currency or non-negative rule than requested. It is safe to call on every start-up.
func (s *Service) Ensure(ctx context.Context, specs ...AccountSpec) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		codes := make([]string, 0, len(specs))
		for _, sp := range specs {
			if err := sp.Validate(); err != nil {
				return err
			}
			if err := s.store.CreateAccountIfMissing(ctx, sp); err != nil {
				return err
			}
			codes = append(codes, sp.Code)
		}
		got, err := s.store.AccountsByCode(ctx, codes)
		if err != nil {
			return err
		}
		for _, sp := range specs {
			a := got[sp.Code]
			if a.Type != sp.Type || a.Currency != sp.Currency || a.NonNegative != sp.NonNegative {
				return apperr.Conflict(fmt.Sprintf("account %s already exists with a different type, currency or balance rule", sp.Code))
			}
		}
		return nil
	})
}

// Account returns one account by code.
func (s *Service) Account(ctx context.Context, code string) (Account, error) {
	m, err := s.store.AccountsByCode(ctx, []string{code})
	if err != nil {
		return Account{}, err
	}
	a, ok := m[code]
	if !ok {
		return Account{}, apperr.NotFound("account")
	}
	return a, nil
}

// Post validates and stores a journal atomically: idempotency, open period, account rules, balance rules,
// legal number and lines are all in one transaction. When ctx already carries a transaction the journal
// joins it, so a business change and its accounting entry commit or fail together.
func (s *Service) Post(ctx context.Context, d Draft) (Journal, error) {
	if err := d.Validate(); err != nil {
		return Journal{}, err
	}
	if d.Kind == "" {
		d.Kind = KindNormal
	}
	d.Date = s.DateOf(d.Date)
	var out Journal
	run := func(ctx context.Context) error {
		j, err := s.post(ctx, d)
		out = j
		return err
	}
	err := s.tx.WithTx(ctx, run)
	if errors.Is(err, ErrDuplicateKey) {
		// A concurrent request with the same key committed first; the retry finds it.
		err = s.tx.WithTx(ctx, run)
	}
	if errors.Is(err, ErrDuplicateKey) {
		return Journal{}, apperr.Conflict("idempotency key is being used by a concurrent request")
	}
	return out, err
}

func (s *Service) post(ctx context.Context, d Draft) (Journal, error) {
	fp := d.Fingerprint()
	if j, err := s.store.JournalByKey(ctx, d.Key); err == nil {
		if j.Fingerprint != fp {
			return Journal{}, apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different journal")
		}
		return j, nil
	} else if !apperr.Is(err, apperr.KindNotFound) {
		return Journal{}, err
	}

	codes := make([]string, 0, len(d.Lines))
	for _, l := range d.Lines {
		if !slices.Contains(codes, l.Account) {
			codes = append(codes, l.Account)
		}
	}
	accts, err := s.store.AccountsByCode(ctx, codes)
	if err != nil {
		return Journal{}, err
	}
	cur := d.Lines[0].Amount.Currency()
	for _, c := range codes {
		a, ok := accts[c]
		switch {
		case !ok:
			return Journal{}, apperr.New(apperr.KindInvalid, "unknown_account", "unknown account "+c)
		case !a.Active:
			return Journal{}, apperr.New(apperr.KindConflict, "account_inactive", "account "+c+" is inactive")
		case a.Currency != cur:
			return Journal{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "account "+c+" is not in "+string(cur))
		}
	}

	period, err := s.store.PeriodFor(ctx, d.Date)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return Journal{}, apperr.New(apperr.KindConflict, "no_fiscal_year", "no fiscal period covers "+d.Date.Format("2006-01-02"))
		}
		return Journal{}, err
	}
	if period.Status != Open {
		return Journal{}, apperr.New(apperr.KindConflict, "period_closed", "the accounting period of "+d.Date.Format("2006-01-02")+" is closed")
	}

	lines := make([]PostedLine, len(d.Lines))
	deltas := map[uuid.UUID]int64{}
	for i, l := range d.Lines {
		a := accts[l.Account]
		lines[i] = PostedLine{No: i + 1, AccountID: a.ID, AccountCode: a.Code, Side: l.Side, Amount: l.Amount, Memo: l.Memo}
		if a.NonNegative {
			deltas[a.ID] += signed(a.Type, l.Side, l.Amount.Amount())
		}
	}
	// Lock tracked balances in a fixed order so two postings can never deadlock on each other.
	ids := make([]uuid.UUID, 0, len(deltas))
	for id := range deltas {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	for _, id := range ids {
		nb, err := s.store.ApplyBalance(ctx, id, deltas[id])
		if err != nil {
			return Journal{}, err
		}
		if nb < 0 {
			return Journal{}, apperr.New(apperr.KindConflict, "insufficient_funds", "insufficient balance")
		}
	}

	number, err := s.store.NextNumber(ctx, "journal:"+strconv.FormatInt(period.FiscalYearID, 10))
	if err != nil {
		return Journal{}, err
	}
	total, _ := money.New(d.Total(), cur)
	j := Journal{
		ID: uuid.New(), FiscalYearID: period.FiscalYearID, PeriodID: period.ID, Number: number, Kind: d.Kind,
		Date: d.Date, Description: d.Description, Total: total, Ref: d.Ref, Key: d.Key, Fingerprint: fp,
		ReversesID: d.reverses, CreatedBy: d.Actor, Lines: lines,
	}
	if err := s.store.InsertJournal(ctx, j); err != nil {
		if errors.Is(err, ErrAlreadyReversed) {
			return Journal{}, apperr.New(apperr.KindConflict, "already_reversed", "the journal has already been reversed")
		}
		return Journal{}, err
	}
	return j, nil
}

// ReverseRequest corrects a posted journal. The original stays untouched; a mirror journal cancels it.
type ReverseRequest struct {
	Key    string    // idempotency key of the reversal
	Date   time.Time // accounting date of the reversal; default today (the original's period may be closed)
	Reason string
	Actor  string
}

// Reverse posts the mirror image of a journal (debits and credits swapped).
func (s *Service) Reverse(ctx context.Context, id uuid.UUID, r ReverseRequest) (Journal, error) {
	orig, err := s.store.JournalByID(ctx, id)
	if err != nil {
		return Journal{}, err
	}
	if orig.Kind == KindReversal {
		return Journal{}, apperr.New(apperr.KindInvalid, "cannot_reverse_reversal", "a reversal cannot be reversed; post a new journal instead")
	}
	date := r.Date
	if date.IsZero() {
		date = s.Today()
	}
	desc := fmt.Sprintf("Reversal of document %d (%s)", orig.Number, orig.Description)
	if r.Reason != "" {
		desc += ": " + r.Reason
	}
	d := Draft{Key: r.Key, Date: date, Description: desc, Kind: KindReversal, Ref: orig.Ref, Actor: r.Actor, reverses: &orig.ID}
	for _, l := range orig.Lines {
		d.Lines = append(d.Lines, Line{Account: l.AccountCode, Side: l.Side.Opposite(), Amount: l.Amount, Memo: l.Memo})
	}
	return s.Post(ctx, d)
}

// Journal returns a journal with its lines.
func (s *Service) Journal(ctx context.Context, id uuid.UUID) (Journal, error) {
	return s.store.JournalByID(ctx, id)
}

// JournalsFor returns the journals posted for a business object.
func (s *Service) JournalsFor(ctx context.Context, ref Ref) ([]Journal, error) {
	return s.store.JournalsByRef(ctx, ref)
}

// ListJournals lists journals with the client's filters, sort and pagination.
func (s *Service) ListJournals(ctx context.Context, p query.Params) (query.Page[JournalSummary], error) {
	return s.store.ListJournals(ctx, p)
}

// Balance returns the natural-direction balance of an account as of a date (nil = now).
func (s *Service) Balance(ctx context.Context, code string, asOf *time.Time) (money.Money, error) {
	a, err := s.Account(ctx, code)
	if err != nil {
		return money.Money{}, err
	}
	if a.NonNegative && asOf == nil {
		v, err := s.store.TrackedBalance(ctx, a.ID)
		if err != nil {
			return money.Money{}, err
		}
		return money.New(v, a.Currency)
	}
	rows, err := s.store.TrialBalance(ctx, TrialBalanceQuery{To: asOf, IncludeZero: true})
	if err != nil {
		return money.Money{}, err
	}
	for _, r := range rows {
		if r.Code == code {
			return money.New(r.Balance(), a.Currency)
		}
	}
	return money.New(0, a.Currency)
}

// TrialBalance (تراز آزمایشی) lists debit and credit totals per account. It also reports whether the books
// balance, which they always do unless the database was tampered with.
func (s *Service) TrialBalance(ctx context.Context, q TrialBalanceQuery) (TrialBalance, error) {
	rows, err := s.store.TrialBalance(ctx, q)
	if err != nil {
		return TrialBalance{}, err
	}
	tb := TrialBalance{Rows: rows}
	for _, r := range rows {
		tb.Debit += r.Debit
		tb.Credit += r.Credit
	}
	tb.Balanced = tb.Debit == tb.Credit
	return tb, nil
}

// TrialBalance is a trial balance with totals.
type TrialBalance struct {
	Rows     []TrialBalanceRow
	Debit    int64
	Credit   int64
	Balanced bool
}

// Statement returns a page of an account's ledger (دفتر معین) with running balance.
func (s *Service) Statement(ctx context.Context, code string, q StatementQuery) (Statement, error) {
	a, err := s.Account(ctx, code)
	if err != nil {
		return Statement{}, err
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	return s.store.Statement(ctx, a, q)
}

// OpenFiscalYear creates the Jalali fiscal year with its 12 monthly periods (idempotent per year).
func (s *Service) OpenFiscalYear(ctx context.Context, jalaliYear int) (FiscalYear, error) {
	start, next, err := jalali.YearBounds(jalaliYear, time.UTC)
	if err != nil {
		return FiscalYear{}, apperr.Invalid("fiscal year out of the supported range")
	}
	fy := FiscalYear{Name: strconv.Itoa(jalaliYear), StartsOn: start, EndsBefore: next, Status: Open}
	for m := 1; m <= 12; m++ {
		ps, pn, err := jalali.MonthBounds(jalaliYear, m, time.UTC)
		if err != nil {
			return FiscalYear{}, apperr.Invalid("fiscal year out of the supported range")
		}
		fy.Periods = append(fy.Periods, Period{Number: m, StartsOn: ps, EndsBefore: pn, Status: Open})
	}
	var out FiscalYear
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		var err error
		out, err = s.store.CreateFiscalYear(ctx, fy)
		return err
	})
	return out, err
}

// EnsureCurrentFiscalYear opens the Jalali year of today if it does not exist.
func (s *Service) EnsureCurrentFiscalYear(ctx context.Context) error {
	jd, err := jalali.FromTime(s.Today())
	if err != nil {
		return err
	}
	_, err = s.OpenFiscalYear(ctx, jd.Year)
	return err
}

// FiscalYears lists the fiscal years with their periods.
func (s *Service) FiscalYears(ctx context.Context) ([]FiscalYear, error) {
	return s.store.FiscalYears(ctx)
}

// SetPeriodStatus closes or reopens a month. Closing refuses further postings dated inside it.
func (s *Service) SetPeriodStatus(ctx context.Context, periodID int64, st Status) error {
	if st != Open && st != Closed {
		return apperr.Invalid("status must be open or closed")
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error { return s.store.SetPeriodStatus(ctx, periodID, st) })
}

// ImportCoding loads a chart of accounts (see IranianCoding); existing codes are updated, nothing is deleted.
func (s *Service) ImportCoding(ctx context.Context, nodes []CodingNode) error {
	if err := ValidateCoding(nodes); err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error { return s.store.ImportCoding(ctx, nodes) })
}

// MapAccount links an account to a chart-of-accounts code. Without a chart nothing else changes: the
// ledger works the same, reports just show the account as unclassified.
func (s *Service) MapAccount(ctx context.Context, accountCode, codingCode string) error {
	return s.store.SetAccountCoding(ctx, accountCode, codingCode)
}

// SetAccountActive deactivates (or reactivates) an account; an inactive account refuses new postings.
func (s *Service) SetAccountActive(ctx context.Context, code string, active bool) error {
	return s.store.SetAccountActive(ctx, code, active)
}

// Coding returns the stored chart of accounts (empty when none was imported).
func (s *Service) Coding(ctx context.Context) ([]CodingNode, error) { return s.store.Coding(ctx) }

// CodingTrialBalance is the trial balance rolled up along the chart of accounts. It works without a chart:
// everything then lands in Unclassified.
func (s *Service) CodingTrialBalance(ctx context.Context, q TrialBalanceQuery) (CodingReport, error) {
	rows, err := s.store.TrialBalance(ctx, q)
	if err != nil {
		return CodingReport{}, err
	}
	chart, err := s.store.Coding(ctx)
	if err != nil {
		return CodingReport{}, err
	}
	return RollUp(rows, chart), nil
}

// NextDocumentNumber allocates the next gap-free number of a document series (for example "invoice") in the
// fiscal year that contains date. Call it inside the transaction of the document it numbers: a rollback
// gives the number back, so there are no gaps.
func (s *Service) NextDocumentNumber(ctx context.Context, series string, date time.Time) (fiscalYearID, number int64, err error) {
	period, err := s.store.PeriodFor(ctx, s.DateOf(date))
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return 0, 0, apperr.New(apperr.KindConflict, "no_fiscal_year", "no fiscal year covers "+date.Format("2006-01-02"))
		}
		return 0, 0, err
	}
	number, err = s.store.NextNumber(ctx, series+":"+strconv.FormatInt(period.FiscalYearID, 10))
	return period.FiscalYearID, number, err
}

// MapOwnerKind maps all accounts of an owner kind that are not mapped yet to a coding code.
func (s *Service) MapOwnerKind(ctx context.Context, ownerKind, codingCode string) error {
	return s.store.SetCodingForOwnerKind(ctx, ownerKind, codingCode)
}

// HasCoding reports whether the chart of accounts contains the code.
func (s *Service) HasCoding(ctx context.Context, codingCode string) (bool, error) {
	return s.store.HasCoding(ctx, codingCode)
}
