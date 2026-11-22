package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Config of the payment service.
type Config struct {
	DefaultGateway string
	// CallbackBaseURL is the public URL gateways redirect payers to, without the gateway name, e.g.
	// "https://api.example.com/v1/gateway-callbacks". The gateway name is appended.
	CallbackBaseURL string
	// AllowedReturnHosts restricts InitiateInput.ReturnURL to these hosts (open-redirect protection). Empty means
	// return URLs are not accepted at all.
	AllowedReturnHosts []string
	DefaultExpiry      time.Duration // default 30 minutes
}

// Service is the payment application layer.
type Service struct {
	repo        Repository
	tx          Transactor
	gateways    map[string]Gateway
	settlements map[string]Settlement
	events      trail.Emitter
	audit       trail.Recorder
	cfg         Config
	clock       func() time.Time
	log         *slog.Logger
}

// Option customises the Service.
type Option func(*Service)

// WithGateway registers a gateway under its Name.
func WithGateway(g Gateway) Option { return func(s *Service) { s.gateways[g.Name()] = g } }

// WithSettlement registers the handler of a purpose type.
func WithSettlement(purposeType string, st Settlement) Option {
	return func(s *Service) { s.settlements[purposeType] = st }
}

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(s *Service) { s.log = l } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.DefaultExpiry <= 0 {
		cfg.DefaultExpiry = 30 * time.Minute
	}
	s := &Service{repo: repo, tx: tx, gateways: map[string]Gateway{}, settlements: map[string]Settlement{},
		events: events, audit: audit, cfg: cfg, clock: time.Now, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// RegisterSettlement adds a purpose handler after construction (the wiring code needs the service first
// when a settlement posts through other services).
func (s *Service) RegisterSettlement(purposeType string, st Settlement) {
	s.settlements[purposeType] = st
}

func (s *Service) gateway(name string) (Gateway, error) {
	if name == "" {
		name = s.cfg.DefaultGateway
	}
	g, ok := s.gateways[name]
	if !ok {
		return nil, apperr.New(apperr.KindInvalid, "unknown_gateway", "unknown payment gateway")
	}
	return g, nil
}

func (s *Service) returnURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !slices.Contains(s.cfg.AllowedReturnHosts, u.Hostname()) {
		return apperr.Invalid("return_url host is not allowed")
	}
	return nil
}

