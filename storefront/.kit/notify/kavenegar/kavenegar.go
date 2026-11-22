// Package kavenegar is a notify.SMSSender for the Kavenegar gateway (https://kavenegar.com), the
// plain-message "sms/send" endpoint.
//
// The API key is part of the request path, so this package neither logs the URL nor attaches HTTP client
// spans (they would record the URL): errors are stripped of it and the notify layer's attempt span carries
// the trace. Transient failures (HTTP 5xx, rate limiting, network) are retried by the notify.Sender;
// everything else (bad number, no credit, bad key) is permanent.
package kavenegar

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

	"github.com/PouryDev/server-infrastructure/gokit/notify"
	"github.com/PouryDev/server-infrastructure/gokit/retry"
)

// Config configures New.
type Config struct {
	APIKey string
	// Sender is the default sender line (Kavenegar's "sender"); leave empty to use the account default.
	Sender string
	// BaseURL defaults to https://api.kavenegar.com (override in tests).
	BaseURL string
	// HTTPClient defaults to a client with a 15s timeout.
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
	if cfg.APIKey == "" {
		return nil, errors.New("kavenegar: APIKey is required")
	}
	if strings.ContainsAny(cfg.APIKey, "/?#% ") {
		return nil, errors.New("kavenegar: APIKey contains invalid characters")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.kavenegar.com"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Sender{cfg: cfg, client: client, base: base}, nil
}

func (*Sender) Name() string { return "kavenegar" }

type response struct {
	Return struct {
		Status  int    `json:"status"`
		Message string `json:"message"`
	} `json:"return"`
}

// SendSMS posts one message.
func (s *Sender) SendSMS(ctx context.Context, m notify.SMS) error {
	form := url.Values{"receptor": {m.To}, "message": {m.Body}}
	if sender := firstNonEmpty(m.From, s.cfg.Sender); sender != "" {
		form.Set("sender", sender)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/v1/"+s.cfg.APIKey+"/sms/send.json", strings.NewReader(form.Encode()))
	if err != nil {
		return retry.Permanent(errors.New("kavenegar: cannot build request"))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // *url.Error embeds the URL, which contains the API key
		}
		return fmt.Errorf("kavenegar: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("kavenegar: read response: %w", err)
	}

	var r response
	if jerr := json.Unmarshal(body, &r); jerr != nil || r.Return.Status == 0 {
		// Not a Kavenegar answer (proxy error page, outage): decide on the HTTP status.
		return statusError(resp.StatusCode, fmt.Sprintf("unexpected response (http %d)", resp.StatusCode))
	}
	if r.Return.Status == http.StatusOK {
		return nil
	}
	return statusError(r.Return.Status, fmt.Sprintf("status %d: %s", r.Return.Status, r.Return.Message))
}

// statusError: 5xx and 429 may succeed later; anything else would fail the same way again.
func statusError(code int, msg string) error {
	err := errors.New("kavenegar: " + msg)
	if code >= 500 || code == http.StatusTooManyRequests {
		return err
	}
	return retry.Permanent(err)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
