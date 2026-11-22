// Package incometax computes, declares and pays the corporate income tax (مالیات بر درآمد اشخاص حقوقی, عملکرد) of a fiscal year.
//
// Law read (consolidated text on thdorsan.com, see mizan/README.md): art. 105 of the Direct Taxes Law (rate 25%; note 6: 25% discount for ordinary
// cooperatives; note 7: one point off per 10% growth of the declared income, up to five points, on condition of paying last year's debt and filing on
// time; note 3: taxes paid before are deducted from the tax and an excess is refundable; note 4: dividends received bear no other tax; notes 8-14, capital
// gains: not modelled), art. 106 (the taxable income of a legal person is determined by profitability and arts. 94, 95, 97), art. 110 (return, balance
// sheet and profit-and-loss statement within four months after the tax year, with the tax paid), art. 147 (accepted costs: documented, normal, solely for earning
// the income; note 2: costs of exempt, zero-rate or flat-rate income are not accepted; note 3: payments of 200,000,000 rials and more must go through the banking
// system), art. 148 (the list of accepted costs; item 12: a loss shown by the books can be set against the income of the following year or years).
//
// WHAT IS DATA: the rate, the cooperative discount, the loss carry limit and the filing months are versioned rules with their source (Rule). The
// taxable income is not derived from the chart of accounts: the accounting profit comes from the books and every difference to the tax profit is an EXPLICIT
// adjustment the accountant supplies with a label (add-backs, exempt income, the depreciation difference). Rate reductions that depend on facts
// the books cannot show (note 7, the production reduction of the 1404 budget law) are an explicit input too.
//
// NOT built: tax on income taxed at its own rates, capital gains (notes 8-14, the regulation was not read), advance-payment instalments (مالیات علی‌الحساب) as
// a schedule, the penalties for late filing or payment (arts. 169, 186, 190, 191 were not read), the five-year limit (the text read has none), the
// deadline extensions (cabinet decisions, enter them as data if needed), and the tax return form itself.
package incometax

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Rule is one version of the legal parameters, applying to tax years that start on or after EffectiveFrom.
type Rule struct {
	ID                    uuid.UUID
	EffectiveFrom         time.Time
	RateBP                int  // 2500 = 25%
	CooperativeDiscountBP int  // 2500 = 25% off the rate for ordinary cooperatives
	LossCarryYears        *int // nil = no limit
	FilingMonths          int  // months after the end of the tax year to file and pay
	Source                string
	CreatedAt             time.Time
}

// RuleInput adds a rule version.
type RuleInput struct {
	EffectiveFrom         time.Time
	RateBP                int
	CooperativeDiscountBP int
	LossCarryYears        *int
	FilingMonths          int
	Source                string
	Actor                 string
}

