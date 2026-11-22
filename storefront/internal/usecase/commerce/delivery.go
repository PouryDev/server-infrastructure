package commerce

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

// ShipOrder snapshots the dispatch details atomically with the fulfillment state.
func (s *Service) ShipOrder(ctx context.Context, u shop.User, id string, shipment shop.Shipment) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	shipment.Carrier = strings.TrimSpace(shipment.Carrier)
	shipment.TrackingCode = strings.TrimSpace(shipment.TrackingCode)
	if len(shipment.Carrier) > 100 || len(shipment.TrackingCode) > 100 || len(shipment.Note) > 2000 {
		return shop.ErrInvalid
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		o, err := s.repo.Order(ctx, id, true)
		if err != nil {
			return err
		}
		if o.Status != "processing" || o.PaymentStatus != "paid" {
			return shop.ErrConflict
		}
		if o.Quote.Delivery.Kind != "pickup" && (shipment.Carrier == "" || shipment.TrackingCode == "") {
			return fmt.Errorf("%w: نام حمل‌کننده و کد رهگیری را وارد کنید", shop.ErrInvalid)
		}
		shipment.ShippedAt = time.Now().UTC()
		o.Shipment = shipment
		o.Status = "shipped"
		if o.Quote.Delivery.Kind == "pickup" {
			o.Status = "ready_for_pickup"
		}
		if err = s.repo.SaveOrder(ctx, o); err != nil {
			return err
		}
		return s.repo.Audit(ctx, u.ID, "order.dispatch", o.ID)
	})
}
