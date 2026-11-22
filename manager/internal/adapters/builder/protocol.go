package builder

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

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Runner interface {
	Sources(context.Context) ([]domain.BuildSource, error)
	Inspect(context.Context, string) (domain.BuildInspection, error)
	Build(context.Context, domain.BuildRequest, func(domain.ShipProgress) error) ([]domain.BuiltImage, error)
}
type progressMessage struct {
	Progress *domain.ShipProgress `json:"progress,omitempty"`
	Images   []domain.BuiltImage  `json:"images,omitempty"`
	Done     bool                 `json:"done,omitempty"`
	Error    string               `json:"error,omitempty"`
}

func Handler(engine Runner, token string) http.Handler {
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, data any, err error) {
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(422)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(data)
	}
	decode := func(w http.ResponseWriter, r *http.Request, dst any) bool {
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		d.DisallowUnknownFields()
		if d.Decode(dst) != nil {
			http.Error(w, "invalid runner request", 400)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /builder/sources", func(w http.ResponseWriter, r *http.Request) {
		result, err := engine.Sources(r.Context())
		respond(w, result, err)
	})
	mux.HandleFunc("POST /builder/inspect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source string `json:"source"`
		}
		if !decode(w, r, &body) {
			return
		}
		result, err := engine.Inspect(r.Context(), body.Source)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /builder/build", func(w http.ResponseWriter, r *http.Request) {
		var req domain.BuildRequest
		if !decode(w, r, &req) {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		encoder := json.NewEncoder(w)
		images, err := engine.Build(r.Context(), req, func(p domain.ShipProgress) error {
			if err := encoder.Encode(progressMessage{Progress: &p}); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		})
		message := progressMessage{Done: true, Images: images}
		if err != nil {
			message.Error = err.Error()
		}
		encoder.Encode(message)
		flusher.Flush()
	})
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

func NewClient(url, token string) *Client {
	return &Client{URL: url, Token: token, HTTP: &http.Client{Timeout: 31 * time.Minute}}
}
func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("build runner unavailable; inspect local images before retrying")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		var body struct{ Error string }
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) == nil && body.Error != "" {
			return nil, errors.New(body.Error)
		}
		return nil, fmt.Errorf("build runner rejected request")
	}
	return res, nil
}
func (c *Client) call(ctx context.Context, method, path string, body, dst any) error {
	res, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(dst)
}
func (c *Client) Sources(ctx context.Context) ([]domain.BuildSource, error) {
	var out []domain.BuildSource
	err := c.call(ctx, "GET", "/builder/sources", nil, &out)
	return out, err
}
func (c *Client) Inspect(ctx context.Context, id string) (domain.BuildInspection, error) {
	var out domain.BuildInspection
	err := c.call(ctx, "POST", "/builder/inspect", map[string]string{"source": id}, &out)
	return out, err
}
func (c *Client) Build(ctx context.Context, req domain.BuildRequest, progress func(domain.ShipProgress) error) ([]domain.BuiltImage, error) {
	res, err := c.request(ctx, "POST", "/builder/build", req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	for {
		var message progressMessage
		if err = decoder.Decode(&message); err != nil {
			return nil, fmt.Errorf("build transport interrupted; inspect local images before retrying")
		}
		if message.Progress != nil {
			if err = progress(*message.Progress); err != nil {
				return nil, err
			}
		}
		if message.Done {
			if message.Error != "" {
				return message.Images, errors.New(message.Error)
			}
			return message.Images, nil
		}
	}
}