// Initiate creates (or returns, for a repeated key) a payment and asks the gateway for the payment page.
func (s *Service) Initiate(ctx context.Context, in InitiateInput) (Payment, error) {
	if err := in.Validate(); err != nil {
		return Payment{}, err
	}
	gw, err := s.gateway(in.Gateway)
	if err != nil {
		return Payment{}, err
	}
	if err := s.returnURL(in.ReturnURL); err != nil {
		return Payment{}, err
	}
	if in.ExpiresIn == 0 {
		in.ExpiresIn = s.cfg.DefaultExpiry
	}
	md := map[string]string{}
	for k, v := range in.Metadata {
		md[k] = v
	}
	if in.ReturnURL != "" {
		md["return_url"] = in.ReturnURL
	}
	now := s.clock()
	p := Payment{
		ID: uuid.New(), IdemKey: in.IdemKey, Status: Created, Purpose: in.Purpose, PayerRef: in.PayerRef, Amount: in.Amount,
		Gateway: gw.Name(), Description: in.Description, CallbackURL: strings.TrimRight(s.cfg.CallbackBaseURL, "/") + "/" + gw.Name(),
		ExpiresAt: now.Add(in.ExpiresIn), Refunded: money.Zero(in.Amount.Currency()), Metadata: md, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	var cur Payment
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.Insert(ctx, p)
		if err != nil {
			return err
		}
		if !created && (stored.Amount != in.Amount || stored.Purpose != in.Purpose || stored.Gateway != gw.Name()) {
			return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different payment")
		}
		cur = stored
		if created {
			return s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "payment.create", EntityType: "payment", EntityID: stored.ID.String(),
				Details: map[string]any{"amount": in.Amount.Amount(), "gateway": gw.Name(), "purpose": in.Purpose.Type + ":" + in.Purpose.ID}})
		}
		return nil
	})
	if err != nil || cur.Status != Created {
		return cur, err // already requested, or already final: the stored payment is the answer
	}

	res, err := gw.Request(ctx, RequestInput{PaymentID: cur.ID.String(), Amount: cur.Amount, CallbackURL: cur.CallbackURL, Description: cur.Description, PayerRef: cur.PayerRef})
	switch {
	case errors.Is(err, ErrRejected):
		var rej *RejectedError
		code, msg := "rejected", err.Error()
		if errors.As(err, &rej) {
			code, msg = rej.Code, rej.Message
		}
		if _, ferr := s.fail(ctx, cur.ID, code, msg, "gateway request"); ferr != nil {
			return cur, ferr
		}
		return cur, apperr.New(apperr.KindUnavailable, "gateway_rejected", "the payment gateway refused the request")
	case err != nil:
		s.log.WarnContext(ctx, "gateway request failed", "gateway", gw.Name(), "payment", cur.ID, "error", err)
		return cur, apperr.New(apperr.KindUnavailable, "gateway_unavailable", "the payment gateway is unavailable, retry with the same key")
	}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.repo.Get(ctx, cur.ID, true)
		if err != nil {
			return err
		}
		if locked.Status != Created { // a concurrent retry got there first
			cur = locked
			return nil
		}
		if err := locked.MarkPending(res.Ref, res.PayURL); err != nil {
			return err
		}
		if err := s.save(ctx, &locked, Created, "gateway accepted"); err != nil {
			return err
		}
		cur = locked
		return nil
	})
	return cur, err
}

func (s *Service) save(ctx context.Context, p *Payment, from Status, reason string) error {
	if err := s.repo.Update(ctx, p); err != nil {
		return err
	}
	return s.repo.AddTransition(ctx, p.ID, Transition{From: from, To: p.Status, Reason: reason, At: s.clock()})
}

// Get returns a payment.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Payment, error) {
	return s.repo.Get(ctx, id, false)
}

// History returns the status changes of a payment.
func (s *Service) History(ctx context.Context, id uuid.UUID) ([]Transition, error) {
	return s.repo.Transitions(ctx, id)
}

// Refunds lists the refunds of a payment.
func (s *Service) Refunds(ctx context.Context, id uuid.UUID) ([]Refund, error) {
	return s.repo.Refunds(ctx, id)
}

// List lists payments with the client's filters, sort and pagination.
func (s *Service) List(ctx context.Context, p query.Params) (query.Page[Payment], error) {
	return s.repo.List(ctx, p)
}

// UnsettledPaid lists paid payments whose settlement failed: money was received but nothing was applied.
func (s *Service) UnsettledPaid(ctx context.Context, limit int) ([]Payment, error) {
	return s.repo.UnsettledPaid(ctx, limit)
}

// CallbackResult is what the HTTP layer needs after a callback.
type CallbackResult struct {
	Payment Payment
	Replay  bool // the payment was already final
}

