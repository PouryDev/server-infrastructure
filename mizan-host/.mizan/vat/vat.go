// Package vat prepares and books the periodic value-added-tax return (اظهارنامه مالیات بر ارزش افزوده) from the ledger: the tax collected on sales
// (output), the tax paid on purchases (input), the settlement with the tax authority and the carried-forward credit.
//
// Structure taken from the full text of the law (قانون مالیات بر ارزش افزوده; the pages read, https://vakilik.com/laws/book/MjUz, show the ORIGINAL 1387/02/17
// wording only): art. 10 (every Jalali year is divided into quarterly tax periods; the minister may set monthly or two-monthly periods for a group of
// taxpayers), art. 17 (tax paid on purchases for the taxpayer's economic activity is deducted from the tax collected, or refunded; an excess is carried to
// the next periods and refunded on request; tax on purchases used for exempt supplies is not recoverable and is an accepted expense of the direct-tax law),
// art. 21 (a return per period and per place of business, paying the tax net of deductible tax by the deadline), art. 23 (late payment: a penalty per
// month on the unpaid tax and the delay).
//
// WHAT IS DATA, NOT CODE: the rate, the filing deadline and the late-payment penalty change by law, so
// they are versioned rules (Rule) with their source; the first one (law of 1400/03/02) is seeded by migration 00017. Without a rule the due date and the penalty are simply not
// computed; the figures and the books do not depend on them (the tax itself comes from the invoices' postings).
//
// Amended returns (Amend), rates by category (rates.go), the exempt-supply list and the apportionment of input tax (apportion.go) were added with migration 00017; the
// law data seeded there and what stays unverified are in mizan/README.md.
//
// NOT built: imports and customs, tobacco rates (a yearly schedule that was not read), per-branch returns (the 1400 law registers taxpayers in the Moadian system and
// the text read has no per-branch return), and the electronic filing itself.
package vat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Rule is one version of the legal parameters of the return, valid from EffectiveFrom until the next version.
type Rule struct {
	ID             uuid.UUID
	EffectiveFrom  time.Time
	PeriodMonths   int    // 1, 2 or 3: the length of a tax period
	FilingDays     int    // days after the end of a period by which the return and payment are due (0 = month-end rule not modelled: use FilingMonthEnd)
	FilingMonthEnd bool   // due at the end of the month after the period instead of a number of days
	LatePenaltyBP  int    // penalty per started month on unpaid tax, in basis points (200 = 2%)
	Source         string // the law, circular or decision these numbers come from
	CreatedAt      time.Time
}

// RuleInput adds a rule version.
type RuleInput struct {
	EffectiveFrom  time.Time
	PeriodMonths   int
	FilingDays     int
	FilingMonthEnd bool
	LatePenaltyBP  int
	Source         string
	Actor          string
}

