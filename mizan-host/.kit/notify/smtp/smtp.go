// Package smtp is a notify.EmailSender over SMTP: implicit TLS (port 465), STARTTLS (587) or plaintext
// (a local catcher such as Mailpit). It dials with a context, applies the context deadline to the whole
// conversation, builds a correct MIME message (UTF-8 subjects and bodies, multipart/alternative) and tells
// permanent (5xx) from transient (4xx, network) failures so the sender retries only what can succeed.
//
// One connection is used per message; for bulk mail use the async driver with several workers.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// TLSMode selects how the connection is secured.
type TLSMode string

const (
	// StartTLS upgrades a plain connection (typically port 587). The upgrade is mandatory: a server that
	// does not offer it is an error, so credentials never travel in clear text by accident.
	StartTLS TLSMode = "starttls"
	// ImplicitTLS wraps the connection in TLS from the first byte (typically port 465).
	ImplicitTLS TLSMode = "tls"
	// NoTLS is for local development servers only.
	NoTLS TLSMode = "none"
)

// Config configures New.
type Config struct {
	Host     string
	Port     int // default 587 (465 for ImplicitTLS, 25 for NoTLS)
	Username string
	Password string
	From     string  // default sender, used when Email.From is empty (required)
	TLS      TLSMode // default StartTLS
	// ServerName overrides the name checked in the certificate (default Host).
	ServerName string
	// LocalName is announced in EHLO (default "localhost").
	LocalName string
	// TLSConfig replaces the default TLS settings (tests, private CAs). ServerName is filled in when empty.
	TLSConfig *tls.Config
}

// Sender implements notify.EmailSender. It is safe for concurrent use.
type Sender struct {
	cfg  Config
	addr string
	from string // bare address for MAIL FROM
}

var _ notify.EmailSender = (*Sender)(nil)

// New validates cfg and returns a Sender. Nothing is dialled until the first send.
func New(cfg Config) (*Sender, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp: Host is required")
	}
	if cfg.TLS == "" {
		cfg.TLS = StartTLS
	}
	switch cfg.TLS {
	case StartTLS, ImplicitTLS, NoTLS:
	default:
		return nil, fmt.Errorf("smtp: unknown TLS mode %q", cfg.TLS)
	}
	if cfg.Port == 0 {
		cfg.Port = map[TLSMode]int{StartTLS: 587, ImplicitTLS: 465, NoTLS: 25}[cfg.TLS]
	}
	if cfg.ServerName == "" {
		cfg.ServerName = cfg.Host
	}
	if cfg.LocalName == "" {
		cfg.LocalName = "localhost"
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("smtp: From: %w", err)
	}
	return &Sender{cfg: cfg, addr: net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), from: from.Address}, nil
}

func (*Sender) Name() string { return "smtp" }

func (s *Sender) tlsConfig() *tls.Config {
	if s.cfg.TLSConfig != nil {
		c := s.cfg.TLSConfig.Clone()
		if c.ServerName == "" {
			c.ServerName = s.cfg.ServerName
		}
		return c
	}
	return &tls.Config{ServerName: s.cfg.ServerName, MinVersion: tls.VersionTLS12}
}

// SendEmail delivers e to all of its recipients, or fails without having sent a partial message to the
// recipients that came after a rejected one (a rejected recipient fails the whole message permanently).
func (s *Sender) SendEmail(ctx context.Context, e notify.Email) error {
	from := s.cfg.From
	envelopeFrom := s.from
	if e.From != "" {
		from = e.From
		p, err := mail.ParseAddress(e.From)
		if err != nil {
			return retry.Permanent(err)
		}
		envelopeFrom = p.Address
	}
	msg, err := buildMessage(from, e, time.Now())
	if err != nil {
		return retry.Permanent(err)
	}
	return classify(s.send(ctx, envelopeFrom, e.Recipients(), msg))
}

func (s *Sender) send(ctx context.Context, from string, rcpts []string, msg []byte) error {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return err
	}
	if s.cfg.TLS == ImplicitTLS {
		tc := tls.Client(conn, s.tlsConfig())
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return err
		}
		conn = tc
	}
	// The deadline covers the whole conversation; cancelling ctx closes the connection, which unblocks
	// whatever read or write is in progress.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()

	if err := c.Hello(s.cfg.LocalName); err != nil {
		return err
	}
	if s.cfg.TLS == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return retry.Permanent(errors.New("smtp: server does not offer STARTTLS"))
		}
		if err := c.StartTLS(s.tlsConfig()); err != nil {
			return err
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth itself refuses to send credentials over an unencrypted connection to a remote host.
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// classify marks errors a retry cannot fix. 5xx replies (bad recipient, bad credentials, message refused)
// are permanent; 4xx replies (greylisting, mailbox busy), timeouts and network errors are transient.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if retry.IsPermanent(err) {
		return err
	}
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 500 {
		return retry.Permanent(err)
	}
	return err
}
