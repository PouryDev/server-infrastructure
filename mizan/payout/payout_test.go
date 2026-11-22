package payout

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// validSheba builds an IBAN with correct check digits from 22 digits.
func validSheba(body string) string {
	rem := 0
	for _, r := range body + "182700" { // ISO 13616: the body, then IR as digits (I=18, R=27), then 00
		rem = (rem*10 + int(r-'0')) % 97
	}
	return "IR" + strconv.Itoa(98 - rem + 100)[1:] + body
}

func TestNormalizeSheba(t *testing.T) {
	good := validSheba("0170000000123456789012")
	for _, in := range []string{good, "  " + good[:4] + " " + good[4:] + " ", ""} {
		if _, err := NormalizeSheba(in); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
	if got, _ := NormalizeSheba(" ir" + good[2:] + " "); got != good {
		t.Errorf("normalised to %q", got)
	}
	wrong := good[:25] + string(rune('0'+(good[25]-'0'+1)%10))
	for _, in := range []string{wrong, "IR12", "GB" + good[2:], "IR" + good[2:25] + "x", good + "0"} {
		if _, err := NormalizeSheba(in); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%q must be refused, got %v", in, err)
		}
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestPickTermsPrecedenceAndDates(t *testing.T) {
	seller, other := uuid.New(), uuid.New()
	mk := func(scope ScopeKind, key string, from time.Time, bp int) Terms {
		return Terms{ID: uuid.New(), Name: "t", Scope: scope, ScopeKey: key, EffectiveFrom: from, RateBP: bp}
	}
	all := []Terms{
		mk(ScopeDefault, "", day(2026, 1, 1), 1000),
		mk(ScopeDefault, "", day(2026, 6, 1), 1200),
		mk(ScopeCategory, "books", day(2026, 1, 1), 500),
		mk(ScopeSeller, seller.String(), day(2026, 3, 1), 300),
	}
	for _, c := range []struct {
		name     string
		seller   uuid.UUID
		category string
		on       time.Time
		want     int
		found    bool
	}{
		{"before any version", seller, "", day(2025, 12, 31), 0, false},
		{"default of its day", other, "", day(2026, 2, 1), 1000, true},
		{"newer default replaces the older from its day", other, "", day(2026, 6, 1), 1200, true},
		{"category beats default", other, "books", day(2026, 7, 1), 500, true},
		{"unknown category falls back to default", other, "toys", day(2026, 7, 1), 1200, true},
		{"seller beats category and default", seller, "books", day(2026, 7, 1), 300, true},
		{"seller terms not yet started: category wins", seller, "books", day(2026, 2, 1), 500, true},
		{"time of day is ignored", other, "", time.Date(2026, 6, 1, 23, 59, 0, 0, time.UTC), 1200, true},
	} {
		got, ok := PickTerms(all, c.seller, c.category, c.on)
		if ok != c.found || (ok && got.RateBP != c.want) {
			t.Errorf("%s: got %d (found %v), want %d (found %v)", c.name, got.RateBP, ok, c.want, c.found)
		}
	}
}

func TestTermsValidation(t *testing.T) {
	ok := Terms{Name: "default", Scope: ScopeDefault, EffectiveFrom: day(2026, 1, 1), RateBP: 1000, HoldDays: 7}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*Terms){
		"no name":             func(x *Terms) { x.Name = " " },
		"no date":             func(x *Terms) { x.EffectiveFrom = time.Time{} },
		"rate above 100%":     func(x *Terms) { x.RateBP = 10001 },
		"negative rate":       func(x *Terms) { x.RateBP = -1 },
		"hold too long":       func(x *Terms) { x.HoldDays = 366 },
		"vat above 100%":      func(x *Terms) { v := 10001; x.VATBP = &v },
		"default with a key":  func(x *Terms) { x.ScopeKey = "x" },
		"category without":    func(x *Terms) { x.Scope = ScopeCategory },
		"seller key not uuid": func(x *Terms) { x.Scope, x.ScopeKey = ScopeSeller, "abc" },
		"unknown scope":       func(x *Terms) { x.Scope = "region" },
	} {
		v := ok
		mut(&v)
		if err := v.Validate(); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
	cat := Terms{Name: "n", Scope: ScopeCategory, ScopeKey: "books", EffectiveFrom: day(2026, 1, 1), RateBP: 0}
	if err := cat.Validate(); err != nil {
		t.Errorf("a zero commission is a valid choice: %v", err)
	}
}

func TestSalePhaseAndDelivery(t *testing.T) {
	s := Sale{Status: SaleHeld, SaleDate: day(2026, 11, 1), HoldDays: 7, Net: money.Must(1, money.IRR)}
	today := day(2026, 11, 9)
	if s.Phase(today) != "awaiting_delivery" {
		t.Fatal(s.Phase(today))
	}
	if err := s.deliver(day(2026, 11, 5), today); err != nil {
		t.Fatal(err)
	}
	if s.ReleaseAt == nil || !s.ReleaseAt.Equal(day(2026, 11, 12)) || s.Phase(today) != "held" || s.Phase(day(2026, 11, 12)) != "available" {
		t.Fatalf("release %v, phase %s", s.ReleaseAt, s.Phase(today))
	}
	for name, d := range map[string]time.Time{"future": day(2026, 11, 10), "before the sale": day(2026, 10, 31)} {
		x := Sale{SaleDate: day(2026, 11, 1)}
		if err := x.deliver(d, today); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s delivery must be refused: %v", name, err)
		}
	}
	s.Status = SalePaid
	if s.Phase(today) != "paid" {
		t.Fatal(s.Phase(today))
	}
}