func (in RuleInput) validate() error {
	var fe []apperr.FieldError
	if in.EffectiveFrom.IsZero() {
		fe = append(fe, apperr.FieldError{Field: "effective_from", Message: "required"})
	}
	if in.PeriodMonths != 1 && in.PeriodMonths != 2 && in.PeriodMonths != 3 {
		fe = append(fe, apperr.FieldError{Field: "period_months", Message: "must be 1, 2 or 3"})
	}
	if (in.FilingDays < 0 || in.FilingDays > 120) || (in.FilingMonthEnd && in.FilingDays != 0) || (!in.FilingMonthEnd && in.FilingDays == 0) {
		fe = append(fe, apperr.FieldError{Field: "filing_days", Message: "give either filing_days (1-120) or filing_month_end, not both and not neither"})
	}
	if in.LatePenaltyBP < 0 || in.LatePenaltyBP > 10000 {
		fe = append(fe, apperr.FieldError{Field: "late_penalty_bp", Message: "must be between 0 and 10000"})
	}
	if strings.TrimSpace(in.Source) == "" {
		fe = append(fe, apperr.FieldError{Field: "source", Message: "required: the law or decision these numbers come from"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid rule", fe...)
	}
	return nil
}

// Figures are the numbers of one tax period read from the books.
type Figures struct {
	Year, No, Months int
	From, To         time.Time // inclusive dates
	Output           int64     // tax collected on sales in the period (credit turnover of the output account, net of reversals)
	Input            int64     // tax paid on purchases in the period (debit turnover of the input account, net of reversals)
	Net              int64     // Output - Input: positive is payable for the period, negative is a credit
	OpeningBalance   int64     // settlement account before the period: positive is still owed, negative is credit carried in
	DueOn            *time.Time
}

// Return is a filed return.
type Return struct {
	ID uuid.UUID
	Figures
	Revision  int        // 0 = the original return, 1.. = amended returns (اظهارنامه اصلاحی)
	Amends    *uuid.UUID // the return this one amends
	DeltaOut  int64      // for an amendment: the change in output tax booked by it
	DeltaIn   int64      // for an amendment: the change in input tax booked by it
	FiledOn   time.Time
	Journal   *uuid.UUID
	IdemKey   string
	Actor     string
	CreatedAt time.Time
}

// Accounts are the ledger accounts the return uses.
type Accounts struct {
	Output     string // liability: VAT collected on sales
	Input      string // asset: VAT paid on purchases that may be deducted
	Settlement string // liability: owed to the tax authority (credit balance) or credit carried forward (debit balance)
	Bank       string // default account of payments and refunds
	Expense    string // expense: input tax that may not be deducted (art. 8 note 5: an accepted expense); needed only for Apportion
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
}

// Balances reads account balances as of a date in their natural direction (credit for liabilities, debit for assets).
type Balances interface {
	BalanceAsOf(ctx context.Context, account string, asOf time.Time) (int64, error)
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertRule stores a version; a taken effective date fails with a conflict "rule_date_taken".
	InsertRule(ctx context.Context, r Rule) error
	Rules(ctx context.Context) ([]Rule, error)
	// InsertReturn stores a return; the same key returns the stored one with created=false; a period already filed fails with a conflict "already_filed".
	InsertReturn(ctx context.Context, r Return) (stored Return, created bool, err error)
	Return(ctx context.Context, id uuid.UUID) (Return, error)
	Returns(ctx context.Context) ([]Return, error)
	// LockPeriod serialises filers of the same period (an advisory lock held until the transaction ends).
	LockPeriod(ctx context.Context, year, months, no int) error
	// SetReturnJournal stores the settlement journal of a return just filed.
	SetReturnJournal(ctx context.Context, id, journal uuid.UUID) error
	// LatestReturn returns the highest revision filed for a period; ok is false when none was filed.
	LatestReturn(ctx context.Context, year, months, no int) (r Return, ok bool, err error)
	ReturnByKey(ctx context.Context, key string) (r Return, ok bool, err error)

	InsertRate(ctx context.Context, r RateVersion) error
	Rates(ctx context.Context) ([]RateVersion, error)
	InsertExemption(ctx context.Context, e Exemption) error
	Exemptions(ctx context.Context) ([]Exemption, error)
	// InsertApportionment stores one; the same key returns the stored one with created=false; a period done already fails with a conflict "already_apportioned".
	InsertApportionment(ctx context.Context, a Apportionment) (stored Apportionment, created bool, err error)
	SetApportionmentJournal(ctx context.Context, id, journal uuid.UUID) error
	Apportionments(ctx context.Context) ([]Apportionment, error)
}

// Service is the VAT application layer.
type Service struct {
	repo     Repository
	tx       Transactor
	books    books.Poster
	balances Balances
	events   trail.Emitter
	audit    trail.Recorder
	cfg      Config
	clock    func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, bal Balances, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, balances: bal, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) amt(v int64) money.Money { return money.Must(v, s.cfg.Currency) }

func (s *Service) note(ctx context.Context, actor, action string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "vat_return", EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "vat_return", AggregateID: id.String(), Payload: details})
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ---- rules ----

// AddRule stores a new version of the legal parameters. Versions are immutable.
func (s *Service) AddRule(ctx context.Context, in RuleInput) (Rule, error) {
	if err := in.validate(); err != nil {
		return Rule{}, err
	}
	r := Rule{ID: uuid.New(), EffectiveFrom: dateOnly(in.EffectiveFrom), PeriodMonths: in.PeriodMonths, FilingDays: in.FilingDays, FilingMonthEnd: in.FilingMonthEnd,
		LatePenaltyBP: in.LatePenaltyBP, Source: strings.TrimSpace(in.Source), CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertRule(ctx, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "vat.rule_added", EntityType: "vat_rule", EntityID: r.ID.String(), Details: map[string]any{"effective_from": r.EffectiveFrom.Format("2006-01-02"), "source": r.Source}})
	})
	return r, err
}

