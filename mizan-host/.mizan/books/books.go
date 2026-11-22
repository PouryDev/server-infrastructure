// Package books is the narrow interface through which business contexts (wallet, invoice, payment
// settlements) ask for accounting entries. They never import the ledger: the composition root adapts this
// port to ledger.Service, so a context can be tested with a fake and the ledger can change freely.
package books

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Side of a line.
type Side uint8

const (
	Debit Side = iota + 1
	Credit
)

// Line is one side of an entry; accounts are addressed by code.
type Line struct {
	Account string
	Side    Side
	Amount  money.Money
	Memo    string
}

// Entry is a balanced set of lines caused by one business event.
type Entry struct {
	Key         string // idempotency key derived from the event, e.g. "invoice:<id>:issue"
	Date        time.Time
	Description string
	RefType     string
	RefID       string
	Actor       string
	Lines       []Line
}

// Dr and Cr build lines.
func Dr(account string, amount money.Money) Line {
	return Line{Account: account, Side: Debit, Amount: amount}
}
func Cr(account string, amount money.Money) Line {
	return Line{Account: account, Side: Credit, Amount: amount}
}

// Poster posts entries. Post is idempotent per Key and joins the transaction carried by ctx.
type Poster interface {
	Post(ctx context.Context, e Entry) (uuid.UUID, error)
	// Reverse cancels a posted entry with a mirror entry.
	Reverse(ctx context.Context, journalID uuid.UUID, key, reason, actor string) (uuid.UUID, error)
	// EnsureAccount creates a liability account for customer balances (non-negative) or a plain account.
	EnsureAccount(ctx context.Context, a Account) error
	// Balance returns the current natural balance of an account.
	Balance(ctx context.Context, code string) (money.Money, error)
	// Today is the current accounting date.
	Today() time.Time
	// NextNumber returns the next gap-free number of a document series within the fiscal year of date.
	NextNumber(ctx context.Context, series string, date time.Time) (fiscalYearID int64, number int64, err error)
}

// AccountType of an account to create.
type AccountType string

const (
	Asset     AccountType = "asset"
	Liability AccountType = "liability"
	Equity    AccountType = "equity"
	Revenue   AccountType = "revenue"
	Expense   AccountType = "expense"
)

// Account describes an account to ensure.
type Account struct {
	Code        string
	Name        string
	Type        AccountType
	Currency    money.Currency
	NonNegative bool
	Coding      string // optional chart-of-accounts code
	OwnerKind   string
	OwnerID     string
}
