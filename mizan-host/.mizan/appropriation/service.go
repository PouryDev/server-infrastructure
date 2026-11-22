package appropriation

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// PaymentMonths is the legal time to pay a declared dividend after the meeting's decision (article 240: eight months).
const PaymentMonths = 8

// Accounts are the ledger accounts the service posts to.
type Accounts struct {
	Retained         string // equity: retained earnings, debited by every appropriation
	LegalReserve     string // equity: اندوخته قانونی
	OptionalReserve  string // equity: other reserves decided by the meeting
	DividendsPayable string // liability: سود سهام پرداختنی
	Bank             string // default account dividends are paid from
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
}

// Record is the stored decision of the general meeting about one fiscal year's profit.
type Record struct {
	ID              uuid.UUID
	FiscalYear      string
	DecidedOn       time.Time
	PayBy           time.Time
	Facts           Facts
	LegalReserve    int64
	OptionalReserve int64
	Dividend        int64
	Paid            int64
	Journal         *uuid.UUID
	IdemKey         string
	Actor           string
	CreatedAt       time.Time
}

// Outstanding is the declared dividend not yet paid.
func (r Record) Outstanding() int64 { return r.Dividend - r.Paid }

// DeclareInput records the meeting's decision.
type DeclareInput struct {
	IdemKey    string
	FiscalYear string
	Facts      Facts
	DecidedOn  time.Time // the meeting's date; default today
	Decision
	Actor string
}

// PayInput pays (part of) the declared dividend.
type PayInput struct {
	IdemKey     string
	Amount      money.Money
	FromAccount string // default the bank account
	Date        time.Time
	Actor       string
}

// Repository is the persistence port.
type Repository interface {
	// Insert stores a record; the same key returns the stored one with created=false; a second decision for one fiscal year fails
	// with a conflict "already_declared".
	Insert(ctx context.Context, r Record) (stored Record, created bool, err error)
	Record(ctx context.Context, id uuid.UUID, forUpdate bool) (Record, error)
	Records(ctx context.Context) ([]Record, error)
	UpdateJournal(ctx context.Context, id uuid.UUID, journal uuid.UUID) error
	// AddPayment stores a payment; the same key returns false and changes nothing. It adds to the record's Paid.
	AddPayment(ctx context.Context, id uuid.UUID, key string, amount int64, journal uuid.UUID, date time.Time) (added bool, err error)
	// HasPayment reports whether a payment with the key was already stored for the decision.
	HasPayment(ctx context.Context, id uuid.UUID, key string) (bool, error)
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service is the profit-appropriation application layer.
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

func (s *Service) note(ctx context.Context, actor, action string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "profit_appropriation", EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "profit_appropriation", AggregateID: id.String(), Payload: details})
}

// Preview applies the legal rules to the facts.
func (s *Service) Preview(f Facts) Plan { return Compute(f) }

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// AddJalaliMonths moves a date forward by whole Jalali months, keeping the day (or the month's last day when it is shorter).
func AddJalaliMonths(t time.Time, n int) (time.Time, error) {
	d, err := jalali.FromTime(t)
	if err != nil {
		return time.Time{}, err
	}
	idx := d.Year*12 + d.Month - 1 + n
	d.Year, d.Month = idx/12, idx%12+1
	d.Day = min(d.Day, jalali.DaysInMonth(d.Year, d.Month))
	return d.Time(time.UTC)
}

