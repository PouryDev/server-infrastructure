package payment

import (
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// RefundMethod says how the money goes back to the payer.
type RefundMethod string

const (
	// RefundViaGateway: the gateway's own refund API returns the money (books: bank.gateway_clearing).
	RefundViaGateway RefundMethod = "gateway"
	// RefundManualCardToCard: a human agent transfers the money card-to-card (کارت به کارت) from the company's
	// bank account, outside this system, and records the bank's tracking number (books: bank.main).
	RefundManualCardToCard RefundMethod = "manual_card_to_card"
)

// RefundStatus of a refund request.
//
//	pending ─► processing ─► done                  manual refunds: an agent claims it, pays it, records the transfer
//	   │  ▲        │
//	   │  └────────┤ (release)
//	   ├────────────┴──► failed | cancelled        the reserved amount is released
//
// A gateway refund goes pending ─► done | failed directly.
type RefundStatus string

const (
	RefundPending    RefundStatus = "pending"    // requested, waiting for the gateway or for an agent
	RefundProcessing RefundStatus = "processing" // claimed by one agent who is paying it out
	RefundDone       RefundStatus = "done"
	RefundFailed     RefundStatus = "failed"
	RefundCancelled  RefundStatus = "cancelled"
)

// Reserved reports whether a refund in this status still holds its amount against the payment.
func (s RefundStatus) Reserved() bool { return s == RefundPending || s == RefundProcessing }

// Refund is a (partial) return of money to the payer.
//
// A manual refund never carries a full card number or a CVV: the agent types the destination card in the
// bank's own app, and the system keeps only its masked form and the bank's tracking number.
type Refund struct {
	ID        uuid.UUID
	PaymentID uuid.UUID
	IdemKey   string
	Amount    money.Money
	Reason    string
	Status    RefundStatus
	Method    RefundMethod

	GatewayRef string // gateway refunds: the gateway's reference
	Failure    string // why it failed or was cancelled
	JournalID  *uuid.UUID

	// Manual payout.
	ClaimedBy             string // agent id while processing (kept after completion as the payer of record)
	ClaimedAt             *time.Time
	DestinationCardMasked string // e.g. 603799******1234, never more than the first 6 and last 4 digits
	TrackingNumber        string // the bank's tracking number (شماره پیگیری) of the card-to-card transfer
	PaidAt                *time.Time
	Note                  string // optional receipt note

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Manual reports whether a human pays this refund out.
func (r Refund) Manual() bool { return r.Method == RefundManualCardToCard }

func notManual() error {
	return apperr.New(apperr.KindConflict, "refund_not_manual", "only manual refunds are handled by agents")
}

func badState(r Refund, want string) error {
	return apperr.New(apperr.KindConflict, "refund_"+string(r.Status), "the refund is "+string(r.Status)+", expected "+want)
}

// Claim lets an agent take a pending manual refund. Claiming again as the same agent is a no-op (a retry);
// a refund held by another agent cannot be taken. changed=false means nothing happened.
func (r *Refund) Claim(agent string, at time.Time) (changed bool, err error) {
	if !r.Manual() {
		return false, notManual()
	}
	if strings.TrimSpace(agent) == "" {
		return false, apperr.InvalidField("agent", "required")
	}
	switch {
	case r.Status == RefundProcessing && r.ClaimedBy == agent:
		return false, nil
	case r.Status == RefundProcessing:
		return false, apperr.New(apperr.KindConflict, "refund_already_claimed", "another agent is already handling this refund")
	case r.Status != RefundPending:
		return false, badState(*r, "pending")
	}
	r.Status, r.ClaimedBy, r.ClaimedAt = RefundProcessing, agent, &at
	return true, nil
}

// canAct is true for the agent who holds the claim, or for anyone when force is set (a supervisor freeing
// a refund whose agent went away).
func (r Refund) canAct(agent string, force bool) error {
	if force || r.ClaimedBy == agent {
		return nil
	}
	return apperr.New(apperr.KindConflict, "refund_claimed_by_other", "the refund is held by another agent")
}

// Release hands a claimed refund back to the queue. Releasing a pending refund is a no-op.
func (r *Refund) Release(agent string, force bool) (changed bool, err error) {
	if !r.Manual() {
		return false, notManual()
	}
	switch r.Status {
	case RefundPending:
		return false, nil
	case RefundProcessing:
		if err := r.canAct(agent, force); err != nil {
			return false, err
		}
		r.Status, r.ClaimedBy, r.ClaimedAt = RefundPending, "", nil
		return true, nil
	}
	return false, badState(*r, "processing")
}

// CompleteManualInput is what the agent records after the transfer.
type CompleteManualInput struct {
	Agent                 string
	TrackingNumber        string
	PaidAt                time.Time
	DestinationCardMasked string
	Note                  string
}

// Complete records the transfer and finishes the refund. Repeating it with the same tracking number is a
// no-op; a different one on a finished refund is a conflict.
func (r *Refund) CompleteManual(in CompleteManualInput, now time.Time) (changed bool, err error) {
	if !r.Manual() {
		return false, notManual()
	}
	in, err = in.normalized(now)
	if err != nil {
		return false, err
	}
	if r.Status == RefundDone {
		if r.TrackingNumber == in.TrackingNumber {
			return false, nil
		}
		return false, apperr.New(apperr.KindConflict, "refund_done", "the refund is already done with another tracking number")
	}
	if r.Status != RefundProcessing {
		return false, badState(*r, "processing: claim it first")
	}
	if err := r.canAct(in.Agent, false); err != nil {
		return false, err
	}
	r.Status, r.TrackingNumber, r.DestinationCardMasked, r.Note, r.PaidAt = RefundDone, in.TrackingNumber, in.DestinationCardMasked, in.Note, &in.PaidAt
	return true, nil
}

// Fail marks a claimed manual refund as failed (the transfer did not go through); the amount is released.
func (r *Refund) Fail(agent, reason string, force bool) (changed bool, err error) {
	return r.end(RefundFailed, agent, reason, force)
}

// Cancel withdraws a pending or claimed manual refund; the amount is released.
func (r *Refund) Cancel(agent, reason string, force bool) (changed bool, err error) {
	return r.end(RefundCancelled, agent, reason, force)
}

func (r *Refund) end(to RefundStatus, agent, reason string, force bool) (bool, error) {
	if !r.Manual() {
		return false, notManual()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxText {
		return false, apperr.InvalidField("reason", "required, at most 500 characters")
	}
	if containsCardNumber(reason) {
		return false, apperr.InvalidField("reason", "must not contain a card number")
	}
	switch r.Status {
	case to:
		return false, nil
	case RefundProcessing:
		if err := r.canAct(agent, force); err != nil {
			return false, err
		}
	case RefundPending:
		if to == RefundFailed { // an agent has to try before it can fail
			return false, badState(*r, "processing")
		}
	default:
		return false, badState(*r, "pending or processing")
	}
	r.Status, r.Failure = to, reason
	return true, nil
}

const maxText = 500

var (
	maskedCard     = regexp.MustCompile(`^[0-9]{0,6}\*{6,16}[0-9]{0,4}$`)
	trackingNumber = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._/-]{3,39}$`)
)

// normalizeDigits turns Persian and Arabic-Indic digits into ASCII so agents can type in any keyboard layout.
func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		}
		return r
	}, s)
}

// ValidateMaskedCard accepts a 16-character card with at most the first 6 and the last 4 digits visible
// (603799******1234, ************1234); a full card number is refused, never stored.
func ValidateMaskedCard(s string) (string, error) {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return r
	}, normalizeDigits(s))
	s = strings.ReplaceAll(s, "•", "*")
	if len(s) != 16 || !maskedCard.MatchString(s) {
		return "", apperr.InvalidField("destination_card_masked",
			"must be 16 characters with only the first 6 and last 4 digits visible, e.g. 603799******1234 (never the full card number)")
	}
	return s, nil
}

// ValidateTrackingNumber checks the bank's tracking number: 4-40 characters of digits, letters and ._/-.
// A Luhn-valid 16-digit value is a card number typed into the wrong field and is refused.
func ValidateTrackingNumber(s string) (string, error) {
	s = strings.TrimSpace(normalizeDigits(s))
	if !trackingNumber.MatchString(s) || (len(s) == 16 && luhn(s)) {
		return "", apperr.InvalidField("tracking_number", "required: 4-40 characters (digits, letters, . _ / -), not a card number")
	}
	return s, nil
}

func (in CompleteManualInput) normalized(now time.Time) (CompleteManualInput, error) {
	var err error
	if strings.TrimSpace(in.Agent) == "" {
		return in, apperr.InvalidField("agent", "required")
	}
	if in.TrackingNumber, err = ValidateTrackingNumber(in.TrackingNumber); err != nil {
		return in, err
	}
	if in.DestinationCardMasked, err = ValidateMaskedCard(in.DestinationCardMasked); err != nil {
		return in, err
	}
	if in.PaidAt.IsZero() || in.PaidAt.After(now.Add(5*time.Minute)) {
		return in, apperr.InvalidField("paid_at", "required and not in the future")
	}
	in.PaidAt = in.PaidAt.UTC()
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > maxText {
		return in, apperr.InvalidField("note", "at most 500 characters")
	}
	if containsCardNumber(in.Note) {
		return in, apperr.InvalidField("note", "must not contain a card number")
	}
	return in, nil
}

// containsCardNumber finds a Luhn-valid run of 13-19 digits (spaces and dashes between digits ignored),
// the shape of a real card number, so one cannot slip into a free-text field.
func containsCardNumber(s string) bool {
	var run []byte
	flush := func() bool {
		defer func() { run = run[:0] }()
		return len(run) >= 13 && len(run) <= 19 && luhn(string(run))
	}
	for _, r := range normalizeDigits(s) {
		switch {
		case r >= '0' && r <= '9':
			run = append(run, byte(r))
		case (r == ' ' || r == '-') && len(run) > 0:
		default:
			if flush() {
				return true
			}
		}
	}
	return flush()
}

func luhn(digits string) bool {
	sum, alt := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if alt {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}