// Rules lists the versions.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) { return s.repo.Rules(ctx) }

// RuleAt returns the version in force on a date; ok is false when none applies yet.
func (s *Service) RuleAt(ctx context.Context, day time.Time) (Rule, bool, error) {
	rs, err := s.repo.Rules(ctx)
	if err != nil {
		return Rule{}, false, err
	}
	var best Rule
	found := false
	for _, r := range rs {
		if !r.EffectiveFrom.After(day) && (!found || r.EffectiveFrom.After(best.EffectiveFrom)) {
			best, found = r, true
		}
	}
	return best, found, nil
}

// ---- periods ----

// bounds returns the first and last day of period no of a Jalali year made of months-long periods.
func bounds(year, months, no int) (from, to time.Time, err error) {
	if months < 1 || 12%months != 0 || no < 1 || no > 12/months {
		return from, to, apperr.Invalid(fmt.Sprintf("period must be 1 to %d for %d-month periods", 12/max(months, 1), months))
	}
	first := (no-1)*months + 1
	from, _, err = jalali.MonthBounds(year, first, time.UTC)
	if err != nil {
		return from, to, apperr.Invalid("period out of the supported range")
	}
	_, next, err := jalali.MonthBounds(year, first+months-1, time.UTC)
	if err != nil {
		return from, to, apperr.Invalid("period out of the supported range")
	}
	return from, next.AddDate(0, 0, -1), nil
}

// dueDate computes the filing deadline of a period from the rule in force at its end.
func dueDate(r Rule, to time.Time) (time.Time, error) {
	if !r.FilingMonthEnd {
		return to.AddDate(0, 0, r.FilingDays), nil
	}
	d, err := jalali.FromTime(to)
	if err != nil {
		return time.Time{}, err
	}
	d.Month++
	if d.Month > 12 {
		d.Month, d.Year = 1, d.Year+1
	}
	d.Day = jalali.DaysInMonth(d.Year, d.Month)
	return d.Time(time.UTC)
}

// PenaltyEstimate is the late-payment penalty on unpaid tax under a rule. Art. 37 of the law of 1400/03/02 sets 2% of the unpaid tax per month of delay,
// counted from the earlier of the end of the return deadline and the payment due date (the same date here: art. 4 ties payment to the end of the month
// after the period). The text does not say how a part of a month counts (it refers to art. 191 of the direct-tax law, which was not read), so every
// started month is counted: a ceiling the accountant must confirm. Penalty-forgiveness circulars are not modelled.
func PenaltyEstimate(r Rule, due, asOf time.Time, unpaid int64) int64 {
	if unpaid <= 0 || r.LatePenaltyBP == 0 || !asOf.After(due) {
		return 0
	}
	days := int(asOf.Sub(due).Hours() / 24)
	months := (days + 29) / 30
	return unpaid * int64(r.LatePenaltyBP) * int64(months) / 10000
}