// Declare books the meeting's decision: Dr retained earnings, Cr legal reserve, Cr optional reserve, Cr dividends payable. The legal
// reserve is the amount the law requires (articles 140 and 238); the decision must respect articles 239 and 90. There is one decision
// per fiscal year, idempotent by IdemKey.
func (s *Service) Declare(ctx context.Context, in DeclareInput) (Record, error) {
	in.IdemKey, in.FiscalYear = strings.TrimSpace(in.IdemKey), strings.TrimSpace(in.FiscalYear)
	if in.IdemKey == "" || len(in.IdemKey) > 120 {
		return Record{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if in.FiscalYear == "" {
		return Record{}, apperr.InvalidField("fiscal_year", "required")
	}
	plan := Compute(in.Facts)
	if err := plan.Validate(in.Decision); err != nil {
		return Record{}, err
	}
	today := dateOnly(s.books.Today())
	decided := today
	if !in.DecidedOn.IsZero() {
		if decided = dateOnly(in.DecidedOn); decided.After(today) {
			return Record{}, apperr.InvalidField("decided_on", "must not be in the future")
		}
	}
	payBy, err := AddJalaliMonths(decided, PaymentMonths)
	if err != nil {
		return Record{}, apperr.Invalid("date out of the supported range")
	}
	rec := Record{ID: uuid.New(), FiscalYear: in.FiscalYear, DecidedOn: decided, PayBy: payBy, Facts: in.Facts, LegalReserve: plan.LegalReserve,
		OptionalReserve: in.OptionalReserve, Dividend: in.Dividend, IdemKey: in.IdemKey, Actor: in.Actor, CreatedAt: s.clock()}
	var out Record
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.Insert(ctx, rec)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		total := rec.LegalReserve + rec.OptionalReserve + rec.Dividend
		if total > 0 {
			a := s.cfg.Accounts
			lines := []books.Line{books.Dr(a.Retained, s.amt(total))}
			for _, l := range []struct {
				acct string
				v    int64
			}{{a.LegalReserve, rec.LegalReserve}, {a.OptionalReserve, rec.OptionalReserve}, {a.DividendsPayable, rec.Dividend}} {
				if l.v > 0 {
					lines = append(lines, books.Cr(l.acct, s.amt(l.v)))
				}
			}
			jid, err := s.books.Post(ctx, books.Entry{Key: "appropriation:" + rec.ID.String(), Date: decided, Description: "Appropriation of the profit of fiscal year " + rec.FiscalYear,
				RefType: "profit_appropriation", RefID: rec.ID.String(), Actor: in.Actor, Lines: lines})
			if err != nil {
				return err
			}
			if err := s.repo.UpdateJournal(ctx, rec.ID, jid); err != nil {
				return err
			}
			rec.Journal = &jid
		}
		out = rec
		return s.note(ctx, in.Actor, "appropriation.declared", rec.ID, "appropriation.declared", map[string]any{"fiscal_year": rec.FiscalYear, "legal_reserve": rec.LegalReserve,
			"optional_reserve": rec.OptionalReserve, "dividend": rec.Dividend, "pay_by": payBy.Format("2006-01-02")})
	})
	return out, err
}

// Pay pays part of the declared dividend: Dr dividends payable, Cr the bank (or another account). More than is outstanding is refused.
// A payment after the legal deadline is still booked; Record.PayBy and Overdue show it.
func (s *Service) Pay(ctx context.Context, id uuid.UUID, in PayInput) (Record, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return Record{}, apperr.InvalidField("idempotency_key", "required")
	}
	if in.Amount.Currency() != s.cfg.Currency || !in.Amount.IsPositive() {
		return Record{}, apperr.InvalidField("amount", "must be a positive amount in "+string(s.cfg.Currency))
	}
	date := dateOnly(s.books.Today())
	if !in.Date.IsZero() {
		if date = dateOnly(in.Date); date.After(dateOnly(s.books.Today())) {
			return Record{}, apperr.InvalidField("date", "must not be in the future")
		}
	}
	from := in.FromAccount
	if from == "" {
		from = s.cfg.Accounts.Bank
	}
	var out Record
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		rec, err := s.repo.Record(ctx, id, true)
		if err != nil {
			return err
		}
		if done, err := s.repo.HasPayment(ctx, id, in.IdemKey); err != nil {
			return err
		} else if done {
			out = rec // a retry of a payment already made
			return nil
		}
		if in.Amount.Amount() > rec.Outstanding() {
			return apperr.New(apperr.KindConflict, "above_outstanding", "the payment exceeds the dividend still owed")
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "appropriation:" + rec.ID.String() + ":pay:" + in.IdemKey, Date: date, Description: "Dividend payment, fiscal year " + rec.FiscalYear,
			RefType: "profit_appropriation", RefID: rec.ID.String(), Actor: in.Actor, Lines: []books.Line{books.Dr(s.cfg.Accounts.DividendsPayable, in.Amount), books.Cr(from, in.Amount)}})
		if err != nil {
			return err
		}
		added, err := s.repo.AddPayment(ctx, id, in.IdemKey, in.Amount.Amount(), jid, date)
		if err != nil {
			return err
		}
		if rec, err = s.repo.Record(ctx, id, false); err != nil {
			return err
		}
		out = rec
		if !added {
			return nil
		}
		return s.note(ctx, in.Actor, "appropriation.dividend_paid", id, "appropriation.dividend.paid", map[string]any{"amount": in.Amount.Amount(), "outstanding": rec.Outstanding()})
	})
	return out, err
}

// Overdue reports whether the dividend is still owed after the legal payment deadline.
func (s *Service) Overdue(r Record) bool {
	return r.Outstanding() > 0 && dateOnly(s.books.Today()).After(r.PayBy)
}

// Record returns a decision.
func (s *Service) Record(ctx context.Context, id uuid.UUID) (Record, error) {
	return s.repo.Record(ctx, id, false)
}

// Records lists the decisions.
func (s *Service) Records(ctx context.Context) ([]Record, error) { return s.repo.Records(ctx) }
