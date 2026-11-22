// Package account holds the sign-in use cases: login with throttling, token refresh, logout and the profile.
// It depends only on interfaces: the user repository (domain), a password hasher and a token
// issuer/verifier (kit).
package account

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"

	"github.com/PouryDev/mizan-host/internal/domain/user"
)

const (
	// MaxFailures wrong passwords lock the account for LockFor.
	MaxFailures = 5
	LockFor     = 15 * time.Minute
)

// Tokens is what the service needs from the token implementation.
type Tokens interface {
	kitauth.SessionIssuer
	kitauth.TokenVerifier
}

// Session is the result of a successful authentication.
type Session struct {
	User   user.User
	Tokens kitauth.TokenPair
}

// LoginInput is a sign-in request.
type LoginInput struct {
	Username string
	Password string
}

// Service implements the account use cases.
type Service struct {
	users   user.Repository
	hasher  kitauth.PasswordHasher
	tokens  Tokens
	revoker kitauth.Revoker // optional: without it tokens live until they expire
	now     func() time.Time
	// dummyHash is verified when the username is unknown, so that response time does not reveal
	// which usernames exist.
	dummyHash string
}

// Option configures optional collaborators.
type Option func(*Service)

// WithRevoker makes Logout revoke the presented tokens (the store is Redis in production).
func WithRevoker(r kitauth.Revoker) Option { return func(s *Service) { s.revoker = r } }

// WithClock replaces time.Now (tests).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// New builds the service.
func New(users user.Repository, hasher kitauth.PasswordHasher, tokens Tokens, opts ...Option) (*Service, error) {
	dummy, err := hasher.Hash("not-a-real-password")
	if err != nil {
		return nil, fmt.Errorf("account: prepare dummy hash: %w", err)
	}
	s := &Service{users: users, hasher: hasher, tokens: tokens, dummyHash: dummy, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Login checks credentials. Unknown username and wrong password give the same error; MaxFailures wrong
// passwords in a row lock the account for LockFor, during which even the right password is refused.
func (s *Service) Login(ctx context.Context, in LoginInput) (Session, error) {
	invalid := apperr.Unauthorized("invalid username or password")
	name, err := user.NormalizeUsername(in.Username)
	if err != nil { // cannot exist: same answer, same cost
		_, _, _ = s.hasher.Verify(in.Password, s.dummyHash)
		return Session{}, invalid
	}
	u, err := s.users.GetByUsername(ctx, name)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			_, _, _ = s.hasher.Verify(in.Password, s.dummyHash)
			return Session{}, invalid
		}
		return Session{}, err
	}
	now := s.now()
	if u.LockedAt(now) {
		return Session{}, errLocked()
	}
	if u.LockedUntil != nil { // the lock has run out: a fresh set of attempts
		if err := s.users.ResetFailures(ctx, u.ID); err != nil {
			return Session{}, err
		}
		u.FailedAttempts, u.LockedUntil = 0, nil
	}
	ok, rehash, err := s.hasher.Verify(in.Password, u.PasswordHash)
	if err != nil {
		return Session{}, apperr.Internal(err)
	}
	if !ok {
		after, err := s.users.RecordFailure(ctx, u.ID, now, MaxFailures, LockFor)
		if err != nil {
			return Session{}, err
		}
		if after.LockedAt(now) {
			return Session{}, errLocked()
		}
		return Session{}, invalid
	}
	if u.FailedAttempts > 0 {
		if err := s.users.ResetFailures(ctx, u.ID); err != nil {
			return Session{}, err
		}
	}
	if rehash { // hashing parameters were raised since this password was stored: upgrade it
		if h, err := s.hasher.Hash(in.Password); err == nil {
			_ = s.users.UpdatePasswordHash(ctx, u.ID, h) // best effort, the login itself succeeded
		}
	}
	return s.session(u)
}

func errLocked() error {
	return apperr.New(apperr.KindForbidden, "account_locked",
		fmt.Sprintf("too many failed attempts; try again in %d minutes", int(LockFor.Minutes())))
}

// Refresh exchanges a refresh token for a new token pair. The user is loaded again, so a changed role
// takes effect and a deleted or locked account can no longer refresh.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Session, error) {
	claims, err := s.tokens.Verify(ctx, refreshToken, kitauth.TypeRefresh)
	if err != nil {
		return Session{}, err
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return Session{}, apperr.Unauthorized("invalid token")
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return Session{}, apperr.Unauthorized("invalid token")
		}
		return Session{}, err
	}
	if u.LockedAt(s.now()) {
		return Session{}, errLocked()
	}
	return s.session(u)
}

// Logout revokes the presented tokens when a revocation store is configured; without one it does nothing
// (stateless tokens simply expire) and the caller still drops its cookies. Tokens that do not verify are
// ignored: signing out is always allowed.
func (s *Service) Logout(ctx context.Context, accessToken, refreshToken string) {
	if s.revoker == nil {
		return
	}
	for _, t := range []struct {
		raw string
		typ kitauth.TokenType
	}{{accessToken, kitauth.TypeAccess}, {refreshToken, kitauth.TypeRefresh}} {
		if t.raw == "" {
			continue
		}
		if c, err := s.tokens.Verify(ctx, t.raw, t.typ); err == nil {
			_ = s.revoker.Revoke(ctx, c) // best effort
		}
	}
}

// Me returns the authenticated user.
func (s *Service) Me(ctx context.Context, id uuid.UUID) (user.User, error) {
	return s.users.GetByID(ctx, id)
}

func (s *Service) session(u user.User) (Session, error) {
	pair, err := s.tokens.IssuePair(u.ID.String(), []string{string(u.Role)}, map[string]any{"name": u.Name})
	if err != nil {
		return Session{}, apperr.Internal(err)
	}
	return Session{User: u, Tokens: pair}, nil
}
