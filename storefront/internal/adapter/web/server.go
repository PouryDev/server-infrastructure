// Package web renders SSR pages and translates HTTP requests into application calls.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/care"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"github.com/PouryDev/storefront/internal/usecase/identity"
	"github.com/PouryDev/storefront/internal/usecase/syndication"

	"github.com/google/uuid"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

//go:embed templates/*.html assets/*
var files embed.FS

type Server struct {
	Care *care.Service

	Feed        *syndication.Service
	TorobVerify func(string, string) error

	assetVersion string
	uploadSlots  chan struct{}

	Ready func(context.Context) error

	App             *commerce.Service
	Auth            *identity.Service
	Origin, Uploads string
	GapchiSecret    string
	Demo            bool
	templates       *template.Template
}
type category struct {
	Name, URL, Icon string
	Count           int
}
type page struct {
	Reviews       []shop.Review
	ReturnRequest shop.ReturnRequest

	ContentPage     shop.ContentPage
	SelectedVariant shop.Variant

	AssetVersion                              string
	CustomerPage, NextPage, PreviousPage      int
	HasNext                                   bool
	AccountView, CollectionID, CollectionName string
	Addresses                                 []shop.SavedAddress
	Summary                                   shop.CustomerSummary
	SelectedOrder                             shop.Order
	CollectionRows                            map[string]commerce.HomeRail
	CategoryRows                              []commerce.HomeRail
	Offers                                    []commerce.Offer
	MinPrice, MaxPrice                        int64
	InStock                                   bool
	Sort, AllCategoryURL                      string
	Preview                                   *themePreview
	Period                                    int

	Categories []category

	Design shop.Design

	Title, Description, Path, View, Query, Category, Notice, Nonce string
	Settings                                                       shop.Settings
	Theme                                                          shop.Theme
	User                                                           shop.User
	Products                                                       []shop.Product
	Product                                                        shop.Product
	Articles                                                       []shop.Article
	Article                                                        shop.Article
	Orders                                                         []shop.Order
	Themes                                                         []shop.Theme
	Analytics                                                      shop.Analytics
	Boot                                                           template.JS
	Schema                                                         template.JS
	Demo                                                           bool
	CSRF                                                           string
}

