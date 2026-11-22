package gateway_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/idpay"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/zarinpal"
)

func server(t *testing.T, h func(path string, body map[string]any, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		h(r.URL.Path, m, w)
	}))
}

func TestZarinpal(t *testing.T) {
	srv := server(t, func(path string, body map[string]any, w http.ResponseWriter) {
		switch path {
		case "/request.json":
			if body["amount"].(float64) != 50000 || body["currency"] != "IRR" {
				t.Errorf("bad request body %v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"code":100,"message":"Success","authority":"A0001","fee":0},"errors":[]}`))
		case "/verify.json":
			switch body["authority"] {
			case "A0001":
				_, _ = w.Write([]byte(`{"data":{"code":100,"message":"Verified","card_pan":"603799******1234","ref_id":201,"fee":0},"errors":[]}`))
			case "A0002":
				_, _ = w.Write([]byte(`{"data":{"code":101,"message":"Verified","card_pan":"603799******1234","ref_id":201},"errors":[]}`))
			case "UNPAID":
				_, _ = w.Write([]byte(`{"data":[],"errors":{"code":-51,"message":"failed","validations":[]}}`))
			case "ERR":
				_, _ = w.Write([]byte(`{"data":[],"errors":{"code":-52,"message":"unexpected","validations":[]}}`))
			default:
				w.WriteHeader(http.StatusBadGateway)
			}
		}
	})
	defer srv.Close()
	g, err := zarinpal.New(zarinpal.Config{MerchantID: "xxxxxxxx-xxxx-xxxx", BaseURL: srv.URL, StartPayURL: "https://pay/"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	amt := money.Must(50000, money.IRR)
	res, err := g.Request(ctx, payment.RequestInput{PaymentID: "p1", Amount: amt, CallbackURL: "https://cb"})
	if err != nil || res.Ref != "A0001" || res.PayURL != "https://pay/A0001" {
		t.Fatalf("request: %+v %v", res, err)
	}
	for _, ref := range []string{"A0001", "A0002"} {
		v, err := g.Verify(ctx, payment.VerifyInput{Ref: ref, Amount: amt})
		if err != nil || !v.Paid || v.TxnRef != "201" || v.CardPAN != "603799******1234" {
			t.Fatalf("verify %s: %+v %v", ref, v, err)
		}
	}
	if v, err := g.Verify(ctx, payment.VerifyInput{Ref: "UNPAID", Amount: amt}); err != nil || v.Paid {
		t.Fatalf("definitive failure must be Paid=false without error: %+v %v", v, err)
	}
	if _, err := g.Verify(ctx, payment.VerifyInput{Ref: "ERR", Amount: amt}); err == nil {
		t.Fatal("an unknown error code must be an error (outcome unknown), never 'not paid'")
	}
	if _, err := g.Verify(ctx, payment.VerifyInput{Ref: "5xx", Amount: amt}); err == nil {
		t.Fatal("http 5xx must be an error")
	}
	cb, err := g.ParseCallback(map[string]string{"Authority": "A0001", "Status": "NOK"})
	if err != nil || cb.ClaimedPaid || cb.Ref != "A0001" {
		t.Fatalf("callback: %+v %v", cb, err)
	}
}

func TestIDPay(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-KEY")
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		switch r.URL.Path {
		case "/payment":
			if m["amount"].(float64) != 100000 {
				t.Errorf("amount must be sent in rials: %v", m)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"d2e353","link":"https://idpay.ir/p/ws/d2e353"}`))
		case "/payment/verify":
			switch m["id"] {
			case "d2e353":
				_, _ = w.Write([]byte(`{"status":100,"track_id":"10012","payment":{"track_id":"888001","amount":100000,"card_no":"123456******1234"}}`))
			case "wrongamount":
				_, _ = w.Write([]byte(`{"status":100,"payment":{"track_id":"1","amount":90000,"card_no":"1"}}`))
			case "gone":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error_code":54,"error_message":"expired"}`))
			default:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error_code":11,"error_message":"bad key"}`))
			}
		}
	}))
	defer srv.Close()
	g, err := idpay.New(idpay.Config{APIKey: "k", Sandbox: true, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	amt := money.Must(100000, money.IRR)
	res, err := g.Request(ctx, payment.RequestInput{PaymentID: "order-1", Amount: amt, CallbackURL: "https://cb"})
	if err != nil || res.Ref != "d2e353" || gotKey != "k" {
		t.Fatalf("request: %+v %v", res, err)
	}
	v, err := g.Verify(ctx, payment.VerifyInput{Ref: "d2e353", Amount: amt, PaymentID: "order-1"})
	if err != nil || !v.Paid || v.TxnRef != "888001" || v.Amount == nil || v.Amount.Amount() != 100000 {
		t.Fatalf("verify: %+v %v", v, err)
	}
	v, _ = g.Verify(ctx, payment.VerifyInput{Ref: "wrongamount", Amount: amt})
	if v.Amount == nil || *v.Amount == amt {
		t.Fatalf("the collected amount must be reported so the service can detect the mismatch: %+v", v)
	}
	if v, err := g.Verify(ctx, payment.VerifyInput{Ref: "gone", Amount: amt}); err != nil || v.Paid {
		t.Fatalf("expired verification is a definitive 'not paid': %+v %v", v, err)
	}
	if _, err := g.Verify(ctx, payment.VerifyInput{Ref: "other", Amount: amt}); err == nil {
		t.Fatal("a configuration error must not be read as 'not paid'")
	}
	cb, _ := g.ParseCallback(map[string]string{"id": "d2e353", "status": "10"})
	if !cb.ClaimedPaid {
		t.Fatal("status 10 is the success callback")
	}
}