// HandleCallback processes the redirect (or server call) of a gateway. The raw event is stored first, the
// parameters are never trusted: the gateway is asked to verify, and only that answer moves the payment.
// A repeated callback is harmless: a final payment is returned unchanged.
func (s *Service) HandleCallback(ctx context.Context, gateway string, params map[string]string, raw, remoteIP string) (CallbackResult, error) {
	gw, err := s.gateway(gateway)
	if err != nil {
		return CallbackResult{}, err
	}
	cb, err := gw.ParseCallback(params)
	if err != nil {
		return CallbackResult{}, apperr.Invalid("malformed gateway callback")
	}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		_, err := s.repo.SaveEvent(ctx, GatewayEvent{Gateway: gw.Name(), EventKey: cb.EventKey, Raw: raw, RemoteIP: remoteIP})
		return err
	})
	if err != nil {
		return CallbackResult{}, err
	}
	p, err := s.repo.ByGatewayRef(ctx, gw.Name(), cb.Ref, false)
	if err != nil {
		_ = s.finishEvent(ctx, gw.Name(), cb.EventKey, nil, "unknown_payment")
		return CallbackResult{}, err
	}
	if p.Status != Pending {
		_ = s.finishEvent(ctx, gw.Name(), cb.EventKey, &p.ID, "replay_"+string(p.Status))
		return CallbackResult{Payment: p, Replay: true}, nil
	}
	if !cb.ClaimedPaid {
		out, err := s.fail(ctx, p.ID, "cancelled", "the payer did not complete the payment", "callback")
		_ = s.finishEvent(ctx, gw.Name(), cb.EventKey, &p.ID, "cancelled")
		return CallbackResult{Payment: out}, err
	}
	vr, err := gw.Verify(ctx, VerifyInput{Ref: p.GatewayRef, Amount: p.Amount, PaymentID: p.ID.String()})
	if err != nil {
		// Unknown outcome: keep the payment pending; the sweeper verifies it again.
		s.log.WarnContext(ctx, "verify failed, payment stays pending", "payment", p.ID, "error", err)
		_ = s.finishEvent(ctx, gw.Name(), cb.EventKey, &p.ID, "verify_unavailable")
		return CallbackResult{Payment: p}, apperr.New(apperr.KindUnavailable, "verify_unavailable", "the payment is being verified")
	}
	var out Payment
	if vr.Paid {
		out, err = s.settle(ctx, p.ID, vr)
	} else {
		out, err = s.fail(ctx, p.ID, vr.Code, vr.Message, "verify")
	}
	outcome := "verified"
	if err != nil {
		outcome = "error"
	} else if !vr.Paid {
		outcome = "not_paid"
	}
	_ = s.finishEvent(ctx, gw.Name(), cb.EventKey, &p.ID, outcome)
	return CallbackResult{Payment: out}, err
}

func (s *Service) finishEvent(ctx context.Context, gateway, key string, id *uuid.UUID, outcome string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error { return s.repo.FinishEvent(ctx, gateway, key, id, outcome) })
}

// settle turns a verified payment into a paid one and applies its purpose, atomically.
func (s *Service) settle(ctx context.Context, id uuid.UUID, vr VerifyResult) (Payment, error) {
	var out Payment
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = p
		if p.Status != Pending {
			return nil // somebody else (a duplicate callback, the sweeper) settled it first
		}
		if vr.Amount != nil && *vr.Amount != p.Amount {
			// Money was collected, but not the amount we asked for: do not apply it, flag it for a human.
			if err := p.MarkFailed("amount_mismatch", fmt.Sprintf("gateway collected %s, expected %s", *vr.Amount, p.Amount)); err != nil {
				return err
			}
			if err := s.save(ctx, &p, Pending, "amount mismatch"); err != nil {
				return err
			}
			out = p
			return s.events.Emit(ctx, trail.Event{Topic: "payment.anomaly", AggregateType: "payment", AggregateID: p.ID.String(),
				Payload: map[string]any{"payment_id": p.ID, "kind": "amount_mismatch", "expected": p.Amount.Amount(), "collected": vr.Amount.Amount()}})
		}
		if err := p.MarkPaid(vr.TxnRef, vr.CardPAN, s.clock()); err != nil {
			return err
		}
		if st, ok := s.settlements[p.Purpose.Type]; ok {
			var jid *uuid.UUID
			serr := s.repo.WithSavepoint(ctx, func(ctx context.Context) error {
				var err error
				jid, err = st.OnPaid(ctx, p)
				return err
			})
			switch {
			case serr == nil:
				p.SettledJournal = jid
			case isBusinessError(serr):
				// Keep the money: record the payment as paid and flag it, instead of losing the capture.
				p.Metadata["settlement_error"] = serr.Error()
				if err := s.events.Emit(ctx, trail.Event{Topic: "payment.settlement_failed", AggregateType: "payment", AggregateID: p.ID.String(),
					Payload: map[string]any{"payment_id": p.ID, "purpose": p.Purpose.Type + ":" + p.Purpose.ID, "error": serr.Error()}}); err != nil {
					return err
				}
			default:
				return serr
			}
		}
		if err := s.save(ctx, &p, Pending, "verified by gateway"); err != nil {
			return err
		}
		out = p
		if err := s.audit.Record(ctx, trail.Entry{Actor: "system", Action: "payment.paid", EntityType: "payment", EntityID: p.ID.String(),
			Details: map[string]any{"txn_ref": p.GatewayTxnRef}}); err != nil {
			return err
		}
		return s.events.Emit(ctx, trail.Event{Topic: "payment.paid", AggregateType: "payment", AggregateID: p.ID.String(), Payload: paidPayload(p)})
	})
	return out, err
}

