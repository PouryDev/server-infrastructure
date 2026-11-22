package web

import (
	"net/http"
	"regexp"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

var previewAccentRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type previewOption struct {
	Theme  shop.Theme  `json:"theme"`
	Design shop.Design `json:"design"`
}
type themePreview struct {
	Accent   string          `json:"accent"`
	Original previewOption   `json:"original"`
	Themes   []previewOption `json:"themes"`
	Selected string          `json:"selected"`
}

func (s *Server) applyThemePreview(r *http.Request, p *page) {
	p.Theme, _ = shop.FindTheme(p.Settings.Theme)
	p.Design = shop.ThemeDesign(p.Settings.Theme)
	if p.Settings.Accent != "" {
		p.Theme.Accent = p.Settings.Accent
	}
	// The temporary debugbar is demo-only. Production sellers/customers never
	// receive its theme catalog or the ability to override the store theme.
	if s.Demo && p.View != "seller" && p.View != "admin" && p.View != "demo-payment" {
		p.Preview = &themePreview{Original: previewOption{Theme: p.Theme, Design: p.Design}}
		for _, theme := range shop.Themes() {
			p.Preview.Themes = append(p.Preview.Themes, previewOption{Theme: theme, Design: shop.ThemeDesign(theme.ID)})
		}
	}
	if !s.Demo && p.User.Role != "admin" {
		return
	}
	id := r.URL.Query().Get("preview")
	if !r.URL.Query().Has("preview") && s.Demo {
		if cookie, err := r.Cookie("storefront-preview"); err == nil {
			id = cookie.Value
		}
	}
	if theme, ok := shop.FindTheme(id); ok {
		p.Theme, p.Design = theme, shop.ThemeDesign(id)
		p.Theme.Accent = p.Settings.Accent
		if p.Preview != nil {
			p.Preview.Selected = theme.ID
		}
	}
	if s.Demo {
		if cookie, err := r.Cookie("storefront-accent"); err == nil && previewAccentRE.MatchString(cookie.Value) {
			p.Theme.Accent = cookie.Value
		}
	}
	if p.Preview != nil {
		p.Preview.Accent = p.Theme.Accent
	}

}
