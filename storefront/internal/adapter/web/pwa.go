package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	settings, err := s.App.Settings(r.Context())
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	theme, _ := shop.FindTheme(settings.Theme)
	if settings.Accent != "" {
		theme.Accent = settings.Accent
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "/", "name": settings.Name, "short_name": settings.Name,
		"description": settings.Tagline, "lang": "fa", "dir": "rtl",
		"start_url": "/", "scope": "/", "display": "standalone",
		"background_color": theme.Background, "theme_color": theme.Accent,
		"icons": []map[string]string{
			{"src": "/app-icon.png?size=192", "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
			{"src": "/app-icon.png?size=512", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
		"shortcuts": []map[string]string{{"name": "فروشگاه", "url": "/shop"}, {"name": "سبد خرید", "url": "/cart"}, {"name": "حساب من", "url": "/account"}},
	})
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	// Embed an asset revision so a release updates the offline shell even when
	// service worker behavior itself has not changed.
	hash := sha256.New()
	for _, path := range []string{"assets/sw.js", "assets/style.css", "assets/commerce.css", "assets/customer.css", "assets/offline.html"} {
		data, _ := files.ReadFile(path)
		_, _ = hash.Write(data)
	}
	data, _ := files.ReadFile("assets/sw.js")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write([]byte(strings.ReplaceAll(string(data), "__STOREFRONT_VERSION__", hex.EncodeToString(hash.Sum(nil))[:16])))
}

func (s *Server) offline(w http.ResponseWriter, r *http.Request) {
	data, _ := files.ReadFile("assets/offline.html")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func iconColor(raw string) color.RGBA {
	var red, green, blue uint8
	if _, err := fmt.Sscanf(raw, "#%02x%02x%02x", &red, &green, &blue); err != nil {
		return color.RGBA{R: 139, G: 80, B: 59, A: 255}
	}
	return color.RGBA{R: red, G: green, B: blue, A: 255}
}

func (s *Server) appIcon(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size != 180 && size != 192 && size != 512 {
		http.NotFound(w, r)
		return
	}
	settings, err := s.App.Settings(r.Context())
	if err != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	theme, _ := shop.FindTheme(settings.Theme)
	if settings.Accent != "" {
		theme.Accent = settings.Accent
	}
	paper, ink := iconColor(theme.Background), iconColor(theme.Accent)
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// The shared flower mark stays entirely inside the maskable icon safe zone.
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := (float64(x)+0.5)/float64(size)-0.5, (float64(y)+0.5)/float64(size)-0.5
			fill := paper
			for petal := 0; petal < 8; petal++ {
				a := float64(petal) * math.Pi / 4
				u, v := dx*math.Cos(a)+dy*math.Sin(a)-0.15, -dx*math.Sin(a)+dy*math.Cos(a)
				if u*u/(0.19*0.19)+v*v/(0.065*0.065) <= 1 {
					fill = ink
					break
				}
			}
			if dx*dx+dy*dy < 0.045*0.045 {
				fill = paper
			}
			img.SetRGBA(x, y, fill)
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	_ = png.Encode(w, img)
}
