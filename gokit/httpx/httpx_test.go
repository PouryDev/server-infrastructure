package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func do(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errBody(t *testing.T, rec *httptest.ResponseRecorder) ErrorDetail {
	t.Helper()
	var b ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return b.Error
}

func TestStatusMapping(t *testing.T) {
	cases := map[error]int{
		apperr.Invalid("x"):                                  400,
		apperr.Unauthorized("x"):                             401,
		apperr.Forbidden("x"):                                403,
		apperr.NotFound("thing"):                             404,
		apperr.Conflict("x"):                                 409,
		apperr.Unavailable("x"):                              503,
		apperr.New(apperr.KindInvalid, "body_too_large", ""): 413,
		errors.New("plain"):                                  500,
	}
	for err, want := range cases {
		if got := StatusOf(err); got != want {
			t.Errorf("%v: %d, want %d", err, got, want)
		}
	}
}

func TestInternalErrorsAreHidden(t *testing.T) {
	r := NewRouter()
	r.Route("GET /boom", func(http.ResponseWriter, *http.Request) error {
		return apperr.Internal(errors.New("password=hunter2 in dsn"))
	})
	h := Chain(r, RequestID(), AccessLog(quiet()))
	rec := do(h, "GET", "/boom", "", "X-Request-ID", "req-1")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if d := errBody(t, rec); d.Code != "internal" || d.RequestID != "req-1" {
		t.Fatalf("%+v", d)
	}
}

func TestRecover(t *testing.T) {
	r := NewRouter()
	r.Handle("GET /panic", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") }))
	rec := do(Chain(r, RequestID(), AccessLog(quiet()), Recover()), "GET", "/panic", "")
	if rec.Code != 500 || errBody(t, rec).Code != "internal" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRequestIDSanitised(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), RequestID())
	if got := do(h, "GET", "/", "", "X-Request-ID", "bad id\r\nX: y").Header().Get("X-Request-ID"); got == "" || strings.ContainsAny(got, " \r\n") {
		t.Fatalf("unsafe id echoed: %q", got)
	}
}

type createReq struct {
	Name string `json:"name"`
}

func (c createReq) Validate() error {
	if c.Name == "" {
		return apperr.InvalidField("name", "is required")
	}
	return nil
}

func TestDecode(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) error {
		v, err := Decode[createReq](r)
		if err != nil {
			return err
		}
		JSON(w, 201, v)
		return nil
	}
	r := NewRouter()
	r.Route("POST /x", handler)
	h := Chain(r, BodyLimit(64))

	if rec := do(h, "POST", "/x", `{"name":"a"}`, "Content-Type", "application/json"); rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"empty":         {``, 400},
		"bad json":      {`{`, 400},
		"unknown field": {`{"name":"a","admin":true}`, 400},
		"wrong type":    {`{"name":5}`, 400},
		"trailing":      {`{"name":"a"}{"name":"b"}`, 400},
		"validation":    {`{"name":""}`, 400},
		"too large":     {`{"name":"` + strings.Repeat("a", 100) + `"}`, 413},
	} {
		rec := do(h, "POST", "/x", c.body, "Content-Type", "application/json")
		if rec.Code != c.want {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec := do(h, "POST", "/x", `{"name":""}`, "Content-Type", "application/json")
	if d := errBody(t, rec); len(d.Fields) != 1 || d.Fields[0].Field != "name" {
		t.Fatalf("%+v", d)
	}
	if rec := do(h, "POST", "/x", `{"name":"a"}`, "Content-Type", "text/plain"); rec.Code != 415 {
		t.Fatalf("content type: %d", rec.Code)
	}
}

func TestAuthMiddleware(t *testing.T) {
	ks, _ := auth.KeyConfig{Secret: strings.Repeat("k", 32)}.KeySet()
	j, _ := auth.NewJWT(auth.JWTConfig{Issuer: "i", Audience: "a"}, ks)
	pair, _ := j.IssuePair("u1", []string{"user"}, nil)

	r := NewRouter()
	r.Route("GET /me", func(w http.ResponseWriter, req *http.Request) error {
		c, _ := auth.ClaimsFrom(req.Context())
		JSON(w, 200, map[string]string{"sub": c.Subject})
		return nil
	}, Authenticate(j))
	r.Route("GET /admin", func(w http.ResponseWriter, req *http.Request) error { NoContent(w); return nil },
		Authenticate(j), RequireRoles("admin"))

	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	if rec := do(r, "GET", "/me", ""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("no token: %d", rec.Code)
	}
	rec := do(r, "GET", "/me", "", bearer(pair.AccessToken)...)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("valid: %d %v", rec.Code, rec.Header())
	}
	if rec := do(r, "GET", "/me", "", bearer(pair.RefreshToken)...); rec.Code != 401 {
		t.Fatalf("refresh token must not authenticate: %d", rec.Code)
	}
	if rec := do(r, "GET", "/admin", "", bearer(pair.AccessToken)...); rec.Code != 403 {
		t.Fatalf("role check: %d", rec.Code)
	}
}

