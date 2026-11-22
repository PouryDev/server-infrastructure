package notify

import "context"

// EmailSender delivers one email to a provider (SMTP server, API). Implementations must be safe for
// concurrent use, honour ctx (deadline and cancellation) and classify failures: wrap errors that retrying
// cannot fix (rejected recipient, bad credentials) with retry.Permanent; every other error is retried.
type EmailSender interface {
	Name() string // provider label used in metrics, traces and logs: low cardinality, e.g. "smtp"
	SendEmail(ctx context.Context, e Email) error
}

// SMSSender delivers one SMS to a provider; the contract is the same as for EmailSender.
type SMSSender interface {
	Name() string
	SendSMS(ctx context.Context, s SMS) error
}

// Notifier is what use cases depend on. The sync driver (Sender) returns after the provider accepted
// the message; the async driver (Async) returns after the queue accepted it. Both validate first, so a
// malformed message is always reported to the caller as apperr.KindInvalid.
type Notifier interface {
	SendEmail(ctx context.Context, e Email) error
	SendSMS(ctx context.Context, s SMS) error
}

var (
	_ Notifier = (*Sender)(nil)
	_ Notifier = (*Async)(nil)
)
