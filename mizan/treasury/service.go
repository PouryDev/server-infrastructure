package treasury

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Accounts are the ledger accounts treasury moves money between. The composition root creates them.
type Accounts struct {
	ChequesInHand       string // asset: received cheques in our safe (اسناد دریافتنی نزد صندوق)
	ChequesInCollection string // asset: received cheques deposited, not yet paid (در جریان وصول)
	ChequesPayable      string // liability: cheques we issued, not yet paid (اسناد پرداختنی)
	Receivable          string // default counter account of a received cheque (customer receivables)
	Payable             string // default counter account of an issued cheque (supplier payables)
	BankFees            string // expense: bank charges
	GatewayClearing     string // asset: money gateways collected, not yet paid to the bank
	GatewayFees         string // expense: what gateways keep
	VATInput            string // asset: deductible input VAT on gateway fees (required only when a settlement names the fee's VAT)
	// BankCoding is the optional chart-of-accounts code new bank accounts are mapped to.
	BankCoding string
}

// Config of the service.
type Config struct {
	Accounts Accounts
	// Currency is the currency of the books; the default of a new bank account.
	Currency money.Currency
}

// Service is the treasury application layer.
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

// when validates the date of a booking: today when empty, never in the future.
func (s *Service) when(t time.Time) (time.Time, error) {
	today := DateOnly(s.books.Today())
	if t.IsZero() {
		return today, nil
	}
	if t = DateOnly(t); t.After(today) {
		return time.Time{}, apperr.InvalidField("date", "must not be in the future")
	}
	return t, nil
}

func (s *Service) note(ctx context.Context, actor, action, entity string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: entity, EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	if topic == "" {
		return nil
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: entity, AggregateID: id.String(), Payload: details})
}

// ---- bank accounts ----

// OpenBankAccount registers a bank account and its ledger account. Calling it again for the same ledger code
// returns the account that exists.
func (s *Service) OpenBankAccount(ctx context.Context, in OpenAccountInput) (BankAccount, error) {
	in, err := in.validate()
	if err != nil {
		return BankAccount{}, err
	}
	if in.Currency == "" {
		in.Currency = s.cfg.Currency
	}
	var out BankAccount
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		a := BankAccount{ID: uuid.New(), Name: in.Name, BankName: in.BankName, IBAN: in.IBAN, AccountNo: in.AccountNo, Currency: in.Currency,
			LedgerCode: in.LedgerCode, Active: true, CreatedAt: s.clock()}
		if a.LedgerCode == "" {
			a.LedgerCode = "bank.account." + a.ID.String()
		}
		if err := s.books.EnsureAccount(ctx, books.Account{Code: a.LedgerCode, Name: "Bank " + a.Name, Type: books.Asset, Currency: a.Currency,
			Coding: s.cfg.Accounts.BankCoding, OwnerKind: "bank_account", OwnerID: a.ID.String()}); err != nil {
			return err
		}
		stored, created, err := s.repo.InsertBankAccount(ctx, a)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			return nil
		}
		return s.note(ctx, in.Actor, "treasury.bank_account_opened", "bank_account", a.ID, "",
			map[string]any{"bank_account_id": a.ID, "name": a.Name, "ledger_code": a.LedgerCode})
	})
	return out, err
}

// BankAccount returns an account.
func (s *Service) BankAccount(ctx context.Context, id uuid.UUID) (BankAccount, error) {
	return s.repo.BankAccount(ctx, id)
}

// BankAccounts lists the accounts.
func (s *Service) BankAccounts(ctx context.Context) ([]BankAccount, error) {
	return s.repo.BankAccounts(ctx)
}

func (s *Service) usable(ctx context.Context, id uuid.UUID, cur money.Currency) (BankAccount, error) {
	a, err := s.repo.BankAccount(ctx, id)
	if err != nil {
		return a, err
	}
	if !a.Active {
		return a, apperr.New(apperr.KindConflict, "bank_account_inactive", "the bank account is not active")
	}
	if a.Currency != cur {
		return a, apperr.New(apperr.KindInvalid, "currency_mismatch", "the bank account is in "+string(a.Currency))
	}
	return a, nil
}

