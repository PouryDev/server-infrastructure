package payout

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Transactor runs fn in a transaction that repositories and the books join through ctx.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertSeller stores a seller; a code already in use fails with an apperr conflict "seller_exists".
	InsertSeller(ctx context.Context, s Seller) error
	Seller(ctx context.Context, id uuid.UUID, forUpdate bool) (Seller, error)
	SellersByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Seller, error)
	Sellers(ctx context.Context) ([]Seller, error)
	UpdateSeller(ctx context.Context, s *Seller) error

	// InsertTerms stores a version; a taken (scope, key, date) fails with a conflict "terms_date_taken".
	InsertTerms(ctx context.Context, t Terms) error
	Terms(ctx context.Context) ([]Terms, error)
	// DeleteTerms removes an unused version; one a sale used fails with a conflict "terms_in_use".
	DeleteTerms(ctx context.Context, id uuid.UUID) error

	// InsertSale stores a sale; the same key returns the stored one with created=false.
	InsertSale(ctx context.Context, s Sale) (stored Sale, created bool, err error)
	Sale(ctx context.Context, id uuid.UUID, forUpdate bool) (Sale, error)
	UpdateSale(ctx context.Context, s *Sale) error
	ListSales(ctx context.Context, p query.Params) (query.Page[Sale], error)
	// DueSales locks the held sales whose release day has come, of active sellers only (and of the given ones when any),
	// ordered by seller and id. Sales whose share is zero are not payable.
	DueSales(ctx context.Context, day time.Time, sellers []uuid.UUID) ([]Sale, error)
	MarkSalesPaid(ctx context.Context, ids []uuid.UUID, batch uuid.UUID) error
	SellerTotals(ctx context.Context, seller uuid.UUID, day time.Time) (Totals, error)

	// InsertBatch stores a batch; the same key returns the stored one (with its items) and created=false.
	InsertBatch(ctx context.Context, b Batch) (stored Batch, created bool, err error)
	// FinishBatch stores the totals, the journal and the items of a batch just created.
	FinishBatch(ctx context.Context, b *Batch) error
	Batch(ctx context.Context, id uuid.UUID) (Batch, error)
	Batches(ctx context.Context) ([]Batch, error)
}
