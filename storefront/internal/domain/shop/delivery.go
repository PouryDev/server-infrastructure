package shop

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// DeliveryMethod is an owner-defined tariff, never a claim of a live carrier quote.
// All amounts use IRR; delivery windows count calendar days in Tehran.
type DeliveryMethod struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Carrier        string   `json:"carrier"`
	Fee            int64    `json:"fee"`
	FreeAbove      int64    `json:"freeAbove"`
	MinSubtotal    int64    `json:"minSubtotal"`
	MaxWeightGrams int      `json:"maxWeightGrams"`
	Provinces      []string `json:"provinces"`
	Cities         []string `json:"cities"`
	MinDays        int      `json:"minDays"`
	MaxDays        int      `json:"maxDays"`
	PickupAddress  string   `json:"pickupAddress"`
	Enabled        bool     `json:"enabled"`
}
type DeliverySelection struct {
	MethodID string `json:"methodId"`
	Province string `json:"province"`
	City     string `json:"city"`
}
type DeliveryQuote struct {
	MethodID      string `json:"methodId"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Carrier       string `json:"carrier"`
	Fee           int64  `json:"fee"`
	From          string `json:"from"`
	To            string `json:"to"`
	PickupAddress string `json:"pickupAddress,omitempty"`
}
type Shipment struct {
	Carrier      string    `json:"carrier"`
	TrackingCode string    `json:"trackingCode"`
	Note         string    `json:"note"`
	ShippedAt    time.Time `json:"shippedAt,omitempty"`
}

var methodIDRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func (m DeliveryMethod) Validate() error {
	if !methodIDRE.MatchString(m.ID) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 200 || !slices.Contains([]string{"shipping", "courier", "pickup"}, m.Kind) || len(m.Carrier) > 100 || m.Fee < 0 || m.Fee > 1_000_000_000_000 || m.FreeAbove < 0 || m.FreeAbove > 1_000_000_000_000_000 || m.MinSubtotal < 0 || m.MinSubtotal > 1_000_000_000_000_000 || m.MaxWeightGrams < 0 || m.MaxWeightGrams > 1_000_000 || m.MinDays < 0 || m.MaxDays < m.MinDays || m.MaxDays > 90 || len(m.PickupAddress) > 1000 || len(m.Provinces) > 40 || len(m.Cities) > 200 {
		return fmt.Errorf("%w: شرایط روش ارسال را بررسی کنید", ErrInvalid)
	}
	if m.Kind == "pickup" && strings.TrimSpace(m.PickupAddress) == "" {
		return fmt.Errorf("%w: نشانی تحویل حضوری لازم است", ErrInvalid)
	}
	for _, place := range append(slices.Clone(m.Provinces), m.Cities...) {
		if strings.TrimSpace(place) == "" || len(place) > 100 {
			return ErrInvalid
		}
	}
	return nil
}
func placeName(value string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("ي", "ی", "ك", "ک", "‌", " ").Replace(value)), " ")
}
func matchesPlace(places []string, value string) bool {
	return len(places) == 0 || slices.ContainsFunc(places, func(p string) bool { return placeName(p) == placeName(value) })
}
func (s Settings) DeliveryQuotes(selection DeliverySelection, subtotal int64, grams int, now time.Time) ([]DeliveryQuote, error) {
	methods := s.DeliveryMethods
	// Older instances keep their configured tariff until the owner adds methods.
	if len(methods) == 0 {
		methods = []DeliveryMethod{{ID: "standard", Name: "ارسال استاندارد", Kind: "shipping", Fee: s.Shipping, FreeAbove: s.FreeShipping, MinDays: 2, MaxDays: 5, Enabled: true}}
	}
	zone := time.FixedZone("Asia/Tehran", 3*3600+30*60)
	now = now.In(zone)
	out := []DeliveryQuote{}
	for _, m := range methods {
		if !m.Enabled || subtotal < m.MinSubtotal || m.MaxWeightGrams > 0 && grams > m.MaxWeightGrams || !matchesPlace(m.Provinces, selection.Province) || !matchesPlace(m.Cities, selection.City) {
			continue
		}
		fee := m.Fee
		if m.FreeAbove > 0 && subtotal >= m.FreeAbove {
			fee = 0
		}
		out = append(out, DeliveryQuote{MethodID: m.ID, Name: m.Name, Kind: m.Kind, Carrier: m.Carrier, Fee: fee, From: now.AddDate(0, 0, m.MinDays).Format("2006-01-02"), To: now.AddDate(0, 0, m.MaxDays).Format("2006-01-02"), PickupAddress: m.PickupAddress})
	}
	if len(out) == 0 {
		return out, fmt.Errorf("%w: برای این نشانی و سبد، روش تحویل فعالی وجود ندارد؛ استان و شهر را تکمیل کنید یا با فروشگاه تماس بگیرید", ErrInvalid)
	}
	return out, nil
}
