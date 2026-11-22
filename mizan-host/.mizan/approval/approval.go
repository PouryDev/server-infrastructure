// Package approval is the two-person rule for the actions that cannot be undone lightly: the preparer requests, a DIFFERENT person approves, and only then the action
// runs. The package knows nothing about the actions: the root package registers an Executor per kind (year-end closing, profit appropriation) and the
// action runs inside the approval's own transaction, so a failed action leaves the request pending and an approved one is never run twice.
package approval

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Status of a request.
type Status string

// Statuses.
const (
	Pending  Status = "pending"
	Executed Status = "executed"
	Rejected Status = "rejected"
)

// Approval is a request to run one action.
type Approval struct {
	ID          uuid.UUID
	Kind        string
	Subject     string // what the action is about (e.g. "fiscal_year:12"): one pending request per kind and subject
	Payload     json.RawMessage
	Status      Status
	RequestedBy string
	DecidedBy   string
	Reason      string // why it was rejected
	Result      json.RawMessage
	IdemKey     string
	CreatedAt   time.Time
	DecidedAt   *time.Time
}

// Executor runs an approved action. key is stable for the approval (idempotent actions must use it) and approver is who approved.
type Executor func(ctx context.Context, payload json.RawMessage, key, approver string) (result any, err error)

// Repository is the persistence port.
type Repository interface {
	// Insert stores a request; the same key returns the stored one with created=false; a second pending request of the same kind and subject fails with a conflict "already_pending".
	Insert(ctx context.Context, a Approval) (stored Approval, created bool, err error)
	Get(ctx context.Context, id uuid.UUID, forUpdate bool) (Approval, error)
	List(ctx context.Context, status Status) ([]Approval, error)
	Decide(ctx context.Context, id uuid.UUID, status Status, by, reason string, result json.RawMessage, at time.Time) error
}

// Transactor runs fn in one database transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service is the approval application layer.
type Service struct {
	repo      Repository
	tx        Transactor
	events    trail.Emitter
	audit     trail.Recorder
	executors map[string]Executor
	clock     func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, events trail.Emitter, audit trail.Recorder, opts ...Option) *Service {
	s := &Service{repo: repo, tx: tx, events: events, audit: audit, executors: map[string]Executor{}, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Register sets the executor of a kind.
func (s *Service) Register(kind string, fn Executor) { s.executors[kind] = fn }

// Kinds lists the registered kinds.
func (s *Service) Kinds() []string {
	out := make([]string, 0, len(s.executors))
	for k := range s.executors {
		out = append(out, k)
	}
	return out
}

func (s *Service) note(ctx context.Context, actor, action string, a Approval) error {
	det := map[string]any{"kind": a.Kind, "subject": a.Subject, "status": string(a.Status)}
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "approval", EntityID: a.ID.String(), Details: det}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: action, AggregateType: "approval", AggregateID: a.ID.String(), Payload: det})
}

// Request files a request for an action. subject says what it is about. Idempotent by key.
func (s *Service) Request(ctx context.Context, kind, subject string, payload json.RawMessage, key, actor string) (Approval, error) {
	if _, ok := s.executors[kind]; !ok {
		return Approval{}, apperr.InvalidField("kind", "unknown kind")
	}
	if strings.TrimSpace(key) == "" || len(key) > 120 {
		return Approval{}, apperr.InvalidField("idempotency_key", "required, at most 120 characters")
	}
	if strings.TrimSpace(actor) == "" {
		return Approval{}, apperr.InvalidField("actor", "the preparer must be identified")
	}
	if !json.Valid(payload) {
		return Approval{}, apperr.InvalidField("payload", "must be JSON")
	}
	a := Approval{ID: uuid.New(), Kind: kind, Subject: subject, Payload: payload, Status: Pending, RequestedBy: actor, IdemKey: key, CreatedAt: s.clock()}
	var out Approval
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.Insert(ctx, a)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			return nil
		}
		return s.note(ctx, actor, "approval.requested", stored)
	})
	return out, err
}

// Approve runs the action as the approver. The approver must not be the preparer (a conflict "same_person"). Approving an executed request returns it unchanged;
// a failing action leaves the request pending and returns the action's error.
func (s *Service) Approve(ctx context.Context, id uuid.UUID, approver string) (Approval, error) {
	if strings.TrimSpace(approver) == "" {
		return Approval{}, apperr.InvalidField("actor", "the approver must be identified")
	}
	var out Approval
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = a
		switch a.Status {
		case Executed:
			return nil
		case Rejected:
			return apperr.New(apperr.KindConflict, "rejected", "the request was rejected")
		}
		if a.RequestedBy == approver {
			return apperr.New(apperr.KindConflict, "same_person", "the approver must be a different person than the preparer")
		}
		fn, ok := s.executors[a.Kind]
		if !ok {
			return apperr.New(apperr.KindConflict, "unknown_kind", "no executor for this kind")
		}
		res, err := fn(ctx, a.Payload, "approval:"+a.ID.String(), approver)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(res)
		if err != nil {
			return err
		}
		at := s.clock()
		if err := s.repo.Decide(ctx, id, Executed, approver, "", raw, at); err != nil {
			return err
		}
		a.Status, a.DecidedBy, a.Result, a.DecidedAt = Executed, approver, raw, &at
		out = a
		return s.note(ctx, approver, "approval.executed", a)
	})
	return out, err
}

// Reject closes a pending request without running it. Anyone with the right may reject, including the preparer withdrawing it.
func (s *Service) Reject(ctx context.Context, id uuid.UUID, by, reason string) (Approval, error) {
	if strings.TrimSpace(by) == "" {
		return Approval{}, apperr.InvalidField("actor", "must be identified")
	}
	var out Approval
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = a
		if a.Status == Rejected {
			return nil
		}
		if a.Status != Pending {
			return apperr.New(apperr.KindConflict, "not_pending", "the request was already executed")
		}
		at := s.clock()
		if err := s.repo.Decide(ctx, id, Rejected, by, reason, nil, at); err != nil {
			return err
		}
		a.Status, a.DecidedBy, a.Reason, a.DecidedAt = Rejected, by, reason, &at
		out = a
		return s.note(ctx, by, "approval.rejected", a)
	})
	return out, err
}

// Get returns a request.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Approval, error) {
	return s.repo.Get(ctx, id, false)
}

// List lists requests (empty status = all).
func (s *Service) List(ctx context.Context, status Status) ([]Approval, error) {
	return s.repo.List(ctx, status)
}
