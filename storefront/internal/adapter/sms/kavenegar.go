// Package sms delivers authentication codes using a configured provider.
package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Kavenegar struct {
	Key, Template string
	Client        *http.Client
}

func (k Kavenegar) Send(ctx context.Context, phone, code string) error {
	if k.Key == "" || k.Template == "" {
		return fmt.Errorf("SMS not configured")
	}
	q := url.Values{"receptor": {strings.TrimPrefix(phone, "+98")}, "token": {code}, "template": {k.Template}}
	req, e := http.NewRequestWithContext(ctx, "POST", "https://api.kavenegar.com/v1/"+url.PathEscape(k.Key)+"/verify/lookup.json", strings.NewReader(q.Encode()))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := k.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, e := client.Do(req)
	if e != nil {
		return fmt.Errorf("SMS transport unavailable")
	}
	defer res.Body.Close()
	var out struct {
		Return struct {
			Status int `json:"status"`
		} `json:"return"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out) != nil || out.Return.Status != 200 {
		return fmt.Errorf("SMS provider rejected delivery")
	}
	return nil
}
