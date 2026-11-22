package commerce

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
)

func validateMerchandising(settings shop.Settings) error {
	if len(settings.Collections) > 30 || len(settings.Sections) > 60 {
		return shop.ErrInvalid
	}
	ids := map[string]bool{}
	for _, c := range settings.Collections {
		if err := c.Validate(); err != nil || ids[c.ID] {
			return fmt.Errorf("%w: تنظیمات کالکشن را بررسی کنید", shop.ErrInvalid)
		}
		ids[c.ID] = true
	}
	for _, section := range settings.Sections {
		if len(section.Body) > 8000 || !shop.LocalImage(section.Image) || section.Href != "" && !shop.LocalLink(section.Href) || len(section.Href) > 500 || len(section.Button) > 100 || len(section.Items) > 12 {
			return shop.ErrInvalid
		}
		for _, item := range section.Items {
			if len(item.Title) > 200 || len(item.Text) > 2000 {
				return shop.ErrInvalid
			}
		}
		if len(section.Title) > 250 || !slices.Contains([]string{"hero", "categories", "products", "category-products", "collection", "story", "blog", "banner", "richtext", "benefits", "faq"}, section.Kind) || (section.Kind == "collection" && !ids[section.CollectionID]) {
			return fmt.Errorf("%w: بخش صفحهٔ اصلی معتبر نیست", shop.ErrInvalid)
		}
	}
	return nil
}
func (s *Service) SaveCollection(ctx context.Context, u shop.User, c shop.Collection, revision, position int) (int, error) {
	if !u.CanManage() {
		return 0, shop.ErrForbidden
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	c.Name = strings.TrimSpace(c.Name)
	if err := c.Validate(); err != nil {
		return 0, err
	}
	var next int
	err := s.repo.Atomic(ctx, func(ctx context.Context) error {
		settings, err := s.repo.Settings(ctx, true)
		if err != nil {
			return err
		}
		if settings.Revision != revision {
			return shop.ErrConflict
		}
		if c.Mode == "manual" {
			products, err := s.repo.Products(ctx)
			if err != nil {
				return err
			}
			for _, id := range c.ProductIDs {
				if !slices.ContainsFunc(products, func(p shop.Product) bool { return p.ID == id }) {
					return fmt.Errorf("%w: یکی از کالاهای انتخاب‌شده حذف شده است", shop.ErrInvalid)
				}
			}
		}
		found := false
		for i := range settings.Collections {
			if settings.Collections[i].ID == c.ID {
				settings.Collections[i] = c
				found = true
			}
		}
		if !found {
			settings.Collections = append(settings.Collections, c)
		}
		sections := []shop.Section{}
		for _, section := range settings.Sections {
			if section.Kind != "collection" || section.CollectionID != c.ID {
				sections = append(sections, section)
			}
		}
		if position < 0 || position > len(sections) {
			position = len(sections)
		}
		section := shop.Section{Kind: "collection", CollectionID: c.ID, Title: c.Name, Enabled: c.Enabled}
		settings.Sections = slices.Insert(sections, position, section)
		if err = validateMerchandising(settings); err != nil {
			return err
		}
		settings.Revision++
		next = settings.Revision
		if err = s.repo.SaveSettings(ctx, settings); err != nil {
			return err
		}
		return s.repo.Audit(ctx, u.ID, "collection.save", c.ID)
	})
	return next, err
}
func (s *Service) DeleteCollection(ctx context.Context, u shop.User, id string, revision int) (int, error) {
	if !u.CanManage() {
		return 0, shop.ErrForbidden
	}
	var next int
	err := s.repo.Atomic(ctx, func(ctx context.Context) error {
		settings, err := s.repo.Settings(ctx, true)
		if err != nil {
			return err
		}
		if settings.Revision != revision {
			return shop.ErrConflict
		}
		before := len(settings.Collections)
		settings.Collections = slices.DeleteFunc(settings.Collections, func(c shop.Collection) bool { return c.ID == id })
		if len(settings.Collections) == before {
			return shop.ErrNotFound
		}
		settings.Sections = slices.DeleteFunc(settings.Sections, func(section shop.Section) bool { return section.Kind == "collection" && section.CollectionID == id })
		settings.Revision++
		next = settings.Revision
		if err = s.repo.SaveSettings(ctx, settings); err != nil {
			return err
		}
		return s.repo.Audit(ctx, u.ID, "collection.delete", id)
	})
	return next, err
}

type HomeRail struct {
	Kind        string
	Key         string
	Title       string
	Description string
	Href        string
	Products    []shop.Product
	Lazy        bool
	Enabled     bool
}

func (s *Service) HomeRail(ctx context.Context, kind, key string) (HomeRail, error) {
	row := HomeRail{Kind: kind, Key: key, Enabled: true, Products: []shop.Product{}}
	if len(key) > 200 {
		return row, shop.ErrInvalid
	}
	products, err := s.Products(ctx)
	if err != nil {
		return row, err
	}
	switch kind {
	case "category":
		c := shop.Collection{Mode: "automatic", Category: key, InStock: false, Sort: "catalog"}
		row.Title = key
		row.Products = c.Select(products, 8)
	case "collection":
		settings, err := s.Settings(ctx)
		if err != nil {
			return row, err
		}
		found := false
		for _, c := range settings.Collections {
			if c.ID == key && c.Enabled {
				row.Title = c.Name
				row.Description = c.Description
				row.Products = c.Select(products, c.Limit)
				found = true
			}
		}
		if !found {
			return row, shop.ErrNotFound
		}
	default:
		return row, shop.ErrNotFound
	}
	return row, nil
}

type Offer struct {
	Benefit string
	Code    string
	Name    string
	Terms   string
}

func (s *Service) Offers(ctx context.Context, u shop.User) ([]Offer, error) {
	out := []Offer{}
	settings, err := s.Settings(ctx)
	if err != nil || !settings.Modules["discounts"] {
		return out, err
	}
	campaigns, err := s.repo.Campaigns(ctx, false)
	if err != nil {
		return out, err
	}
	now := time.Now()
	for _, c := range campaigns {
		if ((c.Kind == "percent" || c.Kind == "fixed") && c.Value <= 0) || !c.Active || c.Automatic || c.Code == "" || now.Before(c.StartsAt) || (!c.EndsAt.IsZero() && !now.Before(c.EndsAt)) || (len(c.Phones) > 0 && !slices.Contains(c.Phones, u.Phone)) {
			continue
		}
		all, own, prior, err := s.repo.Uses(ctx, c.ID, u.ID)
		if err != nil {
			return out, err
		}
		if (c.Limit > 0 && all >= c.Limit) || (c.PerCustomer > 0 && own >= c.PerCustomer) || (c.FirstOrder && prior > 0) {
			continue
		}
		terms := []string{}
		if c.FirstOrder {
			terms = append(terms, "ویژهٔ اولین خرید")
		}
		if c.MinSubtotal > 0 {
			terms = append(terms, fmt.Sprintf("حداقل خرید %d تومان", c.MinSubtotal/10))
		}
		if c.MinQuantity > 0 {
			terms = append(terms, fmt.Sprintf("حداقل %d کالا", c.MinQuantity))
		}
		if len(c.Products)+len(c.Variants) > 0 {
			terms = append(terms, "برای کالاهای منتخب")
		}
		if c.Cap > 0 {
			terms = append(terms, fmt.Sprintf("سقف تخفیف %d تومان", c.Cap/10))
		}
		terms = append(terms, "اعتبار و مبلغ نهایی در تکمیل سفارش بررسی می‌شود")
		benefit := c.Name
		switch c.Kind {
		case "percent":
			benefit = fmt.Sprintf("%d٪ تخفیف", c.Value)
		case "fixed":
			benefit = fmt.Sprintf("%d تومان تخفیف", c.Value/10)
		case "shipping":
			benefit = "تخفیف هزینهٔ ارسال"
		case "buy_x_get_y":
			benefit = fmt.Sprintf("خرید %d + %d هدیه", c.BuyQuantity, c.FreeQuantity)
		}
		if c.FirstOrder {
			benefit += " برای اولین خرید"
		}
		out = append(out, Offer{Benefit: benefit, Code: c.Code, Name: c.Name, Terms: strings.Join(terms, " · ")})
		if len(out) == 3 {
			break
		}
	}
	return out, nil
}
