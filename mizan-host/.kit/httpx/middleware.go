package httpx

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, m := range slices.Backward(mws) {
		h = m(h)
	}
	return h
}

type ctxKey struct{}

// reqInfo is the per-request state shared by the middleware: one context value for all of it.
type reqInfo struct {
	id  string
	log *slog.Logger
}

func infoFrom(ctx context.Context) *reqInfo {
	i, _ := ctx.Value(ctxKey{}).(*reqInfo)
	return i
}

// RequestIDFrom returns the request id set by RequestID.
func RequestIDFrom(ctx context.Context) string {
	if i := infoFrom(ctx); i != nil {
		return i.id
	}
	return ""
}

// LoggerFrom returns the logger of the current request, carrying its request id (built on demand, so
// requests that never log pay nothing), or the default logger outside a request.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if i := infoFrom(ctx); i != nil && i.log != nil {
		return i.log.With("request_id", i.id)
	}
	return slog.Default()
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestID keeps the X-Request-ID that nginx forwards (or creates one) and echoes it back.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var id string
			if h := r.Header["X-Request-Id"]; len(h) > 0 && requestIDRe.MatchString(h[0]) {
				id = h[0]
			} else {
				id = newRequestID()
			}
			w.Header()["X-Request-Id"] = []string{id} // canonical key set directly: no re-canonicalisation
			serveWithContext(next, w, r, context.WithValue(r.Context(), ctxKey{}, &reqInfo{id: id}))
		})
	}
}

// newRequestID is 16 hex digits from the runtime's per-thread ChaCha8 generator: unique enough to
// correlate logs, not secret, and about ten times cheaper than crypto/rand plus hex encoding.
func newRequestID() string {
	var buf [16]byte
	n := rand.Uint64() | 1<<63 //nolint:gosec // request ids correlate logs, they are not secrets
	return string(strconv.AppendUint(buf[:0], n, 16))
}

// serveWithContext calls next with ctx and then copies back the route pattern that the ServeMux
// inside set on its own copy of the request. Without this, middleware wrapped around the router
// (the logger, OpenTelemetry's http.route attribute) would never learn which route matched.
func serveWithContext(next http.Handler, w http.ResponseWriter, r *http.Request, ctx context.Context) {
	req := r.WithContext(ctx)
	defer func() { r.Pattern = req.Pattern }()
	next.ServeHTTP(w, req)
}

// statusRecorder captures what the handler wrote. Unwrap keeps http.ResponseController working
// (flush, deadlines) for streaming handlers.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request and makes the logger available to handlers through LoggerFrom.
// Paths listed in quiet (health probes) are logged at debug level, and nothing is built when the level
// is disabled.
func AccessLog(log *slog.Logger, quiet ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			info := infoFrom(r.Context())
			if info == nil { // used without RequestID: create the state here
				info = &reqInfo{}
				r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, info))
			}
			info.log = log
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			level := slog.LevelInfo
			switch {
			case slices.Contains(quiet, r.URL.Path):
				level = slog.LevelDebug
			case status >= 500:
				level = slog.LevelError
			}
			if !log.Enabled(r.Context(), level) {
				return
			}
			log.LogAttrs(r.Context(), level, "http request",
				slog.String("request_id", info.id), slog.String("method", r.Method), slog.String("path", r.URL.Path),
				slog.String("route", r.Pattern), slog.Int("status", status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.Int("bytes", rec.bytes), slog.String("client_ip", ClientIP(r)))
		})
	}
}

// Recover turns a panic in a handler into a 500 response and a logged stack trace.
func Recover() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				p := recover()
				if p == nil {
					return
				}
				if p == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity per net/http docs
					panic(p)
				}
				LoggerFrom(r.Context()).ErrorContext(r.Context(), "panic in handler",
					"panic", p, "stack", string(debug.Stack()))
				JSON(w, http.StatusInternalServerError, ErrorBody{Error: ErrorDetail{
					Code: "internal", Message: "internal error", RequestID: RequestIDFrom(r.Context())}})
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// Shared, never mutated header values: assigning them directly skips the per-request slice and key
// canonicalisation of Header.Set. A handler that appends to one gets a copy (capacity equals length).
var (
	hdrNoSniff    = []string{"nosniff"}
	hdrNoReferrer = []string{"no-referrer"}
	hdrCSP        = []string{"default-src 'none'; frame-ancestors 'none'"}
)

// SecurityHeaders sets conservative headers for an API (no HTML is served).
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h["X-Content-Type-Options"] = hdrNoSniff
			h["Referrer-Policy"] = hdrNoReferrer
			h["Content-Security-Policy"] = hdrCSP
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit caps request bodies; reads past the limit fail with *http.MaxBytesError (Decode reports 413).
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows browsers on the listed origins (exact match; "*" is deliberately not supported because
// the API uses Authorization headers). With no origins configured it does nothing.
func CORS(origins []string) Middleware {
	return func(next http.Handler) http.Handler {
		if len(origins) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !slices.Contains(origins, origin) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
				h.Set("Access-Control-Max-Age", strconv.Itoa(600))
				h.Add("Vary", "Access-Control-Request-Method")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			h.Set("Access-Control-Expose-Headers", "X-Request-ID")
			next.ServeHTTP(w, r)
		})
	}
}

// Instrument adds OpenTelemetry tracing and the standard http.server.request.duration metric. It
// must be the outermost middleware so later ones (and the logger) see the span. Requests to skip
// (health probes) produce no spans and no metrics.
func Instrument(skipPaths ...string) Middleware {
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, "http.server",
			otelhttp.WithFilter(func(r *http.Request) bool { return !slices.Contains(skipPaths, r.URL.Path) }))
	}
}

// DefaultStack is the standard middleware order for an API; Router.Use or NewServer apply it.
func DefaultStack(log *slog.Logger, cfg StackConfig) []Middleware {
	quiet := []string{"/healthz", "/readyz"}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	return []Middleware{
		Instrument(quiet...),
		RequestID(),
		AccessLog(log, quiet...),
		Recover(),
		SecurityHeaders(),
		CORS(cfg.CORSOrigins),
		BodyLimit(maxBody),
	}
}

// StackConfig tunes DefaultStack.
type StackConfig struct {
	CORSOrigins  []string
	MaxBodyBytes int64 // default 1 MiB
}

// bearerToken extracts the token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}