func isBusinessError(err error) bool {
	return apperr.Is(err, apperr.KindInvalid) || apperr.Is(err, apperr.KindConflict) || apperr.Is(err, apperr.KindNotFound)
}

func paidPayload(p Payment) map[string]any {
	return map[string]any{
		"payment_id": p.ID, "amount": p.Amount.Amount(), "currency": p.Amount.Currency(), "gateway": p.Gateway,
		"txn_ref": p.GatewayTxnRef, "purpose_type": p.Purpose.Type, "purpose_id": p.Purpose.ID, "payer_ref": p.PayerRef, "paid_at": p.PaidAt,
	}
}

// fail moves a created or pending payment to failed.
func (s *Service) fail(ctx context.Context, id uuid.UUID, code, reason, why string) (Payment, error) {
	return s.finalize(ctx, id, "payment.failed", why, func(p *Payment) error { return p.MarkFailed(code, reason) })
}

func (s *Service) expire(ctx context.Context, id uuid.UUID) (Payment, error) {
	return s.finalize(ctx, id, "payment.expired", "not completed in time", func(p *Payment) error { return p.MarkExpired() })
}

func (s *Service) finalize(ctx context.Context, id uuid.UUID, topic, why string, mark func(*Payment) error) (Payment, error) {
	var out Payment
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.repo.Get(ctx, id, true)
		if err != nil {
			return err
		}
		out = p
		if p.Status != Created && p.Status != Pending {
			return nil
		}
		from := p.Status
		if err := mark(&p); err != nil {
			return err
		}
		if err := s.save(ctx, &p, from, why); err != nil {
			return err
		}
		out = p
		return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "payment", AggregateID: p.ID.String(),
			Payload: map[string]any{"payment_id": p.ID, "purpose_type": p.Purpose.Type, "purpose_id": p.Purpose.ID, "reason": p.FailureReason}})
	})
	return out, err
}

// SweepResult counts what a sweep did.
type SweepResult struct{ Checked, Paid, Failed, Expired, Unknown int }

// Sweep recovers payments whose callback never arrived. For every open payment older than minAge it asks
// the gateway: paid ones are settled (the "paid at the gateway but not recorded" case), unpaid ones past their
// expiry are expired, the rest are left alone. Run it every minute or two.
func (s *Service) Sweep(ctx context.Context, minAge time.Duration, limit int) (SweepResult, error) {
	var res SweepResult
	open, err := s.repo.Open(ctx, s.clock().Add(-minAge), limit)
	if err != nil {
		return res, err
	}
	for _, p := range open {
		res.Checked++
		expired := !s.clock().Before(p.ExpiresAt)
		if p.Status == Created { // the gateway never accepted the request
			if expired {
				if _, err := s.expire(ctx, p.ID); err != nil {
					return res, err
				}
				res.Expired++
			}
			continue
		}
		gw, err := s.gateway(p.Gateway)
		if err != nil {
			res.Unknown++
			continue
		}
		vr, err := gw.Verify(ctx, VerifyInput{Ref: p.GatewayRef, Amount: p.Amount, PaymentID: p.ID.String()})
		switch {
		case err != nil:
			res.Unknown++
		case vr.Paid:
			if out, err := s.settle(ctx, p.ID, vr); err != nil {
				return res, err
			} else if out.Status == Paid {
				res.Paid++
			} else {
				res.Failed++
			}
		case expired:
			if _, err := s.expire(ctx, p.ID); err != nil {
				return res, err
			}
			res.Expired++
		}
	}
	return res, nil
}

