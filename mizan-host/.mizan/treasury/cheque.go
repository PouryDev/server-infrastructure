package treasury

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Direction of a cheque.
type Direction string

const (
	Receivable Direction = "receivable" // چک دریافتنی: a customer paid us with it
	Payable    Direction = "payable"    // چک پرداختنی: we paid a supplier with it
)

// ChequeStatus is the lifecycle position of a cheque.
//
//	received:  in_hand ─► deposited ─► cleared | bounced          in_hand ─► returned (given back unpaid)
//	           bounced ─► deposited (presented again) | returned (the customer pays another way)
//	           in_hand ─► endorsed (handed to a supplier in payment) ─► in_hand (the supplier gave it back)
//	issued:    issued ─► cleared | bounced | returned             bounced ─► cleared (paid after all)
type ChequeStatus string

const (
	InHand    ChequeStatus = "in_hand"   // received, in our safe (نزد صندوق)
	Issued    ChequeStatus = "issued"    // we handed it out
	Deposited ChequeStatus = "deposited" // given to the bank for collection (در جریان وصول)
	Cleared   ChequeStatus = "cleared"   // paid: money moved in the bank
	Bounced   ChequeStatus = "bounced"   // the bank did not pay it (برگشتی)
	Returned  ChequeStatus = "returned"  // taken back, never collected: the receipt or the issue is cancelled
	Endorsed  ChequeStatus = "endorsed"  // a received cheque handed on to a supplier in payment (خرج چک / واگذاری)
)

var chequeMoves = map[Direction]map[ChequeStatus][]ChequeStatus{
	Receivable: {InHand: {Deposited, Returned, Endorsed}, Deposited: {Cleared, Bounced}, Bounced: {Deposited, Returned}, Endorsed: {InHand}},
	Payable:    {Issued: {Cleared, Bounced, Returned}, Bounced: {Cleared}},
}

// Initial is the status a new cheque of the direction starts in.
func (d Direction) Initial() ChequeStatus {
	if d == Payable {
		return Issued
	}
	return InHand
}

// CanMoveTo reports whether the lifecycle allows the cheque to become `to`.
func (c Cheque) CanMoveTo(to ChequeStatus) bool {
	for _, t := range chequeMoves[c.Direction][c.Status] {
		if t == to {
			return true
		}
	}
	return false
}

// Open reports whether the cheque still owes or is owed money.
func (c Cheque) Open() bool {
	return c.Status != Cleared && c.Status != Returned
}

// Cheque is a cheque received from or issued to a counterparty.
type Cheque struct {
	ID              uuid.UUID
	IdemKey         string
	Direction       Direction
	Status          ChequeStatus
	SayadID         string // شناسه صیادی, 16 digits
	Number          string
	Amount          money.Money
	DueDate         time.Time
	CounterpartyRef string // the customer or supplier
	DrawerBank      string
	Note            string
	BankAccountID   *uuid.UUID
	CounterAccount  string // ledger account debited on receipt / credited on issue (and back on a bounce)
	OpenJournal     *uuid.UUID
	DepositJournal  *uuid.UUID
	FinalJournal    *uuid.UUID
	Reason          string // why it bounced or was returned
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ChequeEvent is one recorded status change.
type ChequeEvent struct {
	From, To ChequeStatus
	Actor    string
	Note     string
	Journal  *uuid.UUID
	At       time.Time
}

// ChequeInput registers a cheque we received or issued.
type ChequeInput struct {
	// IdemKey makes a retry return the same cheque (derive it from the business intent).
	IdemKey         string
	Direction       Direction
	SayadID         string
	Number          string
	Amount          money.Money
	DueDate         time.Time
	CounterpartyRef string
	DrawerBank      string
	Note            string
	// BankAccountID: for an issued cheque the account it is drawn on (required); for a received one empty.
	BankAccountID *uuid.UUID
	// CounterAccount overrides the default counter account (customer receivables / supplier payables).
	CounterAccount string
	Actor          string
}

var sayadRe = regexp.MustCompile(`^[0-9]{16}$`)

// ValidateSayadID checks the 16-digit Sayad identifier (شناسه صیادی) printed on the cheque. Only the shape is
// checked: the identifier's internal structure is not public, so no checksum is guessed. Empty is allowed.
func ValidateSayadID(s string) (string, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, normalizeDigits(s))
	if s != "" && !sayadRe.MatchString(s) {
		return "", apperr.InvalidField("sayad_id", "must be exactly 16 digits")
	}
	return s, nil
}

func (in ChequeInput) validate() (ChequeInput, error) {
	var err error
	in.IdemKey, in.Number = strings.TrimSpace(in.IdemKey), strings.TrimSpace(normalizeDigits(in.Number))
	in.CounterpartyRef, in.DrawerBank, in.Note = strings.TrimSpace(in.CounterpartyRef), strings.TrimSpace(in.DrawerBank), strings.TrimSpace(in.Note)
	if in.SayadID, err = ValidateSayadID(in.SayadID); err != nil {
		return in, err
	}
	switch {
	case in.IdemKey == "" || len(in.IdemKey) > 200:
		return in, apperr.InvalidField("idempotency_key", "required, at most 200 characters")
	case in.Direction != Receivable && in.Direction != Payable:
		return in, apperr.InvalidField("direction", "receivable or payable")
	case in.Number == "" || len(in.Number) > 40:
		return in, apperr.InvalidField("number", "required, at most 40 characters")
	case !in.Amount.Valid() || !in.Amount.IsPositive():
		return in, apperr.InvalidField("amount", "must be a positive amount")
	case in.DueDate.IsZero():
		return in, apperr.InvalidField("due_date", "required")
	case in.CounterpartyRef == "" || len(in.CounterpartyRef) > 200:
		return in, apperr.InvalidField("counterparty_ref", "required, at most 200 characters")
	case len(in.DrawerBank) > 120 || len(in.Note) > 500:
		return in, apperr.Invalid("drawer_bank (120) or note (500) too long")
	case in.Direction == Payable && in.BankAccountID == nil:
		return in, apperr.InvalidField("bank_account_id", "an issued cheque must name the account it is drawn on")
	case in.Direction == Receivable && in.BankAccountID != nil:
		return in, apperr.InvalidField("bank_account_id", "a received cheque gets its account when it is deposited")
	}
	in.DueDate = DateOnly(in.DueDate)
	return in, nil
}

// DateOnly drops the time of day (UTC).
func DateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
