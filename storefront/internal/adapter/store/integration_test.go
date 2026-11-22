//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	kitpg "github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/storefront/internal/adapter/payments"
	"github.com/PouryDev/storefront/internal/adapter/store"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/care"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"github.com/PouryDev/storefront/internal/usecase/identity"
	"github.com/PouryDev/storefront/internal/usecase/syndication"
	"github.com/PouryDev/storefront/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

type noSMS struct{}

func (noSMS) Send(context.Context, string, string) error { return nil }
func TestRealPostgresCommerceAndIdentity(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, e := kitpg.Connect(ctx, kitpg.Config{URL: url, MaxConns: 4, PreparedStatements: true})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	rollback := errors.New("rollback test fixtures")
	e = db.WithTx(ctx, func(ctx context.Context) error {
		schema := "storefront_it_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, e := db.Querier(ctx).Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
			return e
		}
		if _, e := db.Querier(ctx).Exec(ctx, "SET LOCAL search_path TO "+pgx.Identifier{schema}.Sanitize()); e != nil {
			return e
		}
		for _, file := range []string{"20261115000000_storefront.sql", "20261115000001_payment_context.sql", "20261115000002_media.sql", "20261115000003_redirects.sql", "20261115000004_customer_merchandising.sql", "20261119000000_commerce_delivery_sync.sql", "20261119000001_customer_care.sql"} {
			b, e := migrations.FS.ReadFile(file)
			if e != nil {
				return e
			}
			up := strings.Split(string(b), "-- +goose Down")[0]
			if _, e = db.Querier(ctx).Exec(ctx, up, pgx.QueryExecModeSimpleProtocol); e != nil {
				return e
			}
		}
		r := &store.Store{DB: db, Secret: []byte(strings.Repeat("s", 32))}
		settings := shop.Settings{Revision: 1, Name: "test", Theme: "atelier", Accent: "#123456", Modules: map[string]bool{"discounts": true, "attachments": true}, Gateways: []shop.Gateway{{ID: "demo", Name: "demo", Enabled: true, Credential: "demo", Policy: shop.BNPLPolicy{Mode: "all"}}}}
		if e := r.SaveSettings(ctx, settings); e != nil {
			return e
		}
		p := shop.Product{ID: "p", Slug: "test-product", Name: "test", Active: true, Variants: []shop.Variant{{ID: "v", Name: "one", Price: 100000, Stock: 2}}}
		if e = r.SaveProduct(ctx, p); e != nil {
			return e
		}
		auth, e := identity.New(r, noSMS{}, strings.Repeat("s", 32), true)
		if e != nil {
			return e
		}
		code, e := auth.RequestOTP(ctx, "09121234567", "127.0.0.1")
		if e != nil {
			return e
		}
		u, token, csrf, e := auth.Authenticate(ctx, "+989121234567", "", code, "127.0.0.1")
		if e != nil || u.Role != "customer" || token == "" || csrf == "" {
			return fmt.Errorf("OTP login %v", e)
		}
		if _, _, _, e = auth.Authenticate(ctx, u.Phone, "", code, "127.0.0.1"); e == nil {
			return errors.New("OTP replay accepted")
		}
		if e = auth.Password(ctx, u, "secure-test-password"); e != nil {
			return e
		}
		if _, _, _, e = auth.Authenticate(ctx, u.Phone, "secure-test-password", "", "127.0.0.1"); e != nil {
			return e
		}
		app := commerce.New(r, payments.New(true, "http://shoptest.localhost"), "http://shoptest.localhost")
		d := shop.Discount{ID: "d", Name: "one use", Code: "TEST", Kind: "percent", Value: 10, Active: true, Limit: 1}
		if e = r.SaveCampaign(ctx, d); e != nil {
			return e
		}
		checkout := commerce.Checkout{Lines: []shop.CartLine{{ProductID: "p", VariantID: "v", Quantity: 1}}, Code: "TEST", Gateway: "demo", Key: uuid.NewString(), Address: shop.Address{Name: "test", Phone: u.Phone, City: "Tehran", Province: "Tehran", Street: "Test street number 10", Postal: "1234567890"}}
		o, next, e := app.Checkout(ctx, u, checkout)
		if e != nil || next == "" || o.Quote.Total != 90000 {
			return fmt.Errorf("checkout: %v total %d", e, o.Quote.Total)
		}
		again, _, e := app.Checkout(ctx, u, checkout)
		if e != nil || again.ID != o.ID {
			return errors.New("idempotency failed")
		}
		if _, e = app.Verify(ctx, o.ID); e != nil {
			return e
		}
		if _, e = app.Verify(ctx, o.ID); e != nil {
			return e
		}
		fresh, e := r.Product(ctx, "p")
		if e != nil || fresh.Variants[0].Stock != 1 {
			return errors.New("stock reserved twice")
		}
		if _, e = app.Quote(ctx, u, checkout.Lines, "TEST"); e == nil {
			return errors.New("campaign global cap bypassed")
		}
		checkout.Key = uuid.NewString()
		checkout.Code = ""
		checkout.Lines[0].Quantity = 2
		if _, _, e = app.Checkout(ctx, u, checkout); e == nil {
			return errors.New("overselling accepted")
		}
		analytics, e := r.Analytics(ctx, time.Now().Add(-time.Hour))
		if e != nil || analytics.Paid != 1 || analytics.Revenue != 90000 {
			return fmt.Errorf("purchase attribution %v %+v", e, analytics)
		}
		a := shop.Upload{ID: uuid.NewString(), UserID: u.ID, ProductID: "p", VariantID: "v", RuleID: "proof", MIME: "image/png", Size: 30, CreatedAt: time.Now()}
		if e = r.SaveUpload(ctx, a); e != nil {
			return e
		}
		p.Attachments = []shop.AttachmentRule{{ID: "proof", Label: "proof", Min: 1, Max: 1, MaxBytes: 100, Types: []string{"image/png"}}}
		p.Variants[0].Stock = 1
		if e = r.SaveProduct(ctx, p); e != nil {
			return e
		}
		if _, e = app.Quote(ctx, u, []shop.CartLine{{ProductID: "p", VariantID: "v", Quantity: 1}}, ""); e == nil {
			return errors.New("required attachment bypassed")
		}
		other := u
		other.ID = uuid.NewString()
		if _, e = app.Quote(ctx, other, []shop.CartLine{{ProductID: "p", VariantID: "v", Quantity: 1, Attachments: []string{a.ID}}}, ""); e == nil {
			return errors.New("cross-customer file accepted")
		}

		address := shop.SavedAddress{Label: "خانه", Address: shop.Address{Name: "مشتری تست", Phone: u.Phone, Province: "تهران", City: "تهران", Street: "خیابان آزمایشی، پلاک ده", Postal: "۱۲۳۴۵۶۷۸۹۰"}}
		firstAddress, err := app.SaveAddress(ctx, u, address)
		if err != nil || !firstAddress.Default || firstAddress.Address.Postal != "1234567890" {
			return fmt.Errorf("first address: %v", err)
		}
		address.Default = true
		address.Label = "کار"
		secondAddress, err := app.SaveAddress(ctx, u, address)
		if err != nil {
			return err
		}
		addresses, err := app.Addresses(ctx, u)
		if err != nil || len(addresses) != 2 || addresses[0].ID != secondAddress.ID || !addresses[0].Default || addresses[1].Default {
			return errors.New("default address invariant failed")
		}
		foreign := shop.User{ID: uuid.NewString(), Phone: "+989121234568", Role: "customer"}
		if err = r.SaveUser(ctx, foreign); err != nil {
			return err
		}
		if _, err = app.SaveAddress(ctx, foreign, firstAddress); err == nil {
			return errors.New("foreign address overwrite accepted")
		}
		if err = app.DeleteAddress(ctx, foreign, firstAddress.ID); err == nil {
			return errors.New("foreign address deletion accepted")
		}
		if err = app.DeleteAddress(ctx, u, secondAddress.ID); err != nil {
			return err
		}
		addresses, err = app.Addresses(ctx, u)
		if err != nil || len(addresses) != 1 || !addresses[0].Default {
			return errors.New("default address was not restored")
		}
		stats, err := app.CustomerSummary(ctx, u)
		if err != nil || stats.Total != 1 || stats.Paid != 1 || stats.Spent != 90000 {
			return fmt.Errorf("customer summary: %+v %v", stats, err)
		}
		history, err := app.CustomerOrderPage(ctx, u, 1)
		if err != nil || len(history) != 1 {
			return errors.New("customer history missing")
		}
		if _, err = app.CustomerOrder(ctx, foreign, o.ID); err != shop.ErrNotFound {
			return errors.New("foreign order visible")
		}
		beforeProfile, err := r.User(ctx, u.ID)
		if err != nil {
			return err
		}
		if err = app.UpdateProfile(ctx, u, "نام مشتری"); err != nil {
			return err
		}
		afterProfile, err := r.User(ctx, u.ID)
		if err != nil || afterProfile.Name != "نام مشتری" || afterProfile.PasswordHash != beforeProfile.PasswordHash || afterProfile.Phone != u.Phone || afterProfile.Role != "customer" {
			return errors.New("profile overwrote protected fields")
		}
		beforeCollections, err := app.Settings(ctx)
		if err != nil {
			return err
		}
		collection := shop.Collection{ID: "test-picks", Name: "کالای ویژه", Mode: "manual", ProductIDs: []string{"p"}, Sort: "catalog", Limit: 4, Enabled: true}
		revision, err := app.SaveCollection(ctx, shop.User{ID: u.ID, Role: "seller"}, collection, beforeCollections.Revision, 0)
		if err != nil {
			return err
		}
		rail, err := app.HomeRail(ctx, "collection", collection.ID)
		if err != nil || len(rail.Products) != 1 || rail.Products[0].ID != "p" {
			return errors.New("published collection does not contain its products")
		}
		if _, err = app.SaveCollection(ctx, u, collection, revision, 0); err == nil {
			return errors.New("customer edited seller collection")
		}
		if _, err = app.SaveCollection(ctx, shop.User{ID: u.ID, Role: "seller"}, collection, beforeCollections.Revision, 0); err != shop.ErrConflict {
			return errors.New("stale collection revision accepted")
		}
		if _, err = app.DeleteCollection(ctx, shop.User{ID: u.ID, Role: "seller"}, collection.ID, revision); err != nil {
			return err
		}

		seller := shop.User{ID: u.ID, Role: "seller"}
		if err = app.ShipOrder(ctx, foreign, o.ID, shop.Shipment{Carrier: "post", TrackingCode: "ABC123"}); err != shop.ErrForbidden {
			return errors.New("customer dispatched order")
		}
		if err = app.ShipOrder(ctx, seller, o.ID, shop.Shipment{Carrier: "post", TrackingCode: "ABC123"}); err != nil {
			return err
		}
		if err = app.UpdateOrder(ctx, seller, o.ID, "completed"); err != nil {
			return err
		}
		careApp := care.New(r)
		review := shop.Review{ProductID: "p", Title: "خرید خوب", Body: "تجربه خرید واقعی", Rating: 5}
		if err = careApp.Review(ctx, foreign, review); err != shop.ErrForbidden {
			return errors.New("unverified review accepted")
		}
		if err = careApp.Review(ctx, u, review); err != nil {
			return err
		}
		reviews, err := careApp.Reviews(ctx, "p", shop.User{})
		if err != nil || len(reviews) != 0 {
			return errors.New("unmoderated review published")
		}
		reviews, err = careApp.Reviews(ctx, "p", seller)
		if err != nil || len(reviews) != 1 {
			return errors.New("review not visible to seller")
		}
		if err = careApp.ModerateReview(ctx, seller, reviews[0].ID, "published"); err != nil {
			return err
		}
		reviews, err = careApp.Reviews(ctx, "p", shop.User{})
		if err != nil || len(reviews) != 1 {
			return errors.New("published review missing")
		}
		if err = careApp.RequestReturn(ctx, foreign, o.ID, "علت بازگشت آزمایشی"); err != shop.ErrForbidden {
			return errors.New("foreign return request accepted")
		}
		if err = careApp.RequestReturn(ctx, u, o.ID, "علت بازگشت آزمایشی"); err != nil {
			return err
		}
		if err = careApp.RequestReturn(ctx, u, o.ID, "علت بازگشت آزمایشی"); err != nil {
			return err
		}
		if err = careApp.DecideReturn(ctx, seller, o.ID, "closed", "بسته شد"); err != shop.ErrConflict {
			return errors.New("return transition skipped")
		}
		for _, state := range []string{"approved", "received", "closed"} {
			if err = careApp.DecideReturn(ctx, seller, o.ID, state, "نتیجه رسیدگی آزمایشی"); err != nil {
				return err
			}
		}
		requests, err := careApp.Returns(ctx, u)
		if err != nil || len(requests) != 1 || requests[0].Status != "closed" {
			return errors.New("return workflow")
		}

		nextSettings, err := r.Settings(ctx, false)
		if err != nil {
			return err
		}
		nextSettings.DeliveryMethods = []shop.DeliveryMethod{{ID: "post", Name: "پست", Kind: "shipping", Fee: 50000, MinDays: 2, MaxDays: 4, Enabled: true}, {ID: "courier", Name: "پیک", Kind: "courier", Fee: 90000, Cities: []string{"تهران"}, MaxDays: 1, Enabled: true}}
		nextSettings.Torob = shop.TorobSettings{Enabled: true, WebhookToken: "private-test-token"}
		if err = app.SaveSettings(ctx, seller, nextSettings); err != nil {
			return err
		}
		var stored string
		if err = db.Querier(ctx).QueryRow(ctx, "SELECT data->'torob'->>'webhookToken' FROM shop_settings").Scan(&stored); err != nil || strings.Contains(stored, "private-test-token") {
			return errors.New("webhook token stored plaintext")
		}
		nextSettings, err = r.Settings(ctx, false)
		if err != nil || nextSettings.Torob.WebhookToken != "private-test-token" || nextSettings.Public().Torob.WebhookToken != "" {
			return errors.New("token masking/encryption")
		}
		p2 := shop.Product{ID: "delivery", Slug: "delivery-test", Name: "ارسال آزمایشی", Active: true, Image: "/assets/vase.jpg", Variants: []shop.Variant{{ID: "delivery-v", Price: 100000, Stock: 5}}}
		if err = app.SaveProduct(ctx, seller, p2); err != nil {
			return err
		}
		lines := []shop.CartLine{{ProductID: p2.ID, VariantID: "delivery-v", Quantity: 1}}
		if _, err = app.QuoteDelivery(ctx, u, lines, "", shop.DeliverySelection{MethodID: "courier", City: "شیراز"}); err == nil {
			return errors.New("destination restriction bypassed")
		}
		quote, err := app.QuoteDelivery(ctx, u, lines, "", shop.DeliverySelection{MethodID: "courier", City: "تهران"})
		if err != nil || quote.Total != 190000 || quote.Delivery.MethodID != "courier" {
			return fmt.Errorf("delivery quote %+v %v", quote, err)
		}
		shippingCheckout := commerce.Checkout{Lines: lines, Gateway: "demo", Key: uuid.NewString(), Address: checkout.Address, Delivery: shop.DeliverySelection{MethodID: "courier", City: "تهران"}}
		// The checkout uses the actual address, never the client's delivery destination.
		if _, _, err = app.Checkout(ctx, u, shippingCheckout); err == nil {
			return errors.New("forged delivery destination accepted")
		}
		shippingCheckout.Address.City = "تهران"
		shipped, _, err := app.Checkout(ctx, u, shippingCheckout)
		if err != nil || shipped.Quote.Shipping != 90000 {
			return fmt.Errorf("shipping checkout: %v", err)
		}
		if _, err = app.Verify(ctx, shipped.ID); err != nil {
			return err
		}
		nextSettings.Shipping = 1
		nextSettings.DeliveryMethods[1].Fee = 200000
		if err = r.SaveSettings(ctx, nextSettings); err != nil {
			return err
		}
		snapshot, err := r.Order(ctx, shipped.ID, false)
		if err != nil || snapshot.Quote.Shipping != 90000 {
			return errors.New("order delivery snapshot changed")
		}

		feed := syndication.New(r, r, nil, "http://shoptest.localhost")
		page := 1
		f, err := feed.Read(ctx, syndication.Request{Page: &page, Sort: "date_updated_desc"})
		if err != nil || f.Total != 2 || len(f.Products) != 2 {
			return fmt.Errorf("feed query %+v %v", f, err)
		}
		f, err = feed.Read(ctx, syndication.Request{URLs: []string{"http://shoptest.localhost/products/delivery-test?variant=delivery-v"}})
		if err != nil || f.Total != 1 || f.Products[0].Variants[0].Stock != 4 {
			return fmt.Errorf("feed live stock %+v %v", f, err)
		}
		job, err := r.LeaseSync(ctx)
		if err != nil {
			return err
		}
		if _, err = r.LeaseSync(ctx); err != shop.ErrNotFound {
			return errors.New("shop webhook throttle bypassed")
		}
		pending, err := r.SyncStatus(ctx)
		if err != nil {
			return err
		}
		if err = r.Reserve(ctx, f.Products[0].Variants[0].ID, 1); err != nil {
			return err
		}
		// Mutate the leased product, then acknowledge its older generation.
		leasedProduct, err := r.Product(ctx, job.ProductID)
		if err != nil {
			return err
		}
		if err = r.SaveProduct(ctx, leasedProduct); err != nil {
			return err
		}
		if err = r.FinishSync(ctx, job, true); err != nil {
			return err
		}
		afterSync, err := r.SyncStatus(ctx)
		if err != nil || afterSync.Pending != pending.Pending {
			return errors.New("concurrent catalog update lost")
		}
		job.Generation++
		if err = r.FinishSync(ctx, job, false); err != nil {
			return err
		}
		// Exercise real 100-row paging beyond the storefront's legacy 500-product read.
		_, err = db.Querier(ctx).Exec(ctx, `INSERT INTO shop_products(id,slug,data) SELECT 'bulk-'||i,'bulk-'||i,jsonb_build_object('id','bulk-'||i,'slug','bulk-'||i,'name','Bulk','active',true,'variants',jsonb_build_array(jsonb_build_object('id','bulk-v-'||i,'price',100000,'stock',1))) FROM generate_series(1,505) i`)
		if err != nil {
			return err
		}
		_, err = db.Querier(ctx).Exec(ctx, `INSERT INTO shop_variants(id,product_id,price,stock) SELECT 'bulk-v-'||i,'bulk-'||i,100000,1 FROM generate_series(1,505) i`)
		if err != nil {
			return err
		}
		f, err = feed.Read(ctx, syndication.Request{Sort: "product_id_desc"})
		if err != nil || f.Total != 507 || len(f.Products) != 100 {
			return fmt.Errorf("full feed paging total=%d rows=%d %v", f.Total, len(f.Products), err)
		}
		cursor := f.Products[99].Variants[0].ID
		f, err = feed.Read(ctx, syndication.Request{Sort: "product_id_desc", Cursor: &cursor})
		if err != nil || len(f.Products) != 100 || f.Products[0].Variants[0].ID >= cursor {
			return errors.New("cursor page overlaps")
		}
		page = 6
		f, err = feed.Read(ctx, syndication.Request{Sort: "date_added_desc", Page: &page})
		if err != nil || len(f.Products) != 7 {
			return fmt.Errorf("last feed page %d %v", len(f.Products), err)
		}
		return rollback
	})
	if !errors.Is(e, rollback) {
		t.Fatal(e)
	}
}
