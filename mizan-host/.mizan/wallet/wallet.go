// Package wallet implements customer wallets (stored value). A wallet owns no balance of its own: its
// balance is the balance of a non-negative liability account in the ledger, so the wallet and the books can
// never disagree and an overdraft is impossible by construction.
package wallet

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Status of a wallet.
type Status string

const (
	Active Status = "active"
	Frozen Status = "frozen" // credits allowed, debits refused
	Closed Status = "closed" // nothing allowed; requires a zero balance
)

// Wallet is the aggregate.
type Wallet struct {
	ID          uuid.UUID
	OwnerRef    string
	Currency    money.Currency
	Status      Status
	AccountCode string
	CreatedAt   time.Time
}

// AccountCodeFor is the ledger account of a wallet.
func AccountCodeFor(id uuid.UUID) string { return "wallet." + id.String() }

// Repository is the persistence port.
type Repository interface {
	// LockOwner serialises concurrent Open calls of one (owner, currency) for the rest of the transaction.
	LockOwner(ctx context.Context, owner string, cur money.Currency) error
	// ByOwnerCurrency returns apperr.NotFound when the owner has no wallet in the currency.
	ByOwnerCurrency(ctx context.Context, owner string, cur money.Currency) (Wallet, error)
	// Insert stores a wallet whose ledger account already exists.
	Insert(ctx context.Context, w Wallet) error
	Get(ctx context.Context, id uuid.UUID, forUpdate bool) (Wallet, error)
	ByOwner(ctx context.Context, owner string) ([]Wallet, error)
	SetStatus(ctx context.Context, id uuid.UUID, st Status) error
}

// Transactor runs fn in one transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Accounts are the ledger accounts wallets move money against.
type Accounts struct {
	// Adjustment is the counter account of manual credits and debits by staff.
	Adjustment string
	// Coding is the optional chart-of-accounts code every wallet account is mapped to.
	Coding string
}

// Service is the wallet application layer.
type Service struct {
	repo     Repository
	tx       Transactor
	books    books.Poster
	events   trail.Emitter
	audit    trail.Recorder
	accounts Accounts
}

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, acc Accounts) *Service {
	return &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, accounts: acc}
}

// Open returns the wallet of an owner in a currency, creating it on first use.
func (s *Service) Open(ctx context.Context, ownerRef string, cur money.Currency) (Wallet, error) {
	if strings.TrimSpace(ownerRef) == "" || len(ownerRef) > 200 {
		return Wallet{}, apperr.InvalidField("owner_ref", "required, at most 200 characters")
	}
	if !cur.Valid() {
		return Wallet{}, apperr.InvalidField("currency", "unsupported currency")
	}
	var out Wallet
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockOwner(ctx, ownerRef, cur); err != nil {
			return err
		}
		if w, err := s.repo.ByOwnerCurrency(ctx, ownerRef, cur); err == nil {
			out = w
			return nil
		} else if !apperr.Is(err, apperr.KindNotFound) {
			return err
		}
		id := uuid.New()
		w := Wallet{ID: id, OwnerRef: ownerRef, Currency: cur, Status: Active, AccountCode: AccountCodeFor(id)}
		if err := s.books.EnsureAccount(ctx, books.Account{Code: w.AccountCode, Name: "Wallet " + ownerRef, Type: books.Liability, Currency: cur,
			NonNegative: true, Coding: s.accounts.Coding, OwnerKind: "wallet", OwnerID: w.ID.String()}); err != nil {
			return err
		}
		if err := s.repo.Insert(ctx, w); err != nil {
			return err
		}
		out = w
		return s.audit.Record(ctx, trail.Entry{Action: "wallet.open", EntityType: "wallet", EntityID: w.ID.String(), Details: map[string]any{"owner": ownerRef}})
	})
	return out, err
}

// Get returns a wallet.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Wallet, error) {
	return s.repo.Get(ctx, id, false)
}

// ByOwner lists the wallets of an owner.
func (s *Service) ByOwner(ctx context.Context, owner string) ([]Wallet, error) {
	return s.repo.ByOwner(ctx, owner)
}

// Balance returns the current balance.
func (s *Service) Balance(ctx context.Context, id uuid.UUID) (money.Money, error) {
	w, err := s.repo.Get(ctx, id, false)
	if err != nil {
		return money.Money{}, err
	}
	return s.books.Balance(ctx, w.AccountCode)
}

// Movement is a credit or debit against a counter account.
type Movement struct {
	WalletID    uuid.UUID
	Amount      money.Money
	Key         string // idempotency key of the movement; required
	Counter     string // ledger account on the other side
	Description string
	RefType     string
	RefID       string
	Actor       string
}

func (m Movement) validate() error {
	switch {
	case m.Key == "" || len(m.Key) > 180:
		return apperr.InvalidField("idempotency_key", "required, at most 180 characters")
	case !m.Amount.Valid() || !m.Amount.IsPositive():
		return apperr.InvalidField("amount", "must be a positive amount")
	case m.Counter == "":
		return apperr.InvalidField("counter", "required")
	}
	return nil
}

