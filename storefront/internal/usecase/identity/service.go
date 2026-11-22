// Package identity authenticates Iranian mobile numbers. No email identities exist.
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
	"math/big"
	"strings"
	"time"
)

type Repository interface {
	User(context.Context, string) (shop.User, error)
	UserByPhone(context.Context, string) (shop.User, error)
	SaveUser(context.Context, shop.User) error
	Allow(context.Context, string, int, time.Duration) (bool, error)
	OTP(context.Context, string, string, time.Time) (bool, error)
	ConsumeOTP(context.Context, string, string) (bool, error)
	SaveSession(context.Context, string, string, string, time.Time) error
	Session(context.Context, string) (shop.User, string, error)
	DeleteSession(context.Context, string) error
}
type SMS interface {
	Send(context.Context, string, string) error
}
type Service struct {
	Repo   Repository
	SMS    SMS
	Hasher kitauth.PasswordHasher
	Secret []byte
	Demo   bool
	dummy  string
}

func New(r Repository, sms SMS, secret string, demo bool) (*Service, error) {
	h := kitauth.NewArgon2id()
	dummy, e := h.Hash(uuid.NewString())
	return &Service{Repo: r, SMS: sms, Hasher: h, Secret: []byte(secret), Demo: demo, dummy: dummy}, e
}
func token() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	if e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func Hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (s *Service) codeHash(phone, code string) string {
	m := hmac.New(sha256.New, s.Secret)
	_, _ = m.Write([]byte(phone + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}
func (s *Service) RequestOTP(ctx context.Context, raw, ip string) (string, error) {
	phone, e := shop.Phone(raw)
	if e != nil {
		return "", e
	}
	for _, limit := range []struct {
		key string
		max int
	}{{"otp:phone:" + phone, 5}, {"otp:ip:" + Hash(ip), 30}} {
		ok, e := s.Repo.Allow(ctx, limit.key, limit.max, time.Hour)
		if e != nil {
			return "", e
		}
		if !ok {
			return "", fmt.Errorf("تعداد درخواست‌ها زیاد است؛ کمی بعد تلاش کنید")
		}
	}
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return "", e
	}
	code := fmt.Sprintf("%06d", n.Int64())
	ok, e := s.Repo.OTP(ctx, phone, s.codeHash(phone, code), time.Now().Add(2*time.Minute))
	if e != nil {
		return "", e
	}
	if !ok {
		return "", fmt.Errorf("برای دریافت کد جدید ۶۰ ثانیه صبر کنید")
	}
	if !s.Demo {
		if e = s.SMS.Send(ctx, phone, code); e != nil {
			return "", fmt.Errorf("ارسال پیامک انجام نشد؛ کمی بعد دوباره تلاش کنید")
		}
		return "", nil
	}
	return code, nil
}
func (s *Service) Authenticate(ctx context.Context, raw, password, code, ip string) (shop.User, string, string, error) {
	var u shop.User
	phone, e := shop.Phone(raw)
	if e != nil {
		return u, "", "", e
	}
	ok, e := s.Repo.Allow(ctx, "login:"+Hash(ip)+":"+phone, 10, 10*time.Minute)
	if e != nil || !ok {
		return u, "", "", fmt.Errorf("تعداد تلاش‌ها زیاد است؛ کمی بعد تلاش کنید")
	}
	u, e = s.Repo.UserByPhone(ctx, phone)
	if e != nil && e != shop.ErrNotFound {
		return u, "", "", e
	}
	if code != "" {
		valid, err := s.Repo.ConsumeOTP(ctx, phone, s.codeHash(phone, code))
		if err != nil {
			return u, "", "", err
		}
		if !valid {
			return u, "", "", fmt.Errorf("کد واردشده صحیح نیست یا منقضی شده است")
		}
		if e == shop.ErrNotFound {
			u = shop.User{ID: uuid.NewString(), Phone: phone, Role: "customer"}
			if e = s.Repo.SaveUser(ctx, u); e != nil {
				return u, "", "", e
			}
		}
	} else {
		hash := s.dummy
		if e == nil && u.PasswordHash != "" {
			hash = u.PasswordHash
		}
		valid, _, err := s.Hasher.Verify(password, hash)
		if err != nil || !valid || e != nil || u.PasswordHash == "" {
			return shop.User{}, "", "", fmt.Errorf("شماره موبایل یا رمز عبور صحیح نیست")
		}
	}
	plain, csrf := token(), token()
	e = s.Repo.SaveSession(ctx, Hash(plain), u.ID, csrf, time.Now().Add(24*time.Hour))
	return u, plain, csrf, e
}
func (s *Service) Current(ctx context.Context, t string) (shop.User, string, error) {
	if len(t) != 64 {
		return shop.User{}, "", shop.ErrForbidden
	}
	return s.Repo.Session(ctx, Hash(t))
}
func (s *Service) Logout(ctx context.Context, t string) error {
	return s.Repo.DeleteSession(ctx, Hash(t))
}
func (s *Service) Password(ctx context.Context, u shop.User, password string) error {
	if len(password) < 10 || len(password) > 128 {
		return fmt.Errorf("رمز باید بین ۱۰ و ۱۲۸ نویسه باشد")
	}
	h, e := s.Hasher.Hash(password)
	if e != nil {
		return e
	}
	u.PasswordHash = h
	return s.Repo.SaveUser(ctx, u)
}
func (s *Service) Bootstrap(ctx context.Context, phone, password, role string) error {
	phone, e := shop.Phone(phone)
	if e != nil {
		return e
	}
	if !strings.Contains(" seller admin ", " "+role+" ") {
		return shop.ErrInvalid
	}
	_, e = s.Repo.UserByPhone(ctx, phone)
	if e == nil {
		return nil
	}
	if e != shop.ErrNotFound {
		return e
	}
	if len(password) < 12 {
		return shop.ErrInvalid
	}
	h, e := s.Hasher.Hash(password)
	if e != nil {
		return e
	}
	return s.Repo.SaveUser(ctx, shop.User{ID: uuid.NewString(), Phone: phone, Role: role, Name: "مدیر فروشگاه", PasswordHash: h})
}
