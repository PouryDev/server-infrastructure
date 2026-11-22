package notify

import (
	"net/mail"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Channel is a delivery medium.
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
)

// Email is a message for the email channel. At least one of Text and HTML is required; with both the
// message is multipart/alternative. Addresses may carry a display name ("Ada <ada@example.com>").
type Email struct {
	// ID identifies the message in logs, traces and (for the async driver) the queue. Generated when empty.
	ID      string   `json:"id,omitempty"`
	From    string   `json:"from,omitempty"` // optional: the provider's configured sender is used when empty
	To      []string `json:"to,omitempty"`
	Cc      []string `json:"cc,omitempty"`
	Bcc     []string `json:"bcc,omitempty"` // delivered to, never written into the headers
	ReplyTo string   `json:"reply_to,omitempty"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
	// Headers are extra headers (for example List-Unsubscribe). Names and values must not contain CR/LF.
	Headers map[string]string `json:"headers,omitempty"`
}

// SMS is a message for the SMS channel.
type SMS struct {
	ID   string `json:"id,omitempty"`   // see Email.ID
	From string `json:"from,omitempty"` // optional sender line/number; the provider's configured one is used when empty
	To   string `json:"to"`             // phone number: local (09123456789) or international (+989123456789)
	Body string `json:"body"`
}

const (
	maxRecipients = 50
	maxSMSBody    = 1600 // ten concatenated UCS-2 segments is already far beyond any sane notification
)

// Validate checks the message without touching the network. The error is an apperr.KindInvalid carrying
// one FieldError per problem, so it can be returned straight from a use case.
func (e Email) Validate() error {
	var fields []apperr.FieldError
	bad := func(field, msg string) { fields = append(fields, apperr.FieldError{Field: field, Message: msg}) }

	if len(e.To)+len(e.Cc)+len(e.Bcc) == 0 {
		bad("to", "at least one recipient is required")
	}
	if len(e.To)+len(e.Cc)+len(e.Bcc) > maxRecipients {
		bad("to", "too many recipients")
	}
	for field, list := range map[string][]string{"to": e.To, "cc": e.Cc, "bcc": e.Bcc} {
		for _, a := range list {
			if _, err := mail.ParseAddress(a); err != nil || hasLineBreak(a) {
				bad(field, "invalid address")
				break
			}
		}
	}
	if e.From != "" {
		if _, err := mail.ParseAddress(e.From); err != nil || hasLineBreak(e.From) {
			bad("from", "invalid address")
		}
	}
	if e.ReplyTo != "" {
		if _, err := mail.ParseAddress(e.ReplyTo); err != nil || hasLineBreak(e.ReplyTo) {
			bad("reply_to", "invalid address")
		}
	}
	if strings.TrimSpace(e.Subject) == "" {
		bad("subject", "is required")
	}
	if hasLineBreak(e.Subject) {
		bad("subject", "must be a single line")
	}
	if strings.TrimSpace(e.Text) == "" && strings.TrimSpace(e.HTML) == "" {
		bad("body", "text or html is required")
	}
	for k, v := range e.Headers {
		if k == "" || strings.ContainsAny(k, ":\r\n ") || hasLineBreak(v) {
			bad("headers", "invalid header "+k)
		}
	}
	if len(fields) > 0 {
		return apperr.Invalid("invalid email", fields...)
	}
	return nil
}

// Validate checks the message without touching the network (see Email.Validate).
func (s SMS) Validate() error {
	var fields []apperr.FieldError
	if !validPhone(s.To) {
		fields = append(fields, apperr.FieldError{Field: "to", Message: "invalid phone number"})
	}
	if strings.TrimSpace(s.Body) == "" {
		fields = append(fields, apperr.FieldError{Field: "body", Message: "is required"})
	}
	if len([]rune(s.Body)) > maxSMSBody {
		fields = append(fields, apperr.FieldError{Field: "body", Message: "is too long"})
	}
	if len(fields) > 0 {
		return apperr.Invalid("invalid sms", fields...)
	}
	return nil
}

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\r\n") }

// validPhone accepts an optional leading '+' followed by 7 to 15 digits (E.164 length bounds); spaces and
// dashes are not accepted, normalise before sending.
func validPhone(p string) bool {
	p = strings.TrimPrefix(p, "+")
	if len(p) < 7 || len(p) > 15 {
		return false
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Recipients returns every address the email is delivered to (To, Cc, Bcc), bare addresses only.
func (e Email) Recipients() []string {
	out := make([]string, 0, len(e.To)+len(e.Cc)+len(e.Bcc))
	for _, list := range [][]string{e.To, e.Cc, e.Bcc} {
		for _, a := range list {
			if p, err := mail.ParseAddress(a); err == nil {
				out = append(out, p.Address)
			}
		}
	}
	return out
}

// MaskPhone hides the middle of a phone number for logs: 09123456789 -> 0912***6789.
func MaskPhone(p string) string {
	if len(p) <= 7 {
		return "***"
	}
	return p[:4] + "***" + p[len(p)-4:]
}

// MaskEmail hides most of the local part of an address for logs: ada@example.com -> a***@example.com.
func MaskEmail(a string) string {
	if p, err := mail.ParseAddress(a); err == nil {
		a = p.Address
	}
	at := strings.LastIndexByte(a, '@')
	if at <= 0 {
		return "***"
	}
	return a[:1] + "***" + a[at:]
}
