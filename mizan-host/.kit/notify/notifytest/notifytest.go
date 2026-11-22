// Package notifytest has an in-memory provider for tests: it records what would have been sent and can be
// told to fail, so use cases can be tested without SMTP servers or SMS gateways.
package notifytest

import (
	"context"
	"sync"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
)

// Recorder implements notify.EmailSender, notify.SMSSender and notify.Notifier.
type Recorder struct {
	mu     sync.Mutex
	emails []notify.Email
	sms    []notify.SMS
	errs   []error // consumed one per call, in order; nil entries succeed
}

var (
	_ notify.EmailSender = (*Recorder)(nil)
	_ notify.SMSSender   = (*Recorder)(nil)
	_ notify.Notifier    = (*Recorder)(nil)
)

func (*Recorder) Name() string { return "recorder" }

// FailWith makes the next len(errs) calls (either channel) return those errors in order; use
// retry.Permanent(err) to simulate a rejection. nil entries succeed.
func (r *Recorder) FailWith(errs ...error) {
	r.mu.Lock()
	r.errs = append(r.errs, errs...)
	r.mu.Unlock()
}

func (r *Recorder) nextErr() error {
	if len(r.errs) == 0 {
		return nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return err
}

func (r *Recorder) SendEmail(_ context.Context, e notify.Email) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.nextErr(); err != nil {
		return err
	}
	r.emails = append(r.emails, e)
	return nil
}

func (r *Recorder) SendSMS(_ context.Context, s notify.SMS) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.nextErr(); err != nil {
		return err
	}
	r.sms = append(r.sms, s)
	return nil
}

// Emails returns a copy of the delivered emails.
func (r *Recorder) Emails() []notify.Email {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Email(nil), r.emails...)
}

// SMS returns a copy of the delivered messages.
func (r *Recorder) SMS() []notify.SMS {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.SMS(nil), r.sms...)
}
