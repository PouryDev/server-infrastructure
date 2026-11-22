package store

import (
	"context"
	"encoding/json"
	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Store) Purchased(ctx context.Context, user, product string) (bool, error) {
	var exists bool
	e := s.DB.Querier(ctx).QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shop_orders WHERE user_id=$1 AND payment_status='paid' AND status='completed' AND data->'quote'->'lines' @> jsonb_build_array(jsonb_build_object('productId',$2::text)))`, user, product).Scan(&exists)
	return exists, e
}
func (s *Store) Reviews(ctx context.Context, product string, manage bool) ([]shop.Review, error) {
	return list[shop.Review](ctx, s, `SELECT data FROM shop_reviews WHERE ($1='' OR product_id=$1) AND ($2 OR status='published') ORDER BY created_at DESC,id LIMIT 100`, product, manage)
}
func (s *Store) SaveReview(ctx context.Context, r shop.Review) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, `INSERT INTO shop_reviews(id,product_id,user_id,status,data) VALUES($1,$2,$3,$4,$5) ON CONFLICT(product_id,user_id) DO UPDATE SET status='pending',data=jsonb_set(excluded.data,'{id}',to_jsonb(shop_reviews.id)),created_at=now()`, r.ID, r.ProductID, r.UserID, r.Status, b)
	return e
}
func (s *Store) ModerateReview(ctx context.Context, id, status string) error {
	tag, e := s.DB.Querier(ctx).Exec(ctx, `UPDATE shop_reviews SET status=$2,data=jsonb_set(data,'{status}',to_jsonb($2::text)) WHERE id=$1`, id, status)
	if e == nil && tag.RowsAffected() != 1 {
		return shop.ErrNotFound
	}
	return e
}
func (s *Store) ReturnByOrder(ctx context.Context, order string) (shop.ReturnRequest, error) {
	// UserID is deliberately omitted from JSON responses and hydrated from its column.
	var r shop.ReturnRequest
	var raw []byte
	var user string
	e := s.DB.Querier(ctx).QueryRow(ctx, `SELECT data,user_id FROM shop_returns WHERE order_id=$1`, order).Scan(&raw, &user)
	if e != nil {
		return r, missing(e)
	}
	e = json.Unmarshal(raw, &r)
	r.UserID = user
	return r, e
}
func (s *Store) Returns(ctx context.Context, user string) ([]shop.ReturnRequest, error) {
	return list[shop.ReturnRequest](ctx, s, `SELECT data FROM shop_returns WHERE ($1='' OR user_id=$1) ORDER BY created_at DESC LIMIT 250`, user)
}
func (s *Store) SaveReturn(ctx context.Context, r shop.ReturnRequest) error {
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	_, e = s.DB.Querier(ctx).Exec(ctx, `INSERT INTO shop_returns(id,order_id,user_id,data) VALUES($1,$2,$3,$4) ON CONFLICT(order_id) DO UPDATE SET data=excluded.data`, r.ID, r.OrderID, r.UserID, b)
	return e
}
