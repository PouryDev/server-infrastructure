package einvoice

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in a transaction that repositories join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// Insert stores a document; the same (source type, source id) returns the stored one with created=false.
	Insert(ctx context.Context, d Document) (stored Document, created bool, err error)
	Get(ctx context.Context, id uuid.UUID, forUpdate bool) (Document, error)
	Update(ctx context.Context, d *Document) error
	List(ctx context.Context, p query.Params) (query.Page[Document], error)
	// ClaimQueued locks up to limit queued documents (oldest first) that no other worker holds.
	ClaimQueued(ctx context.Context, limit int) ([]Document, error)
	// ClaimSent locks up to limit sent documents (oldest first) that no other worker holds.
	ClaimSent(ctx context.Context, limit int) ([]Document, error)
}

// Packet is one invoice to hand to the provider.
type Packet struct {
	TraceID  string
	FiscalID string
	Invoice  Invoice
}

// Receipt is the provider's acknowledgement of a packet: from now on the verdict is looked up by the reference number.
type Receipt struct {
	TraceID         string
	ReferenceNumber string
}

// Result is the provider's verdict on a sent packet.
type Result struct {
	ReferenceNumber string
	TraceID         string
	Outcome         Outcome
	Errors          []Issue
	Warnings        []Issue
}

// Limits of one provider call.
type Limits struct {
	Submit  int // packets per Submit
	Inquire int // reference numbers per Inquire
}

// Provider is a tax-authority integration.
type Provider interface {
	Limits() Limits
	// Submit sends packets. A packet the provider rejected outright simply has no receipt.
	Submit(ctx context.Context, packets []Packet) ([]Receipt, error)
	// Inquire looks verdicts up. sentSince is when the oldest of them was sent (providers search a window).
	Inquire(ctx context.Context, references []string, sentSince time.Time) ([]Result, error)
}
