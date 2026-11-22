package store

import (
	"context"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"time"
)

func (s *Store) User(ctx context.Context, id string) (shop.User, error) {
	var u shop.User
	e := s.DB.Querier(ctx).QueryRow(ctx, "SELECT id,phone,name,role,password_hash FROM shop_users WHERE id=$1", id).Scan(&u.ID, &u.Phone, &u.Name, &u.Role, &u.PasswordHash)
	return u, missing(e)
}
func (s *Store) UserByPhone(ctx context.Context, p string) (shop.User, error) {
	var u shop.User
	e := s.DB.Querier(ctx).QueryRow(ctx, "SELECT id,phone,name,role,password_hash FROM shop_users WHERE phone=$1", p).Scan(&u.ID, &u.Phone, &u.Name, &u.Role, &u.PasswordHash)
	return u, missing(e)
}
func (s *Store) SaveUser(ctx context.Context, u shop.User) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_users(id,phone,name,role,password_hash) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET name=excluded.name,password_hash=excluded.password_hash", u.ID, u.Phone, u.Name, u.Role, u.PasswordHash)
	return e
}
func (s *Store) Allow(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	var n int
	e := s.DB.Querier(ctx).QueryRow(ctx, "INSERT INTO shop_rate_limits(key,hits,resets_at) VALUES($1,1,$2) ON CONFLICT(key) DO UPDATE SET hits=CASE WHEN shop_rate_limits.resets_at<now() THEN 1 ELSE shop_rate_limits.hits+1 END,resets_at=CASE WHEN shop_rate_limits.resets_at<now() THEN excluded.resets_at ELSE shop_rate_limits.resets_at END RETURNING hits", key, time.Now().Add(window)).Scan(&n)
	return n <= max, e
}
func (s *Store) OTP(ctx context.Context, phone, hash string, expires time.Time) (bool, error) {
	tag, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_otp(phone,code_hash,expires_at) VALUES($1,$2,$3) ON CONFLICT(phone) DO UPDATE SET code_hash=excluded.code_hash,expires_at=excluded.expires_at,attempts=0,sent_at=now() WHERE shop_otp.sent_at<now()-interval '60 seconds'", phone, hash, expires)
	return tag.RowsAffected() == 1, e
}
func (s *Store) ConsumeOTP(ctx context.Context, phone, hash string) (bool, error) {
	var matched bool
	e := s.DB.Querier(ctx).QueryRow(ctx, "UPDATE shop_otp SET attempts=attempts+1,expires_at=CASE WHEN code_hash=$2 THEN now() ELSE expires_at END WHERE phone=$1 AND expires_at>now() AND attempts<5 RETURNING code_hash=$2", phone, hash).Scan(&matched)
	if missing(e) == shop.ErrNotFound {
		return false, nil
	}
	return matched, e
}
func (s *Store) SaveSession(ctx context.Context, hash, user, csrf string, expires time.Time) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "INSERT INTO shop_sessions(token_hash,user_id,csrf,expires_at) VALUES($1,$2,$3,$4)", hash, user, csrf, expires)
	return e
}
func (s *Store) Session(ctx context.Context, hash string) (shop.User, string, error) {
	var u shop.User
	var csrf string
	e := s.DB.Querier(ctx).QueryRow(ctx, "SELECT u.id,u.phone,u.name,u.role,s.csrf FROM shop_sessions s JOIN shop_users u ON u.id=s.user_id WHERE token_hash=$1 AND expires_at>now()", hash).Scan(&u.ID, &u.Phone, &u.Name, &u.Role, &csrf)
	return u, csrf, missing(e)
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, e := s.DB.Querier(ctx).Exec(ctx, "DELETE FROM shop_sessions WHERE token_hash=$1", hash)
	return e
}