// Position is the cash position now: bank balances, cheques on their way, gateways' money not yet paid out.
func (s *Service) Position(ctx context.Context) (Position, error) {
	cur := s.cfg.Currency
	p := Position{Banks: money.Zero(cur), ChequesIn: money.Zero(cur), ChequesOut: money.Zero(cur)}
	accounts, err := s.repo.BankAccounts(ctx)
	if err != nil {
		return p, err
	}
	add := func(to *money.Money, v money.Money) error {
		var err error
		*to, err = to.Add(v)
		return err
	}
	for _, a := range accounts {
		bal, err := s.books.Balance(ctx, a.LedgerCode)
		if err != nil {
			return p, err
		}
		p.Accounts = append(p.Accounts, AccountBalance{Account: a, Balance: bal})
		if a.Active && a.Currency == cur {
			if err := add(&p.Banks, bal); err != nil {
				return p, err
			}
		}
	}
	sums, err := s.repo.ChequeSums(ctx)
	if err != nil {
		return p, err
	}
	for _, c := range sums {
		switch {
		case c.Direction == Receivable && (c.Status == InHand || c.Status == Deposited):
			err = add(&p.ChequesIn, c.Total)
		case c.Direction == Payable && (c.Status == Issued || c.Status == Bounced):
			err = add(&p.ChequesOut, c.Total)
		default:
			continue
		}
		if err != nil {
			return p, err
		}
		p.Cheques = append(p.Cheques, c)
	}
	if p.GatewayPending, err = s.books.Balance(ctx, s.cfg.Accounts.GatewayClearing); err != nil {
		return p, err
	}
	proj, err := p.Banks.Add(p.ChequesIn)
	if err == nil {
		proj, err = proj.Add(p.GatewayPending)
	}
	if err == nil {
		proj, err = proj.Sub(p.ChequesOut)
	}
	p.Projected = proj
	return p, err
}

// ---- cheques ----

// RegisterCheque records a cheque we received (Dr cheques in hand, Cr the customer's receivable) or issued (Dr the
// supplier's payable, Cr cheques payable). Idempotent per key; a Sayad id can be registered once per direction.
func (s *Service) RegisterCheque(ctx context.Context, in ChequeInput) (Cheque, error) {
	in, err := in.validate()
	if err != nil {
		return Cheque{}, err
	}
	var out Cheque
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if in.BankAccountID != nil {
			if _, err := s.usable(ctx, *in.BankAccountID, in.Amount.Currency()); err != nil {
				return err
			}
		}
		counter := in.CounterAccount
		if counter == "" {
			counter = s.cfg.Accounts.Receivable
			if in.Direction == Payable {
				counter = s.cfg.Accounts.Payable
			}
		}
		now := s.clock()
		c := Cheque{ID: uuid.New(), IdemKey: in.IdemKey, Direction: in.Direction, Status: in.Direction.Initial(), SayadID: in.SayadID, Number: in.Number,
			Amount: in.Amount, DueDate: in.DueDate, CounterpartyRef: in.CounterpartyRef, DrawerBank: in.DrawerBank, Note: in.Note,
			BankAccountID: in.BankAccountID, CounterAccount: counter, CreatedAt: now, UpdatedAt: now}
		stored, created, err := s.repo.InsertCheque(ctx, c)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			if stored.Direction != c.Direction || stored.SayadID != c.SayadID || stored.Number != c.Number || stored.Amount != c.Amount || stored.CounterpartyRef != c.CounterpartyRef {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different cheque")
			}
			return nil
		}
		lines := []books.Line{books.Dr(s.cfg.Accounts.ChequesInHand, c.Amount), books.Cr(counter, c.Amount)}
		desc, topic := "Cheque received "+c.Number, "treasury.cheque.received"
		if c.Direction == Payable {
			lines = []books.Line{books.Dr(counter, c.Amount), books.Cr(s.cfg.Accounts.ChequesPayable, c.Amount)}
			desc, topic = "Cheque issued "+c.Number, "treasury.cheque.issued"
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "cheque:" + c.ID.String() + ":open", Date: s.books.Today(), Description: desc,
			RefType: "cheque", RefID: c.ID.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		c.OpenJournal = &jid
		if err := s.repo.UpdateCheque(ctx, &c); err != nil {
			return err
		}
		if err := s.repo.AddChequeEvent(ctx, c.ID, ChequeEvent{To: c.Status, Actor: in.Actor, Journal: &jid, At: now}); err != nil {
			return err
		}
		out = c
		return s.note(ctx, in.Actor, topic, "cheque", c.ID, topic, chequePayload(c))
	})
	return out, err
}

