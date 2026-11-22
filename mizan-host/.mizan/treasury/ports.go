package treasury

import (
	"context"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Transactor runs fn in a transaction that repositories and the books join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertBankAccount stores an account. When its ledger code is already registered the stored one is returned
	// and created=false.
	InsertBankAccount(ctx context.Context, a BankAccount) (stored BankAccount, created bool, err error)
	BankAccount(ctx context.Context, id uuid.UUID) (BankAccount, error)
	BankAccounts(ctx context.Context) ([]BankAccount, error)

	// InsertCheque stores a cheque; the same idempotency key returns the stored one with created=false. A Sayad id
	// that is already registered fails with an apperr conflict "cheque_exists".
	InsertCheque(ctx context.Context, c Cheque) (stored Cheque, created bool, err error)
	Cheque(ctx context.Context, id uuid.UUID, forUpdate bool) (Cheque, error)
	UpdateCheque(ctx context.Context, c *Cheque) error
	AddChequeEvent(ctx context.Context, id uuid.UUID, e ChequeEvent) error
	ChequeEvents(ctx context.Context, id uuid.UUID) ([]ChequeEvent, error)
	ListCheques(ctx context.Context, p query.Params) (query.Page[Cheque], error)
	ChequeSums(ctx context.Context) ([]ChequeSum, error)
	// ChequeCandidates lists the cheques of a direction in one of the statuses with exactly this amount, drawn on or
	// deposited into the account.
	ChequeCandidates(ctx context.Context, account uuid.UUID, d Direction, statuses []ChequeStatus, amount money.Money) ([]Cheque, error)

	// InsertLines stores the lines whose fingerprint is new and reports how many were inserted.
	InsertLines(ctx context.Context, lines []Line) (int, error)
	Line(ctx context.Context, id uuid.UUID, forUpdate bool) (Line, error)
	UpdateLine(ctx context.Context, l *Line) error
	ListLines(ctx context.Context, p query.Params) (query.Page[Line], error)
	UnmatchedLines(ctx context.Context, account uuid.UUID, limit int) ([]Line, error)

	// InsertSettlement stores a payout; the same (gateway, ref) returns the stored one with created=false.
	InsertSettlement(ctx context.Context, s Settlement) (stored Settlement, created bool, err error)
	Settlement(ctx context.Context, id uuid.UUID, forUpdate bool) (Settlement, error)
	// SettlementByRef returns apperr.NotFound when the payout was not recorded.
	SettlementByRef(ctx context.Context, gateway, ref string) (Settlement, error)
	ListSettlements(ctx context.Context, p query.Params) (query.Page[Settlement], error)
	// UnlinkedSettlements lists payouts of the account with this net amount that no statement line confirmed yet.
	UnlinkedSettlements(ctx context.Context, account uuid.UUID, net money.Money) ([]Settlement, error)
	LinkSettlement(ctx context.Context, id, line uuid.UUID) error
}
