package web

import (
	"encoding/json"
	"encoding/xml"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) render(w http.ResponseWriter, r *http.Request) {
	p := page{Path: r.URL.Path, View: "home", Title: "فروشگاه آنلاین", Description: "اشیای زیبا و کاربردی برای خانه و زندگی؛ انتخابی از محصولات باکیفیت و طراحی ماندگار.", Query: r.URL.Query().Get("q"), Category: r.URL.Query().Get("category"), CollectionID: r.URL.Query().Get("collection")}
	var e error
	p.User, p.CSRF = s.current(r)
	switch {
	case r.URL.Path == "/":
	case r.URL.Path == "/wishlist":
		p.View = "wishlist"
		p.Title = "انتخاب‌های ذخیره‌شده"
	case r.URL.Path == "/products":
		target := "/shop"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	case strings.HasPrefix(r.URL.Path, "/collections/"):
		p.View = "catalog"
		p.CollectionID = strings.TrimPrefix(r.URL.Path, "/collections/")
	case r.URL.Path == "/shop":
		p.View = "catalog"
		p.Title = "همه محصولات"
	case strings.HasPrefix(r.URL.Path, "/products/"):
		slug := strings.TrimPrefix(r.URL.Path, "/products/")
		p.Product, e = s.App.ProductBySlug(r.Context(), slug)
		if e == shop.ErrNotFound {
			if target, err := s.App.Redirect(r.Context(), "product", slug); err == nil && target != slug {
				http.Redirect(w, r, "/products/"+target, http.StatusMovedPermanently)
				return
			}
		}
		if e != nil || !p.Product.Active {
			http.NotFound(w, r)
			return
		}
		p.View = "product"
		p.Title = p.Product.Name
		p.Description = p.Product.Description
	case r.URL.Path == "/blog":
		p.View = "blog"
		p.Title = "یادداشت‌ها"
	case strings.HasPrefix(r.URL.Path, "/blog/"):
		p.View = "article"
		articles, err := s.App.Articles(r.Context())
		if err != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, a := range articles {
			if a.Slug == strings.TrimPrefix(r.URL.Path, "/blog/") && a.Published && !a.PublishedAt.After(time.Now()) {
				p.Article = a
			}
		}
		if p.Article.ID == "" {
			slug := strings.TrimPrefix(r.URL.Path, "/blog/")
			if target, err := s.App.Redirect(r.Context(), "article", slug); err == nil && target != slug {
				http.Redirect(w, r, "/blog/"+target, http.StatusMovedPermanently)
				return
			}
			http.NotFound(w, r)
			return
		}
		p.Title = p.Article.Title
		p.Description = p.Article.Excerpt
	case r.URL.Path == "/cart":
		p.View = "cart"
		p.Title = "سبد خرید شما"
	case r.URL.Path == "/checkout":
		p.View = "checkout"
		p.Title = "تکمیل سفارش"
		if p.User.ID != "" {
			p.Addresses, e = s.App.Addresses(r.Context(), p.User)
			if e != nil {
				http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
				return
			}
		}
	case r.URL.Path == "/login":
		p.View = "login"
		p.Title = "ورود یا ساخت حساب"
	case r.URL.Path == "/account" || strings.HasPrefix(r.URL.Path, "/account/"):
		if p.User.ID == "" {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		p.View = "account"
		p.Title = "حساب من"
		p.CustomerPage, _ = strconv.Atoi(r.URL.Query().Get("page"))
		if p.CustomerPage < 1 || p.CustomerPage > 10000 {
			p.CustomerPage = 1
		}
		p.NextPage = p.CustomerPage + 1
		p.PreviousPage = p.CustomerPage - 1
		p.Orders, e = s.App.CustomerOrderPage(r.Context(), p.User, p.CustomerPage)
		if len(p.Orders) > 25 {
			p.HasNext = true
			p.Orders = p.Orders[:25]
		}
		if e != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		p.AccountView = strings.TrimPrefix(r.URL.Path, "/account")
		if p.AccountView == "" {
			p.AccountView = "overview"
		} else {
			p.AccountView = strings.TrimPrefix(p.AccountView, "/")
		}
		switch p.AccountView {
		case "overview", "orders", "addresses", "profile", "security":
		default:
			if !strings.HasPrefix(p.AccountView, "orders/") {
				http.NotFound(w, r)
				return
			}
			id := strings.TrimPrefix(p.AccountView, "orders/")
			p.SelectedOrder, e = s.App.CustomerOrder(r.Context(), p.User, id)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			if s.Care != nil {
				p.ReturnRequest, e = s.Care.OrderReturn(r.Context(), p.User, id)
				if e != nil && e != shop.ErrNotFound {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			p.AccountView = "order"
		}
		if r.URL.Query().Get("saved") == "1" {
			p.Notice = "اطلاعات شما با موفقیت ذخیره شد."
		}
		p.Summary, e = s.App.CustomerSummary(r.Context(), p.User)
		if e != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		p.Addresses, e = s.App.Addresses(r.Context(), p.User)
		if e != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Query().Get("paid") != "" {
			p.Notice = "پرداخت تأیید شد. سفارش شما با موفقیت ثبت شده است."
		}
		if r.URL.Query().Get("payment") == "unverified" {
			p.Notice = "پرداخت هنوز تأیید نشده است. در صورت کسر وجه، از پشتیبانی پیگیری کنید؛ دوباره پرداخت نکنید."
		}
	case r.URL.Path == "/seller" || r.URL.Path == "/admin":
		if p.User.ID == "" {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
		if !p.User.CanManage() || r.URL.Path == "/admin" && p.User.Role != "admin" {
			http.Error(w, "دسترسی به این بخش ندارید", http.StatusForbidden)
			return
		}
		p.View = "seller"
		p.Title = "مدیریت فروشگاه"
		if r.URL.Path == "/admin" {
			p.View = "admin"
			p.Title = "پیکربندی فروشگاه"
			p.Themes = shop.Themes()
		}
		p.Orders, e = s.App.Orders(r.Context(), p.User)
		if e != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
		p.Period, _ = strconv.Atoi(r.URL.Query().Get("days"))
		if p.Period != 7 && p.Period != 30 && p.Period != 90 {
			p.Period = 30
		}
		p.Analytics, e = s.App.Analytics(r.Context(), p.User, time.Now().AddDate(0, 0, -p.Period))
		if e != nil {
			http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
			return
		}
	case strings.HasPrefix(r.URL.Path, "/pages/"):
		p.View = "content"
	case r.URL.Path == "/about":
		p.View = "about"
		p.Title = "درباره هِرا"
	default:
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, r, p)
}
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, p page) {
	settings, e := s.App.Settings(r.Context())
	if e != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	if p.View == "wishlist" && !settings.Modules["wishlist"] {
		http.NotFound(w, r)
		return
	}
	if (p.View == "blog" || p.View == "article") && !settings.Modules["blog"] {
		http.NotFound(w, r)
		return
	}
	if settings.Modules["support"] && settings.GapchiURL != "" {
		u, err := url.Parse(settings.GapchiURL)
		if err == nil && u.Scheme == "https" && u.Host != "" {
			base := u.Scheme + "://" + u.Host
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' "+base+"; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src "+base+"; form-action 'self' https:; frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		}
	}
	if settings.Content == nil {
		settings.Content = map[string]string{}
	}
	for key, value := range shop.DefaultContent() {
		if _, ok := settings.Content[key]; !ok {
			settings.Content[key] = value
		}
	}
	if p.View == "content" {
		for _, content := range settings.Pages {
			if content.Slug == strings.TrimPrefix(r.URL.Path, "/pages/") && content.Published {
				p.ContentPage = content
				p.Title = content.Title
				p.Description = content.Description
			}
		}
		if p.ContentPage.Slug == "" {
			http.NotFound(w, r)
			return
		}
	}
	if p.View == "product" {
		if s.Care != nil {
			p.Reviews, e = s.Care.Reviews(r.Context(), p.Product.ID, shop.User{})
			if e != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		p.SelectedVariant = defaultVariant(p.Product)
		if id := r.URL.Query().Get("variant"); id != "" {
			v, err := p.Product.Variant(id)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			p.SelectedVariant = v
		}
	}
	if p.View == "home" {
		p.Description = settings.Tagline
	}
	if p.View == "about" {
		p.Title = "دربارهٔ " + settings.Name
	}
	p.Settings = settings
	p.AssetVersion = s.assetVersion
	if p.User.ID != "" {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Cookie")
	}
	p.Demo = s.Demo
	p.Path = r.URL.Path
	s.applyThemePreview(r, &p)
	if p.Preview != nil {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	p.Products, e = s.App.Products(r.Context())
	if e != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	categoryCounts := map[string]int{}
	for _, product := range p.Products {
		if product.Active {
			categoryCounts[product.Category]++
		}
	}
	names := []string{}
	for name := range categoryCounts {
		names = append(names, name)
	}
	sort.Strings(names)
	icons := []string{"◈", "◉", "✳", "▧", "◇"}
	categoryQuery := r.URL.Query()
	if p.CollectionID != "" {
		categoryQuery.Set("collection", p.CollectionID)
	}
	categoryQuery.Del("category")
	p.AllCategoryURL = "/shop?" + categoryQuery.Encode()
	for i, name := range names {
		categoryQuery.Set("category", name)
		p.Categories = append(p.Categories, category{Name: name, URL: "/shop?" + categoryQuery.Encode(), Count: categoryCounts[name], Icon: icons[i%len(icons)]})
	}
	if p.CollectionID != "" && p.View == "catalog" {
		found := false
		for _, c := range settings.Collections {
			if c.ID == p.CollectionID && c.Enabled {
				p.Products = c.Select(p.Products, 0)
				p.Title = c.Name
				p.CollectionName = c.Name
				p.Description = c.Description
				found = true
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	if p.View == "home" {
		s.homeRows(&p)
		p.Offers, e = s.App.Offers(r.Context(), p.User)
		if e != nil {
			slog.Warn("storefront offers unavailable")
			p.Offers = nil
		}
	}
	if p.View != "seller" && p.View != "admin" {
		p.filterProducts(r.URL.Query())
	}
	p.Articles, e = s.App.Articles(r.Context())
	if e != nil {
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}
	if p.View != "seller" && p.View != "admin" {
		filtered := []shop.Article{}
		for _, a := range p.Articles {
			if a.Published && !a.PublishedAt.After(time.Now()) {
				filtered = append(filtered, a)
			}
		}
		p.Articles = filtered
	}
	public := settings.Public()
	if !p.User.CanManage() {
		public.Pages = []shop.ContentPage{}
		for _, content := range settings.Pages {
			if content.Published {
				public.Pages = append(public.Pages, content)
			}
		}
		public.Collections = []shop.Collection{}
		for _, c := range settings.Collections {
			if c.Enabled {
				public.Collections = append(public.Collections, c)
			}
		}
		public.Sections = []shop.Section{}
		for _, section := range settings.Sections {
			if section.Enabled {
				public.Sections = append(public.Sections, section)
			}
		}
	}
	if p.User.Role == "admin" {
		public.Theme = settings.Theme
	}
	b, e := json.Marshal(map[string]any{"settings": public, "user": p.User, "csrf": p.CSRF, "products": p.Products, "demo": s.Demo, "product": p.Product, "selectedVariant": p.SelectedVariant, "view": p.View, "themePreview": p.Preview, "addresses": p.Addresses})
	if e != nil {
		http.Error(w, "Rendering error", http.StatusInternalServerError)
		return
	}
	p.Boot = template.JS(b) // JSON encoder escapes HTML delimiters and script terminators.
	schema := map[string]any{"@context": "https://schema.org", "@type": "Organization", "name": settings.Name, "url": s.Origin}
	if p.View == "product" {
		schema = productSchema(s.Origin, p.Product)
	}

	raw, _ := json.Marshal(schema)
	p.Schema = template.JS(raw)
	p.Nonce = s.Origin
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if e = s.templates.ExecuteTemplate(w, "page", p); e != nil {
		http.Error(w, "Rendering error", http.StatusInternalServerError)
	}
}
func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Loc string `xml:"loc"`
	}
	out := struct {
		XMLName xml.Name `xml:"urlset"`
		NS      string   `xml:"xmlns,attr"`
		Items   []item   `xml:"url"`
	}{NS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, p := range []string{"/", "/shop", "/about"} {
		out.Items = append(out.Items, item{s.Origin + p})
	}
	ps, e := s.App.Products(r.Context())
	if e != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	for _, p := range ps {
		if p.Active {
			out.Items = append(out.Items, item{s.Origin + "/products/" + p.Slug})
		}
	}
	settings, e := s.App.Settings(r.Context())
	if e != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	for _, content := range settings.Pages {
		if content.Published {
			out.Items = append(out.Items, item{s.Origin + "/pages/" + content.Slug})
		}
	}
	for _, c := range settings.Collections {
		if c.Enabled {
			out.Items = append(out.Items, item{s.Origin + "/collections/" + c.ID})
		}
	}
	if settings.Modules["blog"] {
		as, e := s.App.Articles(r.Context())
		if e != nil {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return
		}
		out.Items = append(out.Items, item{s.Origin + "/blog"})
		for _, a := range as {
			if a.Published && !a.PublishedAt.After(time.Now()) {
				out.Items = append(out.Items, item{s.Origin + "/blog/" + a.Slug})
			}
		}
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(out)
}
