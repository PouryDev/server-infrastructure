package syndication

import (
	"context"
	"testing"

	"github.com/PouryDev/storefront/internal/domain/shop"
)

type catalog struct{ query shop.FeedQuery }

func (c *catalog) FeedProducts(_ context.Context, q shop.FeedQuery) (shop.FeedPage, error) {
	c.query = q
	return shop.FeedPage{}, nil
}
func (c *catalog) Product(context.Context, string) (shop.Product, error) { return shop.Product{}, nil }
func (c *catalog) Settings(context.Context, bool) (shop.Settings, error) { return shop.Settings{}, nil }
func TestRequestModesAreExplicitAndBounded(t *testing.T) {
	c := &catalog{}
	s := New(c, nil, nil, "https://shop.example")
	ctx := context.Background()
	one, two := 1, 2
	cursor := "v-100"
	for _, in := range []Request{{}, {Page: &one}, {Sort: "bad", Page: &one}, {IDs: []string{}}, {URLs: []string{}}, {Sort: "date_added_desc"}, {Sort: "product_id_desc", Page: &one}, {Sort: "date_updated_desc", Page: &one, Cursor: &cursor}, {IDs: []string{"v"}, Page: &one}, {URLs: []string{"https://evil.example/products/shirt"}}} {
		if _, e := s.Read(ctx, in); e == nil {
			t.Fatalf("invalid request accepted: %+v", in)
		}
	}
	if _, e := s.Read(ctx, Request{Page: &two, Sort: "date_updated_desc"}); e != nil || c.query.Offset != 100 {
		t.Fatal(c.query, e)
	}
	if _, e := s.Read(ctx, Request{Sort: "product_id_desc", Cursor: &cursor}); e != nil || c.query.BeforeID != cursor {
		t.Fatal(c.query, e)
	}
	if _, e := s.Read(ctx, Request{URLs: []string{"https://shop.example/products/shirt?variant=blue"}}); e != nil || c.query.Slugs[0] != "shirt" || c.query.VariantIDs[0] != "blue" {
		t.Fatal(c.query, e)
	}
}
