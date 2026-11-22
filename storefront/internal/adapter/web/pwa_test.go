package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
)

type storefrontFixture struct {
	commerce.Repository
	settings shop.Settings
	products []shop.Product
}

func (f storefrontFixture) Settings(context.Context, bool) (shop.Settings, error) {
	return f.settings, nil
}
func (f storefrontFixture) Products(context.Context) ([]shop.Product, error) { return f.products, nil }

func fixtureServer() *Server {
	repo := storefrontFixture{settings: shop.Settings{Name: "فروشگاه تست", Theme: shop.Themes()[0].ID, Accent: "#123456"}, products: []shop.Product{{ID: "legacy", Active: true}}}
	return New(Server{App: commerce.New(repo, nil, "http://shoptest.localhost"), Origin: "http://shoptest.localhost"})
}
func getFixture(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://shoptest.localhost"+path, nil))
	return w
}
func TestCatalogResponseCollections(t *testing.T) {
	w := getFixture(fixtureServer(), "/api/products")
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	var products []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &products); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"attachments", "variants"} {
		if _, ok := products[0][key].([]any); !ok {
			t.Fatalf("%s must be an array: %s", key, w.Body.String())
		}
	}
}
func TestPWAManifestAndIcons(t *testing.T) {
	s := fixtureServer()
	w := getFixture(s, "/manifest.webmanifest")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/manifest+json") {
		t.Fatalf("manifest %d %s", w.Code, w.Body.String())
	}
	var manifest struct {
		Name, Display, Scope, Dir, ThemeColor string
		Icons                                 []struct{ Src, Sizes, Purpose string }
	}
	// Use a map for the manifest's snake_case keys.
	var data map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data["name"] != "فروشگاه تست" || data["display"] != "standalone" || data["scope"] != "/" || data["theme_color"] != "#123456" {
		t.Fatalf("unexpected manifest %s", w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Icons) != 2 {
		t.Fatal("missing install icons")
	}
	for _, icon := range manifest.Icons {
		w = getFixture(s, icon.Src)
		img, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		expected := 192
		if strings.Contains(icon.Sizes, "512") {
			expected = 512
		}
		if img.Bounds().Dx() != expected || img.Bounds().Dy() != expected {
			t.Fatal("incorrect icon dimensions")
		}
	}
	if getFixture(s, "/app-icon.png?size=99999999").Code != 404 {
		t.Fatal("unbounded icon size")
	}
}
func TestWorkerAndOfflineShell(t *testing.T) {
	s := fixtureServer()
	w := getFixture(s, "/sw.js")
	if w.Code != 200 || w.Header().Get("Service-Worker-Allowed") != "/" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("worker scope/update headers")
	}
	if strings.Contains(w.Body.String(), "__STOREFRONT_VERSION__") {
		t.Fatal("asset revision not bound")
	}
	w = getFixture(s, "/offline.html")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "تلاش دوباره") {
		t.Fatal("missing offline recovery")
	}
}
func TestAllThemesShareMobileNavigationAndPWA(t *testing.T) {
	s := fixtureServer()
	for _, theme := range shop.Themes() {
		t.Run(theme.ID, func(t *testing.T) {
			for _, wishlist := range []bool{false, true} {
				var html bytes.Buffer
				p := page{View: "cart", Theme: theme, Design: shop.ThemeDesign(theme.ID), Settings: shop.Settings{Name: "تست", Modules: map[string]bool{"wishlist": wishlist}}}
				if err := s.templates.ExecuteTemplate(&html, "page", p); err != nil {
					t.Fatal(err)
				}
				text := html.String()
				if strings.Contains(text, `id="mobile-menu"`) || !strings.Contains(text, `class="bottom-nav"`) || !strings.Contains(text, `rel="manifest"`) {
					t.Fatal("missing shared mobile/PWA behavior")
				}
				if !strings.Contains(text, `href="/cart" aria-current="page"`) {
					t.Fatal("missing active cart navigation")
				}
				if strings.Contains(text, `href="/wishlist"`) != wishlist {
					t.Fatal("wishlist module must gate navigation")
				}
			}
		})
	}
}

func TestTenDistinctStorefrontArchitectures(t *testing.T) {
	s := fixtureServer()
	markers := map[string]string{"catalog": "formal-directory", "orbit": "startup-stack", "pulse": "motion-orbit", "mono": "minimal-heading", "bazaar": "market-directory", "street": "brutal-photo", "atelier": "luxury-picks", "play": "play-collage", "journal": "journal-masthead", "terminal": "terminal-list"}
	for _, theme := range shop.Themes() {
		t.Run(theme.ID, func(t *testing.T) {
			p := page{View: "home", Theme: theme, Design: shop.ThemeDesign(theme.ID), Settings: shop.Settings{Name: "test", Sections: []shop.Section{{Kind: "hero", Enabled: true}}}}
			var html bytes.Buffer
			if err := s.templates.ExecuteTemplate(&html, "page", p); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html.String(), markers[theme.ID]) || !strings.Contains(html.String(), `class="mobile-hub"`) {
				t.Fatal("missing architecture/mobile app home")
			}
		})
	}
}