// Run sweeps periodically until ctx ends (a lifecycle.Runner).
func (s *Service) Run(ctx context.Context, every, minAge time.Duration) error {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if r, err := s.Sweep(ctx, minAge, 200); err != nil && ctx.Err() == nil {
				s.log.ErrorContext(ctx, "payment sweep failed", "error", err)
			} else if r.Paid+r.Expired > 0 {
				s.log.InfoContext(ctx, "payment sweep", "checked", r.Checked, "paid", r.Paid, "expired", r.Expired, "unknown", r.Unknown)
			}
		}
	}
}

// RefundInput requests a refund.
type RefundInput struct {
	PaymentID uuid.UUID
	IdemKey   string
	Amount    money.Money // zero value: everything that is left
	Reason    string
	Actor     string
}

// Refund returns money to the payer, fully or partially. The refund is recorded first. When the gateway has a
// refund API it is called; otherwise (no Refunder, or ErrNotSupported) the refund becomes a manual one: an
// agent claims it, transfers the money card-to-card outside the system and records the transfer with
// CompleteManualRefund. Books and payment move only when a refund is done, never at request or claim.
func (s *Service) Refund(ctx context.Context, in RefundInput) (Refund, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return Refund{}, apperr.InvalidField("idempotency_key", "required")
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) > maxText || containsCardNumber(in.Reason) {
		return Refund{}, apperr.InvalidField("reason", "at most 500 characters and no card number")
	}
	var r Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.repo.Get(ctx, in.PaymentID, true)
		if err != nil {
			return err
		}
		if p.Status != Paid && p.Status != PartiallyRefunded {
			return apperr.New(apperr.KindConflict, "not_refundable", "only a paid payment can be refunded")
		}
		// A retry returns the stored refund; it must not be checked against its own reservation.
		prev, err := s.repo.RefundByKey(ctx, in.IdemKey)
		switch {
		case err == nil:
			if prev.PaymentID != p.ID || (in.Amount.Currency() != "" && prev.Amount != in.Amount) {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different refund")
			}
			r = prev
			return nil
		case !apperr.Is(err, apperr.KindNotFound):
			return err
		}
		amount := in.Amount
		if amount.Currency() == "" {
			amount = p.Refundable()
		}
		if amount.Currency() != p.Amount.Currency() || !amount.IsPositive() {
			return apperr.InvalidField("amount", "must be a positive amount in the payment's currency")
		}
		reserved, err := s.repo.PendingRefundTotal(ctx, p.ID)
		if err != nil {
			return err
		}
		if amount.Amount() > p.Refundable().Amount()-reserved {
			return apperr.New(apperr.KindConflict, "refund_exceeds_payment", "the refund is larger than what is left of the payment")
		}
		if err := s.canRefund(ctx, p, reserved+amount.Amount()); err != nil {
			return err
		}
		gw, err := s.gateway(p.Gateway)
		if err != nil {
			return err
		}
		method := RefundManualCardToCard
		if _, ok := gw.(Refunder); ok {
			method = RefundViaGateway
		}
		now := s.clock()
		stored, created, err := s.repo.InsertRefund(ctx, Refund{ID: uuid.New(), PaymentID: p.ID, IdemKey: in.IdemKey, Amount: amount,
			Reason: in.Reason, Status: RefundPending, Method: method, CreatedAt: now, UpdatedAt: now})
		if err != nil {
			return err
		}
		if !created && (stored.PaymentID != p.ID || stored.Amount != amount) {
			return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different refund")
		}
		r = stored
		if !created {
			return nil
		}
		if err := s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "payment.refund_requested", EntityType: "payment", EntityID: p.ID.String(),
			Details: map[string]any{"refund_id": r.ID, "amount": amount.Amount(), "reason": in.Reason, "method": method}}); err != nil {
			return err
		}
		if method == RefundManualCardToCard {
			return s.manualRequested(ctx, r, in.Actor)
		}
		return nil
	})
	if err != nil || r.Status != RefundPending || r.Method != RefundViaGateway {
		return r, err
	}

	// A gateway refund that is still pending: this is the first attempt or a retry of one that was unavailable.
	p, err := s.repo.Get(ctx, r.PaymentID, false)
	if err != nil {
		return r, err
	}
	gw, err := s.gateway(p.Gateway)
	if err != nil {
		return r, err
	}
	rf, ok := gw.(Refunder)
	if !ok {
		return s.toManual(ctx, r.ID, in.Actor)
	}
	res, err := rf.Refund(ctx, GatewayRefundInput{Ref: p.GatewayRef, TxnRef: p.GatewayTxnRef, Amount: r.Amount, Reason: r.Reason})
	switch {
	case errors.Is(err, ErrNotSupported):
		return s.toManual(ctx, r.ID, in.Actor)
	case errors.Is(err, ErrRejected):
		return s.failGatewayRefund(ctx, r.ID, err.Error())
	case err != nil:
		return r, apperr.New(apperr.KindUnavailable, "gateway_unavailable", "the payment gateway is unavailable, retry with the same key")
	}
	return s.completeGatewayRefund(ctx, r.ID, res.Ref, in.Actor)
}

