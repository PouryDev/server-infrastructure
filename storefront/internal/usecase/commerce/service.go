package commerce

import (
	"context"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/google/uuid"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

type Service struct {
	repo     Repository
	payments PaymentRegistry
	origin   string
}

func (s *Service) Settings(ctx context.Context) (shop.Settings, error) {
	return s.repo.Settings(ctx, false)
}

// Catalog collections are always arrays at the application boundary, including
// products stored before optional attachment rules were configured.
func catalogProduct(p shop.Product) shop.Product {
	if p.Variants == nil {
		p.Variants = []shop.Variant{}
	}
	if p.Attachments == nil {
		p.Attachments = []shop.AttachmentRule{}
	}
	return p
}
func (s *Service) Products(ctx context.Context) ([]shop.Product, error) {
	products, err := s.repo.Products(ctx)
	if err != nil {
		return nil, err
	}
	if products == nil {
		products = []shop.Product{}
	}
	for i := range products {
		products[i] = catalogProduct(products[i])
	}
	return products, nil
}
func (s *Service) Product(ctx context.Context, id string) (shop.Product, error) {
	p, err := s.repo.Product(ctx, id)
	return catalogProduct(p), err
}
func (s *Service) Articles(ctx context.Context) ([]shop.Article, error) { return s.repo.Articles(ctx) }
func (s *Service) SaveProduct(ctx context.Context, u shop.User, p shop.Product) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	for i := range p.Variants {
		if p.Variants[i].ID == "" {
			p.Variants[i].ID = uuid.NewString()
		}
	}
	if err := p.Validate(); err != nil {
		return err
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		if err := s.repo.SaveProduct(ctx, p); err != nil {
			return err
		}
		return s.repo.Audit(ctx, u.ID, "product.save", p.ID)
	})
}

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Service) SaveSettings(ctx context.Context, u shop.User, next shop.Settings) error {
	if err := next.ValidateContent(); err != nil {
		return err
	}
	if err := validateMerchandising(next); err != nil {
		return err
	}
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	for key, value := range next.Content {
		if len(key) > 50 || len(value) > 8000 {
			return shop.ErrInvalid
		}
	}
	if strings.TrimSpace(next.Name) == "" || len(next.Name) > 200 || !colorRE.MatchString(next.Accent) || next.Shipping < 0 || next.Shipping > 1_000_000_000_000 || next.FreeShipping < 0 || next.FreeShipping > 1_000_000_000_000_000 {
		return shop.ErrInvalid
	}
	if next.Logo != "" && !strings.HasPrefix(next.Logo, "/assets/") && !strings.HasPrefix(next.Logo, "/media/") {
		return fmt.Errorf("%w: آدرس لوگو باید از فایل‌های فروشگاه باشد", shop.ErrInvalid)
	}
	if next.GapchiURL != "" {
		v, e := url.Parse(next.GapchiURL)
		if e != nil || v.Scheme != "https" || v.Host == "" || v.User != nil || v.RawQuery != "" || v.Fragment != "" {
			return shop.ErrInvalid
		}
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		old, err := s.repo.Settings(ctx, true)
		if err != nil {
			return err
		}
		if next.Revision != old.Revision {
			return shop.ErrConflict
		}
		if u.Role != "admin" {
			next.Theme = old.Theme
			next.Modules = old.Modules
		}
		if _, ok := shop.FindTheme(next.Theme); !ok {
			return shop.ErrInvalid
		}
		seen := map[string]bool{}
		if len(next.Gateways) != len(old.Gateways) {
			return shop.ErrInvalid
		}
		for i := range next.Gateways {
			g := &next.Gateways[i]
			p, ok := s.payments.Provider(g.ID)
			if !ok || seen[g.ID] {
				return shop.ErrInvalid
			}
			seen[g.ID] = true
			foundDescriptor := false
			for _, existing := range old.Gateways {
				if existing.ID == g.ID {
					g.Name = existing.Name
					g.BNPL = existing.BNPL
					foundDescriptor = true
				}
			}
			if !foundDescriptor {
				return shop.ErrInvalid
			}
			if g.Policy.Mode != "all" && g.Policy.Mode != "blacklist" && g.Policy.Mode != "whitelist" {
				return shop.ErrInvalid
			}
			for _, before := range old.Gateways {
				if before.ID == g.ID && g.Credential == "" {
					g.Credential = before.Credential
				}
			}
			if g.Enabled {
				if validator, ok := p.(interface{ ValidateConfiguration(shop.Gateway) error }); ok {
					if e := validator.ValidateConfiguration(*g); e != nil {
						return e
					}
				}
			}
			if g.Enabled && (!p.Available() || g.Credential == "") {
				return fmt.Errorf("%w: اتصال %s هنوز آماده نیست", shop.ErrInvalid, g.Name)
			}
		}
		if next.Torob.ClearToken {
			next.Torob.WebhookToken = ""
		} else if next.Torob.WebhookToken == "" {
			next.Torob.WebhookToken = old.Torob.WebhookToken
		}
		next.Torob.ClearToken = false
		next.Revision++
		if err = s.repo.SaveSettings(ctx, next); err != nil {
			return err
		}
		return s.repo.Audit(ctx, u.ID, "settings.save", "store")
	})
}
func (s *Service) SaveCampaign(ctx context.Context, u shop.User, d shop.Discount) error {
	settings, e := s.Settings(ctx)
	if e != nil {
		return e
	}
	if !settings.Modules["discounts"] {
		return shop.ErrForbidden
	}
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	d.Code = strings.ToUpper(strings.TrimSpace(d.Code))
	if d.MinQuantity < 0 || d.BuyQuantity < 0 || d.FreeQuantity < 0 || (d.Kind == "buy_x_get_y" && (d.BuyQuantity < 1 || d.FreeQuantity < 1)) {
		return shop.ErrInvalid
	}
	if d.Name == "" || (!d.Automatic && d.Code == "") || !slices.Contains([]string{"fixed", "percent", "shipping", "buy_x_get_y"}, d.Kind) || d.Value < 0 || (d.Kind == "percent" && d.Value > 100) || d.Cap < 0 || d.MinSubtotal < 0 || d.Limit < 0 || d.PerCustomer < 0 || (!d.EndsAt.IsZero() && !d.EndsAt.After(d.StartsAt)) {
		return shop.ErrInvalid
	}
	for i, p := range d.Phones {
		v, e := shop.Phone(p)
		if e != nil {
			return e
		}
		d.Phones[i] = v
	}
	return s.repo.SaveCampaign(ctx, d)
}
func (s *Service) quote(ctx context.Context, u shop.User, lines []shop.CartLine, code string, locked bool, delivery shop.DeliverySelection) (shop.Quote, error) {
	q := shop.Quote{Lines: []shop.OrderLine{}, Campaigns: []string{}, Gateways: []shop.Gateway{}}
	if len(lines) == 0 || len(lines) > 50 {
		return q, fmt.Errorf("%w: سبد خرید خالی است", shop.ErrInvalid)
	}
	settings, err := s.repo.Settings(ctx, locked)
	if err != nil {
		return q, err
	}
	grams := 0
	seen := map[string]bool{}
	usedFiles := map[string]bool{}
	for _, l := range lines {
		if l.Quantity < 1 || l.Quantity > 100 || seen[l.VariantID] {
			return q, shop.ErrInvalid
		}
		seen[l.VariantID] = true
		p, err := s.repo.Product(ctx, l.ProductID)
		if err != nil {
			return q, err
		}
		if !p.Active {
			return q, fmt.Errorf("%w: محصول غیرفعال شده است", shop.ErrInvalid)
		}
		v, err := p.Variant(l.VariantID)
		if err != nil {
			return q, err
		}
		if v.Stock < l.Quantity {
			return q, fmt.Errorf("%w: موجودی %s کافی نیست", shop.ErrInvalid, p.Name)
		}
		rules := p.Attachments
		if !settings.Modules["attachments"] {
			rules = nil
		}
		counts := map[string]int{}
		for _, id := range l.Attachments {
			if usedFiles[id] {
				return q, shop.ErrInvalid
			}
			usedFiles[id] = true
			a, e := s.repo.Upload(ctx, id)
			if e != nil || a.UserID != u.ID || a.ProductID != p.ID || a.VariantID != v.ID {
				return q, shop.ErrForbidden
			}
			found := false
			for _, rule := range rules {
				if rule.ID == a.RuleID && slices.Contains(rule.Types, a.MIME) && a.Size <= rule.MaxBytes {
					found = true
				}
			}
			if !found {
				return q, shop.ErrInvalid
			}
			counts[a.RuleID]++
		}
		for _, rule := range rules {
			n := 1
			if rule.PerUnit {
				n = l.Quantity
			}
			if counts[rule.ID] < rule.Min*n || counts[rule.ID] > rule.Max*n {
				return q, fmt.Errorf("%w: تعداد فایل «%s» باید بین %d و %d باشد", shop.ErrInvalid, rule.Label, rule.Min*n, rule.Max*n)
			}
		}
		q.Lines = append(q.Lines, shop.OrderLine{Slug: p.Slug, ProductID: p.ID, VariantID: v.ID, Name: p.Name, Variant: v.Name, Price: v.Price, Quantity: l.Quantity, Attachments: l.Attachments, Rules: rules})
		q.Subtotal += v.Price * int64(l.Quantity)
		grams += p.WeightGrams * l.Quantity
	}
	q.DeliveryOptions, err = settings.DeliveryQuotes(delivery, q.Subtotal, grams, time.Now())
	if err != nil {
		return q, err
	}
	q.Delivery = q.DeliveryOptions[0]
	if delivery.MethodID != "" {
		found := false
		for _, option := range q.DeliveryOptions {
			if option.MethodID == delivery.MethodID {
				q.Delivery = option
				found = true
			}
		}
		if !found {
			return q, fmt.Errorf("%w: روش تحویل انتخابی برای این نشانی یا سبد در دسترس نیست", shop.ErrInvalid)
		}
	}
	q.Shipping = q.Delivery.Fee
	code = strings.ToUpper(strings.TrimSpace(code))
	matched := code == ""
	campaigns, err := s.repo.Campaigns(ctx, locked)
	if err != nil {
		return q, err
	}
	var picked []shop.Discount
	best := int64(0)
	var exclusive *shop.Discount
	if settings.Modules["discounts"] {
		for _, d := range campaigns {
			if !d.Automatic && d.Code != code {
				continue
			}
			uses, cu, prior, e := s.repo.Uses(ctx, d.ID, u.ID)
			if e != nil {
				return q, e
			}
			a := d.Amount(q, u.Phone, time.Now(), uses, cu, prior)
			if a <= 0 {
				continue
			}
			if d.Code == code {
				matched = true
			}
			if d.Stackable {
				picked = append(picked, d)
			} else if a > best {
				copy := d
				exclusive = &copy
				best = a
			}
		}
		sum := int64(0)
		for _, d := range picked {
			a, b, c, e := s.repo.Uses(ctx, d.ID, u.ID)
			if e != nil {
				return q, e
			}
			sum += d.Amount(q, u.Phone, time.Now(), a, b, c)
		}
		if exclusive != nil && best >= sum {
			q.Discount = best
			q.Campaigns = append(q.Campaigns, exclusive.ID)
		} else {
			q.Discount = sum
			for _, d := range picked {
				q.Campaigns = append(q.Campaigns, d.ID)
			}
		}
	}
	if !matched {
		return q, fmt.Errorf("%w: کد تخفیف معتبر نیست یا شرایط استفاده را ندارد", shop.ErrInvalid)
	}
	q.Discount = min(q.Discount, q.Subtotal+q.Shipping)
	q.Total = q.Subtotal + q.Shipping - q.Discount
	for _, g := range settings.Gateways {
		provider, ok := s.payments.Provider(g.ID)
		if !g.Enabled || g.Credential == "" || !ok || !provider.Available() {
			continue
		}
		allowed := true
		if g.BNPL {
			for _, l := range q.Lines {
				if !g.Policy.Allows(l.ProductID, l.VariantID) {
					allowed = false
				}
			}
		}
		if eligible, ok := provider.(interface {
			Eligibility(context.Context, shop.Gateway, int64) (bool, string, string, error)
		}); ok && allowed && !locked {
			can, title, description, err := eligible.Eligibility(ctx, g, q.Total)
			if err != nil || !can {
				allowed = false
			} else {
				if title != "" {
					g.Name = title
				}
				g.Description = description
			}
		}
		if allowed {
			g.Credential = ""
			q.Gateways = append(q.Gateways, g)
		}
	}
	if q.Total == 0 {
		q.Gateways = []shop.Gateway{{ID: "free", Name: "ثبت سفارش بدون پرداخت"}}
	}
	return q, nil
}
func (s *Service) Quote(ctx context.Context, u shop.User, lines []shop.CartLine, code string) (shop.Quote, error) {
	return s.quote(ctx, u, lines, code, false, shop.DeliverySelection{})
}

