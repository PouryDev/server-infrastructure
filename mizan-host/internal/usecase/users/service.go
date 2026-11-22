// Package users holds the user-management use cases (used by the `user` command; there is no self-service
// sign-up).
package users

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/query"

	"github.com/PouryDev/mizan-host/internal/domain/user"
)

// Service creates users and lists them.
type Service struct {
	repo   user.Repository
	hasher kitauth.PasswordHasher
}

// New builds the service.
func New(repo user.Repository, hasher kitauth.PasswordHasher) *Service {
	return &Service{repo: repo, hasher: hasher}
}

// CreateInput describes a new user.
type CreateInput struct {
	Username string
	Name     string
	Title    string
	Password string
	Role     user.Role
}

// Create validates the input, hashes the password (argon2id) and stores the user.
func (s *Service) Create(ctx context.Context, in CreateInput) (user.User, error) {
	name, err := user.NormalizeUsername(in.Username)
	if err != nil {
		return user.User{}, err
	}
	if !in.Role.Valid() {
		return user.User{}, apperr.InvalidField("role", "must be user, finance or admin")
	}
	display := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(display); n < 1 || n > 100 {
		return user.User{}, apperr.InvalidField("name", "must be 1 to 100 characters")
	}
	if err := user.ValidatePassword(in.Password); err != nil {
		return user.User{}, err
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return user.User{}, apperr.Internal(err)
	}
	return s.repo.Create(ctx, user.User{Username: name, Name: display, Title: strings.TrimSpace(in.Title), PasswordHash: hash, Role: in.Role})
}

// SetPassword replaces a user's password and clears any lock.
func (s *Service) SetPassword(ctx context.Context, username, password string) error {
	name, err := user.NormalizeUsername(username)
	if err != nil {
		return err
	}
	if err := user.ValidatePassword(password); err != nil {
		return err
	}
	u, err := s.repo.GetByUsername(ctx, name)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.repo.UpdatePasswordHash(ctx, u.ID, hash); err != nil {
		return err
	}
	return s.repo.ResetFailures(ctx, u.ID)
}

// List returns a page of users.
func (s *Service) List(ctx context.Context, params query.Params) (query.Page[user.User], error) {
	return s.repo.List(ctx, params)
}