func chequePayload(c Cheque) map[string]any {
	return map[string]any{"cheque_id": c.ID, "direction": c.Direction, "status": c.Status, "sayad_id": c.SayadID, "number": c.Number, "amount": c.Amount.Amount(),
		"currency": c.Amount.Currency(), "due_date": c.DueDate.Format("2006-01-02"), "counterparty_ref": c.CounterpartyRef, "reason": c.Reason}
}

// stepKey names a journal step: the first time plain, later times numbered, so a cheque presented again (or endorsed again) posts its own journals.
func (s *Service) stepKey(ctx context.Context, id uuid.UUID, step string, to ChequeStatus) (string, error) {
	hist, err := s.repo.ChequeEvents(ctx, id)
	if err != nil {
		return "", err
	}
	n := 0
	for _, e := range hist {
		if e.To == to {
			n++
		}
	}
	if n == 0 {
		return step, nil
	}
	return fmt.Sprintf("%s%d", step, n+1), nil
}

// move runs one lifecycle step under the cheque's row lock. Repeating a step the cheque already took is a no-op.
func (s *Service) move(ctx context.Context, id uuid.UUID, to ChequeStatus, actor, note string, do func(ctx context.Context, c *Cheque) (*uuid.UUID, error)) (Cheque, error) {
	var out Cheque
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		c, err := s.repo.Cheque(ctx, id, true)
		if err != nil {
			return err
		}
		out = c
		if c.Status == to {
			return nil
		}
		if !c.CanMoveTo(to) {
			return apperr.New(apperr.KindConflict, "cheque_"+string(c.Status), "a "+string(c.Status)+" "+string(c.Direction)+" cheque cannot become "+string(to))
		}
		from := c.Status
		jid, err := do(ctx, &c)
		if err != nil {
			return err
		}
		c.Status, c.UpdatedAt = to, s.clock()
		if err := s.repo.UpdateCheque(ctx, &c); err != nil {
			return err
		}
		if err := s.repo.AddChequeEvent(ctx, c.ID, ChequeEvent{From: from, To: to, Actor: actor, Note: note, Journal: jid, At: c.UpdatedAt}); err != nil {
			return err
		}
		out = c
		topic := "treasury.cheque." + string(to)
		return s.note(ctx, actor, topic, "cheque", c.ID, topic, chequePayload(c))
	})
	return out, err
}

func (s *Service) post(ctx context.Context, c Cheque, step string, date time.Time, desc, actor string, lines ...books.Line) (*uuid.UUID, error) {
	jid, err := s.books.Post(ctx, books.Entry{Key: "cheque:" + c.ID.String() + ":" + step, Date: date, Description: desc + " " + c.Number,
		RefType: "cheque", RefID: c.ID.String(), Actor: actor, Lines: lines})
	return &jid, err
}

func (s *Service) accountOf(ctx context.Context, c Cheque) (BankAccount, error) {
	if c.BankAccountID == nil {
		return BankAccount{}, apperr.New(apperr.KindConflict, "no_bank_account", "the cheque has no bank account")
	}
	return s.repo.BankAccount(ctx, *c.BankAccountID)
}

// DepositCheque hands a received cheque to the bank for collection (Dr in collection, Cr in hand).
func (s *Service) DepositCheque(ctx context.Context, id, account uuid.UUID, date time.Time, actor string) (Cheque, error) {
	d, err := s.when(date)
	if err != nil {
		return Cheque{}, err
	}
	return s.move(ctx, id, Deposited, actor, "", func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		if c.Direction != Receivable {
			return nil, apperr.New(apperr.KindConflict, "cheque_not_receivable", "only a received cheque is deposited")
		}
		if _, err := s.usable(ctx, account, c.Amount.Currency()); err != nil {
			return nil, err
		}
		c.BankAccountID = &account
		step, err := s.stepKey(ctx, c.ID, "deposit", Deposited)
		if err != nil {
			return nil, err
		}
		from, desc := s.cfg.Accounts.ChequesInHand, "Cheque deposited"
		if c.Status == Bounced {
			// presented again: the bounce put the claim back on the customer's receivable, so it leaves that account
			from, desc = c.CounterAccount, "Bounced cheque presented again"
		}
		jid, err := s.post(ctx, *c, step, d, desc, actor, books.Dr(s.cfg.Accounts.ChequesInCollection, c.Amount), books.Cr(from, c.Amount))
		c.DepositJournal = jid
		return jid, err
	})
}