// Preview reads the figures of a period from the books without filing. periodMonths 0 uses the rule in force (default 3).
func (s *Service) Preview(ctx context.Context, year, periodMonths, no int) (Figures, error) {
	rule, hasRule, err := s.RuleAt(ctx, dateOnly(s.books.Today()))
	if err != nil {
		return Figures{}, err
	}
	if periodMonths == 0 {
		periodMonths = 3
		if hasRule {
			periodMonths = rule.PeriodMonths
		}
	}
	from, to, err := bounds(year, periodMonths, no)
	if err != nil {
		return Figures{}, err
	}
	before := from.AddDate(0, 0, -1)
	a := s.cfg.Accounts
	get := func(acct string, asOf time.Time) (int64, error) { return s.balances.BalanceAsOf(ctx, acct, asOf) }
	outEnd, err := get(a.Output, to)
	if err != nil {
		return Figures{}, err
	}
	outStart, err := get(a.Output, before)
	if err != nil {
		return Figures{}, err
	}
	inEnd, err := get(a.Input, to)
	if err != nil {
		return Figures{}, err
	}
	inStart, err := get(a.Input, before)
	if err != nil {
		return Figures{}, err
	}
	open, err := get(a.Settlement, before)
	if err != nil {
		return Figures{}, err
	}
	f := Figures{Year: year, No: no, Months: periodMonths, From: from, To: to, Output: outEnd - outStart, Input: inEnd - inStart, OpeningBalance: open}
	f.Net = f.Output - f.Input
	if r, ok, err := s.RuleAt(ctx, to); err != nil {
		return Figures{}, err
	} else if ok {
		if d, err := dueDate(r, to); err == nil {
			f.DueOn = &d
		}
	}
	return f, nil
}

// FileInput files a period.
type FileInput struct {
	IdemKey      string
	Year         int
	Period       int
	PeriodMonths int // 0 = the rule in force
	FiledOn      time.Time
	Actor        string
}

