package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/query"

	"github.com/PouryDev/mizan-host/internal/domain/user"
)

type repo struct{ created []user.User }

func (r *repo) Create(_ context.Context, u user.User) (user.User, error) {
	r.created = append(r.created, u)
	return u, nil
}
func (*repo) GetByID(context.Context, uuid.UUID) (user.User, error) { return user.User{}, nil }
func (*repo) GetByUsername(context.Context, string) (user.User, error) {
	return user.User{ID: uuid.New()}, nil
}
func (*repo) UpdatePasswordHash(context.Context, uuid.UUID, string) error { return nil }
func (*repo) RecordFailure(context.Context, uuid.UUID, time.Time, int, time.Duration) (user.User, error) {
	return user.User{}, nil
}
func (*repo) ResetFailures(context.Context, uuid.UUID) error { return nil }
func (*repo) List(context.Context, query.Params) (query.Page[user.User], error) {
	return query.Page[user.User]{}, nil
}

func TestCreateValidatesAndHashes(t *testing.T) {
	r := &repo{}
	s := New(r, kitauth.NewArgon2id())
	ctx := context.Background()
	bad := []CreateInput{
		{Username: "x", Name: "N", Password: "long enough pw", Role: user.RoleAdmin},
		{Username: "ok.user", Name: "N", Password: "short", Role: user.RoleAdmin},
		{Username: "ok.user", Name: "N", Password: "long enough pw", Role: "root"},
		{Username: "ok.user", Name: " ", Password: "long enough pw", Role: user.RoleAdmin},
	}
	for _, in := range bad {
		if _, err := s.Create(ctx, in); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%+v accepted: %v", in, err)
		}
	}
	u, err := s.Create(ctx, CreateInput{Username: " Ok.User ", Name: "N", Password: "long enough pw", Role: user.RoleFinance})
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "ok.user" || u.PasswordHash == "" || u.PasswordHash == "long enough pw" {
		t.Fatalf("%+v", u)
	}
}