// ClearCheque records that the bank paid the cheque: a received one adds to the bank account (Dr bank, Cr in
// collection), an issued one takes from it (Dr cheques payable, Cr bank).
func (s *Service) ClearCheque(ctx context.Context, id uuid.UUID, date time.Time, actor string) (Cheque, error) {
	d, err := s.when(date)
	if err != nil {
		return Cheque{}, err
	}
	return s.move(ctx, id, Cleared, actor, "", func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		acc, err := s.accountOf(ctx, *c)
		if err != nil {
			return nil, err
		}
		lines := []books.Line{books.Dr(acc.LedgerCode, c.Amount), books.Cr(s.cfg.Accounts.ChequesInCollection, c.Amount)}
		if c.Direction == Payable {
			lines = []books.Line{books.Dr(s.cfg.Accounts.ChequesPayable, c.Amount), books.Cr(acc.LedgerCode, c.Amount)}
		}
		jid, err := s.post(ctx, *c, "clear", d, "Cheque cleared", actor, lines...)
		c.FinalJournal = jid
		return jid, err
	})
}

// BounceInput records a cheque the bank did not pay.
type BounceInput struct {
	Reason string
	// Fee is the bank's charge for the bounce, taken from the bank account. Zero value: none.
	Fee   money.Money
	Date  time.Time
	Actor string
}

// BounceCheque records a cheque the bank refused. A received cheque goes back to the customer's receivable (Dr counter,
// Cr in collection); an issued one stays a liability, we still owe the supplier. A fee is booked against the bank.
func (s *Service) BounceCheque(ctx context.Context, id uuid.UUID, in BounceInput) (Cheque, error) {
	d, err := s.when(in.Date)
	if err != nil {
		return Cheque{}, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 500 {
		return Cheque{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	hasFee := in.Fee.Currency() != "" && !in.Fee.IsZero()
	if hasFee && (!in.Fee.Valid() || !in.Fee.IsPositive()) {
		return Cheque{}, apperr.InvalidField("fee", "must be a positive amount")
	}
	return s.move(ctx, id, Bounced, in.Actor, in.Reason, func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		var jid *uuid.UUID
		bounceKey, err := s.stepKey(ctx, c.ID, "bounce", Bounced)
		if err != nil {
			return nil, err
		}
		if c.Direction == Receivable {
			var err error
			if jid, err = s.post(ctx, *c, bounceKey, d, "Cheque bounced", in.Actor,
				books.Dr(c.CounterAccount, c.Amount), books.Cr(s.cfg.Accounts.ChequesInCollection, c.Amount)); err != nil {
				return nil, err
			}
		}
		if hasFee {
			if in.Fee.Currency() != c.Amount.Currency() {
				return nil, apperr.InvalidField("fee", "must be in the cheque's currency")
			}
			acc, err := s.accountOf(ctx, *c)
			if err != nil {
				return nil, err
			}
			fj, err := s.post(ctx, *c, bounceKey+"_fee", d, "Bounced cheque bank fee", in.Actor,
				books.Dr(s.cfg.Accounts.BankFees, in.Fee), books.Cr(acc.LedgerCode, in.Fee))
			if err != nil {
				return nil, err
			}
			if jid == nil {
				jid = fj
			}
		}
		c.FinalJournal, c.Reason = jid, in.Reason
		return jid, nil
	})
}

// ReturnCheque cancels a cheque that never went to the bank: a received one given back to the customer, an issued
// one taken back from the supplier. The receipt or the issue is reversed in the books.
func (s *Service) ReturnCheque(ctx context.Context, id uuid.UUID, reason, actor string) (Cheque, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return Cheque{}, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	return s.move(ctx, id, Returned, actor, reason, func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		if c.Status == Bounced {
			// the bounce already put the claim back on the receivable: nothing to reverse, the customer settles another way
			c.Reason = reason
			return c.FinalJournal, nil
		}
		if c.OpenJournal == nil {
			return nil, apperr.New(apperr.KindConflict, "cheque_not_booked", "the cheque has no journal to reverse")
		}
		jid, err := s.books.Reverse(ctx, *c.OpenJournal, "cheque:"+c.ID.String()+":return", "cheque returned: "+reason, actor)
		c.FinalJournal, c.Reason = &jid, reason
		return &jid, err
	})
}

// EndorseInput hands a received cheque to a supplier in payment.
type EndorseInput struct {
	// To is the supplier (reference) the cheque is handed to.
	To string
	// CounterAccount is debited (default the supplier payables account): the debt the cheque pays.
	CounterAccount string
	Date           time.Time
	Actor          string
}

// EndorseCheque pays a supplier with a received cheque that is in our hands (خرج چک): Dr the supplier's payable, Cr cheques in hand. The cheque is then the supplier's to collect;
// if it comes back, TakeBackCheque reverses this. Only an in-hand received cheque can be endorsed.
func (s *Service) EndorseCheque(ctx context.Context, id uuid.UUID, in EndorseInput) (Cheque, error) {
	d, err := s.when(in.Date)
	if err != nil {
		return Cheque{}, err
	}
	in.To = strings.TrimSpace(in.To)
	if in.To == "" || len(in.To) > 200 {
		return Cheque{}, apperr.InvalidField("to", "required, at most 200 characters")
	}
	counter := in.CounterAccount
	if counter == "" {
		counter = s.cfg.Accounts.Payable
	}
	return s.move(ctx, id, Endorsed, in.Actor, "endorsed to "+in.To, func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		if c.Direction != Receivable {
			return nil, apperr.New(apperr.KindConflict, "cheque_not_receivable", "only a received cheque is endorsed")
		}
		step, err := s.stepKey(ctx, c.ID, "endorse", Endorsed)
		if err != nil {
			return nil, err
		}
		return s.post(ctx, *c, step, d, "Cheque endorsed to "+in.To, in.Actor, books.Dr(counter, c.Amount), books.Cr(s.cfg.Accounts.ChequesInHand, c.Amount))
	})
}

