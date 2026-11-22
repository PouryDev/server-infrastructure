// Package user is the domain core: entities and the contracts the outside world must fulfil. It
// imports nothing from the other layers (see internal/architecture_test.go).
package user

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
)

// Role decides what a user may do. mizan lets "finance" and "admin" use the accounting API; "user" can
// sign in but is refused by every accounting route.
type Role string

const (
	RoleUser    Role = "user"
	RoleFinance Role = "finance"
	RoleAdmin   Role = "admin"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleUser || r == RoleFinance || r == RoleAdmin }

const (
	MinPasswordLen = 10
	MaxPasswordLen = 128
)

// User is an account.
type User struct {
	ID           uuid.UUID
	Username     string
	Name         string
	Title        string
	PasswordHash string
	Role         Role
	// FailedAttempts counts consecutive wrong passwords; LockedUntil is set when it reaches the limit.
	FailedAttempts int
	LockedUntil    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// LockedAt reports whether sign-in is refused at now because of too many failures.
func (u User) LockedAt(now time.Time) bool { return u.LockedUntil != nil && now.Before(*u.LockedUntil) }

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// NormalizeUsername lower-cases and trims a username and checks its shape.
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", apperr.InvalidField("username", "3 to 64 characters: letters, digits, '.', '_' or '-'")
	}
	return s, nil
}

// ValidatePassword enforces the password policy.
func ValidatePassword(p string) error {
	if n := len(p); n < MinPasswordLen || n > MaxPasswordLen {
		return apperr.InvalidField("password", fmt.Sprintf("must be %d to %d characters", MinPasswordLen, MaxPasswordLen))
	}
	return nil
}

// Repository is implemented by the persistence adapter. Errors are classified with the kit's apperr
// kinds: a missing row is KindNotFound, a taken username is KindConflict with code "username_taken".
type Repository interface {
	Create(ctx context.Context, u User) (User, error)
	GetByID(ctx context.Context, id uuid.UUID) (User, error)
	GetByUsername(ctx context.Context, username string) (User, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	// RecordFailure counts one wrong password atomically and locks the user for lockFor when the count
	// reaches maxFailures. It returns the user as stored afterwards.
	RecordFailure(ctx context.Context, id uuid.UUID, now time.Time, maxFailures int, lockFor time.Duration) (User, error)
	// ResetFailures clears the failure counter and any lock.
	ResetFailures(ctx context.Context, id uuid.UUID) error
	// List returns one page of users; params carry the client's filters, sorting and pagination.
	List(ctx context.Context, params query.Params) (query.Page[User], error)
}
