// Package reconcile compares our payments with the gateway's own records and finds the differences that cost
// money: paid at the gateway but not recorded here, recorded as paid here but unknown to the gateway,
// different amounts, different outcomes. Each difference can be followed up as a dispute.
package reconcile

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Kind of a mismatch.
type Kind string

const (
	MissingInternal Kind = "missing_internal" // paid at the gateway, unknown here
	MissingGateway  Kind = "missing_gateway"  // paid here, not paid at the gateway
	AmountMismatch  Kind = "amount_mismatch"
	StatusMismatch  Kind = "status_mismatch" // paid on one side only
)

// Local is one of our payments.
type Local struct {
	ID         uuid.UUID
	GatewayRef string
	Amount     int64
	Status     string // payment.Status
	PaidAt     *time.Time
}

// Paid reports whether our books count the payment as received.
func (l Local) Paid() bool {
	return l.Status == "paid" || l.Status == "partially_refunded" || l.Status == "refunded"
}

// Remote is one transaction of the gateway's statement.
type Remote struct {
	GatewayRef string
	TxnRef     string
	Amount     int64
	Paid       bool
	At         time.Time
}

// Mismatch is one difference.
type Mismatch struct {
	Kind          Kind
	PaymentID     *uuid.UUID
	GatewayRef    string
	OurAmount     *int64
	GatewayAmount *int64
	Details       string
}

