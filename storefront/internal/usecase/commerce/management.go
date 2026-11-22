package commerce

import (
	"context"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
	"regexp"
	"slices"
	"time"
)

func (s *Service) Campaigns(ctx context.Context, u shop.User) ([]shop.Discount, error) {
	if !u.CanManage() {
		return nil, shop.ErrForbidden
	}
	return s.repo.Campaigns(ctx, false)
}
func (s *Service) Analytics(ctx context.Context, u shop.User, since time.Time) (shop.Analytics, error) {
	if !u.CanManage() {
		return shop.Analytics{}, shop.ErrForbidden
	}
	return s.repo.Analytics(ctx, since)
}
func (s *Service) PaymentOrder(ctx context.Context, id string) (shop.Order, error) {
	return s.repo.Order(ctx, id, false)
}
func (s *Service) SaveArticle(ctx context.Context, u shop.User, a shop.Article) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return e
	}
	if !settings.Modules["blog"] {
		return shop.ErrForbidden
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.Title == "" || len(a.Title) > 300 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`).MatchString(a.Slug) || len(a.Body) > 100000 {
		return shop.ErrInvalid
	}
	if a.PublishedAt.IsZero() {
		a.PublishedAt = time.Now()
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		if e := s.repo.SaveArticle(ctx, a); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "article.save", a.ID)
	})
}
func (s *Service) Track(ctx context.Context, e shop.Event) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Modules["analytics"] {
		return shop.ErrNotFound
	}
	if !slices.Contains([]string{"page_view", "product_view", "add_to_cart", "begin_checkout"}, e.Kind) || e.Session == "" || len(e.Session) > 80 || len(e.Campaign) > 100 || len(e.Source) > 100 || len(e.ProductID) > 100 {
		return shop.ErrInvalid
	}
	return s.repo.Event(ctx, e)
}
func (s *Service) SaveUpload(ctx context.Context, u shop.User, a shop.Upload) error {
	if u.ID == "" || a.UserID != u.ID {
		return shop.ErrForbidden
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return e
	}
	if !settings.Modules["attachments"] {
		return shop.ErrForbidden
	}
	p, e := s.Product(ctx, a.ProductID)
	if e != nil {
		return e
	}
	if !p.Active {
		return shop.ErrInvalid
	}
	if _, e = p.Variant(a.VariantID); e != nil {
		return e
	}
	valid := false
	for _, r := range p.Attachments {
		if r.ID == a.RuleID && a.Size > 0 && a.Size <= r.MaxBytes && slices.Contains(r.Types, a.MIME) {
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("%w: شرایط پیوست محصول تغییر کرده است", shop.ErrInvalid)
	}
	return s.repo.SaveUpload(ctx, a)
}
func (s *Service) File(ctx context.Context, u shop.User, id string) (shop.Upload, error) {
	a, e := s.repo.File(ctx, id)
	if e != nil {
		return a, e
	}
	if a.UserID != u.ID && !u.CanManage() {
		return shop.Upload{}, shop.ErrForbidden
	}
	return a, nil
}
func (s *Service) Media(ctx context.Context, id string) (shop.Upload, error) {
	return s.repo.Media(ctx, id)
}
func (s *Service) SaveMedia(ctx context.Context, u shop.User, a shop.Upload) error {
	if !u.CanManage() || a.UserID != u.ID {
		return shop.ErrForbidden
	}
	if !slices.Contains([]string{"image/jpeg", "image/png", "image/webp"}, a.MIME) || a.Size < 1 || a.Size >= 8<<20 {
		return shop.ErrInvalid
	}
	return s.repo.SaveMedia(ctx, a)
}