func (in RuleInput) validate() error {
	var fe []apperr.FieldError
	if in.EffectiveFrom.IsZero() {
		fe = append(fe, apperr.FieldError{Field: "effective_from", Message: "required"})
	}
	if in.RateBP <= 0 || in.RateBP > 10000 {
		fe = append(fe, apperr.FieldError{Field: "rate_bp", Message: "must be between 1 and 10000"})
	}
	if in.CooperativeDiscountBP < 0 || in.CooperativeDiscountBP > 10000 {
		fe = append(fe, apperr.FieldError{Field: "cooperative_discount_bp", Message: "must be between 0 and 10000"})
	}
	if in.LossCarryYears != nil && *in.LossCarryYears < 1 {
		fe = append(fe, apperr.FieldError{Field: "loss_carry_years", Message: "must be positive, or omitted for no limit"})
	}
	if in.FilingMonths < 1 || in.FilingMonths > 12 {
		fe = append(fe, apperr.FieldError{Field: "filing_months", Message: "must be between 1 and 12"})
	}
	if strings.TrimSpace(in.Source) == "" {
		fe = append(fe, apperr.FieldError{Field: "source", Message: "required: the law or decision these numbers come from"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid rule", fe...)
	}
	return nil
}

// Facts are read from the books by the caller (the root package) for one fiscal year.
type Facts struct {
	FiscalYear string
	From, To   time.Time // inclusive dates of the fiscal (tax) year
	Profit     int64     // income statement net of the year, closing documents left out, income tax not yet booked
	BookOn     time.Time // the date the expense is dated: the year's last day while it is open, otherwise today
}

// Adjustments are the accountant's explicit differences between the accounting and the tax profit.
type Adjustments struct {
	AddBacks        []Item
	Deductions      []Item
	DepreciationAdj int64
	Cooperative     bool
	RateReductionBP int
	Credits         int64 // taxes already paid or withheld for this income that the law deducts from the tax (art. 105 note 3)
}

// Assessment is a declared tax of one fiscal year.
type Assessment struct {
	ID         uuid.UUID
	FiscalYear string
	From, To   time.Time
	Facts      Facts
	Adj        Adjustments
	Result     Result
	RuleID     uuid.UUID
	Credits    int64
	Payable    int64 // tax - credits
	Paid       int64
	DueOn      time.Time
	Journal    *uuid.UUID
	IdemKey    string
	Actor      string
	CreatedAt  time.Time
}

// Outstanding is the declared tax not yet paid.
func (a Assessment) Outstanding() int64 { return a.Payable - a.Paid }

// Accounts are the ledger accounts used.
type Accounts struct {
	Expense string // expense: income tax of the year
	Payable string // liability: income tax owed
	Prepaid string // asset: taxes paid in advance or withheld, used up by the assessment
	Bank    string
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
}

// Repository is the persistence port.
type Repository interface {
	InsertRule(ctx context.Context, r Rule) error
	Rules(ctx context.Context) ([]Rule, error)
	// InsertAssessment stores one; the same key returns the stored one with created=false; a second one for a fiscal year fails with a conflict "already_declared".
	InsertAssessment(ctx context.Context, a Assessment) (stored Assessment, created bool, err error)
	Assessment(ctx context.Context, id uuid.UUID, forUpdate bool) (Assessment, error)
	Assessments(ctx context.Context) ([]Assessment, error)
	SetJournal(ctx context.Context, id, journal uuid.UUID) error
	// OpenLosses lists the losses with something left, oldest first, of assessments whose tax year starts before the date.
	OpenLosses(ctx context.Context, before time.Time) ([]Loss, error)
	AddLossUses(ctx context.Context, assessment uuid.UUID, uses []LossUse) error
	AddPayment(ctx context.Context, id uuid.UUID, key string, amount int64, journal uuid.UUID, date time.Time) (added bool, err error)
	HasPayment(ctx context.Context, id uuid.UUID, key string) (bool, error)
	// LockAssessments serialises declarations (they read the open losses).
	LockAssessments(ctx context.Context) error
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service is the income-tax application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) amt(v int64) money.Money { return money.Must(v, s.cfg.Currency) }

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (s *Service) note(ctx context.Context, actor, action string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "income_tax", EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "income_tax", AggregateID: id.String(), Payload: details})
}

// AddRule stores a new version. Versions are immutable.
func (s *Service) AddRule(ctx context.Context, in RuleInput) (Rule, error) {
	if err := in.validate(); err != nil {
		return Rule{}, err
	}
	r := Rule{ID: uuid.New(), EffectiveFrom: dateOnly(in.EffectiveFrom), RateBP: in.RateBP, CooperativeDiscountBP: in.CooperativeDiscountBP, LossCarryYears: in.LossCarryYears,
		FilingMonths: in.FilingMonths, Source: strings.TrimSpace(in.Source), CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertRule(ctx, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "incometax.rule_added", EntityType: "income_tax_rule", EntityID: r.ID.String(),
			Details: map[string]any{"effective_from": r.EffectiveFrom.Format("2006-01-02"), "rate_bp": r.RateBP, "source": r.Source}})
	})
	return r, err
}

// Rules lists the versions.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) { return s.repo.Rules(ctx) }

// RuleFor returns the version that applies to a tax year starting on the given day.
func (s *Service) RuleFor(ctx context.Context, yearStart time.Time) (Rule, error) {
	rs, err := s.repo.Rules(ctx)
	if err != nil {
		return Rule{}, err
	}
	var best Rule
	found := false
	for _, r := range rs {
		if !r.EffectiveFrom.After(yearStart) && (!found || r.EffectiveFrom.After(best.EffectiveFrom)) {
			best, found = r, true
		}
	}
	if !found {
		return Rule{}, apperr.New(apperr.KindConflict, "no_rule", "no income-tax rule applies to this tax year: add one with its source")
	}
	return best, nil
}