func TestCORS(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }), CORS([]string{"https://app.example.com"}))
	rec := do(h, "OPTIONS", "/", "", "Origin", "https://app.example.com", "Access-Control-Request-Method", "POST")
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
	if rec := do(h, "GET", "/", "", "Origin", "https://evil.example"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin must get no CORS headers")
	}
}

func TestHealth(t *testing.T) {
	failing := true
	r := NewRouter()
	RegisterHealth(r, Check{Name: "db", Fn: func(context.Context) error {
		if failing {
			return errors.New("down")
		}
		return nil
	}})
	if rec := do(r, "GET", "/healthz", ""); rec.Code != 200 {
		t.Fatal("liveness must not depend on checks")
	}
	rec := do(r, "GET", "/readyz", "")
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"db"`) || strings.Contains(rec.Body.String(), "down") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	failing = false
	if rec := do(r, "GET", "/readyz", ""); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestRouterGroupMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(n string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { order = append(order, n); next.ServeHTTP(w, r) })
		}
	}
	r := NewRouter()
	g := r.Group(mw("group"))
	g.Route("GET /x", func(http.ResponseWriter, *http.Request) error { order = append(order, "handler"); return nil }, mw("route"))
	do(r, "GET", "/x", "")
	if strings.Join(order, ",") != "group,route,handler" {
		t.Fatalf("order = %v", order)
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(200)
	})
	s := NewServer(ServerConfig{ShutdownTimeout: 2 * time.Second}, h, quiet())
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	respc := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			respc <- -1
			return
		}
		resp.Body.Close()
		respc <- resp.StatusCode
	}()
	<-started
	cancel() // shutdown begins while a request is in flight
	time.Sleep(50 * time.Millisecond)
	close(release)

	if code := <-respc; code != 200 {
		t.Fatalf("in-flight request got %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := Probe(context.Background(), "http://"+ln.Addr().String()); err == nil {
		t.Fatal("server must be stopped")
	}
}

func TestListParamsRejectsMalformedQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/x?sort=email;drop&page=2", nil)
	if _, err := ListParams(req); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("err = %v", err)
	}
	req = httptest.NewRequest("GET", "/x?filter[role]=admin&page=2", nil)
	p, err := ListParams(req)
	if err != nil || p.Page != 2 || len(p.Filters) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestRoutePatternReachesOuterMiddleware(t *testing.T) {
	r := NewRouter()
	r.Route("GET /v1/things/{id}", func(w http.ResponseWriter, _ *http.Request) error { NoContent(w); return nil })

	var seen string
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req)
			seen = req.Pattern // what OpenTelemetry reads after the handler returns
		})
	}
	h := Chain(r, outer, RequestID(), AccessLog(quiet()), Recover())
	do(h, "GET", "/v1/things/42", "")
	if seen != "GET /v1/things/{id}" {
		t.Fatalf("pattern = %q", seen)
	}
}

// BenchmarkStack measures the per-request cost of the whole default middleware stack around a trivial
// handler (tracing is a no-op without a configured provider).
func BenchmarkStack(b *testing.B) {
	r := NewRouter()
	r.Route("GET /v1/things/{id}", func(w http.ResponseWriter, _ *http.Request) error {
		JSON(w, 200, map[string]string{"ok": "yes"})
		return nil
	})
	h := Chain(r, DefaultStack(quiet(), StackConfig{})...)
	req := httptest.NewRequest("GET", "/v1/things/42", nil)
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
}
