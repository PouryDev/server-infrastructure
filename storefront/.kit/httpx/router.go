package httpx

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Router is a thin layer over http.ServeMux (Go 1.22+ patterns such as "GET /v1/users/{id}") that
// adds per-route and per-group middleware and error-returning handlers.
type Router struct {
	mux *http.ServeMux
	mws []Middleware
}

// NewRouter creates an empty router.
func NewRouter() *Router { return &Router{mux: http.NewServeMux()} }

// Group returns a router sharing the same mux whose routes also run mws (after the parent's).
func (r *Router) Group(mws ...Middleware) *Router {
	return &Router{mux: r.mux, mws: append(slices.Clone(r.mws), mws...)}
}

// Handle registers h under pattern with the group's and the given middleware.
func (r *Router) Handle(pattern string, h http.Handler, mws ...Middleware) {
	all := append(slices.Clone(r.mws), mws...)
	r.mux.Handle(pattern, Chain(h, all...))
}

// Route registers an error-returning handler.
func (r *Router) Route(pattern string, h HandlerFunc, mws ...Middleware) {
	r.Handle(pattern, h, mws...)
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// Check is a named readiness probe.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// RegisterHealth adds GET /healthz (liveness: 200 while the process runs, no dependency checks, as the
// infrastructure's compose healthcheck and uptime probe require) and GET /readyz (readiness: runs the
// checks in parallel and answers 503 naming the failing ones, without error details).
func RegisterHealth(r *Router, checks ...Check) {
	r.Handle("GET /healthz", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
	r.Handle("GET /readyz", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		var (
			mu      sync.Mutex
			failing []string
			wg      sync.WaitGroup
		)
		for _, c := range checks {
			wg.Go(func() {
				if err := c.Fn(ctx); err != nil {
					LoggerFrom(req.Context()).WarnContext(req.Context(), "readiness check failed", "check", c.Name, "error", err.Error())
					mu.Lock()
					failing = append(failing, c.Name)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if len(failing) > 0 {
			slices.Sort(failing)
			JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "failing": failing})
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}))
}