// TakeBackCheque records that the supplier gave an endorsed cheque back (it stays ours): the endorsement is reversed and the cheque is in hand again.
func (s *Service) TakeBackCheque(ctx context.Context, id uuid.UUID, actor string) (Cheque, error) {
	return s.move(ctx, id, InHand, actor, "taken back from the endorsee", func(ctx context.Context, c *Cheque) (*uuid.UUID, error) {
		hist, err := s.repo.ChequeEvents(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		var endorse *ChequeEvent
		for i := range hist {
			if hist[i].To == Endorsed {
				endorse = &hist[i]
			}
		}
		if endorse == nil || endorse.Journal == nil {
			return nil, apperr.New(apperr.KindConflict, "no_endorsement", "the cheque has no endorsement journal to reverse")
		}
		n := 0
		for _, e := range hist {
			if e.To == InHand {
				n++
			}
		}
		jid, err := s.books.Reverse(ctx, *endorse.Journal, fmt.Sprintf("cheque:%s:endorse_back%d", c.ID, n+1), "cheque taken back from the endorsee", actor)
		return &jid, err
	})
}

// Cheque returns a cheque.
func (s *Service) Cheque(ctx context.Context, id uuid.UUID) (Cheque, error) {
	return s.repo.Cheque(ctx, id, false)
}

// ChequeHistory returns the status changes of a cheque.
func (s *Service) ChequeHistory(ctx context.Context, id uuid.UUID) ([]ChequeEvent, error) {
	return s.repo.ChequeEvents(ctx, id)
}

// ListCheques lists cheques with the client's filters, sort and pagination (by due date, soonest first, by default).
func (s *Service) ListCheques(ctx context.Context, p query.Params) (query.Page[Cheque], error) {
	return s.repo.ListCheques(ctx, p)
}

// ---- gateway settlement ----

// RecordSettlement books a gateway payout to the bank: Dr bank (net), Dr gateway fees (fee), Cr gateway clearing
// (gross). It refuses a payout larger than what the gateway clearing account holds (a payment is missing from
// the books), and it is idempotent per (gateway, ref).
func (s *Service) RecordSettlement(ctx context.Context, in SettlementInput) (Settlement, error) {
	in, err := in.validate()
	if err != nil {
		return Settlement{}, err
	}
	date, err := s.when(in.SettledOn)
	if err != nil {
		return Settlement{}, err
	}
	var out Settlement
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		acc, err := s.usable(ctx, in.AccountID, in.Gross.Currency())
		if err != nil {
			return err
		}
		net, err := in.Gross.Sub(in.Fee)
		if err != nil {
			return apperr.Invalid("invalid amounts")
		}
		key := "settlement:" + in.Gateway + ":" + in.Ref
		bal, err := s.books.Balance(ctx, s.cfg.Accounts.GatewayClearing)
		if err != nil {
			return err
		}
		// A retry has the journal already; only a new payout is checked against the clearing balance.
		stored, found, err := s.settlementByKey(ctx, in)
		if err != nil {
			return err
		}
		if found {
			out = stored
			return nil
		}
		if c, _ := in.Gross.Cmp(bal); c > 0 {
			return apperr.New(apperr.KindConflict, "exceeds_clearing", "the payout is larger than what the gateway clearing account holds: a payment is missing from the books")
		}
		lines := []books.Line{books.Dr(acc.LedgerCode, net)}
		if in.FeeVAT.IsPositive() && s.cfg.Accounts.VATInput == "" {
			return apperr.New(apperr.KindConflict, "no_vat_account", "no input-VAT account configured for the fee's VAT")
		}
		if feeNet, _ := in.Fee.Sub(in.FeeVAT); feeNet.IsPositive() {
			lines = append(lines, books.Dr(s.cfg.Accounts.GatewayFees, feeNet))
		}
		if in.FeeVAT.IsPositive() {
			lines = append(lines, books.Dr(s.cfg.Accounts.VATInput, in.FeeVAT))
		}
		lines = append(lines, books.Cr(s.cfg.Accounts.GatewayClearing, in.Gross))
		jid, err := s.books.Post(ctx, books.Entry{Key: key, Date: date, Description: "Gateway settlement " + in.Gateway + " " + in.Ref,
			RefType: "gateway_settlement", RefID: in.Gateway + ":" + in.Ref, Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		st := Settlement{ID: uuid.New(), Gateway: in.Gateway, Ref: in.Ref, AccountID: acc.ID, Gross: in.Gross, Fee: in.Fee, Net: net, SettledOn: date,
			JournalID: jid, Note: in.Note, Actor: in.Actor, CreatedAt: s.clock()}
		stored, created, err := s.repo.InsertSettlement(ctx, st)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			return nil
		}
		return s.note(ctx, in.Actor, "treasury.gateway_settled", "gateway_settlement", st.ID, "treasury.gateway_settled", map[string]any{
			"settlement_id": st.ID, "gateway": st.Gateway, "ref": st.Ref, "gross": st.Gross.Amount(), "fee": st.Fee.Amount(), "net": st.Net.Amount(),
			"currency": st.Gross.Currency(), "bank_account_id": st.AccountID, "journal_id": jid})
	})
	return out, err
}

// settlementByKey returns the payout recorded for (gateway, ref), and fails when it was recorded with other amounts.
func (s *Service) settlementByKey(ctx context.Context, in SettlementInput) (Settlement, bool, error) {
	st, err := s.repo.SettlementByRef(ctx, in.Gateway, in.Ref)
	if apperr.Is(err, apperr.KindNotFound) {
		return Settlement{}, false, nil
	}
	if err != nil {
		return Settlement{}, false, err
	}
	if st.Gross != in.Gross || st.Fee != in.Fee || st.AccountID != in.AccountID {
		return Settlement{}, false, apperr.New(apperr.KindConflict, "settlement_ref_reused", "this gateway reference was recorded with other amounts or another account")
	}
	return st, true, nil
}

// Settlement returns a payout.
func (s *Service) Settlement(ctx context.Context, id uuid.UUID) (Settlement, error) {
	return s.repo.Settlement(ctx, id, false)
}

// ListSettlements lists payouts.
func (s *Service) ListSettlements(ctx context.Context, p query.Params) (query.Page[Settlement], error) {
	return s.repo.ListSettlements(ctx, p)
}
