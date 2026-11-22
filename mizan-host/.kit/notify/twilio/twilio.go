// Package twilio is a notify.SMSSender for Twilio's Programmable Messaging REST API. Requests go through
// otelhttp, so each provider call also shows up as an HTTP client span under the attempt span.
package twilio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Config configures New.
type Config struct {
	AccountSID string
	AuthToken  string
	// From is the default sender: a Twilio number in E.164 format, or the sid of a Messaging Service
	// ("MG...", sent as MessagingServiceSid).
	From string
	// BaseURL defaults to https://api.twilio.com (override in tests).
	BaseURL string
	// HTTPClient defaults to a 15s-timeout client with OpenTelemetry instrumentation.
	HTTPClient *http.Client
}

// Sender implements notify.SMSSender. It is safe for concurrent use.
type Sender struct {
	cfg    Config
	client *http.Client
	base   string
}

var _ notify.SMSSender = (*Sender)(nil)

// New validates cfg.
func New(cfg Config) (*Sender, error) {
	if cfg.AccountSID == "" || cfg.AuthToken == "" {
		return nil, errors.New("twilio: AccountSID and AuthToken are required")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.twilio.com"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}
	}
	return &Sender{cfg: cfg, client: client, base: base}, nil
}

func (*Sender) Name() string { return "twilio" }

// SendSMS posts one message.
func (s *Sender) SendSMS(ctx context.Context, m notify.SMS) error {
	to := m.To
	if !strings.HasPrefix(to, "+") {
		return retry.Permanent(errors.New("twilio: the recipient must be in international format (+...)"))
	}
	form := url.Values{"To": {to}, "Body": {m.Body}}
	if from := m.From; from != "" {
		form.Set("From", from)
	} else if strings.HasPrefix(s.cfg.From, "MG") {
		form.Set("MessagingServiceSid", s.cfg.From)
	} else {
		form.Set("From", s.cfg.From)
	}
	endpoint := s.base + "/2010-04-01/Accounts/" + url.PathEscape(s.cfg.AccountSID) + "/Messages.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return retry.Permanent(errors.New("twilio: cannot build request"))
	}
	req.SetBasicAuth(s.cfg.AccountSID, s.cfg.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("twilio: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 == 2 {
		return nil
	}

	var apiErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &apiErr)
	err = fmt.Errorf("twilio: http %d, code %d: %s", resp.StatusCode, apiErr.Code, apiErr.Message)
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return err
	}
	return retry.Permanent(err)
}
