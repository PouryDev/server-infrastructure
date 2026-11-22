// Package care owns verified purchase feedback and the return-request workflow.
package care

import (
	"context"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
	"strings"
	"time"
)

type Repository interface {
	Atomic(context.Context, func(context.Context) error) error
	Order(context.Context, string, bool) (shop.Order, error)
	Purchased(context.Context, string, string) (bool, error)
	Reviews(context.Context, string, bool) ([]shop.Review, error)
	SaveReview(context.Context, shop.Review) error
	ModerateReview(context.Context, string, string) error
	ReturnByOrder(context.Context, string) (shop.ReturnRequest, error)
	Returns(context.Context, string) ([]shop.ReturnRequest, error)
	SaveReturn(context.Context, shop.ReturnRequest) error
	Audit(context.Context, string, string, string) error
}
type Service struct{ repo Repository }

func New(r Repository) *Service { return &Service{repo: r} }
func (s *Service) Reviews(ctx context.Context, product string, u shop.User) ([]shop.Review, error) {
	return s.repo.Reviews(ctx, product, u.CanManage())
}
func (s *Service) Review(ctx context.Context, u shop.User, in shop.Review) error {
	if u.ID == "" {
		return shop.ErrForbidden
	}
	if in.ProductID == "" || len(in.ProductID) > 100 || in.Rating < 1 || in.Rating > 5 || strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 || len(in.Body) > 5000 {
		return shop.ErrInvalid
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		ok, e := s.repo.Purchased(ctx, u.ID, in.ProductID)
		if e != nil {
			return e
		}
		if !ok {
			return shop.ErrForbidden
		}
		in.ID = uuid.NewString()
		in.UserID = u.ID
		in.Author = strings.TrimSpace(u.Name)
		if in.Author == "" {
			in.Author = "خریدار فروشگاه"
		}
		in.Status = "pending"
		in.CreatedAt = time.Now().UTC()
		if e = s.repo.SaveReview(ctx, in); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "review.submit", in.ProductID)
	})
}
func (s *Service) ModerateReview(ctx context.Context, u shop.User, id, status string) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	if status != "published" && status != "hidden" {
		return shop.ErrInvalid
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		if e := s.repo.ModerateReview(ctx, id, status); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "review."+status, id)
	})
}
func (s *Service) Returns(ctx context.Context, u shop.User) ([]shop.ReturnRequest, error) {
	if u.ID == "" {
		return nil, shop.ErrForbidden
	}
	user := u.ID
	if u.CanManage() {
		user = ""
	}
	return s.repo.Returns(ctx, user)
}
func (s *Service) OrderReturn(ctx context.Context, u shop.User, id string) (shop.ReturnRequest, error) {
	o, e := s.repo.Order(ctx, id, false)
	if e != nil {
		return shop.ReturnRequest{}, e
	}
	if o.UserID != u.ID && !u.CanManage() {
		return shop.ReturnRequest{}, shop.ErrForbidden
	}
	return s.repo.ReturnByOrder(ctx, id)
}
func (s *Service) RequestReturn(ctx context.Context, u shop.User, order, reason string) error {
	if u.ID == "" {
		return shop.ErrForbidden
	}
	if len(reason) > 5000 || len(strings.TrimSpace(reason)) < 10 {
		return shop.ErrInvalid
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		o, e := s.repo.Order(ctx, order, true)
		if e != nil {
			return e
		}
		if o.UserID != u.ID {
			return shop.ErrForbidden
		}
		if o.PaymentStatus != "paid" || (o.Status != "completed" && o.Status != "shipped") {
			return shop.ErrConflict
		}
		if _, e = s.repo.ReturnByOrder(ctx, order); e == nil {
			return nil
		} else if e != shop.ErrNotFound {
			return e
		}
		now := time.Now().UTC()
		in := shop.ReturnRequest{ID: uuid.NewString(), OrderID: order, UserID: u.ID, Reason: reason, Status: "requested", CreatedAt: now, UpdatedAt: now}
		if e = s.repo.SaveReturn(ctx, in); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "return.request", order)
	})
}

// DecideReturn records operations only; they never claim a bank refund or restock goods.
func (s *Service) DecideReturn(ctx context.Context, u shop.User, order, status, note string) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	if len(note) > 5000 {
		return shop.ErrInvalid
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		if _, e := s.repo.Order(ctx, order, true); e != nil {
			return e
		}
		r, e := s.repo.ReturnByOrder(ctx, order)
		if e != nil {
			return e
		}
		valid := r.Status == "requested" && (status == "approved" || status == "rejected") || r.Status == "approved" && status == "received" || r.Status == "received" && status == "closed"
		if !valid {
			return shop.ErrConflict
		}
		if (status == "rejected" || status == "closed") && strings.TrimSpace(note) == "" {
			return shop.ErrInvalid
		}
		r.Status = status
		r.SellerNote = note
		r.UpdatedAt = time.Now().UTC()
		if e = s.repo.SaveReturn(ctx, r); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "return."+status, order)
	})
}
