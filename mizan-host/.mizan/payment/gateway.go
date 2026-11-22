package payment

import (
	"context"
	"errors"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Gateway errors. Adapters return apperr.Unavailable-style (any other) errors for transient problems such as
// timeouts; only these two sentinels are definitive answers.
var (
	// ErrRejected: the gateway refused the request for good (bad merchant, invalid amount).
	ErrRejected = errors.New("payment: rejected by the gateway")
	// ErrNotSupported: the gateway has no API for the operation (refunds on most Iranian gateways).
	ErrNotSupported = errors.New("payment: operation not supported by the gateway")
)

// RejectedError carries the gateway's code and message with ErrRejected.
type RejectedError struct {
	Code    string
	Message string
}

func (e *RejectedError) Error() string {
	return "gateway rejected the request (" + e.Code + "): " + e.Message
}
func (e *RejectedError) Is(t error) bool { return t == ErrRejected }

// Gateway is a payment service provider (Zarinpal, IDPay, ...).
type Gateway interface {
	Name() string
	// Request registers the payment and returns where to send the payer.
	Request(ctx context.Context, in RequestInput) (RequestResult, error)
	// ParseCallback reads the parameters (query and form merged) the gateway sends to the callback URL.
	// It must not trust them: Verify is the only proof of payment.
	ParseCallback(params map[string]string) (Callback, error)
	// Verify asks the gateway whether the payment really succeeded. Paid=false with a nil error is a
	// definitive "not paid"; any error means "unknown, try again".
	Verify(ctx context.Context, in VerifyInput) (VerifyResult, error)
}

// Refunder is implemented by gateways with a refund API.
type Refunder interface {
	Refund(ctx context.Context, in GatewayRefundInput) (GatewayRefundResult, error)
}

// Reporter is implemented by gateways that can list their transactions (used by reconciliation).
type Reporter interface {
	Report(ctx context.Context, from, to time.Time) ([]ReportItem, error)
}

type RequestInput struct {
	PaymentID   string
	Amount      money.Money
	CallbackURL string
	Description string
	PayerRef    string
	Mobile      string
	Email       string
}

type RequestResult struct {
	Ref    string
	PayURL string
}

type Callback struct {
	Ref         string // the reference to find our payment by
	ClaimedPaid bool   // what the payer's browser claims; never used as proof
	EventKey    string // identifies the event for deduplication
}

type VerifyInput struct {
	Ref    string
	Amount money.Money
	// PaymentID is our id; some gateways (IDPay) need the order id again.
	PaymentID string
}

type VerifyResult struct {
	Paid    bool
	TxnRef  string
	CardPAN string
	Amount  *money.Money // the amount the gateway says it collected, when it reports one
	Code    string
	Message string
}

type GatewayRefundInput struct {
	Ref    string
	TxnRef string
	Amount money.Money
	Reason string
}

type GatewayRefundResult struct{ Ref string }

// ReportItem is one transaction in a gateway's own records.
type ReportItem struct {
	Ref     string
	TxnRef  string
	Amount  money.Money
	Paid    bool
	At      time.Time
	Details string
}
