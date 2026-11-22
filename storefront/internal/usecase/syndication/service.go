// Package syndication exposes bounded catalog reads and durable update delivery.
package syndication

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

type Catalog interface {
	FeedProducts(context.Context, shop.FeedQuery) (shop.FeedPage, error)
	Product(context.Context, string) (shop.Product, error)
	Settings(context.Context, bool) (shop.Settings, error)
}
type Queue interface {
	LeaseSync(context.Context) (shop.SyncJob, error)
	FinishSync(context.Context, shop.SyncJob, bool) error
	SyncStatus(context.Context) (shop.SyncStatus, error)
}
type Notifier interface {
	Notify(context.Context, string, shop.Product) error
}
type Service struct {
	catalog  Catalog
	queue    Queue
	notifier Notifier
	origin   string
}

func New(c Catalog, q Queue, n Notifier, origin string) *Service {
	return &Service{catalog: c, queue: q, notifier: n, origin: origin}
}

type Request struct {
	URLs   []string `json:"page_urls"`
	IDs    []string `json:"page_uniques"`
	Page   *int     `json:"page"`
	Sort   string   `json:"sort"`
	Cursor *string  `json:"cursor"`
}

func (s *Service) Read(ctx context.Context, in Request) (shop.FeedPage, error) {
	q := shop.FeedQuery{IDs: in.IDs, Sort: in.Sort}
	selectors := 0
	if in.URLs != nil {
		selectors++
	}
	if in.IDs != nil {
		selectors++
	}
	if in.Page != nil || in.Sort != "" || in.Cursor != nil {
		selectors++
	}
	if selectors != 1 || len(in.IDs) > 100 || len(in.URLs) > 100 {
		return shop.FeedPage{}, shop.ErrInvalid
	}
	if in.IDs != nil {
		if len(in.IDs) == 0 {
			return shop.FeedPage{}, shop.ErrInvalid
		}
		for _, id := range in.IDs {
			if id == "" || len(id) > 200 {
				return shop.FeedPage{}, shop.ErrInvalid
			}
		}
	} else if in.URLs != nil {
		if len(in.URLs) == 0 {
			return shop.FeedPage{}, shop.ErrInvalid
		}
		q.Slugs = []string{}
		q.VariantIDs = []string{}
		for _, raw := range in.URLs {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme+"://"+u.Host != s.origin || !strings.HasPrefix(u.Path, "/products/") || len(raw) > 1500 {
				return shop.FeedPage{}, shop.ErrInvalid
			}
			slug := strings.TrimPrefix(u.Path, "/products/")
			if slug == "" || strings.Contains(slug, "/") {
				return shop.FeedPage{}, shop.ErrInvalid
			}
			q.Slugs = append(q.Slugs, slug)
			q.VariantIDs = append(q.VariantIDs, u.Query().Get("variant"))
		}
	} else {
		switch in.Sort {
		case "date_added_desc", "date_updated_desc", "product_id_desc":
		default:
			return shop.FeedPage{}, fmt.Errorf("%w: sort parameter is required", shop.ErrInvalid)
		}
		if in.Sort == "product_id_desc" {
			if in.Page != nil {
				return shop.FeedPage{}, shop.ErrInvalid
			}
			if in.Cursor != nil {
				if *in.Cursor == "" || len(*in.Cursor) > 200 {
					return shop.FeedPage{}, shop.ErrInvalid
				}
				q.BeforeID = *in.Cursor
			}
		} else {
			if in.Page == nil || *in.Page < 1 || *in.Page > 1_000_000 || in.Cursor != nil {
				return shop.FeedPage{}, shop.ErrInvalid
			}
			q.Offset = (*in.Page - 1) * 100
		}
	}
	return s.catalog.FeedProducts(ctx, q)
}
func (s *Service) Enabled(ctx context.Context) (bool, error) {
	v, e := s.catalog.Settings(ctx, false)
	return v.Torob.Enabled, e
}
func (s *Service) Status(ctx context.Context) (shop.SyncStatus, error) {
	return s.queue.SyncStatus(ctx)
}

// Tick leases one product, allowing at most 12 requests/minute at a five-second tick.
// Acknowledgments compare generations so concurrent inventory changes are not lost.
func (s *Service) Tick(ctx context.Context) error {
	settings, err := s.catalog.Settings(ctx, false)
	if err != nil {
		return err
	}
	if !settings.Torob.Enabled || settings.Torob.WebhookToken == "" {
		return nil
	}
	job, err := s.queue.LeaseSync(ctx)
	if err == shop.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := s.catalog.Product(ctx, job.ProductID)
	if err == nil {
		err = s.notifier.Notify(ctx, settings.Torob.WebhookToken, p)
	}
	if finish := s.queue.FinishSync(ctx, job, err == nil); finish != nil {
		return finish
	}
	return err
}
