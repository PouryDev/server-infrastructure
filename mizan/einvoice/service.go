package einvoice

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Config of the service.
type Config struct {
	// FiscalID is the taxpayer's fiscal-memory id the invoices are sent under (Moadian: شناسه یکتای حافظه مالیاتی).
	FiscalID string
}

// Service is the e-invoice application layer. The provider is optional: without one invoices can still be queued and
// are sent once a provider is configured.
type Service struct {
	repo     Repository
	tx       Transactor
	provider Provider
	events   trail.Emitter
	audit    trail.Recorder
	cfg      Config
	clock    func() time.Time
	log      *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// WithLogger sets the logger of Run.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.log = l } }

// NewService creates the service; provider may be nil.
func NewService(repo Repository, tx Transactor, provider Provider, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	s := &Service{repo: repo, tx: tx, provider: provider, events: events, audit: audit, cfg: cfg, clock: time.Now, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) note(ctx context.Context, actor, action string, d Document, topic string) error {
	details := map[string]any{"document_id": d.ID, "source_type": d.SourceType, "source_id": d.SourceID, "status": d.Status, "reference_number": d.ReferenceNumber}
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "einvoice", EntityID: d.ID.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "einvoice", AggregateID: d.ID.String(), Payload: details})
}

// Enqueue queues an invoice for the authority. It is idempotent per source: a second call returns the same document.
func (s *Service) Enqueue(ctx context.Context, in EnqueueInput) (Document, error) {
	if in.SourceType == "" || len(in.SourceType) > 60 || in.SourceID == "" || len(in.SourceID) > 200 {
		return Document{}, apperr.InvalidField("source", "type (at most 60) and id (at most 200) are required")
	}
	if len(in.Invoice.Lines) == 0 {
		return Document{}, apperr.InvalidField("invoice", "at least one line is required")
	}
	now := s.clock()
	d := Document{ID: uuid.New(), SourceType: in.SourceType, SourceID: in.SourceID, Status: Queued, TraceID: uuid.New(), FiscalID: s.cfg.FiscalID,
		Invoice: in.Invoice, CreatedAt: now, UpdatedAt: now}
	var out Document
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.Insert(ctx, d)
		if err != nil {
			return err
		}
		out = stored
		if !created {
			return nil
		}
		return s.note(ctx, in.Actor, "einvoice.queued", stored, "einvoice.queued")
	})
	return out, err
}

// Get returns a document.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Document, error) {
	return s.repo.Get(ctx, id, false)
}

// List lists documents with filters, sorting and paging.
func (s *Service) List(ctx context.Context, p query.Params) (query.Page[Document], error) {
	return s.repo.List(ctx, p)
}

// Requeue sends a rejected document again under a new trace id (the authority remembers the old one). Fix the source
// data first: the stored invoice is what gets sent, so pass the corrected one.
func (s *Service) Requeue(ctx context.Context, id uuid.UUID, corrected *Invoice, actor string) (Document, error) {
	var out Document
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		d, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = d
		if d.Status != Failed {
			return apperr.New(apperr.KindConflict, "not_failed", "only a rejected document can be sent again; this one is "+string(d.Status))
		}
		d.Status, d.TraceID, d.ReferenceNumber, d.Errors, d.Warnings, d.LastError, d.SentAt, d.UpdatedAt = Queued, uuid.New(), "", nil, nil, "", nil, s.clock()
		if corrected != nil {
			d.Invoice = *corrected
		}
		if err := s.repo.Update(ctx, &d); err != nil {
			return err
		}
		out = d
		return s.note(ctx, actor, "einvoice.requeued", d, "einvoice.queued")
	})
	return out, err
}

func (s *Service) needProvider() error {
	if s.provider == nil {
		return apperr.New(apperr.KindConflict, "no_provider", "no e-invoice provider is configured")
	}
	return nil
}

