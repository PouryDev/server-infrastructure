package mizan

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// ledgerBooks adapts ledger.Service to books.Poster, the port the business contexts use.
type ledgerBooks struct{ l *ledger.Service }

var _ books.Poster = ledgerBooks{}

func (b ledgerBooks) Post(ctx context.Context, e books.Entry) (uuid.UUID, error) {
	d := ledger.Draft{Key: e.Key, Date: e.Date, Description: e.Description, Ref: ledger.Ref{Type: e.RefType, ID: e.RefID}, Actor: e.Actor}
	for _, l := range e.Lines {
		side := ledger.Debit
		if l.Side == books.Credit {
			side = ledger.Credit
		}
		d.Lines = append(d.Lines, ledger.Line{Account: l.Account, Side: side, Amount: l.Amount, Memo: l.Memo})
	}
	j, err := b.l.Post(ctx, d)
	return j.ID, err
}

func (b ledgerBooks) Reverse(ctx context.Context, id uuid.UUID, key, reason, actor string) (uuid.UUID, error) {
	j, err := b.l.Reverse(ctx, id, ledger.ReverseRequest{Key: key, Reason: reason, Actor: actor})
	return j.ID, err
}

func (b ledgerBooks) EnsureAccount(ctx context.Context, a books.Account) error {
	spec := ledger.AccountSpec{Code: a.Code, Name: a.Name, Type: ledger.AccountType(a.Type), Currency: a.Currency,
		NonNegative: a.NonNegative, Owner: ledger.Owner{Kind: a.OwnerKind, ID: a.OwnerID}}
	// The chart of accounts is optional: map only when it is there, never fail because it is not.
	if a.Coding != "" {
		if ok, err := b.l.HasCoding(ctx, a.Coding); err == nil && ok {
			spec.CodingCode = a.Coding
		}
	}
	return b.l.Ensure(ctx, spec)
}

func (b ledgerBooks) Balance(ctx context.Context, code string) (money.Money, error) {
	return b.l.Balance(ctx, code, nil)
}

func (b ledgerBooks) Today() time.Time { return b.l.Today() }

func (b ledgerBooks) NextNumber(ctx context.Context, series string, date time.Time) (int64, int64, error) {
	return b.l.NextDocumentNumber(ctx, series, date)
}

// ledgerBalances adapts ledger.Service to the balance-as-of port of the vat context.
type ledgerBalances struct{ l *ledger.Service }

func (b ledgerBalances) BalanceAsOf(ctx context.Context, account string, asOf time.Time) (int64, error) {
	m, err := b.l.Balance(ctx, account, &asOf)
	return m.Amount(), err
}
