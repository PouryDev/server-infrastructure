package payments

import (
	"context"
	"encoding/json"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testCredentials() shop.Gateway {
	return shop.Gateway{Credential: `{"username":"merchant","password":"password","clientId":"client","clientSecret":"secret","merchantId":"123","serviceId":"svc","base":"https://api.snapppay.ir"}`}
}
func TestOnlineBNPLMustSettle(t *testing.T) {
	for _, kind := range []string{"snappay", "torobpay"} {
		t.Run(kind, func(t *testing.T) {
			verified, settled := false, false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "oauth/token"):
					user, pass, ok := r.BasicAuth()
					if !ok || user != "client" || pass != "secret" {
						t.Error("OAuth basic auth missing")
					}
					_, _ = w.Write([]byte(`{"access_token":"access"}`))
				case strings.HasSuffix(r.URL.Path, "/status"):
					state := "PENDING"
					if verified {
						state = "VERIFY"
					}
					if settled {
						state = "SETTLE"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"successful": true, "response": map[string]string{"status": state}})
				case strings.HasSuffix(r.URL.Path, "/verify"):
					verified = true
					_, _ = w.Write([]byte(`{"successful":true}`))
				case strings.HasSuffix(r.URL.Path, "/settle"):
					if !verified {
						t.Error("settle before verify")
					}
					settled = true
					_, _ = w.Write([]byte(`{"successful":true}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			b := BNPL{Kind: kind, Client: server.Client(), TestBase: server.URL}
			o := shop.Order{ID: "order", Authority: "token"}
			if _, e := b.Verify(context.Background(), testCredentials(), o); e != nil || !verified || !settled {
				t.Fatal(e, verified, settled)
			}
			if _, e := b.Verify(context.Background(), testCredentials(), o); e != nil {
				t.Fatal("repeat settled verification", e)
			}
		})
	}
}
func TestAzkivamUsesOfficialPathsAndReconciles(t *testing.T) {
	state := 8
	verifyCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/auth/authenticate":
			if in["username"] != "merchant" {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"accessToken":"access"}`))
		case "/payment/status":
			if in["ticket_id"] != "ticket" {
				t.Error(in)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"rsCode": 0, "result": map[string]any{"rsCode": 0, "ticket_id": "ticket", "status": state}})
		case "/payment/verify":
			verifyCount++
			state = 2
			_, _ = w.Write([]byte(`{"rsCode":0,"result":{"rsCode":0,"ticket_id":"ticket","status":2}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := BNPL{Kind: "azkivam", Client: server.Client(), TestBase: server.URL}
	o := shop.Order{Authority: "ticket"}
	for range 2 {
		if _, e := b.Verify(context.Background(), testCredentials(), o); e != nil {
			t.Fatal(e)
		}
	}
	if verifyCount != 1 {
		t.Fatal(verifyCount)
	}
}
func TestAzkiDiscountAllocationIsExact(t *testing.T) {
	in := commerce.PaymentRequest{OrderID: "o", Origin: "https://shop.example", Amount: 123457, Shipping: 5000, Lines: []shop.OrderLine{{Name: "a", Price: 50000, Quantity: 3}, {Name: "b", Price: 10000, Quantity: 7}}}
	items := azkiItems(in)
	sum := int64(0)
	for _, i := range items {
		sum += i["amount"].(int64) * int64(i["count"].(int))
	}
	if sum != in.Amount {
		t.Fatal(sum, in.Amount)
	}
	if azkiProviderID("a") == azkiProviderID("b") || azkiProviderID("a") == 0 {
		t.Fatal("provider identifiers")
	}
}
func TestCallbackCannotSubstituteOrderOrPrice(t *testing.T) {
	o := shop.Order{ID: "order", Gateway: "digipay", Quote: shop.Quote{Total: 100000}}
	base := map[string]string{"providerId": "order", "amount": "100000", "result": "SUCCESS", "trackingCode": "123", "type": "13"}
	if _, e := BindCallback(o, base); e != nil {
		t.Fatal(e)
	}
	for _, change := range []struct{ k, v string }{{"providerId", "other"}, {"amount", "1"}, {"type", "99"}, {"result", "FAILURE"}} {
		copy := map[string]string{}
		for k, v := range base {
			copy[k] = v
		}
		copy[change.k] = change.v
		if _, e := BindCallback(o, copy); e == nil {
			t.Fatal(change)
		}
	}
}
func TestTaraMissingResultIsNeverSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "authenticate") {
			_, _ = w.Write([]byte(`{"accessToken":"token"}`))
		} else {
			_, _ = w.Write([]byte(`{"rrn":123}`))
		}
	}))
	defer server.Close()
	b := BNPL{Kind: "tara", Client: server.Client(), TestBase: server.URL}
	if _, e := b.Verify(context.Background(), testCredentials(), shop.Order{Authority: "token"}); e == nil {
		t.Fatal("absent result accepted")
	}
}

func TestSnappAndTorobHaveSeparateDocumentedCartArithmetic(t *testing.T) {
	in := commerce.PaymentRequest{Amount: 95000, Discount: 10000, Shipping: 5000, Lines: []shop.OrderLine{{Price: 100000, Quantity: 1}}}
	for _, test := range []struct {
		kind string
		cart int64
	}{{"snappay", 105000}, {"torobpay", 95000}} {
		payload := cartPayload(in, test.kind)
		cart := payload["cartList"].([]any)[0].(map[string]any)
		if cart["totalAmount"] != test.cart || payload["amount"] != in.Amount {
			t.Fatal(test.kind, payload)
		}
		if test.kind == "snappay" && cart["isShipmentIncluded"] != false {
			t.Fatal("shipping counted incorrectly")
		}
	}
}

func TestDigiPayTicketVerificationAndDelivery(t *testing.T) {
	delivered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var in map[string]any
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_, _ = w.Write([]byte(`{"access_token":"access"}`))
			return
		}
		if r.Header.Get("Agent") != "WEB" || r.Header.Get("Digipay-Version") == "" {
			t.Error("required headers")
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/tickets/business":
			if r.URL.Query().Get("type") != "11" || in["amount"] != float64(100000) {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"result":{"status":0},"ticket":"ticket","redirectUrl":"https://www.mydigipay.com/web-pay/tgs/v2:ticket"}`))
		case "/purchases/verify":
			if r.URL.Query().Get("type") != "13" || in["trackingCode"] != "123" || in["providerId"] != "order" {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"result":{"status":0},"amount":100000,"providerId":"order","trackingCode":123}`))
		case "/purchases/deliver":
			delivered = true
			_, _ = w.Write([]byte(`{"result":{"status":0}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := BNPL{Kind: "digipay", Client: server.Client(), TestBase: server.URL}
	in := commerce.PaymentRequest{OrderID: "12345678-1234-1234-1234-123456789012", Amount: 100000, Phone: "+989121234567", Lines: []shop.OrderLine{{VariantID: "v", Quantity: 1}}}
	result, e := b.Request(context.Background(), testCredentials(), in)
	if e != nil || result.URL != "https://www.mydigipay.com/web-pay/tgs/v2:ticket" {
		t.Fatal(result, e)
	}
	o := shop.Order{ID: "order", Authority: "ticket", Quote: shop.Quote{Total: 100000}, Callback: map[string]string{"trackingCode": "123", "type": "13"}}
	o.Reference, e = b.Verify(context.Background(), testCredentials(), o)
	if e != nil || o.Reference != "123" {
		t.Fatal(e)
	}
	if e = b.Deliver(context.Background(), testCredentials(), o); e != nil || !delivered {
		t.Fatal(e)
	}
}
func TestTaraInvoiceAndFormRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/api/v2/authenticate":
			_, _ = w.Write([]byte(`{"accessToken":"access"}`))
		case "/api/getToken":
			sum := float64(0)
			for _, raw := range in["taraInvoiceItemList"].([]any) {
				item := raw.(map[string]any)
				sum += item["fee"].(float64) * item["count"].(float64)
			}
			if sum != in["amount"] {
				t.Error("invoice does not reconcile", sum, in)
			}
			svc := in["serviceAmountList"].([]any)[0].(map[string]any)
			if svc["serviceId"] != "svc" {
				t.Error(svc)
			}
			_, _ = w.Write([]byte(`{"token":"tara-token"}`))
		case "/api/purchaseVerify":
			if in["token"] != "tara-token" {
				t.Error(in)
			}
			_, _ = w.Write([]byte(`{"result":0,"rrn":123456}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	b := BNPL{Kind: "tara", Client: server.Client(), TestBase: server.URL}
	in := commerce.PaymentRequest{OrderID: "order", Amount: 95001, Shipping: 5000, Origin: "https://shop.example", Phone: "+989121234567", Lines: []shop.OrderLine{{Name: "item", Price: 50000, Quantity: 2}}}
	result, e := b.Request(context.Background(), testCredentials(), in)
	if e != nil || result.URL != "https://shop.example/payment/redirect/order" {
		t.Fatal(result, e)
	}
	target, fields, e := b.Redirect(testCredentials(), result.Authority)
	if e != nil || target != "https://pay.tara360.ir/pay/api/ipgPurchase" || fields["token"] != "tara-token" {
		t.Fatal(target, fields, e)
	}
	ref, e := b.Verify(context.Background(), testCredentials(), shop.Order{Authority: result.Authority})
	if e != nil || ref != "123456" {
		t.Fatal(ref, e)
	}
}
