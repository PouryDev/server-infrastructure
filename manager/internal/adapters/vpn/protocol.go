package vpn

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func Handler(engine application.VPNRunner, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /vpn", func(w http.ResponseWriter, r *http.Request) {
		out, err := engine.Status(r.Context())
		reply(w, out, err)
	})
	mux.HandleFunc("PUT /vpn", func(w http.ResponseWriter, r *http.Request) {
		var in domain.VPNChange
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || d.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "Invalid VPN request", 400)
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 20*time.Second)
		defer cancel()
		out, err := engine.Change(ctx, in)
		reply(w, out, err)
	})
	mux.HandleFunc("POST /vpn/test", func(w http.ResponseWriter, r *http.Request) {
		err := engine.Test(r.Context())
		reply(w, map[string]bool{"reachable": err == nil}, err)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == "GET" && r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		if len(token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func reply(w http.ResponseWriter, out any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(422)
		out = map[string]string{"error": err.Error()}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func New(url, token string) *Client {
	return &Client{url, token, &http.Client{Timeout: 25 * time.Second}}
}
func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("VPN service unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("VPN service unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var body struct{ Error string }
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) == nil {
			// Only our fixed messages may cross the broker boundary.
			if safeErrors[body.Error] {
				return fmt.Errorf("%s", body.Error)
			}
		}
		return fmt.Errorf("VPN service unavailable")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(out)
}

var safeErrors = map[string]bool{}

func init() {
	for _, message := range []string{"Invalid VPN link", "Unsupported VPN protocol", "Unsupported VPN link options", "Insecure VPN links are not allowed", "Unsupported VPN transport", "Unsupported VPN security", "Invalid REALITY settings", "VPN settings changed; refresh before saving", "Enter a VPN link first", "Xray rejected VPN configuration", "Xray could not start", "VPN storage unavailable", "VPN change failed; previous connection could not restart", "Enable VPN before testing", "VPN connectivity test failed"} {
		safeErrors[message] = true
	}
}
func (c *Client) Status(ctx context.Context) (domain.VPNStatus, error) {
	var out domain.VPNStatus
	err := c.call(ctx, "GET", "/vpn", nil, &out)
	return out, err
}
func (c *Client) Change(ctx context.Context, in domain.VPNChange) (domain.VPNStatus, error) {
	var out domain.VPNStatus
	err := c.call(ctx, "PUT", "/vpn", in, &out)
	return out, err
}
func (c *Client) Test(ctx context.Context) error {
	var out map[string]bool
	return c.call(ctx, "POST", "/vpn/test", nil, &out)
}