// canRefund asks the purpose's settlement whether `total` (everything reserved for the payment) can be returned.
func (s *Service) canRefund(ctx context.Context, p Payment, total int64) error {
	if st, ok := s.settlements[p.Purpose.Type]; ok {
		return st.CanRefund(ctx, p, total)
	}
	return nil
}

func (s *Service) manualRequested(ctx context.Context, r Refund, actor string) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "refund.manual_requested", EntityType: "refund", EntityID: r.ID.String(),
		Details: map[string]any{"payment_id": r.PaymentID, "amount": r.Amount.Amount(), "reason": r.Reason}}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: "refund.manual_requested", AggregateType: "refund", AggregateID: r.ID.String(), Payload: refundPayload(r)})
}

func refundPayload(r Refund) map[string]any {
	return map[string]any{"refund_id": r.ID, "payment_id": r.PaymentID, "amount": r.Amount.Amount(), "currency": r.Amount.Currency(),
		"method": r.Method, "status": r.Status, "reason": r.Reason}
}

// toManual turns a pending gateway refund into a manual one (the gateway cannot refund after all).
func (s *Service) toManual(ctx context.Context, id uuid.UUID, actor string) (Refund, error) {
	var out Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.GetRefund(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		if r.Status != RefundPending || r.Method != RefundViaGateway {
			return nil
		}
		r.Method, r.UpdatedAt = RefundManualCardToCard, s.clock()
		if err := s.repo.UpdateRefund(ctx, &r); err != nil {
			return err
		}
		out = r
		return s.manualRequested(ctx, r, actor)
	})
	return out, err
}

func (s *Service) failGatewayRefund(ctx context.Context, id uuid.UUID, reason string) (Refund, error) {
	var out Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.GetRefund(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		if r.Status != RefundPending || r.Method != RefundViaGateway {
			return nil
		}
		r.Status, r.Failure, r.UpdatedAt = RefundFailed, reason, s.clock()
		out = r
		if err := s.repo.UpdateRefund(ctx, &r); err != nil {
			return err
		}
		return s.refundEnded(ctx, r, "system")
	})
	if err == nil && out.Status == RefundFailed {
		err = apperr.New(apperr.KindConflict, "refund_rejected", "the gateway rejected the refund")
	}
	return out, err
}