func (s *Service) move(ctx context.Context, m Movement, credit bool) (uuid.UUID, error) {
	if err := m.validate(); err != nil {
		return uuid.Nil, err
	}
	if strings.TrimSpace(m.Description) == "" {
		m.Description = "Wallet debit"
		if credit {
			m.Description = "Wallet credit"
		}
	}
	var jid uuid.UUID
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		w, err := s.repo.Get(ctx, m.WalletID, true)
		if err != nil {
			return err
		}
		if w.Currency != m.Amount.Currency() {
			return apperr.New(apperr.KindInvalid, "currency_mismatch", "the wallet is in "+string(w.Currency))
		}
		if w.Status == Closed || (!credit && w.Status == Frozen) {
			return apperr.New(apperr.KindConflict, "wallet_"+string(w.Status), "the wallet is "+string(w.Status))
		}
		lines := []books.Line{books.Dr(m.Counter, m.Amount), books.Cr(w.AccountCode, m.Amount)}
		topic := "wallet.credited"
		if !credit {
			lines = []books.Line{books.Dr(w.AccountCode, m.Amount), books.Cr(m.Counter, m.Amount)}
			topic = "wallet.debited"
		}
		if jid, err = s.books.Post(ctx, books.Entry{Key: "wallet:" + m.Key, Date: s.books.Today(), Description: m.Description, RefType: m.RefType, RefID: m.RefID, Actor: m.Actor, Lines: lines}); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, trail.Entry{Actor: m.Actor, Action: topic, EntityType: "wallet", EntityID: w.ID.String(),
			Details: map[string]any{"amount": m.Amount.Amount(), "journal": jid, "key": m.Key}}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "wallet", AggregateID: w.ID.String(),
			Payload: map[string]any{"wallet_id": w.ID, "owner_ref": w.OwnerRef, "amount": m.Amount.Amount(), "currency": m.Amount.Currency(), "journal_id": jid, "key": m.Key}})
	})
	return jid, err
}

// Credit adds money to a wallet (Dr counter, Cr wallet). Idempotent per key.
func (s *Service) Credit(ctx context.Context, m Movement) (uuid.UUID, error) {
	return s.move(ctx, m, true)
}

// Debit takes money from a wallet (Dr wallet, Cr counter); it fails with insufficient_funds instead of going negative.
func (s *Service) Debit(ctx context.Context, m Movement) (uuid.UUID, error) {
	return s.move(ctx, m, false)
}

// Adjust is a staff credit or debit against the configured adjustment account.
func (s *Service) Adjust(ctx context.Context, m Movement, credit bool) (uuid.UUID, error) {
	m.Counter = s.accounts.Adjustment
	return s.move(ctx, m, credit)
}

// Transfer moves money between two wallets in one journal (the same currency).
func (s *Service) Transfer(ctx context.Context, from, to uuid.UUID, amount money.Money, key, description, actor string) (uuid.UUID, error) {
	if key == "" || len(key) > 180 {
		return uuid.Nil, apperr.InvalidField("idempotency_key", "required, at most 180 characters")
	}
	if !amount.Valid() || !amount.IsPositive() {
		return uuid.Nil, apperr.InvalidField("amount", "must be a positive amount")
	}
	if from == to {
		return uuid.Nil, apperr.Invalid("source and destination wallets must differ")
	}
	if strings.TrimSpace(description) == "" {
		description = "Wallet transfer"
	}
	var jid uuid.UUID
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		// Lock in id order so two opposite transfers cannot deadlock.
		first, second := from, to
		if strings.Compare(first.String(), second.String()) > 0 {
			first, second = second, first
		}
		ws := map[uuid.UUID]Wallet{}
		for _, id := range []uuid.UUID{first, second} {
			w, err := s.repo.Get(ctx, id, true)
			if err != nil {
				return err
			}
			ws[id] = w
		}
		src, dst := ws[from], ws[to]
		if src.Currency != amount.Currency() || dst.Currency != amount.Currency() {
			return apperr.New(apperr.KindInvalid, "currency_mismatch", "both wallets must be in "+string(amount.Currency()))
		}
		if src.Status != Active || dst.Status != Active {
			return apperr.New(apperr.KindConflict, "wallet_not_active", "both wallets must be active")
		}
		var err error
		if jid, err = s.books.Post(ctx, books.Entry{Key: "transfer:" + key, Date: s.books.Today(), Description: description, RefType: "wallet_transfer", RefID: key, Actor: actor,
			Lines: []books.Line{books.Dr(src.AccountCode, amount), books.Cr(dst.AccountCode, amount)}}); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "wallet.transfer", EntityType: "wallet", EntityID: from.String(),
			Details: map[string]any{"to": to, "amount": amount.Amount(), "journal": jid}}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "wallet.transferred", AggregateType: "wallet", AggregateID: from.String(),
			Payload: map[string]any{"from": from, "to": to, "amount": amount.Amount(), "currency": amount.Currency(), "journal_id": jid}})
	})
	return jid, err
}

// SetStatus freezes, unfreezes or closes a wallet. Closing needs a zero balance.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, st Status, actor string) error {
	if st != Active && st != Frozen && st != Closed {
		return apperr.InvalidField("status", "must be active, frozen or closed")
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		w, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		if w.Status == Closed {
			return apperr.New(apperr.KindConflict, "wallet_closed", "a closed wallet cannot change")
		}
		if st == Closed {
			bal, err := s.books.Balance(ctx, w.AccountCode)
			if err != nil {
				return err
			}
			if !bal.IsZero() {
				return apperr.New(apperr.KindConflict, "wallet_not_empty", "a wallet can only be closed when its balance is zero")
			}
		}
		if err := s.repo.SetStatus(ctx, id, st); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "wallet.status", EntityType: "wallet", EntityID: id.String(), Details: map[string]any{"status": st}})
	})
}
