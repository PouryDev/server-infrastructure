package delivery

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

type privatePlan struct {
	Plan   domain.ShipPlan `json:"plan"`
	Bundle []byte          `json:"bundle"`
}
type prepareRequest struct {
	Target  domain.ShipTarget  `json:"target"`
	Request domain.ShipRequest `json:"request"`
}
type envTarget struct {
	Target  domain.ShipTarget `json:"target"`
	Project string            `json:"project"`
}
type envWriteRequest struct {
	Target   domain.ShipTarget  `json:"target"`
	Project  string             `json:"project"`
	Revision string             `json:"revision"`
	Changes  []domain.EnvChange `json:"changes"`
}
type nativeStateRequest struct {
	Target domain.ShipTarget `json:"target"`
	Source string            `json:"source"`
}
type progressMessage struct {
	Progress *domain.ShipProgress `json:"progress,omitempty"`
	Done     bool                 `json:"done,omitempty"`
	Error    string               `json:"error,omitempty"`
}

// Runner is the shipping engine's full protocol surface: the deployment use-case
// port plus the typed destination env operations.
type Runner interface {
	application.Delivery
	application.EnvRemote
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
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<20))
		d.DisallowUnknownFields()
		if d.Decode(dst) != nil {
			http.Error(w, "invalid runner request", 400)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /delivery/catalog", func(w http.ResponseWriter, r *http.Request) {
		result, err := engine.Catalog(r.Context())
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/probe", func(w http.ResponseWriter, r *http.Request) {
		var t domain.ShipTarget
		if !decode(w, r, &t) {
			return
		}
		result, err := engine.Probe(r.Context(), t)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/native/state", func(w http.ResponseWriter, r *http.Request) {
		var req nativeStateRequest
		if !decode(w, r, &req) {
			return
		}
		native, ok := engine.(application.NativeDeployment)
		if !ok {
			respond(w, nil, fmt.Errorf("destination project management is unavailable"))
			return
		}
		result, err := native.NativeState(r.Context(), req.Target, req.Source)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/prepare", func(w http.ResponseWriter, r *http.Request) {
		var req prepareRequest
		if !decode(w, r, &req) {
			return
		}
		result, err := engine.Prepare(r.Context(), req.Target, req.Request)
		respond(w, privatePlan{Plan: result, Bundle: result.Bundle}, err)
	})
	mux.HandleFunc("POST /delivery/env/projects", func(w http.ResponseWriter, r *http.Request) {
		var t domain.ShipTarget
		if !decode(w, r, &t) {
			return
		}
		result, err := engine.EnvProjects(r.Context(), t)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/env/state", func(w http.ResponseWriter, r *http.Request) {
		var req envTarget
		if !decode(w, r, &req) {
			return
		}
		result, err := engine.EnvState(r.Context(), req.Target, req.Project)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/env/write", func(w http.ResponseWriter, r *http.Request) {
		var req envWriteRequest
		if !decode(w, r, &req) {
			return
		}
		err := engine.EnvWrite(r.Context(), req.Target, req.Project, req.Revision, req.Changes)
		respond(w, map[string]bool{"ok": true}, err)
	})
	mux.HandleFunc("POST /delivery/buildargs", func(w http.ResponseWriter, r *http.Request) {
		var p privatePlan
		if !decode(w, r, &p) {
			return
		}
		p.Plan.Bundle = p.Bundle
		result, err := engine.BuildArgs(r.Context(), p.Plan)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /delivery/run", func(w http.ResponseWriter, r *http.Request) {
		var p privatePlan
		if !decode(w, r, &p) {
			return
		}
		p.Plan.Bundle = p.Bundle
		w.Header().Set("Content-Type", "application/x-ndjson")
		encoder := json.NewEncoder(w)
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unavailable", 500)
			return
		}
		err := engine.Execute(r.Context(), p.Plan, func(progress domain.ShipProgress) error {
			if err := encoder.Encode(progressMessage{Progress: &progress}); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		})
		message := progressMessage{Done: true}
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
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
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
		return nil, fmt.Errorf("shipping runner unavailable; inspect any active destination deployment")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		var body struct{ Error string }
		if json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body) == nil && body.Error != "" {
			return nil, errors.New(body.Error)
		}
		return nil, fmt.Errorf("shipping runner rejected request")
	}
	return res, nil
}
func (c *Client) call(ctx context.Context, method, path string, body, dst any) error {
	res, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(io.LimitReader(res.Body, 96<<20)).Decode(dst)
}
func (c *Client) Catalog(ctx context.Context) (domain.ShipCatalog, error) {
	var result domain.ShipCatalog
	err := c.call(ctx, "GET", "/delivery/catalog", nil, &result)
	return result, err
}
func (c *Client) NativeState(ctx context.Context, target domain.ShipTarget, source string) (domain.NativeDeploymentState, error) {
	var out domain.NativeDeploymentState
	err := c.call(ctx, "POST", "/delivery/native/state", nativeStateRequest{target, source}, &out)
	return out, err
}
func (c *Client) Prepare(ctx context.Context, t domain.ShipTarget, r domain.ShipRequest) (domain.ShipPlan, error) {
	var result privatePlan
	err := c.call(ctx, "POST", "/delivery/prepare", prepareRequest{t, r}, &result)
	result.Plan.Bundle = result.Bundle
	return result.Plan, err
}
func (c *Client) Probe(ctx context.Context, t domain.ShipTarget) (domain.ShipProbe, error) {
	var result domain.ShipProbe
	err := c.call(ctx, "POST", "/delivery/probe", t, &result)
	return result, err
}
func (c *Client) Execute(ctx context.Context, p domain.ShipPlan, progress func(domain.ShipProgress) error) error {
	res, err := c.request(ctx, "POST", "/delivery/run", privatePlan{p, p.Bundle})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	for {
		var message progressMessage
		if err = decoder.Decode(&message); err != nil {
			return fmt.Errorf("shipping transport interrupted; inspect destination before retrying")
		}
		if message.Progress != nil {
			if err = progress(*message.Progress); err != nil {
				return err
			}
		}
		if message.Done {
			if message.Error != "" {
				return errors.New(message.Error)
			}
			return nil
		}
	}
}

func (c *Client) EnvProjects(ctx context.Context, t domain.ShipTarget) ([]domain.EnvProject, error) {
	var out []domain.EnvProject
	err := c.call(ctx, "POST", "/delivery/env/projects", t, &out)
	return out, conflict(err)
}
func (c *Client) EnvState(ctx context.Context, t domain.ShipTarget, project string) (domain.EnvStateRaw, error) {
	var out domain.EnvStateRaw
	err := c.call(ctx, "POST", "/delivery/env/state", envTarget{t, project}, &out)
	return out, conflict(err)
}
func (c *Client) EnvWrite(ctx context.Context, t domain.ShipTarget, project, revision string, changes []domain.EnvChange) error {
	var out map[string]bool
	return conflict(c.call(ctx, "POST", "/delivery/env/write", envWriteRequest{t, project, revision, changes}, &out))
}
func (c *Client) BuildArgs(ctx context.Context, plan domain.ShipPlan) (map[string]string, error) {
	var out map[string]string
	err := c.call(ctx, "POST", "/delivery/buildargs", privatePlan{plan, plan.Bundle}, &out)
	return out, err
}

// conflict restores the typed error that the string-only runner protocol flattened.
func conflict(err error) error {
	if err != nil && err.Error() == EnvConflictMessage {
		return application.ErrEnvConflict
	}
	return err
}