// File books the settlement of a finished period: Dr output tax, Cr input tax, and the difference to the settlement account (credit = owed to the
// authority, debit = credit carried to the next periods, art. 17 note 1). The journal is dated the last day of the period, so book it before that
// month of the ledger is closed. A period is filed once.
func (s *Service) File(ctx context.Context, in FileInput) (Return, error) {
	if strings.TrimSpace(in.IdemKey) == "" || len(in.IdemKey) > 120 {
		return Return{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	today := dateOnly(s.books.Today())
	filed := today
	if !in.FiledOn.IsZero() {
		if filed = dateOnly(in.FiledOn); filed.After(today) {
			return Return{}, apperr.InvalidField("filed_on", "must not be in the future")
		}
	}
	var out Return
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		f, err := s.Preview(ctx, in.Year, in.PeriodMonths, in.Period)
		if err != nil {
			return err
		}
		if err := s.repo.LockPeriod(ctx, f.Year, f.Months, f.No); err != nil {
			return err
		}
		// Read the figures again under the lock: a concurrent filing of this period has finished by now.
		if f, err = s.Preview(ctx, in.Year, f.Months, in.Period); err != nil {
			return err
		}
		if !today.After(f.To) {
			return apperr.New(apperr.KindConflict, "period_not_over", "the tax period has not ended yet")
		}
		r := Return{ID: uuid.New(), Figures: f, FiledOn: filed, IdemKey: in.IdemKey, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertReturn(ctx, r)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		lines := s.settlementLines(f.Output, f.Input)
		if len(lines) >= 2 {
			jid, err := s.books.Post(ctx, books.Entry{Key: "vat:return:" + r.ID.String(), Date: f.To, Description: fmt.Sprintf("VAT return %d period %d", f.Year, f.No),
				RefType: "vat_return", RefID: r.ID.String(), Actor: in.Actor, Lines: lines})
			if err != nil {
				return err
			}
			r.Journal = &jid
			if err := s.repo.SetReturnJournal(ctx, r.ID, jid); err != nil {
				return err
			}
		}
		out = r
		return s.note(ctx, in.Actor, "vat.return_filed", r.ID, "vat.return.filed", map[string]any{"year": f.Year, "period": f.No, "output": f.Output, "input": f.Input, "net": f.Net})
	})
	return out, err
}

// settlementLines turns the amounts of output and input tax (signed: a negative amount reverses) into the balanced lines that settle them against the
// settlement account: Dr output, Cr input, and the difference to the settlement account.
func (s *Service) settlementLines(out, in int64) []books.Line {
	a := s.cfg.Accounts
	var lines []books.Line
	add := func(acct string, v int64, debitPositive bool) {
		switch {
		case v == 0:
		case (v > 0) == debitPositive:
			lines = append(lines, books.Dr(acct, s.amt(abs64(v))))
		default:
			lines = append(lines, books.Cr(acct, s.amt(abs64(v))))
		}
	}
	add(a.Output, out, true)
	add(a.Input, in, false)
	add(a.Settlement, out-in, false)
	return lines
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// AmendInput files an amended return for a period that was filed before.
type AmendInput struct {
	IdemKey string
	Year    int
	Period  int
	Months  int // 0 = the period length in force
	Actor   string
}

// Amend files an amended return (اظهارنامه اصلاحی) for a period already filed. Filing settled the period's output and input accounts, so whatever
// postings dated inside the period appeared since (late invoices, credit notes) are exactly what the amendment adds: it books that difference, and its
// figures are the totals of the previous revision plus the difference. Nothing to add is a conflict "nothing_to_amend".
func (s *Service) Amend(ctx context.Context, in AmendInput) (Return, error) {
	if strings.TrimSpace(in.IdemKey) == "" || len(in.IdemKey) > 120 {
		return Return{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	var out Return
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if stored, ok, err := s.repo.ReturnByKey(ctx, in.IdemKey); err != nil {
			return err
		} else if ok { // a retry of an amendment that went through: the difference is settled by now
			out = stored
			return nil
		}
		f, err := s.Preview(ctx, in.Year, in.Months, in.Period)
		if err != nil {
			return err
		}
		if err := s.repo.LockPeriod(ctx, f.Year, f.Months, f.No); err != nil {
			return err
		}
		if f, err = s.Preview(ctx, in.Year, f.Months, in.Period); err != nil {
			return err
		}
		prev, found, err := s.repo.LatestReturn(ctx, f.Year, f.Months, f.No)
		if err != nil {
			return err
		}
		if !found {
			return apperr.New(apperr.KindConflict, "not_filed", "the period has no filed return to amend: file it first")
		}
		r := Return{ID: uuid.New(), Figures: f, Revision: prev.Revision + 1, Amends: &prev.ID, DeltaOut: f.Output, DeltaIn: f.Input,
			FiledOn: dateOnly(s.books.Today()), IdemKey: in.IdemKey, Actor: in.Actor, CreatedAt: s.clock()}
		r.Output, r.Input = prev.Output+f.Output, prev.Input+f.Input
		r.Net = r.Output - r.Input
		if f.Output == 0 && f.Input == 0 {
			return apperr.New(apperr.KindConflict, "nothing_to_amend", "no posting in the period is outside the filed return")
		}
		stored, created, err := s.repo.InsertReturn(ctx, r)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "vat:return:" + r.ID.String(), Date: f.To, Description: fmt.Sprintf("VAT amended return %d period %d revision %d", f.Year, f.No, r.Revision),
			RefType: "vat_return", RefID: r.ID.String(), Actor: in.Actor, Lines: s.settlementLines(f.Output, f.Input)})
		if err != nil {
			return err
		}
		r.Journal = &jid
		if err := s.repo.SetReturnJournal(ctx, r.ID, jid); err != nil {
			return err
		}
		out = r
		return s.note(ctx, in.Actor, "vat.return_amended", r.ID, "vat.return.amended", map[string]any{"year": f.Year, "period": f.No, "revision": r.Revision, "delta_output": f.Output, "delta_input": f.Input})
	})
	return out, err
}

// Return returns a filed return.
func (s *Service) Return(ctx context.Context, id uuid.UUID) (Return, error) {
	return s.repo.Return(ctx, id)
}

// Returns lists the filed returns.
func (s *Service) Returns(ctx context.Context) ([]Return, error) { return s.repo.Returns(ctx) }

// Position is what is owed to the authority (positive) or the credit the business holds (negative), as of today.
func (s *Service) Position(ctx context.Context) (int64, error) {
	return s.balances.BalanceAsOf(ctx, s.cfg.Accounts.Settlement, dateOnly(s.books.Today()))
}

// PayInput moves money between the business and the tax authority.
type PayInput struct {
	IdemKey string
	Amount  money.Money
	Account string // default the bank account
	Date    time.Time
	Actor   string
}

