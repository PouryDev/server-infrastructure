package identity

import (
	"context"
	"strings"
	"testing"
	"time"
)

type otpRepo struct {
	Repository
	limits map[string]int
}

func (r *otpRepo) Allow(_ context.Context, key string, max int, _ time.Duration) (bool, error) {
	r.limits[key] = max
	return true, nil
}
func (r *otpRepo) OTP(context.Context, string, string, time.Time) (bool, error) { return true, nil }
func TestOTPSeparatesPhoneAndSharedNetworkBudgets(t *testing.T) {
	r := &otpRepo{limits: map[string]int{}}
	s := Service{Repo: r, Secret: []byte(strings.Repeat("s", 32)), Demo: true}
	for _, phone := range []string{"09121234567", "09121234568"} {
		code, e := s.RequestOTP(context.Background(), phone, "127.0.0.1")
		if e != nil || len(code) != 6 {
			t.Fatal(code, e)
		}
	}
	if r.limits["otp:phone:+989121234567"] != 5 || r.limits["otp:ip:"+Hash("127.0.0.1")] != 30 {
		t.Fatal(r.limits)
	}
}
