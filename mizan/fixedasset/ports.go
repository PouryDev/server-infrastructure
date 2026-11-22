package fixedasset

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Repository is the persistence port.
type Repository interface {
	// InsertCategory stores a category; a taken code fails with a conflict "category_exists".
	InsertCategory(ctx context.Context, c Category) error
	Category(ctx context.Context, id uuid.UUID) (Category, error)
	Categories(ctx context.Context) ([]Category, error)
	SetCategoryActive(ctx context.Context, id uuid.UUID, active bool) error

	// InsertAsset stores an asset; the same key returns the stored one with created=false.
	InsertAsset(ctx context.Context, a Asset) (stored Asset, created bool, err error)
	Asset(ctx context.Context, id uuid.UUID, forUpdate bool) (Asset, error)
	Assets(ctx context.Context, status Status) ([]Asset, error)
	UpdateAsset(ctx context.Context, a *Asset) error
	// ReduceAsset stores the reduced cost, salvage, accumulated and yearly figures of a partly disposed asset.
	ReduceAsset(ctx context.Context, a *Asset) error
	// DepreciableAssets locks the active assets that still have something to depreciate, ordered by id.
	DepreciableAssets(ctx context.Context) ([]Asset, error)

	InsertItems(ctx context.Context, runPeriod, runSeq int, items []Item) error
	InsertRun(ctx context.Context, r Run) error
	// LatestRun returns the last run of a period; ok is false when there is none.
	LatestRun(ctx context.Context, periodIndex int) (Run, bool, error)
	RunCount(ctx context.Context, periodIndex int) (int, error)
	Depreciations(ctx context.Context, assetID uuid.UUID) ([]Item, error)
	// MarkRunVoided marks the run and its items voided and stores the reversal journal.
	MarkRunVoided(ctx context.Context, periodIndex, seq int, journal uuid.UUID, at time.Time) error

	// SetUsage stores the units of an asset in a month (replacing an earlier figure); UnitsUpTo sums them up to and including a month.
	SetUsage(ctx context.Context, assetID uuid.UUID, periodIndex int, units int64, actor string) error
	UnitsUpTo(ctx context.Context, assetID uuid.UUID, periodIndex int) (int64, error)

	// InsertPartial stores a partial disposal; the same key returns the stored asset id with created=false.
	InsertPartial(ctx context.Context, key string, assetID uuid.UUID, portionBP int, journal uuid.UUID) (existing uuid.UUID, created bool, err error)

	// PartialByKey returns the asset a partial disposal key was used for; ok is false when the key is new.
	PartialByKey(ctx context.Context, key string) (assetID uuid.UUID, ok bool, err error)

	InsertIdleSpan(ctx context.Context, sp IdleSpan) error
	IdleSpans(ctx context.Context, assetID uuid.UUID) ([]IdleSpan, error)
	DeleteIdleSpan(ctx context.Context, id uuid.UUID) error

	InsertThreshold(ctx context.Context, t Threshold) error
	Thresholds(ctx context.Context) ([]Threshold, error)
}