// Compare matches by gateway reference. Failed, expired and pending payments that the gateway also did not
// collect are consistent and not reported.
func Compare(local []Local, remote []Remote) []Mismatch {
	byRef := make(map[string]Local, len(local))
	for _, l := range local {
		if l.GatewayRef != "" {
			byRef[l.GatewayRef] = l
		}
	}
	seen := map[string]bool{}
	var out []Mismatch
	p64 := func(v int64) *int64 { return &v }
	for _, r := range remote {
		seen[r.GatewayRef] = true
		l, ok := byRef[r.GatewayRef]
		switch {
		case !ok && r.Paid:
			out = append(out, Mismatch{Kind: MissingInternal, GatewayRef: r.GatewayRef, GatewayAmount: p64(r.Amount),
				Details: "paid at the gateway, no such payment here"})
		case !ok:
		case r.Paid && !l.Paid():
			id := l.ID
			out = append(out, Mismatch{Kind: StatusMismatch, PaymentID: &id, GatewayRef: r.GatewayRef, OurAmount: p64(l.Amount), GatewayAmount: p64(r.Amount),
				Details: "paid at the gateway but " + l.Status + " here (run the payment sweep, or refund the payer)"})
		case !r.Paid && l.Paid():
			id := l.ID
			out = append(out, Mismatch{Kind: StatusMismatch, PaymentID: &id, GatewayRef: r.GatewayRef, OurAmount: p64(l.Amount), GatewayAmount: p64(r.Amount),
				Details: "paid here but not collected by the gateway"})
		case r.Paid && l.Paid() && r.Amount != l.Amount:
			id := l.ID
			out = append(out, Mismatch{Kind: AmountMismatch, PaymentID: &id, GatewayRef: r.GatewayRef, OurAmount: p64(l.Amount), GatewayAmount: p64(r.Amount),
				Details: "the amounts differ"})
		}
	}
	for _, l := range local {
		if l.Paid() && !seen[l.GatewayRef] {
			id := l.ID
			out = append(out, Mismatch{Kind: MissingGateway, PaymentID: &id, GatewayRef: l.GatewayRef, OurAmount: p64(l.Amount),
				Details: "paid here, absent from the gateway statement"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].GatewayRef < out[j].GatewayRef })
	return out
}

// ParseCSV reads a gateway statement with the header "ref,amount,status,txn_ref,time" (status is paid or
// anything else; time is RFC 3339 and optional). Amounts are in rials.
func ParseCSV(r io.Reader) ([]Remote, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	head, err := cr.Read()
	if err != nil {
		return nil, apperr.Invalid("the statement is empty")
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, need := range []string{"ref", "amount", "status"} {
		if _, ok := idx[need]; !ok {
			return nil, apperr.Invalid("the statement needs a " + need + " column")
		}
	}
	get := func(rec []string, k string) string {
		if i, ok := idx[k]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var out []Remote
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, apperr.Invalid(fmt.Sprintf("line %d: %v", line, err))
		}
		amount, err := strconv.ParseInt(strings.ReplaceAll(get(rec, "amount"), ",", ""), 10, 64)
		if err != nil || amount < 0 || get(rec, "ref") == "" {
			return nil, apperr.Invalid(fmt.Sprintf("line %d: invalid ref or amount", line))
		}
		it := Remote{GatewayRef: get(rec, "ref"), TxnRef: get(rec, "txn_ref"), Amount: amount, Paid: strings.EqualFold(get(rec, "status"), "paid")}
		if ts := get(rec, "time"); ts != "" {
			if it.At, err = time.Parse(time.RFC3339, ts); err != nil {
				return nil, apperr.Invalid(fmt.Sprintf("line %d: time must be RFC 3339", line))
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// Run is a stored reconciliation.
type Run struct {
	ID        uuid.UUID
	Gateway   string
	From, To  time.Time
	Summary   Summary
	CreatedBy string
	CreatedAt time.Time
}

// Summary counts a run.
type Summary struct {
	Local      int          `json:"local"`
	Remote     int          `json:"remote"`
	Mismatches int          `json:"mismatches"`
	ByKind     map[Kind]int `json:"by_kind"`
}

// Item is a stored mismatch.
type Item struct {
	ID int64
	Mismatch
}

// DisputeStatus of a dispute.
type DisputeStatus string

const (
	DisputeOpen          DisputeStatus = "open"
	DisputeInvestigating DisputeStatus = "investigating"
	DisputeResolved      DisputeStatus = "resolved"
	DisputeRejected      DisputeStatus = "rejected"
)

// Note on a dispute.
type Note struct {
	At    time.Time `json:"at"`
	Actor string    `json:"actor"`
	Text  string    `json:"text"`
}

// Dispute tracks the follow-up of a mismatch or a complaint.
type Dispute struct {
	ID         uuid.UUID
	PaymentID  *uuid.UUID
	ReconItem  *int64
	Kind       string
	Status     DisputeStatus
	OpenedBy   string
	Notes      []Note
	Resolution string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Repository is the persistence port.
type Repository interface {
	Locals(ctx context.Context, gateway string, from, to time.Time) ([]Local, error)
	SaveRun(ctx context.Context, r Run, items []Mismatch) error
	GetRun(ctx context.Context, id uuid.UUID) (Run, error)
	Items(ctx context.Context, runID uuid.UUID) ([]Item, error)
	InsertDispute(ctx context.Context, d Dispute) error
	GetDispute(ctx context.Context, id uuid.UUID, forUpdate bool) (Dispute, error)
	UpdateDispute(ctx context.Context, d *Dispute) error
	Disputes(ctx context.Context, status string, limit int) ([]Dispute, error)
}

// Transactor runs fn in one transaction.
type Transactor interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service is the reconciliation application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	events trail.Emitter
	audit  trail.Recorder
	clock  func() time.Time
}

// NewService creates the service.
func NewService(repo Repository, tx Transactor, events trail.Emitter, audit trail.Recorder) *Service {
	return &Service{repo: repo, tx: tx, events: events, audit: audit, clock: time.Now}
}

// Reconcile compares our payments of [from, to) with the statement and stores the result.
func (s *Service) Reconcile(ctx context.Context, gateway string, from, to time.Time, statement []Remote, actor string) (Run, []Item, error) {
	if gateway == "" || !from.Before(to) {
		return Run{}, nil, apperr.Invalid("a gateway and a valid time range are required")
	}
	var (
		run   Run
		items []Item
	)
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		local, err := s.repo.Locals(ctx, gateway, from, to)
		if err != nil {
			return err
		}
		mm := Compare(local, statement)
		sum := Summary{Local: len(local), Remote: len(statement), Mismatches: len(mm), ByKind: map[Kind]int{}}
		for _, m := range mm {
			sum.ByKind[m.Kind]++
		}
		run = Run{ID: uuid.New(), Gateway: gateway, From: from, To: to, Summary: sum, CreatedBy: actor, CreatedAt: s.clock()}
		if err := s.repo.SaveRun(ctx, run, mm); err != nil {
			return err
		}
		if items, err = s.repo.Items(ctx, run.ID); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "reconcile.run", EntityType: "reconciliation", EntityID: run.ID.String(),
			Details: map[string]any{"gateway": gateway, "mismatches": len(mm)}}); err != nil {
			return err
		}
		if len(mm) == 0 {
			return nil
		}
		return s.events.Emit(ctx, trail.Event{Topic: "reconciliation.mismatches_found", AggregateType: "reconciliation", AggregateID: run.ID.String(),
			Payload: map[string]any{"run_id": run.ID, "gateway": gateway, "count": len(mm), "by_kind": sum.ByKind}})
	})
	return run, items, err
}

// Run returns a stored run with its items.
func (s *Service) Run(ctx context.Context, id uuid.UUID) (Run, []Item, error) {
	r, err := s.repo.GetRun(ctx, id)
	if err != nil {
		return Run{}, nil, err
	}
	items, err := s.repo.Items(ctx, id)
	return r, items, err
}

// OpenDispute starts following up a payment or a reconciliation item.
func (s *Service) OpenDispute(ctx context.Context, paymentID *uuid.UUID, reconItem *int64, kind, note, actor string) (Dispute, error) {
	if strings.TrimSpace(kind) == "" {
		return Dispute{}, apperr.InvalidField("kind", "required")
	}
	if paymentID == nil && reconItem == nil {
		return Dispute{}, apperr.Invalid("a dispute needs a payment or a reconciliation item")
	}
	now := s.clock()
	d := Dispute{ID: uuid.New(), PaymentID: paymentID, ReconItem: reconItem, Kind: kind, Status: DisputeOpen, OpenedBy: actor, CreatedAt: now, UpdatedAt: now}
	if note != "" {
		d.Notes = []Note{{At: now, Actor: actor, Text: note}}
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertDispute(ctx, d); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "dispute.open", EntityType: "dispute", EntityID: d.ID.String(), Details: map[string]any{"kind": kind}})
	})
	return d, err
}