func (s *Service) compute(ctx context.Context, f Facts, adj Adjustments) (Result, Rule, error) {
	rule, err := s.RuleFor(ctx, f.From)
	if err != nil {
		return Result{}, Rule{}, err
	}
	losses, err := s.repo.OpenLosses(ctx, f.From)
	if err != nil {
		return Result{}, Rule{}, err
	}
	res, err := Compute(Input{Profit: f.Profit, AddBacks: adj.AddBacks, Deductions: adj.Deductions, DepreciationAdj: adj.DepreciationAdj, Rule: rule,
		Cooperative: adj.Cooperative, RateReductionBP: adj.RateReductionBP, YearStart: f.From, Losses: losses})
	return res, rule, err
}

// Preview computes the tax without booking.
func (s *Service) Preview(ctx context.Context, f Facts, adj Adjustments) (Result, error) {
	res, _, err := s.compute(ctx, f, adj)
	return res, err
}

// DeclareInput declares the tax of a fiscal year.
type DeclareInput struct {
	IdemKey string
	Facts   Facts
	Adj     Adjustments
	Actor   string
}

// Declare books the tax of one fiscal year, once: Dr income-tax expense, Cr income tax payable (tax - credits), Cr prepaid taxes (credits). It
// also uses up the older losses it set off and records a new loss when the year is a tax loss. Idempotent by key; a second declaration for the year is a conflict.
func (s *Service) Declare(ctx context.Context, in DeclareInput) (Assessment, error) {
	if strings.TrimSpace(in.IdemKey) == "" || len(in.IdemKey) > 120 {
		return Assessment{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if strings.TrimSpace(in.Facts.FiscalYear) == "" {
		return Assessment{}, apperr.InvalidField("fiscal_year", "required")
	}
	if in.Adj.Credits < 0 {
		return Assessment{}, apperr.InvalidField("credits", "must not be negative")
	}
	var out Assessment
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockAssessments(ctx); err != nil {
			return err
		}
		res, rule, err := s.compute(ctx, in.Facts, in.Adj)
		if err != nil {
			return err
		}
		if in.Adj.Credits > res.Tax {
			return apperr.InvalidField("credits", "the credits exceed the tax: the excess is a refund claim, not booked here")
		}
		due, err := dueAfterMonths(in.Facts.To, rule.FilingMonths)
		if err != nil {
			return apperr.Invalid("date out of the supported range")
		}
		a := Assessment{ID: uuid.New(), FiscalYear: in.Facts.FiscalYear, From: in.Facts.From, To: in.Facts.To, Facts: in.Facts, Adj: in.Adj, Result: res, RuleID: rule.ID,
			Credits: in.Adj.Credits, Payable: res.Tax - in.Adj.Credits, DueOn: due, IdemKey: in.IdemKey, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertAssessment(ctx, a)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		if err := s.repo.AddLossUses(ctx, a.ID, res.LossUsed); err != nil {
			return err
		}
		if res.Tax > 0 {
			ac := s.cfg.Accounts
			lines := []books.Line{books.Dr(ac.Expense, s.amt(res.Tax))}
			if a.Payable > 0 {
				lines = append(lines, books.Cr(ac.Payable, s.amt(a.Payable)))
			}
			if a.Credits > 0 {
				lines = append(lines, books.Cr(ac.Prepaid, s.amt(a.Credits)))
			}
			date := in.Facts.BookOn
			if date.IsZero() {
				date = in.Facts.To
			}
			jid, err := s.books.Post(ctx, books.Entry{Key: "incometax:" + a.ID.String(), Date: dateOnly(date), Description: "Income tax of fiscal year " + a.FiscalYear,
				RefType: "income_tax", RefID: a.ID.String(), Actor: in.Actor, Lines: lines})
			if err != nil {
				return err
			}
			if err := s.repo.SetJournal(ctx, a.ID, jid); err != nil {
				return err
			}
			a.Journal = &jid
		}
		out = a
		return s.note(ctx, in.Actor, "incometax.declared", a.ID, "incometax.declared", map[string]any{"fiscal_year": a.FiscalYear, "taxable": res.Taxable, "tax": res.Tax,
			"credits": a.Credits, "new_loss": res.NewLoss, "loss_used": res.LossUsedTotal, "due_on": a.DueOn.Format("2006-01-02")})
	})
	return out, err
}

// PayInput pays (part of) a declared tax.
type PayInput struct {
	IdemKey string
	Amount  money.Money
	Account string // default the bank account
	Date    time.Time
	Actor   string
}

// Pay books a payment: Dr income tax payable, Cr bank. More than is outstanding is refused. Idempotent by key.
func (s *Service) Pay(ctx context.Context, id uuid.UUID, in PayInput) (Assessment, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return Assessment{}, apperr.InvalidField("idempotency_key", "required")
	}
	if in.Amount.Currency() != s.cfg.Currency || !in.Amount.IsPositive() {
		return Assessment{}, apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	today := dateOnly(s.books.Today())
	date := today
	if !in.Date.IsZero() {
		if date = dateOnly(in.Date); date.After(today) {
			return Assessment{}, apperr.InvalidField("date", "must not be in the future")
		}
	}
	from := in.Account
	if from == "" {
		from = s.cfg.Accounts.Bank
	}
	var out Assessment
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Assessment(ctx, id, true)
		if err != nil {
			return err
		}
		if done, err := s.repo.HasPayment(ctx, id, in.IdemKey); err != nil {
			return err
		} else if done {
			out = a
			return nil
		}
		if in.Amount.Amount() > a.Outstanding() {
			return apperr.New(apperr.KindConflict, "above_outstanding", "the payment exceeds the tax still owed")
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "incometax:" + a.ID.String() + ":pay:" + in.IdemKey, Date: date, Description: "Income tax payment, fiscal year " + a.FiscalYear,
			RefType: "income_tax", RefID: a.ID.String(), Actor: in.Actor, Lines: []books.Line{books.Dr(s.cfg.Accounts.Payable, in.Amount), books.Cr(from, in.Amount)}})
		if err != nil {
			return err
		}
		added, err := s.repo.AddPayment(ctx, id, in.IdemKey, in.Amount.Amount(), jid, date)
		if err != nil {
			return err
		}
		if a, err = s.repo.Assessment(ctx, id, false); err != nil {
			return err
		}
		out = a
		if !added {
			return nil
		}
		return s.note(ctx, in.Actor, "incometax.paid", id, "incometax.paid", map[string]any{"amount": in.Amount.Amount(), "outstanding": a.Outstanding()})
	})
	return out, err
}