func (s *Service) settle(ctx context.Context, in PayInput, pay bool) (uuid.UUID, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return uuid.Nil, apperr.InvalidField("idempotency_key", "required")
	}
	if in.Amount.Currency() != s.cfg.Currency || !in.Amount.IsPositive() {
		return uuid.Nil, apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	today := dateOnly(s.books.Today())
	date := today
	if !in.Date.IsZero() {
		if date = dateOnly(in.Date); date.After(today) {
			return uuid.Nil, apperr.InvalidField("date", "must not be in the future")
		}
	}
	acct := in.Account
	if acct == "" {
		acct = s.cfg.Accounts.Bank
	}
	var jid uuid.UUID
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		pos, err := s.Position(ctx)
		if err != nil {
			return err
		}
		a := s.cfg.Accounts
		lines := []books.Line{books.Dr(a.Settlement, in.Amount), books.Cr(acct, in.Amount)}
		kind := "payment"
		if pay && in.Amount.Amount() > pos {
			return apperr.New(apperr.KindConflict, "above_owed", "the payment exceeds the tax owed to the authority")
		}
		if !pay {
			kind = "refund"
			if in.Amount.Amount() > -pos {
				return apperr.New(apperr.KindConflict, "above_credit", "the refund exceeds the credit held with the authority")
			}
			lines = []books.Line{books.Dr(acct, in.Amount), books.Cr(a.Settlement, in.Amount)}
		}
		jid, err = s.books.Post(ctx, books.Entry{Key: "vat:" + kind + ":" + in.IdemKey, Date: date, Description: "VAT " + kind, RefType: "vat_" + kind, RefID: in.IdemKey, Actor: in.Actor, Lines: lines})
		return err
	})
	return jid, err
}

// Pay books a payment of VAT to the authority: Dr settlement, Cr bank. More than is owed is refused. Idempotent by key.
func (s *Service) Pay(ctx context.Context, in PayInput) (uuid.UUID, error) {
	return s.settle(ctx, in, true)
}

// ReceiveRefund books a refund of excess credit received from the authority (art. 17 note 1): Dr bank, Cr settlement. More than the credit is refused.
func (s *Service) ReceiveRefund(ctx context.Context, in PayInput) (uuid.UUID, error) {
	return s.settle(ctx, in, false)
}

func trailEntry(actor, action, entityType, id string, details map[string]any) trail.Entry {
	return trail.Entry{Actor: actor, Action: action, EntityType: entityType, EntityID: id, Details: details}
}

// PenaltyReport is the late-payment penalty of one return as of a day.
type PenaltyReport struct {
	Unpaid  int64
	Months  int
	Penalty int64
	RateBP  int
}

// PenaltyFor estimates the penalty on the unpaid tax of a return as of a day: the smaller of the return's net tax and what is still owed to the authority
// (positive settlement balance), under the rule in force at the end of the period (see PenaltyEstimate for the legal basis and its limits).
func (s *Service) PenaltyFor(ctx context.Context, id uuid.UUID, asOf time.Time) (PenaltyReport, error) {
	r, err := s.repo.Return(ctx, id)
	if err != nil {
		return PenaltyReport{}, err
	}
	rule, ok, err := s.RuleAt(ctx, r.To)
	if err != nil {
		return PenaltyReport{}, err
	}
	if !ok {
		return PenaltyReport{}, apperr.New(apperr.KindConflict, "no_rule", "no VAT rule applies to this period")
	}
	due, err := dueDate(rule, r.To)
	if err != nil {
		return PenaltyReport{}, err
	}
	pos, err := s.Position(ctx)
	if err != nil {
		return PenaltyReport{}, err
	}
	unpaid := min(max(r.Net, 0), max(pos, 0))
	asOf = dateOnly(asOf)
	rep := PenaltyReport{Unpaid: unpaid, RateBP: rule.LatePenaltyBP, Penalty: PenaltyEstimate(rule, due, asOf, unpaid)}
	if asOf.After(due) {
		rep.Months = (int(asOf.Sub(due).Hours()/24) + 29) / 30
	}
	return rep, nil
}