func money(n int64) string {
	n /= 10
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return fa(s)
}
func fa(s string) string {
	return strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹", ",", "٬").Replace(s)
}
func accentInk(raw string) string {
	c := iconColor(raw)
	if int(c.R)*299+int(c.G)*587+int(c.B)*114 > 150000 {
		return "#172520"
	}
	return "#ffffff"
}
func New(s Server) *Server {
	hash := sha256.New()
	for _, dir := range []string{"assets", "templates"} {
		entries, _ := files.ReadDir(dir)
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".html") {
				raw, _ := files.ReadFile(dir + "/" + name)
				_, _ = hash.Write([]byte(name))
				_, _ = hash.Write(raw)
			}
		}
	}
	s.assetVersion = hex.EncodeToString(hash.Sum(nil))[:16]
	s.uploadSlots = make(chan struct{}, 2)
	s.templates = template.Must(template.New("pages").Funcs(template.FuncMap{"stockText": func(p shop.Product) string {
		count := 0
		for _, v := range p.Variants {
			count += v.Stock
		}
		if count > 0 && count <= 3 {
			return fa(fmt.Sprintf("فقط %d عدد موجود", count))
		}
		return "موجود و قابل سفارش"
	}, "lineTotal": func(l shop.OrderLine) int64 { return l.Price * int64(l.Quantity) }, "money": money, "accentInk": accentInk,
		"copy": func(values map[string]string, key, fallback string) string {
			if v, ok := values[key]; ok {
				return v
			}
			return fallback
		}, "fa": fa, "first": defaultVariant, "status": status, "short": func(s string) string {
			if len(s) > 8 {
				return strings.ToUpper(s[:8])
			}
			return s
		}, "date": func(t time.Time) template.HTML {
			return template.HTML(`<time datetime="` + t.Format(time.RFC3339) + `">` + fa(t.Format("2006/01/02")) + `</time>`)
		}, "add": func(a, b int) int { return a + b }, "divide": func(a, b int64) int64 {
			if b == 0 {
				return 0
			}
			return a / b
		}}).ParseFS(files, "templates/*.html"))
	return &s
}
func status(v string) string {
	m := map[string]string{"requested": "درخواست ثبت‌شده", "approved": "بازگشت تأییدشده", "rejected": "ردشده", "received": "کالا دریافت شد", "closed": "رسیدگی بسته شد", "canceled": "لغوشده", "cancel_pending": "در حال بررسی لغو", "refunded": "بازپرداخت‌شده", "awaiting_payment": "در انتظار پرداخت", "processing": "در حال آماده‌سازی", "shipped": "ارسال‌شده", "ready_for_pickup": "آمادهٔ تحویل حضوری", "completed": "تحویل‌شده", "expired": "مهلت پرداخت تمام شده", "paid": "پرداخت‌شده", "pending": "پرداخت‌نشده", "payment_review": "نیازمند بررسی پرداخت"}
	if s, ok := m[v]; ok {
		return s
	}
	return v
}
func (s *Server) current(r *http.Request) (shop.User, string) {
	c, e := r.Cookie("shop_session")
	if e != nil {
		return shop.User{}, ""
	}
	u, csrf, e := s.Auth.Current(r.Context(), c.Value)
	if e != nil {
		return shop.User{}, ""
	}
	return u, csrf
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, e error) {
	code := 400
	if errors.Is(e, shop.ErrForbidden) {
		code = 403
	}
	if errors.Is(e, shop.ErrNotFound) {
		code = 404
	}
	if errors.Is(e, shop.ErrConflict) {
		code = 409
	}
	slog.Warn("storefront request rejected", "kind", code)

	message := e.Error()
	safe := false
	for _, r := range message {
		if r >= 0x0600 && r <= 0x06ff {
			safe = true
			break
		}
	}
	if !safe {
		slog.Error("storefront internal request failure", "error", e)
		message = "انجام درخواست ممکن نشد؛ دوباره تلاش کنید."
		code = 503
	}
	jsonOut(w, code, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return shop.ErrInvalid
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return shop.ErrInvalid
	}
	return nil
}
func (s *Server) protected(fn func(http.ResponseWriter, *http.Request, shop.User), manage bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, csrf := s.current(r)
		if u.ID == "" {
			jsonOut(w, 401, map[string]string{"error": "برای ادامه وارد حساب خود شوید"})
			return
		}
		if manage && !u.CanManage() {
			failure(w, shop.ErrForbidden)
			return
		}
		if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
			failure(w, shop.ErrForbidden)
			return
		}
		fn(w, r, u)
	}
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	s.careRoutes(m)
	s.torobRoutes(m)
	s.deliveryRoutes(m)
	s.customerRoutes(m)
	s.merchandisingRoutes(m)
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.Ready != nil {
			if e := s.Ready(r.Context()); e != nil {
				jsonOut(w, 503, map[string]string{"status": "unavailable"})
				return
			}
		}
		jsonOut(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	m.Handle("GET /assets/", http.FileServerFS(files))
	m.HandleFunc("GET /manifest.webmanifest", s.manifest)
	m.HandleFunc("GET /sw.js", s.serviceWorker)
	m.HandleFunc("GET /offline.html", s.offline)
	m.HandleFunc("GET /app-icon.png", s.appIcon)
	m.HandleFunc("GET /", s.render)
	m.HandleFunc("GET /sitemap.xml", s.sitemap)
	m.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "User-agent: *\nAllow: /\nDisallow: /seller\nDisallow: /admin\nDisallow: /account\nDisallow: /checkout\nDisallow: /api/\nSitemap: "+s.Origin+"/sitemap.xml\n")
	})
	m.HandleFunc("GET /api/support/identity", s.protected(s.supportIdentity, false))
	m.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		u, c := s.current(r)
		jsonOut(w, 200, map[string]any{"user": u, "csrf": c, "demo": s.Demo})
	})
	m.HandleFunc("POST /api/auth/otp", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Phone string `json:"phone"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		code, e := s.Auth.RequestOTP(r.Context(), in.Phone, r.Header.Get("X-Real-IP"))
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]string{"message": "کد ورود ارسال شد", "demoCode": code})
	})
	m.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Phone    string `json:"phone"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		u, t, c, e := s.Auth.Authenticate(r.Context(), in.Phone, in.Password, in.Code, r.Header.Get("X-Real-IP"))
		if e != nil {
			failure(w, e)
			return
		}
		secure := strings.HasPrefix(s.Origin, "https:")
		http.SetCookie(w, &http.Cookie{Name: "shop_session", Value: t, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
		jsonOut(w, 200, map[string]any{"user": u, "csrf": c})
	})
	m.HandleFunc("POST /api/auth/logout", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		c, _ := r.Cookie("shop_session")
		if e := s.Auth.Logout(r.Context(), c.Value); e != nil {
			failure(w, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "shop_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("POST /api/auth/password", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Password string `json:"password"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.Auth.Password(r.Context(), u, in.Password); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("GET /api/products", func(w http.ResponseWriter, r *http.Request) {
		ps, e := s.App.Products(r.Context())
		if e != nil {
			failure(w, e)
			return
		}
		out := []shop.Product{}
		for _, p := range ps {
			if p.Active {
				out = append(out, p)
			}
		}
		jsonOut(w, 200, out)
	})
	m.HandleFunc("POST /api/quote", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Lines    []shop.CartLine        `json:"lines"`
			Code     string                 `json:"code"`
			Delivery shop.DeliverySelection `json:"delivery"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		q, e := s.App.QuoteDelivery(r.Context(), u, in.Lines, in.Code, in.Delivery)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, q)
	}, false))
	m.HandleFunc("POST /api/checkout", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in commerce.Checkout
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		in.ClientIP = r.Header.Get("X-Real-IP")
		o, next, e := s.App.Checkout(r.Context(), u, in)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 201, map[string]any{"order": o, "redirect": next})
	}, false))
	m.HandleFunc("POST /api/uploads", s.protected(s.upload, false))
	m.HandleFunc("GET /api/uploads/{id}", s.protected(s.download, false))
	m.HandleFunc("POST /api/seller/media", s.protected(s.mediaUpload, true))
	m.HandleFunc("GET /media/{id}", s.media)
	m.HandleFunc("GET /api/orders", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		o, e := s.App.Orders(r.Context(), u)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, o)
	}, false))
	m.HandleFunc("POST /api/orders/{id}/cancel", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if e := s.App.CancelOrder(r.Context(), u, r.PathValue("id")); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, false))
	m.HandleFunc("POST /api/seller/settings", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in shop.Settings
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.App.SaveSettings(r.Context(), u, in); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("POST /api/seller/products", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var p shop.Product
		if e := decode(w, r, &p); e != nil {
			failure(w, e)
			return
		}
		if e := s.App.SaveProduct(r.Context(), u, p); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("GET /api/seller/analytics.csv", s.protected(s.analyticsCSV, true))
	m.HandleFunc("GET /api/seller/campaigns", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		ds, e := s.App.Campaigns(r.Context(), u)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, ds)
	}, true))
	m.HandleFunc("POST /api/seller/campaigns", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var d shop.Discount
		if e := decode(w, r, &d); e != nil {
			failure(w, e)
			return
		}
		if e := s.App.SaveCampaign(r.Context(), u, d); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("POST /api/seller/orders/{id}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var in struct {
			Status string `json:"status"`
		}
		if e := decode(w, r, &in); e != nil {
			failure(w, e)
			return
		}
		if e := s.App.UpdateOrder(r.Context(), u, r.PathValue("id"), in.Status); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("GET /api/seller/articles", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		as, e := s.App.Articles(r.Context())
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, as)
	}, true))
	m.HandleFunc("POST /api/seller/articles", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		var a shop.Article
		if e := decode(w, r, &a); e != nil {
			failure(w, e)
			return
		}
		settings, e := s.App.Settings(r.Context())
		if e != nil || !settings.Modules["blog"] {
			failure(w, shop.ErrForbidden)
			return
		}
		if a.ID == "" {
			a.ID = uuid.NewString()
		}
		if a.Title == "" || a.Slug == "" || len(a.Body) > 100000 {
			failure(w, shop.ErrInvalid)
			return
		}
		if a.PublishedAt.IsZero() {
			a.PublishedAt = time.Now()
		}
		if e := s.App.SaveArticle(r.Context(), u, a); e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}, true))
	m.HandleFunc("POST /api/events", func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.App.Settings(r.Context())
		if err != nil || !settings.Modules["analytics"] {
			w.WriteHeader(404)
			return
		}
		var e shop.Event
		if err := decode(w, r, &e); err != nil {
			failure(w, err)
			return
		}
		if !slices.Contains([]string{"page_view", "product_view", "add_to_cart", "begin_checkout"}, e.Kind) || len(e.Session) > 80 || len(e.Campaign) > 100 || len(e.Source) > 100 || len(e.ProductID) > 100 {
			failure(w, shop.ErrInvalid)
			return
		}
		if err := s.App.Track(r.Context(), e); err != nil {
			failure(w, err)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /payment/demo/{id}", s.demoPayment)
	m.HandleFunc("GET /payment/redirect/{id}", s.paymentRedirect)
	m.HandleFunc("POST /api/payment/demo/{id}", s.protected(func(w http.ResponseWriter, r *http.Request, u shop.User) {
		if !s.Demo {
			http.NotFound(w, r)
			return
		}
		o, e := s.App.PaymentOrder(r.Context(), r.PathValue("id"))
		if e != nil || o.UserID != u.ID || o.Gateway != "demo" {
			failure(w, shop.ErrForbidden)
			return
		}
		o, e = s.App.Verify(r.Context(), o.ID)
		if e != nil {
			failure(w, e)
			return
		}
		jsonOut(w, 200, map[string]string{"redirect": "/account?paid=" + o.ID})
	}, false))
	m.HandleFunc("GET /payment/callback/{id}", s.callback)
	m.HandleFunc("POST /payment/callback/{id}", s.callback)
	expected, _ := url.Parse(s.Origin)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src https:; form-action 'self' https:; frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		if r.URL.Path != "/healthz" && r.Host != expected.Host {
			http.Error(w, "Unexpected host", http.StatusForbidden)
			return
		}
		if r.URL.Path != torobPath && !strings.HasPrefix(r.URL.Path, "/payment/callback/") && r.Method != "GET" && r.Method != "HEAD" && (r.Header.Get("Origin") != s.Origin || r.Header.Get("Sec-Fetch-Site") == "cross-site") {
			failure(w, shop.ErrForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/seller") || strings.HasPrefix(r.URL.Path, "/admin") || strings.HasPrefix(r.URL.Path, "/account") || strings.HasPrefix(r.URL.Path, "/checkout") || strings.HasPrefix(r.URL.Path, "/payment/") {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		m.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request, u shop.User) {
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		jsonOut(w, 429, map[string]string{"error": "بارگذاری‌ها در حال پردازش‌اند؛ کمی بعد تلاش کنید"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		failure(w, shop.ErrInvalid)
		return
	}
	defer r.MultipartForm.RemoveAll()
	p, e := s.App.Product(r.Context(), r.FormValue("productId"))
	if e != nil {
		failure(w, e)
		return
	}
	settings, e := s.App.Settings(r.Context())
	if e != nil || !settings.Modules["attachments"] {
		failure(w, shop.ErrForbidden)
		return
	}
	if _, e = p.Variant(r.FormValue("variantId")); e != nil {
		failure(w, e)
		return
	}
	var rule shop.AttachmentRule
	for _, a := range p.Attachments {
		if a.ID == r.FormValue("ruleId") {
			rule = a
		}
	}
	if rule.ID == "" {
		failure(w, shop.ErrInvalid)
		return
	}
	f, h, e := r.FormFile("file")
	if e != nil {
		failure(w, shop.ErrInvalid)
		return
	}
	defer f.Close()
	if h.Size > rule.MaxBytes {
		failure(w, fmt.Errorf("%w: حجم فایل بیش از حد مجاز است", shop.ErrInvalid))
		return
	}
	raw, e := io.ReadAll(io.LimitReader(f, rule.MaxBytes+1))
	if e != nil || int64(len(raw)) > rule.MaxBytes {
		failure(w, shop.ErrInvalid)
		return
	}
	mime := http.DetectContentType(raw)
	if !slices.Contains(rule.Types, mime) {
		failure(w, fmt.Errorf("%w: فرمت فایل مجاز نیست", shop.ErrInvalid))
		return
	}
	id := uuid.NewString()
	path := filepath.Join(s.Uploads, id)
	if e = os.WriteFile(path, raw, 0600); e != nil {
		failure(w, fmt.Errorf("ذخیره فایل ممکن نشد؛ دوباره تلاش کنید"))
		return
	}
	a := shop.Upload{ID: id, UserID: u.ID, ProductID: p.ID, VariantID: r.FormValue("variantId"), RuleID: rule.ID, Name: filepath.Base(h.Filename), MIME: mime, Size: int64(len(raw)), CreatedAt: time.Now()}
	if e = s.App.SaveUpload(r.Context(), u, a); e != nil {
		_ = os.Remove(path)
		failure(w, e)
		return
	}
	jsonOut(w, 201, a)
}
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if e := r.ParseForm(); e != nil {
		http.Error(w, "Invalid callback", http.StatusBadRequest)
		return
	}
	o, e := s.App.PaymentOrder(r.Context(), r.PathValue("id"))
	if e != nil {
		http.NotFound(w, r)
		return
	}
	authority := r.FormValue("Authority")
	if authority == "" {
		authority = r.FormValue("trackId")
	}
	if authority != "" && authority != o.Authority {
		http.Error(w, "Invalid payment binding", http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for key := range r.Form {
		values[key] = r.FormValue(key)
	}
	values["ip"] = r.Header.Get("X-Real-IP")
	if err := s.App.AcceptCallback(r.Context(), o.ID, values); err != nil {
		http.Redirect(w, r, "/account?payment=unverified", http.StatusSeeOther)
		return
	}
	_, e = s.App.Verify(r.Context(), o.ID)
	if e != nil {
		http.Redirect(w, r, "/account?payment=unverified", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account?paid="+o.ID, http.StatusSeeOther)
}

func (s *Server) paymentRedirect(w http.ResponseWriter, r *http.Request) {
	u, c := s.current(r)
	o, e := s.App.PaymentOrder(r.Context(), r.PathValue("id"))
	if e != nil || o.UserID != u.ID || o.Gateway != "tara" {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}
	target, fields, e := s.App.PaymentForm(r.Context(), u, o.ID)
	if e != nil {
		failure(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = c
	t := template.Must(template.New("tara").Parse(`<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><title>انتقال به تارا</title><link rel="stylesheet" href="/assets/style.css"><main class="container auth-single"><h1>پرداخت با تارا</h1><p>برای ادامه به درگاه امن تارا منتقل می‌شوید.</p><form method="post" action="{{.Target}}"><input type="hidden" name="username" value="{{.Username}}"><input type="hidden" name="token" value="{{.Token}}"><button class="btn full" type="submit">ادامه در تارا ↗</button></form><a href="/account">بازگشت به سفارش‌ها</a></main></html>`))
	_ = t.Execute(w, map[string]string{"Target": target, "Username": fields["username"], "Token": fields["token"]})
}
func (s *Server) demoPayment(w http.ResponseWriter, r *http.Request) {
	if !s.Demo {
		http.NotFound(w, r)
		return
	}
	u, c := s.current(r)
	o, e := s.App.PaymentOrder(r.Context(), r.PathValue("id"))
	if e != nil || o.UserID != u.ID || o.Gateway != "demo" {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}
	s.renderPage(w, r, page{Title: "پرداخت آزمایشی", View: "demo-payment", User: u, CSRF: c, Orders: []shop.Order{o}})
}

func (s *Server) supportIdentity(w http.ResponseWriter, r *http.Request, u shop.User) {
	settings, e := s.App.Settings(r.Context())
	if e != nil || !settings.Modules["support"] || s.GapchiSecret == "" {
		failure(w, shop.ErrNotFound)
		return
	}
	ts := time.Now().Unix()
	external := s.Origin + ":" + u.ID
	payload := fmt.Sprintf("%s:%s:%d", settings.GapchiChannel, external, ts)
	mac := hmac.New(sha256.New, []byte(s.GapchiSecret))
	_, _ = mac.Write([]byte(payload))
	jsonOut(w, 200, map[string]any{"external_id": external, "name": u.Name, "phone": u.Phone, "timestamp": ts, "signature": hex.EncodeToString(mac.Sum(nil))})
}