// Overdue reports whether tax is still owed after the filing/payment deadline of art. 110.
func (s *Service) Overdue(a Assessment) bool {
	return a.Outstanding() > 0 && dateOnly(s.books.Today()).After(a.DueOn)
}

// Assessment returns one declaration.
func (s *Service) Assessment(ctx context.Context, id uuid.UUID) (Assessment, error) {
	return s.repo.Assessment(ctx, id, false)
}

// Assessments lists the declarations.
func (s *Service) Assessments(ctx context.Context) ([]Assessment, error) {
	return s.repo.Assessments(ctx)
}

// OpenLosses lists the tax losses still available.
func (s *Service) OpenLosses(ctx context.Context) ([]Loss, error) {
	return s.repo.OpenLosses(ctx, dateOnly(s.books.Today()).AddDate(100, 0, 0))
}

// AdjustmentsJSON is the stored form of the explicit adjustments.
func AdjustmentsJSON(a Adjustments) ([]byte, error) {
	return json.Marshal(struct {
		AddBacks        []Item `json:"add_backs"`
		Deductions      []Item `json:"deductions"`
		DepreciationAdj int64  `json:"depreciation_adj"`
		Cooperative     bool   `json:"cooperative"`
		RateReductionBP int    `json:"rate_reduction_bp"`
		Credits         int64  `json:"credits"`
	}{a.AddBacks, a.Deductions, a.DepreciationAdj, a.Cooperative, a.RateReductionBP, a.Credits})
}
