package gateway

import (
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

// Handler exposes only sanitized inventory and start/stop. No generic Docker
// proxy, exec, create, update, remove, volume, network or filesystem endpoints.
func Handler(runtime application.Runtime, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers", func(w http.ResponseWriter, r *http.Request) {
		containers, err := runtime.List(r.Context())
		if err != nil {
			http.Error(w, "runtime unavailable", 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(containers)
	})
	for _, action := range []string{"start", "stop"} {
		mux.HandleFunc("POST /containers/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var err error
			if action == "start" {
				err = runtime.Start(r.Context(), r.PathValue("id"))
			} else {
				err = runtime.Stop(r.Context(), r.PathValue("id"))
			}
			if err != nil {
				http.Error(w, "runtime action rejected or failed", 502)
				return
			}
			w.WriteHeader(204)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if len(token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func New(url, token string) *Client {
	return &Client{URL: url, Token: token, HTTP: &http.Client{Timeout: 45 * time.Second}}
}
func (c *Client) call(ctx context.Context, method, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("gateway status %d", res.StatusCode)
	}
	if dst != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(dst)
	}
	return nil
}
func (c *Client) List(ctx context.Context) ([]domain.Container, error) {
	var result []domain.Container
	err := c.call(ctx, "GET", "/containers", &result)
	return result, err
}
func (c *Client) Start(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/containers/"+id+"/start", nil)
}
func (c *Client) Stop(ctx context.Context, id string) error {
	return c.call(ctx, "POST", "/containers/"+id+"/stop", nil)
}