// AddNote appends a note and optionally moves the dispute to investigating.
func (s *Service) AddNote(ctx context.Context, id uuid.UUID, text, actor string) (Dispute, error) {
	if strings.TrimSpace(text) == "" {
		return Dispute{}, apperr.InvalidField("text", "required")
	}
	return s.update(ctx, id, actor, func(d *Dispute) error {
		if d.Status == DisputeResolved || d.Status == DisputeRejected {
			return apperr.New(apperr.KindConflict, "dispute_closed", "the dispute is closed")
		}
		d.Notes = append(d.Notes, Note{At: s.clock(), Actor: actor, Text: text})
		d.Status = DisputeInvestigating
		return nil
	})
}

// Close resolves or rejects a dispute.
func (s *Service) Close(ctx context.Context, id uuid.UUID, status DisputeStatus, resolution, actor string) (Dispute, error) {
	if status != DisputeResolved && status != DisputeRejected {
		return Dispute{}, apperr.InvalidField("status", "must be resolved or rejected")
	}
	if strings.TrimSpace(resolution) == "" {
		return Dispute{}, apperr.InvalidField("resolution", "required")
	}
	return s.update(ctx, id, actor, func(d *Dispute) error {
		if d.Status == DisputeResolved || d.Status == DisputeRejected {
			return apperr.New(apperr.KindConflict, "dispute_closed", "the dispute is already closed")
		}
		d.Status, d.Resolution = status, resolution
		return nil
	})
}

func (s *Service) update(ctx context.Context, id uuid.UUID, actor string, fn func(*Dispute) error) (Dispute, error) {
	var out Dispute
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		d, err := s.repo.GetDispute(ctx, id, true)
		if err != nil {
			return err
		}
		if err := fn(&d); err != nil {
			return err
		}
		if err := s.repo.UpdateDispute(ctx, &d); err != nil {
			return err
		}
		out = d
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "dispute.update", EntityType: "dispute", EntityID: d.ID.String(), Details: map[string]any{"status": d.Status}})
	})
	return out, err
}

// Dispute returns one dispute.
func (s *Service) Dispute(ctx context.Context, id uuid.UUID) (Dispute, error) {
	return s.repo.GetDispute(ctx, id, false)
}

// Disputes lists disputes, optionally by status.
func (s *Service) Disputes(ctx context.Context, status string, limit int) ([]Dispute, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	return s.repo.Disputes(ctx, status, limit)
}
