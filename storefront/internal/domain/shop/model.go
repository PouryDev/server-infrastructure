// Package shop contains framework-independent commerce rules. All money is integer IRR.
package shop

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var ErrInvalid = errors.New("اطلاعات واردشده معتبر نیست")
var ErrForbidden = errors.New("دسترسی به این بخش ندارید")
var ErrNotFound = errors.New("مورد درخواستی پیدا نشد")
var ErrConflict = errors.New("اطلاعات تغییر کرده است؛ دوباره تلاش کنید")

type User struct {
	ID           string `json:"id"`
	Phone        string `json:"phone"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	PasswordHash string `json:"-"`
}

func (u User) CanManage() bool { return u.Role == "seller" || u.Role == "admin" }

var phoneRE = regexp.MustCompile(`^\+989[0-9]{9}$`)

func Phone(raw string) (string, error) {
	raw = strings.NewReplacer("۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9", "٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9", " ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "0098") {
		raw = "+98" + raw[4:]
	}
	if strings.HasPrefix(raw, "09") {
		raw = "+98" + raw[1:]
	}
	if strings.HasPrefix(raw, "989") {
		raw = "+" + raw
	}
	if !phoneRE.MatchString(raw) {
		return "", fmt.Errorf("%w: شماره موبایل ایران را وارد کنید", ErrInvalid)
	}
	return raw, nil
}

type AttachmentRule struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Min      int      `json:"min"`
	Max      int      `json:"max"`
	MaxBytes int64    `json:"maxBytes"`
	Types    []string `json:"types"`
	PerUnit  bool     `json:"perUnit"`
	Help     string   `json:"help"`
}

func (a AttachmentRule) Validate() error {
	if a.ID == "" || a.Label == "" || a.Min < 0 || a.Max < a.Min || a.Max > 20 || a.MaxBytes < 1 || a.MaxBytes > 20<<20 || len(a.Types) == 0 {
		return ErrInvalid
	}
	return nil
}

type Variant struct {
	Color     string `json:"color,omitempty"`
	ColorName string `json:"colorName,omitempty"`
	Size      string `json:"size,omitempty"`
	Image     string `json:"image,omitempty"`
	OldPrice  int64  `json:"oldPrice,omitempty"`

	StockChange *int `json:"stockChange,omitempty"`

	ID    string `json:"id"`
	Name  string `json:"name"`
	SKU   string `json:"sku"`
	Price int64  `json:"price"`
	Stock int    `json:"stock"`
}
type Product struct {
	Brand       string            `json:"brand,omitempty"`
	Guarantee   string            `json:"guarantee,omitempty"`
	WeightGrams int               `json:"weightGrams,omitempty"`
	Images      []string          `json:"images,omitempty"`
	Specs       map[string]string `json:"specs,omitempty"`
	CreatedAt   time.Time         `json:"createdAt,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt,omitempty"`

	ID          string           `json:"id"`
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Category    string           `json:"category"`
	Description string           `json:"description"`
	Image       string           `json:"image"`
	Badge       string           `json:"badge"`
	Active      bool             `json:"active"`
	Variants    []Variant        `json:"variants"`
	Attachments []AttachmentRule `json:"attachments"`
}

func (p Product) Variant(id string) (Variant, error) {
	for _, v := range p.Variants {
		if v.ID == id {
			return v, nil
		}
	}
	return Variant{}, ErrNotFound
}
func (p Product) Validate() error {
	if err := p.ValidateDetails(); err != nil {
		return err
	}
	if p.ID == "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`).MatchString(p.Slug) || len(p.Name) > 300 || p.Name == "" || len(p.Variants) == 0 || len(p.Variants) > 100 || (!strings.HasPrefix(p.Image, "/assets/") && !strings.HasPrefix(p.Image, "/media/") && p.Image != "") {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, v := range p.Variants {
		if v.ID == "" || seen[v.ID] || v.Price < 10000 || v.Price > 1_000_000_000_000 || v.Stock < 0 || v.Stock > 1_000_000 || (v.StockChange != nil && (*v.StockChange < -1_000_000 || *v.StockChange > 1_000_000)) {
			return ErrInvalid
		}
		seen[v.ID] = true
	}
	for _, a := range p.Attachments {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type BNPLPolicy struct {
	Mode     string   `json:"mode"`
	Products []string `json:"products"`
	Variants []string `json:"variants"`
}

func (p BNPLPolicy) Allows(product, variant string) bool {
	found := slices.Contains(p.Products, product) || slices.Contains(p.Variants, variant)
	switch p.Mode {
	case "all", "":
		return true
	case "whitelist":
		return found
	case "blacklist":
		return !found
	default:
		return false
	}
}

type Gateway struct {
	Description string `json:"description,omitempty"`

	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	BNPL       bool       `json:"bnpl"`
	Credential string     `json:"credential,omitempty"`
	Policy     BNPLPolicy `json:"policy"`
	Configured bool       `json:"configured"`
}
type SectionItem struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}
type Section struct {
	Body   string        `json:"body,omitempty"`
	Image  string        `json:"image,omitempty"`
	Href   string        `json:"href,omitempty"`
	Button string        `json:"button,omitempty"`
	Items  []SectionItem `json:"items,omitempty"`

	CollectionID string `json:"collectionId,omitempty"`
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Enabled      bool   `json:"enabled"`
}
type Settings struct {
	DeliveryMethods []DeliveryMethod `json:"deliveryMethods"`
	Torob           TorobSettings    `json:"torob"`
	Pages           []ContentPage    `json:"pages"`
	Navigation      []NavigationLink `json:"navigation"`

	Collections []Collection      `json:"collections"`
	Content     map[string]string `json:"content"`

	Revision      int             `json:"revision"`
	Name          string          `json:"name"`
	Tagline       string          `json:"tagline"`
	Logo          string          `json:"logo"`
	Accent        string          `json:"accent"`
	Theme         string          `json:"theme"`
	Modules       map[string]bool `json:"modules"`
	Sections      []Section       `json:"sections"`
	Gateways      []Gateway       `json:"gateways"`
	GapchiURL     string          `json:"gapchiUrl"`
	GapchiChannel string          `json:"gapchiChannel"`
	Shipping      int64           `json:"shipping"`
	FreeShipping  int64           `json:"freeShipping"`
	Announcement  string          `json:"announcement"`
}

func (s Settings) Public() Settings {
	s.Gateways = append([]Gateway(nil), s.Gateways...)
	s.Theme = ""
	s.Torob.Configured = s.Torob.WebhookToken != ""
	s.Torob.WebhookToken = ""
	for i := range s.Gateways {
		s.Gateways[i].Configured = s.Gateways[i].Credential != ""
		s.Gateways[i].Credential = ""
	}
	return s
}

type Theme struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Layout     string `json:"layout"`
	Accent     string `json:"accent"`
	Background string `json:"background"`
}

