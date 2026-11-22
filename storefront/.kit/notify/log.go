package notify

import (
	"context"
	"log/slog"
)

// LogSender implements both provider interfaces by writing a log line instead of sending anything: for
// local development and tests of a whole service. It logs masked recipients and the subject, never the
// body, because bodies carry one-time codes and reset links.
type LogSender struct{ log *slog.Logger }

// NewLogSender returns a LogSender (slog.Default when log is nil).
func NewLogSender(log *slog.Logger) *LogSender {
	if log == nil {
		log = slog.Default()
	}
	return &LogSender{log: log}
}

func (*LogSender) Name() string { return "log" }

func (l *LogSender) SendEmail(ctx context.Context, e Email) error {
	to := e.Recipients()
	l.log.InfoContext(ctx, "email not sent (log provider)", "message_id", e.ID, "to", maskFirst(to, MaskEmail), "recipients", len(to), "subject", e.Subject)
	return nil
}

func (l *LogSender) SendSMS(ctx context.Context, s SMS) error {
	l.log.InfoContext(ctx, "sms not sent (log provider)", "message_id", s.ID, "to", MaskPhone(s.To), "body_chars", len([]rune(s.Body)))
	return nil
}