// refundEnded audits and announces a refund that failed or was cancelled.
func (s *Service) refundEnded(ctx context.Context, r Refund, actor string) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "refund." + string(r.Status), EntityType: "refund", EntityID: r.ID.String(),
		Details: map[string]any{"payment_id": r.PaymentID, "reason": r.Failure}}); err != nil {
		return err
	}
	pl := refundPayload(r)
	pl["failure"] = r.Failure
	return s.events.Emit(ctx, trail.Event{Topic: "refund.failed", AggregateType: "refund", AggregateID: r.ID.String(), Payload: pl})
}

// completeGatewayRefund records a refund the gateway returned. It is idempotent.
func (s *Service) completeGatewayRefund(ctx context.Context, id uuid.UUID, gatewayRef, actor string) (Refund, error) {
	var out Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.GetRefund(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		if r.Status == RefundDone {
			return nil
		}
		if r.Status != RefundPending || r.Method != RefundViaGateway {
			return apperr.New(apperr.KindConflict, "refund_not_pending", "the refund is not a pending gateway refund")
		}
		r.Status, r.GatewayRef = RefundDone, gatewayRef
		out, err = s.applyRefund(ctx, r, actor)
		return err
	})
	return out, err
}

// applyRefund is the one place where a refund reaches the payment and the books. The caller holds the refund's
// row lock and has already set it to done; everything below is in its transaction.
func (s *Service) applyRefund(ctx context.Context, r Refund, actor string) (Refund, error) {
	p, err := s.repo.Get(ctx, r.PaymentID, true)
	if err != nil {
		return r, err
	}
	from := p.Status
	if err := p.ApplyRefund(r.Amount); err != nil {
		return r, err
	}
	if st, ok := s.settlements[p.Purpose.Type]; ok {
		if r.JournalID, err = st.OnRefunded(ctx, p, r); err != nil {
			return r, err
		}
	}
	r.UpdatedAt = s.clock()
	if err := s.repo.UpdateRefund(ctx, &r); err != nil {
		return r, err
	}
	if err := s.save(ctx, &p, from, "refund "+r.ID.String()); err != nil {
		return r, err
	}
	details := map[string]any{"refund_id": r.ID, "amount": r.Amount.Amount(), "method": r.Method}
	payload := map[string]any{"payment_id": p.ID, "refund_id": r.ID, "amount": r.Amount.Amount(), "total_refunded": p.Refunded.Amount(), "status": p.Status,
		"purpose_type": p.Purpose.Type, "purpose_id": p.Purpose.ID, "method": r.Method}
	if r.Manual() {
		details["tracking_number"], details["destination_card"] = r.TrackingNumber, r.DestinationCardMasked
		payload["tracking_number"], payload["paid_at"], payload["agent"] = r.TrackingNumber, r.PaidAt, r.ClaimedBy
	}
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "payment.refund_completed", EntityType: "payment", EntityID: p.ID.String(), Details: details}); err != nil {
		return r, err
	}
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "refund.completed", EntityType: "refund", EntityID: r.ID.String(), Details: details}); err != nil {
		return r, err
	}
	return r, s.events.Emit(ctx, trail.Event{Topic: "payment.refunded", AggregateType: "payment", AggregateID: p.ID.String(), Payload: payload})
}

// ---- the manual refund workflow ----

// ClaimRefund lets an agent take a pending manual refund so nobody else pays it. Exactly one agent can win;
// the same agent claiming again is a no-op. The purpose's check runs again, so an agent is never sent to
// transfer money the books can no longer return (a spent wallet top-up).
func (s *Service) ClaimRefund(ctx context.Context, id uuid.UUID, agent string) (Refund, error) {
	return s.step(ctx, id, agent, "refund.claimed", "refund.claimed", map[string]any{"agent": agent},
		func(r *Refund) (bool, error) { return r.Claim(agent, s.clock()) },
		func(ctx context.Context, r Refund) error {
			p, err := s.repo.Get(ctx, r.PaymentID, false)
			if err != nil {
				return err
			}
			reserved, err := s.repo.PendingRefundTotal(ctx, p.ID) // includes this refund
			if err != nil {
				return err
			}
			return s.canRefund(ctx, p, reserved)
		})
}

