package store

import (
	"context"
	"encoding/json"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Store) LockCustomer(ctx context.Context, user string) error {
	var id string
	return missing(s.DB.Querier(ctx).QueryRow(ctx, "SELECT id FROM shop_users WHERE id=$1 FOR UPDATE", user).Scan(&id))
}
func (s *Store) Addresses(ctx context.Context, user string) ([]shop.SavedAddress, error) {
	rows, err := s.DB.Querier(ctx).Query(ctx, "SELECT id,label,address,is_default FROM shop_address_book WHERE user_id=$1 ORDER BY is_default DESC,updated_at DESC,id LIMIT 30", user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []shop.SavedAddress{}
	for rows.Next() {
		var a shop.SavedAddress
		var raw []byte
		if err = rows.Scan(&a.ID, &a.Label, &raw, &a.Default); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &a.Address); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) SaveAddress(ctx context.Context, user string, a shop.SavedAddress) error {
	if a.Default {
		if _, err := s.DB.Querier(ctx).Exec(ctx, "UPDATE shop_address_book SET is_default=false WHERE user_id=$1 AND is_default", user); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(a.Address)
	if err != nil {
		return err
	}
	tag, err := s.DB.Querier(ctx).Exec(ctx, `INSERT INTO shop_address_book(id,user_id,label,address,is_default) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET label=excluded.label,address=excluded.address,is_default=excluded.is_default,updated_at=now() WHERE shop_address_book.user_id=excluded.user_id`, a.ID, user, a.Label, raw, a.Default)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return shop.ErrNotFound
	}
	return nil
}
func (s *Store) DeleteAddress(ctx context.Context, user, id string) error {
	tag, err := s.DB.Querier(ctx).Exec(ctx, "DELETE FROM shop_address_book WHERE id=$1 AND user_id=$2", id, user)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return shop.ErrNotFound
	}
	return nil
}
func (s *Store) UpdateCustomerName(ctx context.Context, user, name string) error {
	tag, err := s.DB.Querier(ctx).Exec(ctx, "UPDATE shop_users SET name=$2 WHERE id=$1", user, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return shop.ErrNotFound
	}
	return nil
}

func (s *Store) CustomerOrderPage(ctx context.Context, user string, offset, limit int) ([]shop.Order, error) {
	return list[shop.Order](ctx, s, "SELECT data FROM shop_orders WHERE user_id=$1 ORDER BY created_at DESC,id LIMIT $2 OFFSET $3", user, limit, offset)
}
func (s *Store) CustomerSummary(ctx context.Context, user string) (shop.CustomerSummary, error) {
	var out shop.CustomerSummary
	err := s.DB.Querier(ctx).QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE status='awaiting_payment'),count(*) FILTER(WHERE status IN ('processing','shipped','ready_for_pickup')),count(*) FILTER(WHERE status='completed'),count(*) FILTER(WHERE payment_status='paid'),COALESCE(sum((data#>>'{quote,total}')::bigint) FILTER(WHERE payment_status='paid'),0) FROM shop_orders WHERE user_id=$1`, user).Scan(&out.Total, &out.Pending, &out.Processing, &out.Delivered, &out.Paid, &out.Spent)
	return out, err
}
