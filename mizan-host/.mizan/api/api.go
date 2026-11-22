// Package api is the HTTP adapter of mizan: JSON endpoints for accounting, payments, invoices, wallets and
// reconciliation, built on gokit's httpx. It owns no business rules; every handler decodes, calls a use case
// and encodes the result.
//
// Three groups of routes are mounted:
//
//	public   GET|POST {prefix}/gateway-callbacks/{gateway}      what gateways and payers' browsers call
//	customer {prefix}/me/...                                    the authenticated subject's own wallet and invoices
//	finance  {prefix}/accounting|payments|invoices|wallets|cheques|bank-accounts|...  staff, requires one of Options.FinanceRoles
//
// Money-moving POSTs need an Idempotency-Key header: it becomes the idempotency key of the operation, so a
// retry never moves money twice.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
)

// Options configure Mount.
type Options struct {
	Prefix string // default "/v1"
	// Authenticate verifies the access token (httpx.Authenticate(verifier)). Required: the module never
	// serves finance data without it.
	Authenticate httpx.Middleware
	// FinanceRoles may use the staff endpoints. Default: "finance", "admin".
	FinanceRoles []string
}

// API holds the handlers' dependencies.
type API struct {
	m *mizan.Mizan
}

// Mount registers all routes on r.
func Mount(r *httpx.Router, m *mizan.Mizan, opt Options) {
	if opt.Authenticate == nil {
		panic("mizan/api: Options.Authenticate is required")
	}
	if opt.Prefix == "" {
		opt.Prefix = "/v1"
	}
	if len(opt.FinanceRoles) == 0 {
		opt.FinanceRoles = []string{"finance", "admin"}
	}
	a := &API{m: m}
	p := strings.TrimRight(opt.Prefix, "/")

	// Gateways redirect the payer here and may call it more than once; the service verifies server-side.
	r.Route("GET "+p+"/gateway-callbacks/{gateway}", a.callback)
	r.Route("POST "+p+"/gateway-callbacks/{gateway}", a.callback)

	me := r.Group(opt.Authenticate)
	a.mountCustomer(me, p)

	fin := r.Group(opt.Authenticate, httpx.RequireRoles(opt.FinanceRoles...))
	a.mountAccounting(fin, p)
	a.mountPayments(fin, p)
	a.mountInvoices(fin, p)
	a.mountWallets(fin, p)
	a.mountReconcile(fin, p)
	a.mountTreasury(fin, p)
	a.mountPayroll(fin, p)
	a.mountPayrollFiles(fin, p)
	a.mountPayout(fin, p)
	a.mountEInvoice(fin, p)
	a.mountCoupons(fin, p)
	a.mountSubscriptions(fin, p)
	a.mountFixedAssets(fin, p)
	a.mountFixedAssets2(fin, p)
	a.mountInventory(fin, p)
	a.mountAppropriation(fin, p)
	a.mountVAT(fin, p)
	a.mountVATData(fin, p)
	a.mountIncomeTax(fin, p)
	a.mountStatements(fin, p)
	a.mountApprovals(fin, p)
}

// ---- helpers ----

func actorOf(ctx context.Context) string {
	if c, ok := auth.ClaimsFrom(ctx); ok {
		return c.Subject
	}
	return "unknown"
}

func idemKey(r *http.Request) (string, error) {
	k := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if k == "" || len(k) > 120 {
		return "", apperr.New(apperr.KindInvalid, "idempotency_key_required", "the Idempotency-Key header (1-120 characters) is required")
	}
	return k, nil
}

func pathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue(name))
	if err != nil {
		return uuid.Nil, apperr.Invalid("invalid " + name)
	}
	return id, nil
}

// parseDate accepts 2026-11-09 (Gregorian) and 1405/08/18 (Jalali).
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' })
	if len(parts) == 3 {
		var n [3]int
		good := true
		for i, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil {
				good = false
				break
			}
			n[i] = v
		}
		if good {
			if t, err := (jalali.Date{Year: n[0], Month: n[1], Day: n[2]}).Time(time.UTC); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, apperr.Invalid("dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
}

func optDate(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	t, err := parseDate(v)
	if err != nil {
		return nil, apperr.InvalidField(name, "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	return &t, nil
}

func jdate(t time.Time) string {
	if d, err := jalali.FromTime(t); err == nil {
		return d.String()
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

func created(w http.ResponseWriter, v any) error { httpx.JSON(w, http.StatusCreated, v); return nil }
func ok(w http.ResponseWriter, v any) error      { httpx.JSON(w, http.StatusOK, v); return nil }
func returnURL(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