// ReleaseRefund gives a claimed refund back to the queue. force lets a supervisor release another agent's claim.
func (s *Service) ReleaseRefund(ctx context.Context, id uuid.UUID, agent string, force bool) (Refund, error) {
	return s.step(ctx, id, agent, "refund.released", "refund.released", map[string]any{"agent": agent, "forced": force},
		func(r *Refund) (bool, error) { return r.Release(agent, force) }, nil)
}

// FailRefund records that the claimed agent could not pay the refund; the reserved amount is released.
func (s *Service) FailRefund(ctx context.Context, id uuid.UUID, agent, reason string, force bool) (Refund, error) {
	return s.step(ctx, id, agent, "refund.failed", "refund.failed", map[string]any{"agent": agent, "reason": reason},
		func(r *Refund) (bool, error) { return r.Fail(agent, reason, force) }, nil)
}

// CancelRefund withdraws a pending or claimed manual refund; the reserved amount is released.
func (s *Service) CancelRefund(ctx context.Context, id uuid.UUID, agent, reason string, force bool) (Refund, error) {
	return s.step(ctx, id, agent, "refund.failed", "refund.cancelled", map[string]any{"agent": agent, "reason": reason},
		func(r *Refund) (bool, error) { return r.Cancel(agent, reason, force) }, nil)
}

// step runs one state change of a refund under its row lock: apply (pure), optional guard, save, audit, event.
// Nothing is written when apply reports no change (a retry).
func (s *Service) step(ctx context.Context, id uuid.UUID, agent, topic, action string, details map[string]any,
	apply func(*Refund) (bool, error), guard func(context.Context, Refund) error) (Refund, error) {
	var out Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.GetRefund(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		changed, err := apply(&r)
		if err != nil || !changed {
			return err
		}
		if guard != nil {
			if err := guard(ctx, r); err != nil {
				return err
			}
		}
		r.UpdatedAt = s.clock()
		if err := s.repo.UpdateRefund(ctx, &r); err != nil {
			return err
		}
		out = r
		details["payment_id"] = r.PaymentID
		if err := s.audit.Record(ctx, trail.Entry{Actor: agent, Action: action, EntityType: "refund", EntityID: r.ID.String(), Details: details}); err != nil {
			return err
		}
		pl := refundPayload(r)
		pl["agent"] = agent
		if r.Failure != "" {
			pl["failure"] = r.Failure
		}
		return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "refund", AggregateID: r.ID.String(), Payload: pl})
	})
	return out, err
}

// CompleteManualRefund records the card-to-card transfer an agent made and applies the refund to the payment and
// the books (credit to the company's bank account). Only the agent holding the claim can complete it. Retrying
// with the same tracking number is a no-op; a tracking number already recorded for another refund is a conflict.
func (s *Service) CompleteManualRefund(ctx context.Context, id uuid.UUID, in CompleteManualInput) (Refund, error) {
	var out Refund
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		r, err := s.repo.GetRefund(ctx, id, true)
		if err != nil {
			return err
		}
		out = r
		changed, err := r.CompleteManual(in, s.clock())
		if err != nil || !changed {
			return err
		}
		out, err = s.applyRefund(ctx, r, in.Agent)
		return err
	})
	return out, err
}

// RefundByID returns a refund.
func (s *Service) RefundByID(ctx context.Context, id uuid.UUID) (Refund, error) {
	return s.repo.GetRefund(ctx, id, false)
}

// ListRefunds lists refunds with the client's filters, sort and pagination (oldest first by default), e.g. the
// agents' queue: filter[method]=manual_card_to_card&filter[status]=pending,processing.
func (s *Service) ListRefunds(ctx context.Context, p query.Params) (query.Page[Refund], error) {
	return s.repo.ListRefunds(ctx, p)
}
