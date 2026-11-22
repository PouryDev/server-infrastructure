// Package commerce orchestrates transactions and provider-independent shop workflows.
package commerce

import (
	"context"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"time"
)

type Transactor interface {
	Atomic(context.Context, func(context.Context) error) error
}
type SettingsStore interface {
	Settings(context.Context, bool) (shop.Settings, error)
	SaveSettings(context.Context, shop.Settings) error
}
type CatalogStore interface {
	Products(context.Context) ([]shop.Product, error)
	Product(context.Context, string) (shop.Product, error)
	ProductBySlug(context.Context, string) (shop.Product, error)
	Redirect(context.Context, string, string) (string, error)
	SaveProduct(context.Context, shop.Product) error
	Reserve(context.Context, string, int) error
	Release(context.Context, string, int) error
}
type CampaignStore interface {
	Campaigns(context.Context, bool) ([]shop.Discount, error)
	SaveCampaign(context.Context, shop.Discount) error
	Uses(context.Context, string, string) (int, int, int, error)
	Redeem(context.Context, string, string, string) error
}
type FileStore interface {
	Upload(context.Context, string) (shop.Upload, error)
	File(context.Context, string) (shop.Upload, error)
	Media(context.Context, string) (shop.Upload, error)
	SaveMedia(context.Context, shop.Upload) error
	SaveUpload(context.Context, shop.Upload) error
	BindUpload(context.Context, string, string, string) error
}
type OrderStore interface {
	Order(context.Context, string, bool) (shop.Order, error)
	OrderByKey(context.Context, string, string) (shop.Order, error)
	Orders(context.Context, string) ([]shop.Order, error)
	SaveOrder(context.Context, shop.Order) error
	Expired(context.Context, time.Time) ([]string, error)
}
type ContentStore interface {
	Articles(context.Context) ([]shop.Article, error)
	SaveArticle(context.Context, shop.Article) error
}
type AnalyticsStore interface {
	Event(context.Context, shop.Event) error
	Analytics(context.Context, time.Time) (shop.Analytics, error)
}
type AuditStore interface {
	Audit(context.Context, string, string, string) error
}
type CustomerStore interface {
	CustomerOrderPage(context.Context, string, int, int) ([]shop.Order, error)
	CustomerSummary(context.Context, string) (shop.CustomerSummary, error)
	LockCustomer(context.Context, string) error
	Addresses(context.Context, string) ([]shop.SavedAddress, error)
	SaveAddress(context.Context, string, shop.SavedAddress) error
	DeleteAddress(context.Context, string, string) error
	UpdateCustomerName(context.Context, string, string) error
}
type Repository interface {
	CustomerStore
	Transactor
	SettingsStore
	CatalogStore
	CampaignStore
	FileStore
	OrderStore
	ContentStore
	AnalyticsStore
	AuditStore
}

type PaymentRequest struct {
	Shipping, Discount int64
	IP, Origin         string

	OrderID         string
	Amount          int64
	Phone, Callback string
	Lines           []shop.OrderLine
}
type PaymentResult struct{ Authority, URL string }
type PaymentProvider interface {
	ID() string
	Available() bool
	Request(context.Context, shop.Gateway, PaymentRequest) (PaymentResult, error)
	Verify(context.Context, shop.Gateway, shop.Order) (string, error)
}
type PaymentRegistry interface {
	Provider(string) (PaymentProvider, bool)
}
