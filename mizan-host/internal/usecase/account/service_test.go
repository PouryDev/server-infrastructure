package account

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/query"

	"github.com/PouryDev/mizan-host/internal/domain/user"
)

// memRepo is an in-memory user.Repository.
type memRepo struct {
	mu    sync.Mutex
	users map[uuid.UUID]user.User
}

func newRepo() *memRepo { return &memRepo{users: map[uuid.UUID]user.User{}} }

func (r *memRepo) Create(_ context.Context, u user.User) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u.ID = uuid.New()
	r.users[u.ID] = u
	return u, nil
}

func (r *memRepo) GetByID(_ context.Context, id uuid.UUID) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.users[id]; ok {
		return u, nil
	}
	return user.User{}, apperr.NotFound("user")
}

func (r *memRepo) GetByUsername(_ context.Context, name string) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Username == name {
			return u, nil
		}
	}
	return user.User{}, apperr.NotFound("user")
}

func (r *memRepo) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.users[id]
	u.PasswordHash = hash
	r.users[id] = u
	return nil
}

func (r *memRepo) RecordFailure(_ context.Context, id uuid.UUID, now time.Time, max int, lockFor time.Duration) (user.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.users[id]
	u.FailedAttempts++
	if u.FailedAttempts >= max {
		until := now.Add(lockFor)
		u.LockedUntil = &until
	}
	r.users[id] = u
	return u, nil
}

func (r *memRepo) ResetFailures(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := r.users[id]
	u.FailedAttempts, u.LockedUntil = 0, nil
	r.users[id] = u
	return nil
}

func (r *memRepo) List(context.Context, query.Params) (query.Page[user.User], error) {
	return query.Page[user.User]{}, nil
}

type fixture struct {
	svc   *Service
	repo  *memRepo
	jwt   *kitauth.JWT
	clock *time.Time
	user  user.User
}

const goodPassword = "correct horse battery"

func setup(t *testing.T, role user.Role) *fixture {
	t.Helper()
	keys, err := kitauth.KeyConfig{Secret: strings.Repeat("k", 32)}.KeySet()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	jwt, err := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "t", Audience: "t", Now: func() time.Time { return now }}, keys)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{repo: newRepo(), jwt: jwt, clock: &now}
	hasher := kitauth.NewArgon2id()
	f.svc, err = New(f.repo, hasher, jwt, WithClock(func() time.Time { return *f.clock }))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := hasher.Hash(goodPassword)
	f.user, _ = f.repo.Create(context.Background(), user.User{Username: "m.ahmadi", Name: "M", PasswordHash: hash, Role: role})
	return f
}

func TestLoginIssuesAccessAndRefresh(t *testing.T) {
	f := setup(t, user.RoleFinance)
	s, err := f.svc.Login(context.Background(), LoginInput{Username: "  M.Ahmadi ", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.jwt.Verify(context.Background(), s.Tokens.AccessToken, kitauth.TypeAccess)
	if err != nil || c.Subject != f.user.ID.String() || !c.HasAnyRole("finance") {
		t.Fatalf("claims %+v err %v", c, err)
	}
	if _, err := f.jwt.Verify(context.Background(), s.Tokens.RefreshToken, kitauth.TypeRefresh); err != nil {
		t.Fatal(err)
	}
	// a refresh token is not an access token and vice versa
	if _, err := f.jwt.Verify(context.Background(), s.Tokens.RefreshToken, kitauth.TypeAccess); err == nil {
		t.Fatal("refresh token accepted as access token")
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	f := setup(t, user.RoleAdmin)
	for _, in := range []LoginInput{{"m.ahmadi", "wrong password!"}, {"nobody", goodPassword}, {"??", "x"}} {
		_, err := f.svc.Login(context.Background(), in)
		if !apperr.Is(err, apperr.KindUnauthorized) {
			t.Fatalf("%+v: %v", in, err)
		}
	}
}

func TestLockoutAfterFiveFailuresThenExpires(t *testing.T) {
	f := setup(t, user.RoleAdmin)
	ctx := context.Background()
	for i := 1; i <= MaxFailures; i++ {
		_, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: "wrong password!"})
		want := apperr.KindUnauthorized
		if i == MaxFailures {
			want = apperr.KindForbidden // the failure that reaches the limit already reports the lock
		}
		if !apperr.Is(err, want) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// the right password does not help while locked
	if _, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword}); !apperr.Is(err, apperr.KindForbidden) {
		t.Fatalf("locked account signed in: %v", err)
	}
	*f.clock = f.clock.Add(LockFor + time.Second)
	if _, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword}); err != nil {
		t.Fatalf("lock should have expired: %v", err)
	}
	if u, _ := f.repo.GetByID(ctx, f.user.ID); u.FailedAttempts != 0 || u.LockedUntil != nil {
		t.Fatalf("counter not reset: %+v", u)
	}
}

func TestSuccessResetsTheFailureCounter(t *testing.T) {
	f := setup(t, user.RoleAdmin)
	ctx := context.Background()
	for range MaxFailures - 1 {
		_, _ = f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: "wrong password!"})
	}
	if _, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	// four more failures must not lock: the counter started again
	for range MaxFailures - 1 {
		_, _ = f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: "wrong password!"})
	}
	if _, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword}); err != nil {
		t.Fatalf("locked although the counter was reset: %v", err)
	}
}

func TestRefreshRereadsTheRole(t *testing.T) {
	f := setup(t, user.RoleFinance)
	ctx := context.Background()
	s, err := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	u := f.repo.users[f.user.ID]
	u.Role = user.RoleUser // demoted after the token was issued
	f.repo.users[f.user.ID] = u

	r, err := f.svc.Refresh(ctx, s.Tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := f.jwt.Verify(ctx, r.Tokens.AccessToken, kitauth.TypeAccess)
	if c.HasAnyRole("finance", "admin") {
		t.Fatalf("stale role in the new access token: %v", c.Roles)
	}
	// an access token cannot refresh
	if _, err := f.svc.Refresh(ctx, s.Tokens.AccessToken); !apperr.Is(err, apperr.KindUnauthorized) {
		t.Fatalf("access token accepted for refresh: %v", err)
	}
	// a deleted user cannot refresh
	delete(f.repo.users, f.user.ID)
	if _, err := f.svc.Refresh(ctx, s.Tokens.RefreshToken); !apperr.Is(err, apperr.KindUnauthorized) {
		t.Fatalf("deleted user refreshed: %v", err)
	}
}

func TestExpiredRefreshToken(t *testing.T) {
	f := setup(t, user.RoleAdmin)
	ctx := context.Background()
	s, _ := f.svc.Login(ctx, LoginInput{Username: "m.ahmadi", Password: goodPassword})
	// a verifier whose clock is 31 days ahead
	keys, _ := kitauth.KeyConfig{Secret: strings.Repeat("k", 32)}.KeySet()
	later, _ := kitauth.NewJWT(kitauth.JWTConfig{Issuer: "t", Audience: "t", Now: func() time.Time { return f.clock.Add(31 * 24 * time.Hour) }}, keys)
	svc, _ := New(f.repo, kitauth.NewArgon2id(), later)
	if _, err := svc.Refresh(ctx, s.Tokens.RefreshToken); !apperr.Is(err, apperr.KindUnauthorized) {
		t.Fatalf("expired refresh token: %v", err)
	}
}

func TestLogoutWithoutRevokerIsANoop(t *testing.T) {
	f := setup(t, user.RoleAdmin)
	f.svc.Logout(context.Background(), "garbage", "") // must not panic or fail
}
