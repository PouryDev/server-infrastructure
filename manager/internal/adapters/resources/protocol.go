package resources

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func Handler(engine application.ResourceRunner, token string) http.Handler {
	mux := http.NewServeMux()
	for _, action := range []string{"preview", "apply"} {
		mux.HandleFunc("POST /resources/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in domain.ResourceRequest
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&in) != nil || decoder.Decode(&struct{}{}) != io.EOF {
				http.Error(w, "invalid request", 400)
				return
			}
			if err := in.Validate(); err != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 150*time.Second)
			defer cancel()
			var out any
			var err error
			if action == "preview" {
				out, err = engine.Preview(ctx, in)
			} else {
				out, err = engine.Apply(ctx, in)
			}
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(422)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(out)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

type Client struct {
	URL, Token string
	HTTP       *http.Client
}

func New(url, token string) *Client {
	return &Client{url, token, &http.Client{Timeout: 160 * time.Second}}
}
func (c *Client) call(ctx context.Context, action string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.URL+"/resources/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("resource runner unavailable; inspect service state before retrying")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var body struct{ Error string }
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) == nil && body.Error != "" {
			return errors.New(body.Error)
		}
		return fmt.Errorf("resource runner rejected request")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func (c *Client) Preview(ctx context.Context, in domain.ResourceRequest) (domain.ResourcePreview, error) {
	var out domain.ResourcePreview
	err := c.call(ctx, "preview", in, &out)
	return out, err
}
func (c *Client) Apply(ctx context.Context, in domain.ResourceRequest) (domain.ResourceResult, error) {
	var out domain.ResourceResult
	err := c.call(ctx, "apply", in, &out)
	return out, err
}
