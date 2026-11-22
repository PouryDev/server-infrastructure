// Package payments contains provider-specific protocols, never commerce decisions.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Registry struct {
	Providers map[string]commerce.PaymentProvider
}

func (r Registry) Provider(id string) (commerce.PaymentProvider, bool) {
	p, ok := r.Providers[id]
	return p, ok
}
func New(demo bool, origin string) Registry {
	r := Registry{Providers: map[string]commerce.PaymentProvider{}}
	for _, id := range []string{"zibal", "zarinpal"} {
		r.Providers[id] = &Gateway{Kind: id, Client: &http.Client{Timeout: 15 * time.Second}}
	}
	for _, id := range []string{"snappay", "digipay", "azkivam", "torobpay", "tara"} {
		r.Providers[id] = &BNPL{Kind: id, Client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
	}
	if demo {
		for id := range r.Providers {
			r.Providers[id] = Unavailable{Kind: id}
		}
		r.Providers["demo"] = &Demo{Origin: origin}
	}
	return r
}

type Gateway struct {
	Kind, Base string
	Client     *http.Client
}

func (g *Gateway) ID() string      { return g.Kind }
func (g *Gateway) Available() bool { return true }
func (g *Gateway) post(ctx context.Context, path string, in, out any) error {
	b, e := json.Marshal(in)
	if e != nil {
		return e
	}
	base := g.Base
	if base == "" {
		if g.Kind == "zibal" {
			base = "https://gateway.zibal.ir/v1"
		} else {
			base = "https://payment.zarinpal.com/pg/v4/payment"
		}
	}
	req, e := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := g.Client.Do(req)
	if e != nil {
		return fmt.Errorf("درگاه در دسترس نیست")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("درگاه درخواست را نپذیرفت")
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out); e != nil {
		return fmt.Errorf("پاسخ درگاه معتبر نیست")
	}
	return nil
}
func (g *Gateway) Request(ctx context.Context, c shop.Gateway, in commerce.PaymentRequest) (commerce.PaymentResult, error) {
	if c.Credential == "" {
		return commerce.PaymentResult{}, shop.ErrInvalid
	}
	if g.Kind == "zibal" {
		var out struct {
			Result  int   `json:"result"`
			TrackID int64 `json:"trackId"`
		}
		e := g.post(ctx, "/request", map[string]any{"merchant": c.Credential, "amount": in.Amount, "callbackUrl": in.Callback, "description": "سفارش " + in.OrderID, "orderId": in.OrderID, "mobile": in.Phone}, &out)
		if e != nil {
			return commerce.PaymentResult{}, e
		}
		if out.Result != 100 || out.TrackID <= 0 {
			return commerce.PaymentResult{}, fmt.Errorf("زیبال درخواست پرداخت را نپذیرفت")
		}
		id := strconv.FormatInt(out.TrackID, 10)
		return commerce.PaymentResult{Authority: id, URL: "https://gateway.zibal.ir/start/" + id}, nil
	}
	var out struct {
		Data struct {
			Code      int    `json:"code"`
			Authority string `json:"authority"`
		} `json:"data"`
	}
	e := g.post(ctx, "/request.json", map[string]any{"merchant_id": c.Credential, "amount": in.Amount, "currency": "IRR", "callback_url": in.Callback, "description": "سفارش " + in.OrderID, "metadata": map[string]string{"mobile": in.Phone}}, &out)
	if e != nil {
		return commerce.PaymentResult{}, e
	}
	if out.Data.Code != 100 || out.Data.Authority == "" {
		return commerce.PaymentResult{}, fmt.Errorf("زرین‌پال درخواست پرداخت را نپذیرفت")
	}
	return commerce.PaymentResult{Authority: out.Data.Authority, URL: "https://payment.zarinpal.com/pg/StartPay/" + url.PathEscape(out.Data.Authority)}, nil
}
func (g *Gateway) Verify(ctx context.Context, c shop.Gateway, o shop.Order) (string, error) {
	if c.Credential == "" {
		return "", shop.ErrInvalid
	}
	if g.Kind == "zibal" {
		id, e := strconv.ParseInt(o.Authority, 10, 64)
		if e != nil {
			return "", e
		}
		var out struct {
			Result    int         `json:"result"`
			Status    int         `json:"status"`
			Amount    int64       `json:"amount"`
			RefNumber json.Number `json:"refNumber"`
			OrderID   string      `json:"orderId"`
		}
		e = g.post(ctx, "/verify", map[string]any{"merchant": c.Credential, "trackId": id}, &out)
		if e != nil {
			return "", e
		}
		if out.Result == 201 {
			out.Result = 0
			if e = g.post(ctx, "/inquiry", map[string]any{"merchant": c.Credential, "trackId": id}, &out); e != nil {
				return "", e
			}
		}
		if out.Result != 100 || out.Status != 1 {
			return "", fmt.Errorf("تأیید زیبال تکمیل نشد؛ وضعیت تراکنش باید بررسی شود")
		}
		if out.Amount != o.Quote.Total || out.OrderID != o.ID || out.RefNumber == "" {
			return "", fmt.Errorf("اطلاعات تأیید زیبال با سفارش همخوانی ندارد")
		}
		return out.RefNumber.String(), nil
	}
	var out struct {
		Data struct {
			Code  int         `json:"code"`
			RefID json.Number `json:"ref_id"`
		} `json:"data"`
	}
	e := g.post(ctx, "/verify.json", map[string]any{"merchant_id": c.Credential, "amount": o.Quote.Total, "authority": o.Authority}, &out)
	if e != nil {
		return "", e
	}
	if (out.Data.Code != 100 && out.Data.Code != 101) || out.Data.RefID == "" {
		return "", fmt.Errorf("پرداخت تأیید نشده است")
	}
	return out.Data.RefID.String(), nil
}

// Unavailable explicitly prevents enabling partner integrations before their contracts are configured.
type Unavailable struct{ Kind string }

func (p Unavailable) ID() string      { return p.Kind }
func (p Unavailable) Available() bool { return false }
func (p Unavailable) Request(context.Context, shop.Gateway, commerce.PaymentRequest) (commerce.PaymentResult, error) {
	return commerce.PaymentResult{}, fmt.Errorf("اتصال پذیرندگی %s آماده نیست", p.Kind)
}
func (p Unavailable) Verify(context.Context, shop.Gateway, shop.Order) (string, error) {
	return "", fmt.Errorf("اتصال پذیرندگی %s آماده نیست", p.Kind)
}

type Demo struct{ Origin string }

func (d *Demo) ID() string      { return "demo" }
func (d *Demo) Available() bool { return true }
func (d *Demo) Request(_ context.Context, _ shop.Gateway, r commerce.PaymentRequest) (commerce.PaymentResult, error) {
	return commerce.PaymentResult{Authority: "demo-" + r.OrderID, URL: d.Origin + "/payment/demo/" + r.OrderID}, nil
}
func (d *Demo) Verify(_ context.Context, _ shop.Gateway, o shop.Order) (string, error) {
	if o.Authority != "demo-"+o.ID {
		return "", shop.ErrInvalid
	}
	return "TEST-" + o.ID[:8], nil
}