func (s *Service) QuoteDelivery(ctx context.Context, u shop.User, lines []shop.CartLine, code string, delivery shop.DeliverySelection) (shop.Quote, error) {
	return s.quote(ctx, u, lines, code, false, delivery)
}

type Checkout struct {
	Delivery     shop.DeliverySelection `json:"delivery"`
	CustomerNote string                 `json:"customerNote"`

	ClientIP string `json:"-"`

	Lines   []shop.CartLine `json:"lines"`
	Code    string          `json:"code"`
	Gateway string          `json:"gateway"`
	Address shop.Address    `json:"address"`
	Key     string          `json:"key"`
}

func (s *Service) Checkout(ctx context.Context, u shop.User, in Checkout) (shop.Order, string, error) {
	var out shop.Order
	if len(in.CustomerNote) > 2000 {
		return out, "", shop.ErrInvalid
	}
	if len(in.Key) < 16 || len(in.Key) > 100 {
		return out, "", shop.ErrInvalid
	}
	var e error
	in.Address, e = in.Address.Normalize()
	if e != nil {
		return out, "", e
	}

	existing, err := s.repo.OrderByKey(ctx, u.ID, in.Key)
	if err == nil {
		if existing.PaymentURL != "" && existing.Status == "awaiting_payment" {
			return existing, existing.PaymentURL, nil
		}
		return existing, "", fmt.Errorf("%w: سفارش قبلاً ثبت شده؛ آن را از پنل سفارش‌ها پیگیری کنید", shop.ErrConflict)
	}
	if err != shop.ErrNotFound {
		return out, "", err
	}
	err = s.repo.Atomic(ctx, func(ctx context.Context) error {
		q, e := s.quote(ctx, u, in.Lines, in.Code, true, shop.DeliverySelection{MethodID: in.Delivery.MethodID, Province: in.Address.Province, City: in.Address.City})
		if e != nil {
			return e
		}
		if !slices.ContainsFunc(q.Gateways, func(g shop.Gateway) bool { return g.ID == in.Gateway }) {
			return fmt.Errorf("%w: روش پرداخت برای این سبد فعال نیست", shop.ErrInvalid)
		}
		if q.Total > 0 && q.Total < 10000 {
			return fmt.Errorf("%w: مبلغ قابل پرداخت کمتر از حد مجاز است", shop.ErrInvalid)
		}
		for _, l := range q.Lines {
			if e = s.repo.Reserve(ctx, l.VariantID, l.Quantity); e != nil {
				return e
			}
		}
		out = shop.Order{CustomerNote: in.CustomerNote, ID: uuid.NewString(), UserID: u.ID, Phone: u.Phone, Status: "awaiting_payment", PaymentStatus: "pending", Gateway: in.Gateway, Quote: q, Address: in.Address, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(30 * time.Minute).UTC(), Key: in.Key}
		if q.Total == 0 {
			out.PaymentStatus = "paid"
			out.Status = "processing"
			out.Gateway = "free"
			out.Reference = "FREE-" + out.ID[:8]
			out.PaymentURL = "/account?paid=" + out.ID
		}
		savedSettings, configErr := s.repo.Settings(ctx, false)
		if configErr != nil {
			return configErr
		}
		for _, g := range savedSettings.Gateways {
			if g.ID == out.Gateway {
				out.GatewayConfig = g
			}
		}
		if e = s.repo.SaveOrder(ctx, out); e != nil {
			return e
		}
		for _, id := range q.Campaigns {
			if e = s.repo.Redeem(ctx, out.ID, id, u.ID); e != nil {
				return e
			}
		}
		for _, l := range q.Lines {
			for _, id := range l.Attachments {
				if e = s.repo.BindUpload(ctx, id, u.ID, out.ID); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if err != nil {
		return out, "", err
	}
	if out.Gateway == "free" {
		return out, out.PaymentURL, nil
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return out, "", err
	}
	gateway := out.GatewayConfig
	_ = settings
	provider, _ := s.payments.Provider(out.Gateway)
	res, err := provider.Request(ctx, gateway, PaymentRequest{OrderID: out.ID, Amount: out.Quote.Total, Phone: u.Phone, Shipping: out.Quote.Shipping, Discount: out.Quote.Discount, Origin: s.origin, IP: in.ClientIP, Callback: s.origin + "/payment/callback/" + out.ID, Lines: out.Quote.Lines})
	if err != nil {
		return out, "", fmt.Errorf("ارتباط با درگاه کامل نشد. سفارش %s ذخیره شد؛ از پنل سفارش‌ها پیگیری کنید", out.ID[:8])
	}
	out.Authority = res.Authority
	err = s.repo.Atomic(ctx, func(ctx context.Context) error {
		current, e := s.repo.Order(ctx, out.ID, true)
		if e != nil {
			return e
		}
		current.Authority = res.Authority
		current.PaymentURL = res.URL
		out = current
		return s.repo.SaveOrder(ctx, current)
	})
	return out, res.URL, err
}
func (s *Service) Verify(ctx context.Context, id string) (shop.Order, error) {
	o, e := s.repo.Order(ctx, id, false)
	if e != nil {
		return o, e
	}
	if o.PaymentStatus == "paid" {
		return o, nil
	}
	if o.Authority == "" {
		return o, shop.ErrConflict
	}
	settings, e := s.Settings(ctx)
	if e != nil {
		return o, e
	}
	g := o.GatewayConfig
	_ = settings
	p, ok := s.payments.Provider(o.Gateway)
	if !ok {
		return o, shop.ErrInvalid
	}
	ref, e := p.Verify(ctx, g, o)
	if e != nil {
		return o, e
	}
	e = s.repo.Atomic(ctx, func(ctx context.Context) error {
		current, e := s.repo.Order(ctx, id, true)
		if e != nil {
			return e
		}
		if current.PaymentStatus == "paid" {
			o = current
			return nil
		}
		expired := current.Status == "expired"
		current.PaymentStatus = "paid"
		current.Reference = ref
		previousStatus := current.Status
		current.Status = "processing"
		if previousStatus == "cancel_pending" {
			current.Status = "cancel_pending"
		}
		if expired {
			current.Status = "payment_review"
		}
		if e = s.repo.SaveOrder(ctx, current); e != nil {
			return e
		}
		o = current
		return s.repo.Audit(ctx, "gateway", "payment.verified", id)
	})
	return o, e
}
func (s *Service) Expire(ctx context.Context) error {
	ids, e := s.repo.Expired(ctx, time.Now())
	if e != nil {
		return e
	}
	for _, id := range ids {
		e = s.repo.Atomic(ctx, func(ctx context.Context) error {
			o, e := s.repo.Order(ctx, id, true)
			if e != nil {
				return e
			}
			if o.Status != "awaiting_payment" || o.PaymentStatus != "pending" {
				return nil
			}
			for _, l := range o.Quote.Lines {
				if e = s.repo.Release(ctx, l.VariantID, l.Quantity); e != nil {
					return e
				}
			}
			o.Status = "expired"
			return s.repo.SaveOrder(ctx, o)
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) Orders(ctx context.Context, u shop.User) ([]shop.Order, error) {
	id := u.ID
	if u.CanManage() {
		id = ""
	}
	return s.repo.Orders(ctx, id)
}
func (s *Service) UpdateOrder(ctx context.Context, u shop.User, id, status string) error {
	if !u.CanManage() {
		return shop.ErrForbidden
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		o, e := s.repo.Order(ctx, id, true)
		if e != nil {
			return e
		}
		allowed := map[string]string{"shipped": "completed", "ready_for_pickup": "completed"}
		if allowed[o.Status] != status || o.PaymentStatus != "paid" {
			return shop.ErrConflict
		}
		if status == "completed" {
			if delivery, ok := s.payments.Provider(o.Gateway); ok {
				if reporter, ok := delivery.(interface {
					Deliver(context.Context, shop.Gateway, shop.Order) error
				}); ok {
					if e := reporter.Deliver(ctx, o.GatewayConfig, o); e != nil {
						return e
					}
				}
			}
		}
		o.Status = status
		return s.repo.SaveOrder(ctx, o)
	})
}

func (s *Service) AcceptCallback(ctx context.Context, id string, values map[string]string) error {
	o, e := s.repo.Order(ctx, id, false)
	if e != nil {
		return e
	}
	p, ok := s.payments.Provider(o.Gateway)
	if !ok {
		return shop.ErrInvalid
	}
	callback, ok := p.(interface {
		Callback(shop.Order, map[string]string) (map[string]string, error)
	})
	if !ok {
		return shop.ErrInvalid
	}
	bound, e := callback.Callback(o, values)
	if e != nil {
		return e
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		current, e := s.repo.Order(ctx, id, true)
		if e != nil {
			return e
		}
		if current.PaymentStatus == "paid" {
			return nil
		}
		current.Callback = bound
		return s.repo.SaveOrder(ctx, current)
	})
}
func (s *Service) PaymentForm(ctx context.Context, u shop.User, id string) (string, map[string]string, error) {
	o, e := s.repo.Order(ctx, id, false)
	if e != nil {
		return "", nil, e
	}
	if u.ID != o.UserID {
		return "", nil, shop.ErrForbidden
	}
	p, ok := s.payments.Provider(o.Gateway)
	if !ok {
		return "", nil, shop.ErrInvalid
	}
	redirect, ok := p.(interface {
		Redirect(shop.Gateway, string) (string, map[string]string, error)
	})
	if !ok {
		return "", nil, shop.ErrInvalid
	}
	return redirect.Redirect(o.GatewayConfig, o.Authority)
}

// CancelOrder records intent before contacting the gateway; uncertain outcomes retain their reservation.
func (s *Service) CancelOrder(ctx context.Context, u shop.User, id string) error {
	var order shop.Order
	e := s.repo.Atomic(ctx, func(ctx context.Context) error {
		o, e := s.repo.Order(ctx, id, true)
		if e != nil {
			return e
		}
		if o.UserID != u.ID && !u.CanManage() {
			return shop.ErrForbidden
		}
		if o.Status == "canceled" {
			order = o
			return nil
		}
		if o.Status != "awaiting_payment" && o.Status != "processing" && o.Status != "cancel_pending" {
			return shop.ErrConflict
		}
		o.Status = "cancel_pending"
		order = o
		return s.repo.SaveOrder(ctx, o)
	})
	if e != nil || order.Status == "canceled" {
		return e
	}
	if order.Authority != "" && order.Gateway != "demo" {
		provider, ok := s.payments.Provider(order.Gateway)
		if !ok {
			return shop.ErrInvalid
		}
		cancel, ok := provider.(interface {
			Cancel(context.Context, shop.Gateway, shop.Order) error
		})
		if !ok {
			return fmt.Errorf("لغو این پرداخت نیازمند رسیدگی از پنل پذیرندگی است؛ موجودی تا تعیین نتیجه محفوظ می‌ماند")
		}
		if e = cancel.Cancel(ctx, order.GatewayConfig, order); e != nil {
			return e
		}
	}
	return s.repo.Atomic(ctx, func(ctx context.Context) error {
		o, e := s.repo.Order(ctx, id, true)
		if e != nil {
			return e
		}
		if o.Status == "canceled" {
			return nil
		}
		if o.Status != "cancel_pending" {
			return shop.ErrConflict
		}
		for _, l := range o.Quote.Lines {
			if e = s.repo.Release(ctx, l.VariantID, l.Quantity); e != nil {
				return e
			}
		}
		o.Status = "canceled"
		if o.PaymentStatus == "paid" {
			o.PaymentStatus = "refunded"
		} else {
			o.PaymentStatus = "canceled"
		}
		if e = s.repo.SaveOrder(ctx, o); e != nil {
			return e
		}
		return s.repo.Audit(ctx, u.ID, "order.cancel", id)
	})
}

func (s *Service) ProductBySlug(ctx context.Context, slug string) (shop.Product, error) {
	p, err := s.repo.ProductBySlug(ctx, slug)
	return catalogProduct(p), err
}
func (s *Service) Redirect(ctx context.Context, kind, slug string) (string, error) {
	return s.repo.Redirect(ctx, kind, slug)
}

func New(repo Repository, payments PaymentRegistry, origin string) *Service {
	return &Service{repo: repo, payments: payments, origin: origin}
}
