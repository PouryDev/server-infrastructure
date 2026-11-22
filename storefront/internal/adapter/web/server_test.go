package web

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestTemplatesParse(t *testing.T) {
	s := New(Server{Origin: "http://shoptest.localhost"})
	if s.templates == nil {
		t.Fatal("missing templates")
	}
}
func TestHostAndOriginBoundary(t *testing.T) {
	s := New(Server{Origin: "http://shoptest.localhost"})
	h := s.Handler()
	for _, test := range []struct {
		method, url, origin string
		want                int
	}{{"GET", "http://evil.localhost/healthz", "", 200}, {"GET", "http://evil.localhost/", "", 403}, {"POST", "http://shoptest.localhost/api/auth/otp", "http://evil.localhost", 403}, {"POST", "http://shoptest.localhost/api/auth/otp", "", 403}} {
		r := httptest.NewRequestWithContext(context.Background(), test.method, test.url, nil)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Errorf("%s %s => %d", test.method, test.url, w.Code)
		}
	}
}
