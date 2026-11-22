// Package gatewaytest is an in-memory payment.Gateway for tests: it behaves like a real provider (issues
// references, lets "the payer" pay, answers verification) and can be told to fail.
package gatewaytest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
)

// Gateway is a fake gateway named "fake".
type Gateway struct {
	mu       sync.Mutex
	n        int
	sessions map[string]*session

	RequestErr  error // returned by Request when set
	VerifyErr   error // returned by Verify when set (transient failure)
	RefundErr   error
	NoRefunds   bool // when true Refund reports ErrNotSupported
	VerifyCalls int
	Collected   *money.Money // when set, Verify reports this amount instead of the requested one
}

type session struct {
	amount money.Money
	paid   bool
	txn    string
	at     time.Time
}

// New creates the fake.
func New() *Gateway { return &Gateway{sessions: map[string]*session{}} }

func (g *Gateway) Name() string { return "fake" }

func (g *Gateway) Request(_ context.Context, in payment.RequestInput) (payment.RequestResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.RequestErr != nil {
		return payment.RequestResult{}, g.RequestErr
	}
	g.n++
	ref := fmt.Sprintf("FAKE%06d", g.n)
	g.sessions[ref] = &session{amount: in.Amount}
	return payment.RequestResult{Ref: ref, PayURL: "https://pay.example/" + ref}, nil
}

// Pay simulates the payer completing the payment at the gateway.
func (g *Gateway) Pay(ref string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[ref]
	s.paid, s.txn, s.at = true, "TXN-"+ref, time.Now()
}

func (g *Gateway) ParseCallback(p map[string]string) (payment.Callback, error) {
	if p["ref"] == "" {
		return payment.Callback{}, errors.New("missing ref")
	}
	return payment.Callback{Ref: p["ref"], ClaimedPaid: p["status"] == "OK", EventKey: p["ref"] + ":" + p["status"]}, nil
}

func (g *Gateway) Verify(_ context.Context, in payment.VerifyInput) (payment.VerifyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.VerifyCalls++
	if g.VerifyErr != nil {
		return payment.VerifyResult{}, g.VerifyErr
	}
	s, ok := g.sessions[in.Ref]
	if !ok || !s.paid {
		return payment.VerifyResult{Code: "not_paid", Message: "no successful payment"}, nil
	}
	amt := s.amount
	if g.Collected != nil {
		amt = *g.Collected
	}
	return payment.VerifyResult{Paid: true, TxnRef: s.txn, CardPAN: "603799******1234", Amount: &amt}, nil
}

func (g *Gateway) Refund(_ context.Context, in payment.GatewayRefundInput) (payment.GatewayRefundResult, error) {
	if g.NoRefunds {
		return payment.GatewayRefundResult{}, payment.ErrNotSupported
	}
	if g.RefundErr != nil {
		return payment.GatewayRefundResult{}, g.RefundErr
	}
	return payment.GatewayRefundResult{Ref: "RF-" + in.Ref}, nil
}
