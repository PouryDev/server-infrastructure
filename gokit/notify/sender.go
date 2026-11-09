package notify

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Config configures NewSender. At least one provider is required; a channel without a provider fails
// with apperr.KindUnavailable (code "channel_not_configured") instead of silently dropping messages.
type Config struct {
	Email EmailSender
	SMS   SMSSender

	// Retry applies to transient provider failures. MaxAttempts defaults to 3 (set 1 to disable retries);
	// Initial/Max default to 500ms/5s. OnRetry, when set, is called in addition to the built-in logging.
	Retry retry.Policy
	// AttemptTimeout bounds one provider call (default 15s). The caller's ctx bounds the whole delivery.
	AttemptTimeout time.Duration
	Logger         *slog.Logger
}

// Sender is the sync driver: SendEmail and SendSMS return after the provider accepted the message.
// Transient failures are retried with backoff, every delivery is traced and measured. It is safe for
// concurrent use.
type Sender struct {
	email   EmailSender
	sms     SMSSender
	policy  retry.Policy
	timeout time.Duration
	log     *slog.Logger
	inst    *instruments
}

// NewSender builds the sync driver.
func NewSender(cfg Config) (*Sender, error) {
	if cfg.Email == nil && cfg.SMS == nil {
		return nil, errors.New("notify: configure at least one of Email and SMS")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.Initial <= 0 {
		cfg.Retry.Initial = 500 * time.Millisecond
	}
	if cfg.Retry.Max <= 0 {
		cfg.Retry.Max = 5 * time.Second
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = 15 * time.Second
	}
	return &Sender{
		email: cfg.Email, sms: cfg.SMS, policy: cfg.Retry, timeout: cfg.AttemptTimeout,
		log: cfg.Logger, inst: newInstruments(),
	}, nil
}

// SendEmail validates and delivers e.
func (s *Sender) SendEmail(ctx context.Context, e Email) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if s.email == nil {
		return notConfigured(ChannelEmail)
	}
	if e.ID == "" {
		e.ID = newID()
	}
	to := e.Recipients()
	return s.deliver(ctx, ChannelEmail, s.email.Name(), e.ID, len(to), maskFirst(to, MaskEmail),
		func(ctx context.Context) error { return s.email.SendEmail(ctx, e) })
}

// SendSMS validates and delivers m.
func (s *Sender) SendSMS(ctx context.Context, m SMS) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if s.sms == nil {
		return notConfigured(ChannelSMS)
	}
	if m.ID == "" {
		m.ID = newID()
	}
	return s.deliver(ctx, ChannelSMS, s.sms.Name(), m.ID, 1, MaskPhone(m.To),
		func(ctx context.Context) error { return s.sms.SendSMS(ctx, m) })
}

func notConfigured(ch Channel) error {
	return apperr.New(apperr.KindUnavailable, "channel_not_configured", string(ch)+" notifications are not configured")
}

func maskFirst(list []string, mask func(string) string) string {
	if len(list) == 0 {
		return ""
	}
	return mask(list[0])
}

// deliver runs fn with retries inside one client span, one child span per attempt, and records metrics.
func (s *Sender) deliver(ctx context.Context, ch Channel, provider, id string, recipients int, masked string, fn func(context.Context) error) error {
	base := []attribute.KeyValue{channelAttr(ch), attrProvider.String(provider)}
	ctx, span := s.inst.tracer.Start(ctx, spanName("notify.send", ch),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(base, attrMsgID.String(id), attrCount.Int(recipients))...))
	start := time.Now()
	log := s.log.With("channel", string(ch), "provider", provider, "message_id", id, "to", masked)

	attempts := 0
	permanent := false
	policy := s.policy
	userOnRetry := policy.OnRetry
	policy.OnRetry = func(attempt int, err error, delay time.Duration) {
		span.AddEvent("retry", trace.WithAttributes(attrAttempt.Int(attempt), attribute.String("error.type", errorType(err)),
			attribute.Int64("notify.retry_in_ms", delay.Milliseconds())))
		log.WarnContext(ctx, "notification attempt failed, retrying", "attempt", attempt, "retry_in", delay.String(), "error", err.Error())
		if userOnRetry != nil {
			userOnRetry(attempt, err, delay)
		}
	}

	err := retry.Do(ctx, policy, func(ctx context.Context) error {
		attempts++
		actx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		actx, aspan := s.inst.tracer.Start(actx, spanName("notify.attempt", ch), trace.WithAttributes(append(base, attrAttempt.Int(attempts))...))
		aerr := fn(actx)
		result := "ok"
		switch {
		case aerr == nil:
		case retry.IsPermanent(aerr):
			permanent, result = true, "permanent_error"
		default:
			result = "transient_error"
		}
		s.inst.attempts.Add(ctx, 1, metric.WithAttributes(append(base, attrResult.String(result))...))
		finish(aspan, aerr)
		return aerr
	})

	outcome := outcomeSent
	switch {
	case err == nil:
	case ctx.Err() != nil && !permanent:
		outcome = outcomeCanceled
		err = apperr.Wrap(apperr.KindUnavailable, "delivery_canceled", "the delivery was interrupted", err)
	case permanent:
		outcome = outcomeRejected
		err = apperr.Wrap(apperr.KindInvalid, "delivery_rejected", "the provider rejected the message", err)
	default:
		outcome = outcomeFailed
		err = apperr.Wrap(apperr.KindUnavailable, "delivery_failed", "the message could not be delivered", err)
	}

	span.SetAttributes(attrAttempts.Int(attempts), attrOutcome.String(outcome))
	finish(span, err)
	s.inst.deliveries.Add(ctx, 1, metric.WithAttributes(append(base, attrOutcome.String(outcome))...))
	s.inst.duration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(append(base, attrOutcome.String(outcome))...))

	switch outcome {
	case outcomeSent:
		log.DebugContext(ctx, "notification sent", "attempts", attempts)
	case outcomeRejected:
		log.WarnContext(ctx, "notification rejected by provider", "attempts", attempts, "error", err.Error())
	default:
		log.ErrorContext(ctx, "notification failed", "outcome", outcome, "attempts", attempts, "error", err.Error())
	}
	return err
}
