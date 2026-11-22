package payments

import (
	"context"
	"encoding/json"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestZarinpalUsesSavedRialAmount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["amount"] != float64(150000) {
			t.Error(in)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/request.json" {
			if in["currency"] != "IRR" {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"data":{"code":100,"authority":"A123"}}`))
		} else {
			if in["authority"] != "A123" {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"data":{"code":101,"ref_id":123456}}`))
		}
	}))
	defer server.Close()
	g := Gateway{Kind: "zarinpal", Base: server.URL, Client: server.Client()}
	c := shop.Gateway{Credential: "merchant"}
	p, e := g.Request(context.Background(), c, commerce.PaymentRequest{Amount: 150000})
	if e != nil || p.Authority != "A123" {
		t.Fatal(p, e)
	}
	ref, e := g.Verify(context.Background(), c, shop.Order{Authority: "A123", Quote: shop.Quote{Total: 150000}})
	if e != nil || ref != "123456" {
		t.Fatal(ref, e)
	}
}
func TestMalformedGatewayNeverSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":100}`)) }))
	defer server.Close()
	g := Gateway{Kind: "zibal", Base: server.URL, Client: server.Client()}
	if _, e := g.Request(context.Background(), shop.Gateway{Credential: "merchant"}, commerce.PaymentRequest{Amount: 100000}); e == nil {
		t.Fatal("missing track accepted")
	}
}

func TestZibalAlreadyVerifiedRequiresMatchingInquiry(t *testing.T) {
	inquiry := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/verify" {
			_, _ = w.Write([]byte(`{"result":201}`))
			return
		}
		if r.URL.Path != "/inquiry" {
			t.Error(r.URL.Path)
		}
		inquiry = true
		_, _ = w.Write([]byte(`{"result":100,"status":1,"amount":100000,"refNumber":123,"orderId":"order"}`))
	}))
	defer server.Close()
	g := Gateway{Kind: "zibal", Base: server.URL, Client: server.Client()}
	ref, e := g.Verify(context.Background(), shop.Gateway{Credential: "merchant"}, shop.Order{ID: "order", Authority: "123", Quote: shop.Quote{Total: 100000}})
	if e != nil || ref != "123" || !inquiry {
		t.Fatal(ref, e)
	}
	if _, e = g.Verify(context.Background(), shop.Gateway{Credential: "merchant"}, shop.Order{ID: "other", Authority: "123", Quote: shop.Quote{Total: 100000}}); e == nil {
		t.Fatal("order substitution accepted")
	}
}

func TestDemoCannotActivateLiveGateways(t *testing.T) {
	demo := New(true, "http://shoptest.localhost")
	live := New(false, "https://shop.example")
	for _, id := range []string{"zibal", "zarinpal", "snappay", "digipay", "azkivam", "torobpay", "tara"} {
		p, ok := demo.Provider(id)
		if !ok || p.Available() {
			t.Fatalf("live gateway %s available in demo", id)
		}
		p, ok = live.Provider(id)
		if !ok || !p.Available() {
			t.Fatal(id)
		}
	}
	if p, ok := demo.Provider("demo"); !ok || !p.Available() {
		t.Fatal("demo provider missing")
	}
	if _, ok := live.Provider("demo"); ok {
		t.Fatal("demo provider in live registry")
	}
}
