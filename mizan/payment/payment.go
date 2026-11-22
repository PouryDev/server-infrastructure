// Package payment is the payment context: a strict state machine for online payments, gateway adapters
// behind one interface, server-side verification of callbacks, idempotent creation, refunds, and the
// recovery of payments whose callback was lost.
//
// A payment never talks to the ledger. When it is paid or refunded it calls the Settlement registered for
// its purpose ("invoice", "wallet_topup", ...), inside the same transaction; that handler decides what the
// payment means (mark an invoice paid, credit a wallet) and posts the accounting entry. A purpose without a
// handler still works, it simply has no accounting effect.
package payment

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Status of a payment.
//
//	created ─► pending ─► paid ─► partially_refunded ─► refunded
//	   │          │        └──────────────────────────────▲
//	   │          ├─► failed
//	   └──────────┴─► expired            (created may also go straight to failed)
type Status string

const (
	Created           Status = "created"
	Pending           Status = "pending"
	Paid              Status = "paid"
	Failed            Status = "failed"
	Expired           Status = "expired"
	PartiallyRefunded Status = "partially_refunded"
	Refunded          Status = "refunded"
)

var transitions = map[Status][]Status{
	Created:           {Pending, Failed, Expired},
	Pending:           {Paid, Failed, Expired},
	Paid:              {PartiallyRefunded, Refunded},
	PartiallyRefunded: {PartiallyRefunded, Refunded},
}

// CanTransitionTo reports whether the state machine allows s -> to.
func (s Status) CanTransitionTo(to Status) bool {
	for _, t := range transitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// Terminal states can never change again (a fully refunded payment is final).
func (s Status) Terminal() bool { return len(transitions[s]) == 0 }

// Purpose says what the payment is for; the Settlement of that type is called when it is paid.
type Purpose struct{ Type, ID string }

// Payment is the aggregate.
type Payment struct {
	ID             uuid.UUID
	IdemKey        string
	Status         Status
	Purpose        Purpose
	PayerRef       string
	Amount         money.Money
	Gateway        string
	GatewayRef     string // issued by the gateway when the payment is requested (authority / id)
	GatewayTxnRef  string // reference number after verification
	CardPAN        string // masked
	Description    string
	CallbackURL    string
	PayURL         string
	ExpiresAt      time.Time
	PaidAt         *time.Time
	FailureCode    string
	FailureReason  string
	Refunded       money.Money
	SettledJournal *uuid.UUID
	Metadata       map[string]string
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Transition is one recorded status change.
type Transition struct {
	From, To Status
	Reason   string
	At       time.Time
}

func (p *Payment) move(to Status) error {
	if !p.Status.CanTransitionTo(to) {
		return apperr.New(apperr.KindConflict, "invalid_transition", fmt.Sprintf("a %s payment cannot become %s", p.Status, to))
	}
	p.Status = to
	return nil
}

// MarkPending records the gateway's answer to the payment request.
func (p *Payment) MarkPending(gatewayRef, payURL string) error {
	if gatewayRef == "" {
		return apperr.Invalid("gateway returned no reference")
	}
	if err := p.move(Pending); err != nil {
		return err
	}
	p.GatewayRef, p.PayURL = gatewayRef, payURL
	return nil
}

// MarkPaid records a verified payment.
func (p *Payment) MarkPaid(txnRef, cardPAN string, at time.Time) error {
	if err := p.move(Paid); err != nil {
		return err
	}
	p.GatewayTxnRef, p.CardPAN, p.PaidAt = txnRef, cardPAN, &at
	return nil
}

// MarkFailed records a definitive failure.
func (p *Payment) MarkFailed(code, reason string) error {
	if err := p.move(Failed); err != nil {
		return err
	}
	p.FailureCode, p.FailureReason = code, reason
	return nil
}

// MarkExpired records that the payer never completed the payment.
func (p *Payment) MarkExpired() error { return p.move(Expired) }

// Refundable is what can still be refunded.
func (p *Payment) Refundable() money.Money {
	r, _ := p.Amount.Sub(p.Refunded)
	return r
}

// ApplyRefund adds a completed refund and moves to partially_refunded or refunded.
func (p *Payment) ApplyRefund(amount money.Money) error {
	if !amount.IsPositive() || amount.Currency() != p.Amount.Currency() {
		return apperr.Invalid("invalid refund amount")
	}
	total, err := p.Refunded.Add(amount)
	if err != nil {
		return apperr.Invalid("invalid refund amount")
	}
	if c, _ := total.Cmp(p.Amount); c > 0 {
		return apperr.New(apperr.KindConflict, "refund_exceeds_payment", "the refund is larger than what is left of the payment")
	}
	next := PartiallyRefunded
	if c, _ := total.Cmp(p.Amount); c == 0 {
		next = Refunded
	}
	if err := p.move(next); err != nil {
		return err
	}
	p.Refunded = total
	return nil
}

// InitiateInput creates a payment.
type InitiateInput struct {
	// IdemKey makes creation safe to retry: the same key returns the same payment. Derive it from the
	// business intent ("invoice:<id>:attempt:1"), not from a random value.
	IdemKey     string
	Purpose     Purpose
	PayerRef    string
	Amount      money.Money
	Gateway     string // default: the service's default gateway
	Description string
	ReturnURL   string        // where the payer's browser goes after the callback; must be on an allowed host
	ExpiresIn   time.Duration // default 30 minutes
	Metadata    map[string]string
	Actor       string
}

// Validate checks the input.
func (in InitiateInput) Validate() error {
	var fe []apperr.FieldError
	add := func(f, m string) { fe = append(fe, apperr.FieldError{Field: f, Message: m}) }
	if k := strings.TrimSpace(in.IdemKey); k == "" || len(k) > 200 {
		add("idempotency_key", "required, at most 200 characters")
	}
	if in.Purpose.Type == "" || in.Purpose.ID == "" {
		add("purpose", "type and id are required")
	}
	if !in.Amount.Valid() || !in.Amount.IsPositive() {
		add("amount", "must be a positive amount")
	}
	if in.ExpiresIn < 0 || in.ExpiresIn > 24*time.Hour {
		add("expires_in", "must be between 0 and 24 hours")
	}
	if len(in.Description) > 500 {
		add("description", "at most 500 characters")
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid payment", fe...)
	}
	return nil
}
