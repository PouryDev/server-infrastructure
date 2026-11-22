package commerce

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
)

func (s *Service) Addresses(ctx context.Context, u shop.User) ([]shop.SavedAddress, error) {
	if u.ID == "" {
		return nil, shop.ErrForbidden
	}
	return s.repo.Addresses(ctx, u.ID)
}
func (s *Service) SaveAddress(ctx context.Context, u shop.User, a shop.SavedAddress) (shop.SavedAddress, error) {
	if u.ID == "" {
		return a, shop.ErrForbidden
	}
	a.Label = strings.TrimSpace(a.Label)
	if a.Label == "" || utf8.RuneCountInString(a.Label) > 50 {
		return a, fmt.Errorf("%w: برای نشانی یک عنوان کوتاه بنویسید", shop.ErrInvalid)
	}
	var err error
	a.Address, err = a.Address.Normalize()
	if err != nil {
		return a, err
	}
	err = s.repo.Atomic(ctx, func(ctx context.Context) error {
		if err := s.repo.LockCustomer(ctx, u.ID); err != nil {
			return err
		}
		addresses, err := s.repo.Addresses(ctx, u.ID)
		if err != nil {
			return err
		}
		if a.ID == "" {
			if len(addresses) >= 20 {
				return fmt.Errorf("%w: حداکثر ۲۰ نشانی قابل ذخیره است", shop.ErrInvalid)
			}
			a.ID = uuid.NewString()
		} else {
			found := false
			for _, old := range addresses {
				if old.ID == a.ID {
					found = true
				}
			}
			if !found {
				return shop.ErrNotFound
			}
		}
		anotherDefault := false
		for _, old := range addresses {
			if old.ID != a.ID && old.Default {
				anotherDefault = true
			}
		}
		if !anotherDefault {
			a.Default = true
		}
		return s.repo.SaveAddress(ctx, u.ID, a)
	})
	return a, err
}
func (s *Service) DeleteAddress(ctx context.Context, u shop.User, id string) error {
	if u.ID == "" {
		return shop.ErrForbidden
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		if err := s.repo.LockCustomer(ctx, u.ID); err != nil {
			return err
		}
		if err := s.repo.DeleteAddress(ctx, u.ID, id); err != nil {
			return err
		}
		remaining, err := s.repo.Addresses(ctx, u.ID)
		if err != nil {
			return err
		}
		if len(remaining) > 0 && !remaining[0].Default {
			remaining[0].Default = true
			return s.repo.SaveAddress(ctx, u.ID, remaining[0])
		}
		return nil
	})
}
func (s *Service) UpdateProfile(ctx context.Context, u shop.User, name string) error {
	name = strings.TrimSpace(name)
	if u.ID == "" {
		return shop.ErrForbidden
	}
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 120 {
		return fmt.Errorf("%w: نام و نام خانوادگی را کامل بنویسید", shop.ErrInvalid)
	}
	return s.repo.UpdateCustomerName(ctx, u.ID, name)
}
func (s *Service) CustomerOrders(ctx context.Context, u shop.User) ([]shop.Order, error) {
	if u.ID == "" {
		return nil, shop.ErrForbidden
	}
	return s.repo.Orders(ctx, u.ID)
}

func (s *Service) CustomerOrderPage(ctx context.Context, u shop.User, page int) ([]shop.Order, error) {
	if u.ID == "" {
		return nil, shop.ErrForbidden
	}
	if page < 1 || page > 10000 {
		return nil, shop.ErrInvalid
	}
	return s.repo.CustomerOrderPage(ctx, u.ID, (page-1)*25, 26)
}
func (s *Service) CustomerSummary(ctx context.Context, u shop.User) (shop.CustomerSummary, error) {
	if u.ID == "" {
		return shop.CustomerSummary{}, shop.ErrForbidden
	}
	return s.repo.CustomerSummary(ctx, u.ID)
}
func (s *Service) CustomerOrder(ctx context.Context, u shop.User, id string) (shop.Order, error) {
	if u.ID == "" {
		return shop.Order{}, shop.ErrForbidden
	}
	o, err := s.repo.Order(ctx, id, false)
	if err != nil {
		return shop.Order{}, err
	}
	if o.UserID != u.ID {
		return shop.Order{}, shop.ErrNotFound
	}
	return o, nil
}