func Themes() []Theme {
	return []Theme{
		{"catalog", "کلاسیک", "رسمی · کاتالوگ ساختاریافته", "formal", "#17655d", "#f6f7f9"},
		{"orbit", "اوربیت", "استارتاپی · کارت‌های شناور", "startup", "#6040b0", "#f5f3fc"},
		{"pulse", "پالس", "پویا · حرکت و تعامل", "motion", "#b52742", "#fff7f8"},
		{"mono", "مونو", "مینیمال · محصولمحور", "minimal-shop", "#252525", "#fafafa"},
		{"bazaar", "بازار", "مارکت‌پلیس · خرید سریع", "marketplace", "#b93b21", "#fff8f4"},
		{"street", "بروت", "جسور · تایپوگرافی و خطوط", "brutalist", "#4844a8", "#f3f3fc"},
		{"atelier", "آتلیه", "لوکس · ویترین گزیده", "luxury", "#855137", "#faf7f4"},
		{"play", "پلی", "بازیگوش · فرم‌های نرم", "playful", "#276d59", "#f6faf7"},
		{"journal", "ژورنال", "مجله‌ای · کشف و خرید", "editorial-shop", "#94412e", "#fcfaf6"},
		{"terminal", "ترمینال", "فنی · مشخصات و مقایسه", "terminal", "#23715c", "#f1f7f5"},
		{"mode", "مد", "پوشاک · ویترین تمام‌عرض", "fashion", "#963344", "#fff9fa"},
		{"bloom", "بلوم", "زیبایی · فرم‌های نرم", "beauty", "#8b376b", "#fff6fb"},
		{"volt", "ولت", "دیجیتال · مشخصات فنی", "electronics", "#2855bc", "#f3f6fd"},
		{"fresh", "فرش", "سوپرمارکت · خرید روزانه", "grocery", "#23633a", "#f5faf4"},
		{"little", "لیتل", "کودک · رنگ و بازی", "kids", "#6445a2", "#faf7ff"},
		{"craft", "کرافت", "دست‌ساز · داستان محصول", "artisan", "#805233", "#faf7f2"},
	}
}
func FindTheme(id string) (Theme, bool) {
	// Resolve older store selections to their maintained design family.
	aliases := map[string]string{"boutique": "atelier", "active": "pulse", "pixel": "terminal", "forge": "catalog", "drive": "catalog", "desk": "mono", "form": "atelier", "nest": "play", "aurelia": "atelier", "harvest": "bazaar", "paper": "mono"}
	if next, ok := aliases[id]; ok {
		id = next
	}
	for _, t := range Themes() {
		if t.ID == id {
			return t, true
		}
	}
	return Theme{}, false
}

