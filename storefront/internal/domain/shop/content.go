package shop

import (
	"net/url"
	"regexp"
	"strings"
)

type TorobSettings struct {
	ClearToken bool `json:"clearToken,omitempty"`

	Enabled      bool   `json:"enabled"`
	WebhookToken string `json:"webhookToken,omitempty"`
	Configured   bool   `json:"configured"`
}
type ContentPage struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Published   bool   `json:"published"`
}
type NavigationLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func LocalLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && u.Host == "" && u.Scheme == "" && !strings.ContainsAny(raw, "\\\r\n")
}
func LocalImage(raw string) bool {
	return raw == "" || LocalLink(raw) && (strings.HasPrefix(raw, "/assets/") || strings.HasPrefix(raw, "/media/"))
}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (p Product) ValidateDetails() error {
	if len(p.Brand) > 200 || len(p.Guarantee) > 200 || p.WeightGrams < 0 || p.WeightGrams > 1_000_000 || len(p.Images) > 12 || len(p.Specs) > 40 || len(p.Description) > 30000 || len(p.Category) > 200 {
		return ErrInvalid
	}
	for _, image := range p.Images {
		if !LocalImage(image) || image == "" {
			return ErrInvalid
		}
	}
	for k, v := range p.Specs {
		if strings.TrimSpace(k) == "" || len(k) > 100 || len(v) > 1000 {
			return ErrInvalid
		}
	}
	for _, v := range p.Variants {
		if len(v.Name) > 200 || len(v.SKU) > 100 || len(v.Size) > 80 || len(v.ColorName) > 80 || v.Color != "" && !hexColorRE.MatchString(v.Color) || !LocalImage(v.Image) || v.OldPrice != 0 && (v.OldPrice < v.Price || v.OldPrice > 1_000_000_000_000) || v.Price%10 != 0 || v.OldPrice%10 != 0 {
			return ErrInvalid
		}
	}
	return nil
}
func (s Settings) ValidateContent() error {
	if len(s.Pages) > 40 || len(s.Navigation) > 12 || len(s.DeliveryMethods) > 20 || len(s.Torob.WebhookToken) > 4000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, m := range s.DeliveryMethods {
		if e := m.Validate(); e != nil {
			return e
		}
		if seen[m.ID] {
			return ErrInvalid
		}
		seen[m.ID] = true
	}
	seen = map[string]bool{}
	for _, p := range s.Pages {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`).MatchString(p.Slug) || seen[p.Slug] || strings.TrimSpace(p.Title) == "" || len(p.Title) > 250 || len(p.Description) > 1000 || len(p.Body) > 30000 {
			return ErrInvalid
		}
		seen[p.Slug] = true
	}
	for _, l := range s.Navigation {
		if strings.TrimSpace(l.Label) == "" || len(l.Label) > 100 || len(l.URL) > 500 || !LocalLink(l.URL) {
			return ErrInvalid
		}
	}
	return nil
}
