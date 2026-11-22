// Package ledger is mizan's accounting core: an append-only, double-entry journal with fiscal years and
// periods (Jalali by default), gap-free legal document numbers, an optional Iranian chart of accounts
// (coding) and the standard reports.
//
// Nothing here knows about payments, wallets or invoices: those contexts post journals through this
// package's Service and never touch its tables. The package depends only on money, jalali and the
// database-free parts of gokit (apperr, query); PostgreSQL lives in ledger/pgledger.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// AccountType is the accounting class of an account; it decides the normal (increasing) side.
type AccountType string

const (
	Asset     AccountType = "asset"
	Liability AccountType = "liability"
	Equity    AccountType = "equity"
	Revenue   AccountType = "revenue"
	Expense   AccountType = "expense"
)

// Valid reports whether t is a known type.
func (t AccountType) Valid() bool {
	switch t {
	case Asset, Liability, Equity, Revenue, Expense:
		return true
	}
	return false
}

// NormalSide is the side that increases the account.
func (t AccountType) NormalSide() Side {
	if t == Asset || t == Expense {
		return Debit
	}
	return Credit
}

// Side is the debit or credit side of a journal line.
type Side uint8

const (
	Debit Side = iota + 1
	Credit
)

func (s Side) String() string {
	switch s {
	case Debit:
		return "debit"
	case Credit:
		return "credit"
	}
	return "invalid"
}

// Opposite returns the other side.
func (s Side) Opposite() Side {
	if s == Debit {
		return Credit
	}
	return Debit
}

// Kind classifies a journal.
type Kind string

const (
	KindNormal   Kind = "normal"
	KindReversal Kind = "reversal"
	KindOpening  Kind = "opening"
	KindClosing  Kind = "closing"
)

// Owner links an account to an external entity (a wallet, a customer).
type Owner struct{ Kind, ID string }

// AccountSpec describes an account to create.
type AccountSpec struct {
	Code        string
	Name        string
	Type        AccountType
	Currency    money.Currency
	NonNegative bool   // a posting that would push the balance below zero is refused (wallets)
	CodingCode  string // optional link into the chart of accounts
	Owner       Owner
}

