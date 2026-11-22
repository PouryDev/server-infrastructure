package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func TestThemePreviewBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, role, query, cookie, want string
		demo, bar                       bool
	}{
		{name: "local current customer", demo: true, bar: true, want: "atelier"},
		{name: "local preset palette", demo: true, bar: true, query: "?preview=mono", want: "mono"},
		{name: "local navigation keeps selection", demo: true, bar: true, cookie: "mono", want: "mono"},
		{name: "local reset overrides cookie", demo: true, bar: true, query: "?preview=", cookie: "mono", want: "atelier"},
		{name: "unknown cookie ignored", demo: true, bar: true, cookie: "not-a-theme", want: "atelier"},
		{name: "production customer", query: "?preview=mono", cookie: "mono", want: "atelier"},
		{name: "production seller", role: "seller", query: "?preview=mono", cookie: "mono", want: "atelier"},
		{name: "production admin query", role: "admin", query: "?preview=mono", want: "mono"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://shoptest.localhost/"+tt.query, nil)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "storefront-preview", Value: tt.cookie})
			}
			p := page{View: "home", Settings: shop.Settings{Theme: "atelier", Accent: "#123456"}, User: shop.User{Role: tt.role}}
			s := Server{Demo: tt.demo}
			s.applyThemePreview(r, &p)
			if p.Theme.ID != tt.want || (p.Preview != nil) != tt.bar {
				t.Fatalf("theme %s debugbar %v", p.Theme.ID, p.Preview != nil)
			}
			if p.Theme.ID == "atelier" && p.Theme.Accent != "#123456" {
				t.Fatal("brand color was lost")
			}
			if p.Theme.Accent != "#123456" {
				t.Fatal("style changed the independently configured brand color")
			}

			if p.Preview != nil && len(p.Preview.Themes) != 16 {
				t.Fatal("missing presets")
			}
			if p.Settings.Theme != "atelier" {
				t.Fatal("preview changed persistent settings")
			}
		})
	}
}
