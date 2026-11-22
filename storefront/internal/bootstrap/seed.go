package bootstrap

import (
	"context"
	"github.com/PouryDev/storefront/internal/adapter/store"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"os"
	"strings"
	"time"
)

func seed(ctx context.Context, r *store.Store, demo bool) error {
	_, e := r.Settings(ctx, false)
	if e == nil {
		return nil
	}
	if e != shop.ErrNotFound {
		return e
	}
	return r.Atomic(ctx, func(ctx context.Context) error {
		gs := []shop.Gateway{}
		for _, g := range []struct {
			id, name string
			bnpl     bool
		}{{"zibal", "زیبال", false}, {"zarinpal", "زرین‌پال", false}, {"snappay", "اسنپ‌پی", true}, {"digipay", "دیجی‌پی", true}, {"azkivam", "ازکی‌وام", true}, {"torobpay", "ترب‌پی", true}, {"tara", "تارا", true}} {
			gs = append(gs, shop.Gateway{ID: g.id, Name: g.name, BNPL: g.bnpl, Policy: shop.BNPLPolicy{Mode: "all"}})
		}
		if demo {
			gs = append(gs, shop.Gateway{ID: "demo", Name: "پرداخت آزمایشی — بدون برداشت وجه", Enabled: true, Credential: "local-demo", Policy: shop.BNPLPolicy{Mode: "all"}})
		}
		settings := shop.Settings{Revision: 1, Name: "هِرا", Tagline: "برای زندگی، با سلیقه انتخاب کن.", Accent: "#17655d", Theme: "catalog", Modules: map[string]bool{"blog": true, "attachments": true, "discounts": true, "wishlist": true, "support": false, "analytics": true}, Sections: []shop.Section{{Kind: "hero", Title: "چیزهای ساده، حالِ خوب زندگی.", Enabled: true}, {Kind: "categories", Title: "برای هر گوشه از زندگی", Enabled: true}, {Kind: "products", Title: "انتخاب‌های این روزها", Enabled: true}, {Kind: "story", Title: "خانه، جایی برای خودت بودن.", Enabled: true}, {Kind: "blog", Title: "یادداشت‌های هِرا", Enabled: true}}, Gateways: gs, Shipping: 850000, FreeShipping: 15000000, Announcement: "ارسال رایگان برای خریدهای بالای ۱٬۵۰۰٬۰۰۰ تومان"}
		settings.Collections = []shop.Collection{{ID: "store-picks", Name: "پیشنهادهای فروشگاه", Description: "منتخب کالاهای موجود؛ آمادهٔ انتخاب و سفارش", Mode: "automatic", Sort: "catalog", Limit: 8, InStock: true, WithBadge: true, Enabled: true}}
		settings.Sections = append(settings.Sections[:1], append([]shop.Section{{Kind: "collection", CollectionID: "store-picks", Title: "پیشنهادهای فروشگاه", Enabled: true}}, settings.Sections[1:]...)...)
		settings.Sections = append(settings.Sections, shop.Section{Kind: "category-products", Title: "خرید بر اساس دسته", Enabled: true})
		if t, ok := shop.FindTheme(os.Getenv("STORE_INITIAL_THEME")); ok {
			settings.Theme = t.ID
			settings.Accent = t.Accent
		}
		if accent := os.Getenv("STORE_INITIAL_ACCENT"); len(accent) == 7 && strings.HasPrefix(accent, "#") {
			settings.Accent = accent
		}
		if title := os.Getenv("STORE_INITIAL_TITLE"); title != "" {
			settings.Name = title
		}
		if modules, ok := os.LookupEnv("STORE_INITIAL_MODULES"); ok {
			for key := range settings.Modules {
				settings.Modules[key] = false
			}
			for _, key := range strings.Split(modules, ",") {
				if _, exists := settings.Modules[key]; exists {
					settings.Modules[key] = true
				}
			}
		}
		if e = r.SaveSettings(ctx, settings); e != nil {
			return e
		}
		if !demo {
			return nil
		}
		products := []shop.Product{
			{ID: "ceramic", Slug: "ceramic-vase", Name: "گلدان سرامیکی آوا", Category: "دکور و اکسسوری", Description: "یک فرم آرام، برای گوشه‌ای از خانه. گلدان سرامیکی دست‌ساز با لعاب مات و بافت طبیعی؛ هر قطعه کمی متفاوت و منحصربه‌فرد است. مناسب شاخه‌های خشک و گل‌های تازه. ارتفاع ۲۴ سانتی‌متر، ساخت ایران.", Image: "/assets/vase.jpg", Badge: "دست‌ساز", Active: true, Variants: []shop.Variant{{ID: "ceramic-sand", Name: "ماسه‌ای", SKU: "HRA-V01", Price: 12800000, Stock: 18}, {ID: "ceramic-cream", Name: "شیری", SKU: "HRA-V02", Price: 12800000, Stock: 12}}},
			{ID: "chair", Slug: "oak-lounge-chair", Name: "صندلی لانژ اُک", Category: "مبلمان", Description: "ترکیب چوب طبیعی و خطوط نرم برای لحظه‌های استراحت. ساخته‌شده با چوب بلوط و پارچه باکیفیت، با امکان انتخاب رنگ پارچه. ابعاد ۷۰ × ۷۶ × ۸۲ سانتی‌متر.", Image: "/assets/chair.jpg", Badge: "انتخاب هِرا", Active: true, Variants: []shop.Variant{{ID: "chair-oat", Name: "بلوط / کرم", SKU: "HRA-C01", Price: 189000000, Stock: 5}, {ID: "chair-walnut", Name: "گردویی / طوسی", SKU: "HRA-C02", Price: 199000000, Stock: 3}}},
			{ID: "lamp", Slug: "warm-table-lamp", Name: "چراغ رومیزی نورا", Category: "روشنایی", Description: "نوری ملایم برای شب‌های آرام. پایه فلزی با پرداخت مات و شید پارچه‌ای با قابلیت تعویض لامپ. نور گرم و مناسب مطالعه و کنار تخت.", Image: "/assets/lamp.jpg", Badge: "تازه رسیده", Active: true, Variants: []shop.Variant{{ID: "lamp-cream", Name: "کرم", SKU: "HRA-L01", Price: 34500000, Stock: 14}}},
			{ID: "linen", Slug: "linen-cushion", Name: "کوسن لینن زمین", Category: "پارچه و بافت", Description: "بافت طبیعی لینن، رنگی از زمین. روکش قابل شست‌وشو با زیپ مخفی و الیاف نرم. ابعاد ۴۵ × ۴۵ سانتی‌متر.", Image: "/assets/linen.jpg", Active: true, Variants: []shop.Variant{{ID: "linen-earth", Name: "خاکی", SKU: "HRA-T01", Price: 8900000, Stock: 30}}},
			{ID: "mug", Slug: "handmade-mug", Name: "ماگ سرامیکی صبح", Category: "آشپزخانه", Description: "برای قهوه‌های آرام صبح. سرامیک دست‌ساز با لعاب خوراکی و دسته راحت. امکان حک اسم و طرح دلخواه با ارسال فایل.", Image: "/assets/mug.jpg", Badge: "شخصی‌سازی", Active: true, Variants: []shop.Variant{{ID: "mug-custom", Name: "با طرح اختصاصی", SKU: "HRA-M01", Price: 6800000, Stock: 25}}, Attachments: []shop.AttachmentRule{{ID: "design", Label: "طرح یا نوشته دلخواه", Min: 1, Max: 3, MaxBytes: 5 << 20, Types: []string{"image/jpeg", "image/png", "application/pdf"}, Help: "یک تصویر یا PDF از طرح خود بارگذاری کنید."}}},
			{ID: "basket", Slug: "woven-basket", Name: "سبد بافت نخل", Category: "دکور و اکسسوری", Description: "گرمای بافت‌های طبیعی برای نظم دادن به خانه. بافته‌شده با دست، از الیاف طبیعی نخل. مناسب نگهداری مجله، پتو یا گلدان.", Image: "/assets/basket.jpg", Active: true, Variants: []shop.Variant{{ID: "basket-natural", Name: "طبیعی", SKU: "HRA-B01", Price: 15400000, Stock: 16}}},
		}
		for _, p := range products {
			p.Brand = settings.Name
			p.Specs = map[string]string{"دسته‌بندی": p.Category, "برند": settings.Name}
			p.Images = []string{p.Image}
			colors := map[string]string{"ceramic-sand": "#c1a580", "ceramic-cream": "#f0e8d4", "chair-oat": "#d5c7aa", "chair-walnut": "#684d39", "lamp-cream": "#e5d8b8", "linen-earth": "#9a8062", "mug-custom": "#e2d8c7", "basket-natural": "#b99466"}
			for i := range p.Variants {
				p.Variants[i].Color = colors[p.Variants[i].ID]
				p.Variants[i].ColorName = p.Variants[i].Name
				p.Variants[i].Image = p.Image
			}

			if e = r.SaveProduct(ctx, p); e != nil {
				return e
			}
		}
		now := time.Now().UTC()
		for _, d := range []shop.Discount{{ID: "welcome", Code: "HELLOHERA", Name: "خوش‌آمد به هِرا", Kind: "percent", Value: 10, Cap: 2000000, MinSubtotal: 5000000, FirstOrder: true, PerCustomer: 1, Active: true, StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(1, 0, 0)}} {
			if e = r.SaveCampaign(ctx, d); e != nil {
				return e
			}
		}
		for _, a := range []shop.Article{{ID: "01", Slug: "a-calm-corner", Title: "چطور یک گوشه آرام برای خودمان بسازیم؟", Excerpt: "با چند انتخاب کوچک، فضایی برای مکث و نفس کشیدن بسازید.", Body: "گاهی برای تغییر حال خانه، یک تغییر کوچک کافی است. یک صندلی راحت کنار پنجره، نور گرم و یک گلدان ساده می‌تواند گوشه‌ای آرام بسازد.\n\nاز رنگ‌های طبیعی شروع کنید. کرم، خاکی و سبز زیتونی با بافت چوب و لینن هماهنگ‌اند. فضای خالی را هم بخشی از طراحی بدانید.\n\nدر نهایت خانه باید شبیه شما باشد؛ با چیزهایی که دوستشان دارید و هر روز از دیدنشان حال خوبی می‌گیرید.", Image: "/assets/room.jpg", Published: true, PublishedAt: now}, {ID: "02", Slug: "care-for-ceramics", Title: "سرامیک دست‌ساز؛ زیبایی در تفاوت‌ها", Excerpt: "قصه اشیایی که رد دست سازنده را با خود دارند.", Body: "هر قطعه سرامیک دست‌ساز مسیر خودش را از خاک تا کوره طی می‌کند. تفاوت‌های کوچک رنگ و فرم، بخشی از هویت آن است.\n\nبرای نگهداری بهتر از شوینده ملایم و اسفنج نرم استفاده کنید و از تغییر دمای ناگهانی بپرهیزید.", Image: "/assets/mug.jpg", Published: true, PublishedAt: now}} {
			if e = r.SaveArticle(ctx, a); e != nil {
				return e
			}
		}
		return nil
	})
}
