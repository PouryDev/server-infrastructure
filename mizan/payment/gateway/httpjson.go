// Package gateway holds helpers shared by the gateway adapters; the adapters live in sub-packages.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client posts JSON and returns the status and body; it never retries (callers know what is safe to repeat).
type Client struct {
	HTTP    *http.Client
	Headers map[string]string
}

// DefaultHTTP is a client with a sane timeout.
func DefaultHTTP() *http.Client { return &http.Client{Timeout: 15 * time.Second} }

// PostJSON sends body as JSON. Transport errors and bodies over 1 MiB are errors; HTTP status handling is the caller's.
func (c Client) PostJSON(ctx context.Context, url string, body any) (int, []byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = DefaultHTTP()
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gateway request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("gateway response unreadable: %w", err)
	}
	return resp.StatusCode, data, nil
}
