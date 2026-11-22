package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
)

type Server struct {
	Manager        *application.Manager
	Shipping       *application.Shipping
	Storefront     *application.Storefront
	Deployments    *application.Deployments
	Resources      *application.Resources
	Envs           *application.Envs
	VPN            *application.VPN
	limits         limiter
	Auth           *Auth
	Origin, WebDir string
	Wake           func()
}

func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, code int, message string) {
	respond(w, code, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	return decodeLimit(w, r, target, 4096)
}

// decodeLimit is strict JSON decoding (no unknown fields, one document) with a
// body bound; env edits need more than the 4 KiB default.
func decodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		problem(w, 400, "Invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		problem(w, 400, "Invalid request body")
		return false
	}
	return true
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	s.shippingRoutes(mux)
	s.envRoutes(mux)
	s.resourceRoutes(mux)
	s.storefrontRoutes(mux)
	s.deploymentRoutes(mux)
	s.vpnRoutes(mux)
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &body) {
			return
		}
		sess, code := s.Auth.login(w, r, body.Username, body.Password)
		outcome := "failure"
		actor := "authentication"
		if code == 200 {
			outcome = "success"
			actor = sess.User
		}
		if err := s.Manager.Record(r.Context(), actor, "login", outcome); err != nil {
			s.Auth.logout(w, r)
			problem(w, 503, "Audit storage unavailable")
			return
		}
		if code != 200 {
			message := "Invalid username or password"
			if code == 429 {
				message = "Too many attempts. Try again in five minutes."
			}
			problem(w, code, message)
			return
		}
		respond(w, 200, sessView(sess))
	})
	mux.HandleFunc("GET /api/session", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) { respond(w, 200, sessView(sess)) }))
	mux.HandleFunc("POST /api/logout", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		if err := s.Manager.Record(r.Context(), sess.User, "logout", "success"); err != nil {
			problem(w, 503, "Audit storage unavailable")
			return
		}
		s.Auth.logout(w, r)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/snapshot", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) { respond(w, 200, s.Manager.Snapshot()) }))
	mux.HandleFunc("POST /api/sync", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		if err := s.Manager.Record(r.Context(), sess.User, "sync", "accepted"); err != nil {
			problem(w, 503, "Audit storage unavailable")
			return
		}
		if s.Wake != nil {
			s.Wake()
		}
		respond(w, 202, map[string]string{"status": "accepted"})
	}))
	mux.HandleFunc("GET /api/audit", s.protected(func(w http.ResponseWriter, r *http.Request, _ session) {
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		events, err := s.Manager.Audit(r.Context(), r.URL.Query().Get("project"), before, 50)
		if err != nil {
			problem(w, 503, "Audit storage unavailable")
			return
		}
		respond(w, 200, events)
	}))
	mux.HandleFunc("POST /api/projects/{project}/{action}", s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		var body struct {
			ExpiresAt *time.Time `json:"expiresAt"`
			Revision  int64      `json:"revision"`
		}
		if !decode(w, r, &body) {
			return
		}
		err := s.Manager.Change(r.Context(), sess.User, r.PathValue("project"), r.PathValue("action"), body.ExpiresAt, body.Revision)
		switch {
		case errors.Is(err, application.ErrNotFound):
			problem(w, 404, "Project is unavailable or protected")
		case errors.Is(err, application.ErrConflict):
			problem(w, 409, err.Error())
		case errors.Is(err, application.ErrInvalid):
			problem(w, 400, "Choose a future expiration time")
		case err != nil:
			problem(w, 503, "Change could not be persisted; retry after synchronization")
		default:
			if s.Wake != nil {
				s.Wake()
			}
			respond(w, 202, map[string]string{"status": "accepted"})
		}
	}))
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "User-agent: *\nDisallow: /\n")
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "Endpoint not found") })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.NotFound(w, r)
			return
		}
		path := filepath.Join(s.WebDir, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		rel, err := filepath.Rel(s.WebDir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			http.NotFound(w, r)
			return
		}
		stat, err := os.Stat(path)
		if err != nil || stat.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
			path = filepath.Join(s.WebDir, "index.html")
		}
		http.ServeFile(w, r, path)
	})
	origin, _ := url.Parse(s.Origin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive, nosnippet")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'; object-src 'none'")
		if r.URL.Path != "/healthz" && (origin == nil || r.Host != origin.Host) {
			problem(w, 403, "Unexpected host")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Origin") != s.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
			problem(w, 403, "Request origin rejected")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func sessView(s session) map[string]string { return map[string]string{"user": s.User, "csrf": s.CSRF} }
func (s *Server) protected(handler func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.Auth.current(r)
		if !ok {
			problem(w, 401, "Please sign in")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRF)) != 1 {
			problem(w, 403, "Invalid request token")
			return
		}
		handler(w, r, sess)
	}
}

// limiter is a small per-user sliding-window rate limit for the new endpoints.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	now := time.Now()
	recent := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= max {
		l.hits[key] = recent
		return false
	}
	l.hits[key] = append(recent, now)
	return true
}

// rated wraps a protected handler with a per-user request budget per minute.
func (s *Server) rated(name string, perMinute int, handler func(http.ResponseWriter, *http.Request, session)) http.HandlerFunc {
	return s.protected(func(w http.ResponseWriter, r *http.Request, sess session) {
		if !s.limits.allow(name+"\x00"+sess.User, perMinute, time.Minute) {
			problem(w, 429, "Too many requests; wait a minute and try again")
			return
		}
		handler(w, r, sess)
	})
}