type Discount struct {
	MinQuantity  int `json:"minQuantity"`
	BuyQuantity  int `json:"buyQuantity"`
	FreeQuantity int `json:"freeQuantity"`

	ID          string    `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Value       int64     `json:"value"`
	Cap         int64     `json:"cap"`
	MinSubtotal int64     `json:"minSubtotal"`
	Products    []string  `json:"products"`
	Variants    []string  `json:"variants"`
	Phones      []string  `json:"phones"`
	StartsAt    time.Time `json:"startsAt"`
	EndsAt      time.Time `json:"endsAt"`
	Limit       int       `json:"limit"`
	PerCustomer int       `json:"perCustomer"`
	FirstOrder  bool      `json:"firstOrder"`
	Automatic   bool      `json:"automatic"`
	Active      bool      `json:"active"`
	Stackable   bool      `json:"stackable"`
}
type CartLine struct {
	ProductID   string   `json:"productId"`
	VariantID   string   `json:"variantId"`
	Quantity    int      `json:"quantity"`
	Attachments []string `json:"attachments"`
}
type OrderLine struct {
	Slug string `json:"slug"`

	ProductID   string           `json:"productId"`
	VariantID   string           `json:"variantId"`
	Name        string           `json:"name"`
	Variant     string           `json:"variant"`
	Price       int64            `json:"price"`
	Quantity    int              `json:"quantity"`
	Attachments []string         `json:"attachments"`
	Rules       []AttachmentRule `json:"rules"`
}
type Quote struct {
	Delivery        DeliveryQuote   `json:"delivery"`
	DeliveryOptions []DeliveryQuote `json:"deliveryOptions"`

	Lines     []OrderLine `json:"lines"`
	Subtotal  int64       `json:"subtotal"`
	Discount  int64       `json:"discount"`
	Shipping  int64       `json:"shipping"`
	Total     int64       `json:"total"`
	Campaigns []string    `json:"campaigns"`
	Gateways  []Gateway   `json:"gateways"`
}

func (d Discount) Amount(q Quote, phone string, now time.Time, uses, customerUses, priorOrders int) int64 {
	if !d.Active || now.Before(d.StartsAt) || (!d.EndsAt.IsZero() && !now.Before(d.EndsAt)) || q.Subtotal < d.MinSubtotal || (d.Limit > 0 && uses >= d.Limit) || (d.PerCustomer > 0 && customerUses >= d.PerCustomer) || (d.FirstOrder && priorOrders > 0) || (len(d.Phones) > 0 && !slices.Contains(d.Phones, phone)) {
		return 0
	}
	eligible := int64(0)
	eligibleQty := 0
	eligibleLines := []OrderLine{}
	for _, l := range q.Lines {
		if len(d.Products) == 0 && len(d.Variants) == 0 || slices.Contains(d.Products, l.ProductID) || slices.Contains(d.Variants, l.VariantID) {
			eligible += l.Price * int64(l.Quantity)
			eligibleQty += l.Quantity
			eligibleLines = append(eligibleLines, l)
		}
	}
	if eligible == 0 || eligibleQty < d.MinQuantity {
		return 0
	}
	var amount int64
	switch d.Kind {
	case "buy_x_get_y":
		if d.BuyQuantity < 1 || d.FreeQuantity < 1 {
			return 0
		}
		remaining := eligibleQty / (d.BuyQuantity + d.FreeQuantity) * d.FreeQuantity
		slices.SortFunc(eligibleLines, func(a, b OrderLine) int {
			if a.Price < b.Price {
				return -1
			}
			if a.Price > b.Price {
				return 1
			}
			return 0
		})
		for _, line := range eligibleLines {
			n := min(remaining, line.Quantity)
			amount += line.Price * int64(n)
			remaining -= n
			if remaining == 0 {
				break
			}
		}
	case "percent":
		if d.Value < 0 || d.Value > 100 {
			return 0
		}
		amount = eligible * d.Value / 100
	case "fixed":
		amount = d.Value
	case "shipping":
		amount = q.Shipping
	default:
		return 0
	}
	if d.Cap > 0 && amount > d.Cap {
		amount = d.Cap
	}
	if amount < 0 {
		return 0
	}
	return min(amount, eligible+q.Shipping)
}

type Address struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Province string `json:"province"`
	City     string `json:"city"`
	Street   string `json:"street"`
	Postal   string `json:"postal"`
}
type Order struct {
	Shipment     Shipment `json:"shipment"`
	CustomerNote string   `json:"customerNote,omitempty"`

	PaymentURL string `json:"paymentUrl,omitempty"`

	GatewayConfig Gateway           `json:"-"`
	Callback      map[string]string `json:"callback,omitempty"`

	ID            string    `json:"id"`
	UserID        string    `json:"userId"`
	Phone         string    `json:"phone"`
	Status        string    `json:"status"`
	PaymentStatus string    `json:"paymentStatus"`
	Gateway       string    `json:"gateway"`
	Authority     string    `json:"authority"`
	Reference     string    `json:"reference"`
	Quote         Quote     `json:"quote"`
	Address       Address   `json:"address"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Key           string    `json:"-"`
}
type Upload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	ProductID string    `json:"productId"`
	VariantID string    `json:"variantId"`
	RuleID    string    `json:"ruleId"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"createdAt"`
}
type Article struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Excerpt     string    `json:"excerpt"`
	Body        string    `json:"body"`
	Image       string    `json:"image"`
	Published   bool      `json:"published"`
	PublishedAt time.Time `json:"publishedAt"`
}
type Event struct {
	Kind      string    `json:"kind"`
	ProductID string    `json:"productId"`
	Session   string    `json:"session"`
	Source    string    `json:"source"`
	Campaign  string    `json:"campaign"`
	At        time.Time `json:"at"`
}
type Metric struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
}
type Analytics struct {
	Visitors  int64    `json:"visitors"`
	Views     int64    `json:"views"`
	Adds      int64    `json:"adds"`
	Checkouts int64    `json:"checkouts"`
	Orders    int64    `json:"orders"`
	Paid      int64    `json:"paid"`
	Revenue   int64    `json:"revenue"`
	Discounts int64    `json:"discounts"`
	Daily     []Metric `json:"daily"`
	Products  []Metric `json:"products"`
	Campaigns []Metric `json:"campaigns"`
	Gateways  []Metric `json:"gateways"`
}