// SendQueued hands queued documents to the provider, at most one provider batch. A failed call keeps them queued
// (with the error and the attempt count) for the next run. It returns how many were sent.
func (s *Service) SendQueued(ctx context.Context) (int, error) {
	if err := s.needProvider(); err != nil {
		return 0, err
	}
	var sent int
	var callErr error
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		docs, err := s.repo.ClaimQueued(ctx, s.provider.Limits().Submit)
		if err != nil || len(docs) == 0 {
			return err
		}
		packets := make([]Packet, len(docs))
		for i, d := range docs {
			packets[i] = Packet{TraceID: d.TraceID.String(), FiscalID: d.FiscalID, Invoice: d.Invoice}
		}
		receipts, callErr2 := s.provider.Submit(ctx, packets)
		callErr = callErr2
		byTrace := make(map[string]string, len(receipts))
		for _, r := range receipts {
			byTrace[r.TraceID] = r.ReferenceNumber
		}
		now := s.clock()
		for i := range docs {
			d := &docs[i]
			d.Attempts++
			d.UpdatedAt = now
			switch ref, ok := byTrace[d.TraceID.String()]; {
			case ok && ref != "":
				d.Status, d.ReferenceNumber, d.SentAt, d.LastError = Sent, ref, &now, ""
				sent++
			case callErr != nil:
				d.LastError = trim(callErr.Error())
			default:
				d.LastError = "the provider returned no receipt for this invoice"
			}
			if err := s.repo.Update(ctx, d); err != nil {
				return err
			}
			if d.Status == Sent {
				if err := s.note(ctx, "system", "einvoice.sent", *d, "einvoice.sent"); err != nil {
					return err
				}
			}
		}
		return nil // commit the attempt counters even when the provider call failed
	})
	if err != nil {
		return sent, err
	}
	return sent, callErr
}

// RefreshSent asks the provider for the verdict of sent documents, at most one provider batch, and records it. It
// returns how many documents reached a final state.
func (s *Service) RefreshSent(ctx context.Context) (int, error) {
	if err := s.needProvider(); err != nil {
		return 0, err
	}
	var done int
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		docs, err := s.repo.ClaimSent(ctx, s.provider.Limits().Inquire)
		if err != nil || len(docs) == 0 {
			return err
		}
		refs, since := make([]string, len(docs)), *docs[0].SentAt
		for i, d := range docs {
			refs[i] = d.ReferenceNumber
			if d.SentAt != nil && d.SentAt.Before(since) {
				since = *d.SentAt
			}
		}
		results, err := s.provider.Inquire(ctx, refs, since)
		if err != nil {
			return err
		}
		byRef := make(map[string]Result, len(results))
		for _, r := range results {
			byRef[r.ReferenceNumber] = r
		}
		now := s.clock()
		for i := range docs {
			d := &docs[i]
			r, ok := byRef[d.ReferenceNumber]
			if !ok || (r.Outcome != OutcomeSuccess && r.Outcome != OutcomeFailed) {
				continue // still pending, or outside the provider's search window: look again next time
			}
			d.Status, d.Errors, d.Warnings, d.UpdatedAt = Success, r.Errors, r.Warnings, now
			topic := "einvoice.success"
			if r.Outcome == OutcomeFailed {
				d.Status, topic = Failed, "einvoice.failed"
			}
			if err := s.repo.Update(ctx, d); err != nil {
				return err
			}
			if err := s.note(ctx, "system", topic, *d, topic); err != nil {
				return err
			}
			done++
		}
		return nil
	})
	return done, err
}

// Run sends and follows documents every interval until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if s.provider == nil {
				continue
			}
			if n, err := s.SendQueued(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				s.log.ErrorContext(ctx, "e-invoice send failed", "error", err)
			} else if n > 0 {
				s.log.InfoContext(ctx, "e-invoices sent", "count", n)
			}
			if n, err := s.RefreshSent(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				s.log.ErrorContext(ctx, "e-invoice inquiry failed", "error", err)
			} else if n > 0 {
				s.log.InfoContext(ctx, "e-invoice verdicts", "count", n)
			}
		}
	}
}

func trim(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