// Validate checks the spec.
func (s AccountSpec) Validate() error {
	var fe []apperr.FieldError
	if c := strings.TrimSpace(s.Code); c == "" || len(c) > 100 {
		fe = append(fe, apperr.FieldError{Field: "code", Message: "required, at most 100 characters"})
	}
	if strings.TrimSpace(s.Name) == "" {
		fe = append(fe, apperr.FieldError{Field: "name", Message: "required"})
	}
	if !s.Type.Valid() {
		fe = append(fe, apperr.FieldError{Field: "type", Message: "must be asset, liability, equity, revenue or expense"})
	}
	if !s.Currency.Valid() {
		fe = append(fe, apperr.FieldError{Field: "currency", Message: "unsupported currency"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid account", fe...)
	}
	return nil
}

// Account is a stored account.
type Account struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Type        AccountType
	Currency    money.Currency
	NonNegative bool
	CodingCode  string
	Owner       Owner
	Active      bool
	CreatedAt   time.Time
}

// Ref ties a journal to the business object that caused it.
type Ref struct{ Type, ID string }

// Line is one side of a draft journal; the account is addressed by code.
type Line struct {
	Account string
	Side    Side
	Amount  money.Money
	Memo    string
}

// Dr and Cr build lines.
func Dr(account string, amount money.Money, memo ...string) Line {
	return Line{Account: account, Side: Debit, Amount: amount, Memo: strings.Join(memo, " ")}
}
func Cr(account string, amount money.Money, memo ...string) Line {
	return Line{Account: account, Side: Credit, Amount: amount, Memo: strings.Join(memo, " ")}
}

// Draft is a journal that has not been posted yet.
type Draft struct {
	// Key is the idempotency key: posting the same key again with the same content returns the journal
	// that already exists; with different content it is a conflict. Derive it from the business event
	// ("payment:<id>:paid"), never from a random value.
	Key         string
	Date        time.Time // the accounting date; the time of day is ignored
	Description string
	Kind        Kind // default KindNormal
	Ref         Ref
	Actor       string
	Lines       []Line

	reverses *uuid.UUID
}

// Validate checks everything that can be checked without the database.
func (d Draft) Validate() error {
	var fe []apperr.FieldError
	add := func(f, m string) { fe = append(fe, apperr.FieldError{Field: f, Message: m}) }
	if k := strings.TrimSpace(d.Key); k == "" || len(k) > 200 {
		add("key", "required, at most 200 characters")
	}
	if strings.TrimSpace(d.Description) == "" {
		add("description", "required")
	}
	if d.Date.IsZero() {
		add("date", "required")
	}
	if len(d.Lines) < 2 {
		add("lines", "at least two lines are required")
	}
	var debit, credit int64
	var cur money.Currency
	for i, l := range d.Lines {
		f := "lines[" + strconv.Itoa(i) + "]"
		switch {
		case strings.TrimSpace(l.Account) == "":
			add(f+".account", "required")
		case l.Side != Debit && l.Side != Credit:
			add(f+".side", "must be debit or credit")
		case !l.Amount.Valid() || !l.Amount.IsPositive():
			add(f+".amount", "must be a positive amount")
		case cur != "" && l.Amount.Currency() != cur:
			add(f+".amount", "all lines of a journal must use one currency")
		default:
			cur = l.Amount.Currency()
			var over bool
			if l.Side == Debit {
				debit, over = addSafe(debit, l.Amount.Amount())
			} else {
				credit, over = addSafe(credit, l.Amount.Amount())
			}
			if over {
				add(f+".amount", "total too large")
			}
		}
	}
	if len(fe) == 0 && debit != credit {
		add("lines", fmt.Sprintf("debits (%d) and credits (%d) must be equal", debit, credit))
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid journal", fe...)
	}
	return nil
}

func addSafe(a, b int64) (int64, bool) {
	s := a + b
	return s, s < a
}

// Fingerprint is a stable hash of the content that identifies a posting: date, kind, description and
// every line. It is what makes a retry with the same key recognisable as a retry.
func (d Draft) Fingerprint() string {
	h := sha256.New()
	k := d.Kind
	if k == "" {
		k = KindNormal
	}
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s\n", d.Date.Format("2006-01-02"), k, d.Description, d.Ref.Type, d.Ref.ID)
	for _, l := range d.Lines {
		_, _ = fmt.Fprintf(h, "%s|%d|%d|%s|%s\n", l.Account, l.Side, l.Amount.Amount(), l.Amount.Currency(), l.Memo)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Total is the sum of the debit side.
func (d Draft) Total() int64 {
	var t int64
	for _, l := range d.Lines {
		if l.Side == Debit {
			t += l.Amount.Amount()
		}
	}
	return t
}

// PostedLine is a stored journal line.
type PostedLine struct {
	No          int
	AccountID   uuid.UUID
	AccountCode string
	Side        Side
	Amount      money.Money
	Memo        string
}

// Journal is a posted, immutable accounting document (سند حسابداری).
type Journal struct {
	ID           uuid.UUID
	FiscalYearID int64
	PeriodID     int64
	Number       int64 // gap-free within the fiscal year
	Kind         Kind
	Date         time.Time
	Description  string
	Total        money.Money
	Ref          Ref
	Key          string
	Fingerprint  string
	ReversesID   *uuid.UUID
	CreatedBy    string
	CreatedAt    time.Time
	Lines        []PostedLine
}

// Status of fiscal years and periods.
type Status string

const (
	Open   Status = "open"
	Closed Status = "closed"
)

// FiscalYear is an accounting year; for Iran the Jalali year.
type FiscalYear struct {
	ID         int64
	Name       string
	StartsOn   time.Time
	EndsBefore time.Time // exclusive
	Status     Status
	Periods    []Period
}

// Period is a month of a fiscal year. Posting into a closed period is refused.
type Period struct {
	ID           int64
	FiscalYearID int64
	Number       int
	StartsOn     time.Time
	EndsBefore   time.Time // exclusive
	Status       Status
}

// signed converts a line into the change of the account's natural balance.
func signed(t AccountType, side Side, amount int64) int64 {
	if t.NormalSide() == side {
		return amount
	}
	return -amount
}

// Natural converts debit and credit totals into a balance in the account's natural direction.
func Natural(t AccountType, debit, credit int64) int64 {
	if t.NormalSide() == Debit {
		return debit - credit
	}
	return credit - debit
}