// Design independently varies composition, density, cards and navigation for each shipped theme.
type Design struct{ Hero, Grid, Card, Header string }

func ThemeDesign(id string) Design {
	theme, ok := FindTheme(id)
	if !ok {
		theme = Themes()[0]
	}
	return Design{Hero: theme.Layout, Grid: theme.ID, Card: theme.ID, Header: theme.Layout}
}

func DefaultContent() map[string]string {
	return map[string]string{
		"heroTitle": "انتخاب‌های تازه برای هر روز شما", "brandSubtitle": "فروشگاه آنلاین شما", "heroKicker": "با سلیقه زندگی کن / مجموعه تازه", "heroText": "اشیایی که هر روز می‌بینیم، بخشی از حال خوب ما هستند. انتخاب‌هایی با فکر، برای خانه‌ای شبیه خودتان.", "heroButton": "کشف مجموعه", "heroCaption": "برای لحظه‌های آرام", "heroCaptionSub": "جزئیات، همه‌چیزند.", "storyText": "ما به چیزهایی باور داریم که با گذشت زمان، دوست‌داشتنی‌تر می‌شوند. به بافت طبیعی، به طراحی با فکر، به اشیایی که قصه دارند.", "storyText2": "هر محصول برای یک دلیل انتخاب شده: اینکه زندگی روزمره را کمی زیباتر کند.", "footerText": "انتخاب‌های کوچک، تفاوت‌های ماندگار.", "aboutText": "ما به انتخاب آگاهانه باور داریم. اشیایی باکیفیت و طراحی ماندگار که بخشی از زندگی روزمره می‌شوند.", "shippingPolicy": "هزینه ارسال پیش از پرداخت نمایش داده می‌شود. وضعیت سفارش و مراحل آماده‌سازی را از حساب کاربری پیگیری کنید.", "returnPolicy": "برای بررسی شرایط بازگشت، با پشتیبانی فروشگاه تماس بگیرید.", "contactPhone": "", "contactAddress": "", "heroImage": "/assets/hero.jpg", "storyImage": "/assets/room.jpg",
	}
}
