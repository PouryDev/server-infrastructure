package payments

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"github.com/PouryDev/storefront/internal/usecase/commerce"
	"math/big"
	"strconv"
	"strings"
)

func azkiProviderID(order string) uint64 {
	sum := sha256.Sum256([]byte(order))
	return binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff
}

type azkiEnvelope struct {
	Code   *int `json:"rsCode"`
	Result struct {
		Code     int    `json:"rsCode"`
		Ticket   string `json:"ticket_id"`
		Page     string `json:"payment_uri"`
		Status   int    `json:"status"`
		Fallback string `json:"fallbackUri"`
	} `json:"result"`
}

// Allocate the exact payable amount over integer-price items without losing discount remainders.
func azkiItems(in commerce.PaymentRequest) []map[string]any {
	type line struct {
		name  string
		count int
		total int64
		url   string
	}
	lines := []line{}
	sum := int64(0)
	for _, l := range in.Lines {
		total := l.Price * int64(l.Quantity)
		sum += total
		lines = append(lines, line{l.Name, l.Quantity, total, in.Origin + "/products/" + l.Slug})
	}
	if in.Shipping > 0 {
		sum += in.Shipping
		lines = append(lines, line{"هزینه ارسال", 1, in.Shipping, in.Origin})
	}
	items := []map[string]any{}
	remaining := in.Amount
	for i, l := range lines {
		product := new(big.Int).Mul(big.NewInt(l.total), big.NewInt(in.Amount))
		product.Quo(product, big.NewInt(sum))
		part := product.Int64()
		if i == len(lines)-1 {
			part = remaining
		}
		remaining -= part
		base := part / int64(l.count)
		extra := int(part % int64(l.count))
		if l.count > extra && base > 0 {
			items = append(items, map[string]any{"name": l.name, "count": l.count - extra, "amount": base, "url": l.url})
		}
		if extra > 0 {
			items = append(items, map[string]any{"name": l.name, "count": extra, "amount": base + 1, "url": l.url})
		}
	}
	return items
}
func (b *BNPL) azkiRequest(ctx context.Context, c Credentials, t string, in commerce.PaymentRequest) (commerce.PaymentResult, error) {
	if c.MerchantID == "" {
		return commerce.PaymentResult{}, fmt.Errorf("شناسه پذیرنده ازکی‌وام لازم است")
	}
	var out azkiEnvelope
	e := b.call(ctx, c, t, "POST", "/payment/purchase", map[string]any{"amount": in.Amount, "redirect_uri": in.Callback, "fallback_uri": in.Callback, "provider_id": azkiProviderID(in.OrderID), "mobile_number": "0" + strings.TrimPrefix(in.Phone, "+98"), "merchant_id": c.MerchantID, "items": azkiItems(in)}, &out)
	if e != nil {
		return commerce.PaymentResult{}, e
	}
	if out.Code == nil || *out.Code != 0 || out.Result.Ticket == "" || !safeRedirect(out.Result.Page) {
		return commerce.PaymentResult{}, fmt.Errorf("ازکی‌وام پرداخت را ایجاد نکرد")
	}
	return commerce.PaymentResult{Authority: out.Result.Ticket, URL: out.Result.Page}, nil
}
func (b *BNPL) azkiStatus(ctx context.Context, c Credentials, t, authority string) (int, error) {
	var out azkiEnvelope
	e := b.call(ctx, c, t, "POST", "/payment/status", map[string]string{"ticket_id": authority}, &out)
	if e != nil {
		return 0, e
	}
	if out.Code == nil || *out.Code != 0 || out.Result.Code != 0 || out.Result.Ticket != authority {
		return 0, fmt.Errorf("وضعیت ازکی‌وام با پرداخت همخوانی ندارد")
	}
	return out.Result.Status, nil
}
func (b *BNPL) azkiVerify(ctx context.Context, c Credentials, t string, o shop.Order) (string, error) {
	state, e := b.azkiStatus(ctx, c, t, o.Authority)
	if e != nil {
		return "", e
	}
	if state == 2 || state == 6 || state == 9 {
		return o.Authority, nil
	}
	if state != 8 {
		return "", fmt.Errorf("پرداخت ازکی‌وام انجام نشده است")
	}
	var out azkiEnvelope
	e = b.call(ctx, c, t, "POST", "/payment/verify", map[string]string{"ticket_id": o.Authority}, &out)
	if e == nil && out.Code != nil && *out.Code == 0 && out.Result.Code == 0 && out.Result.Ticket == o.Authority && out.Result.Status == 2 {
		return o.Authority, nil
	}
	state, e = b.azkiStatus(ctx, c, t, o.Authority)
	if e == nil && (state == 2 || state == 6 || state == 9) {
		return o.Authority, nil
	}
	return "", fmt.Errorf("تأیید ازکی‌وام کامل نشد؛ وضعیت نیازمند پیگیری است")
}
func (b *BNPL) azkiCancel(ctx context.Context, c Credentials, t string, o shop.Order) error {
	state, e := b.azkiStatus(ctx, c, t, o.Authority)
	if e != nil {
		return e
	}
	if state == 3 || state == 5 || state == 7 {
		return nil
	}
	path := "/payment/cancel"
	payload := map[string]any{"ticketId": o.Authority}
	if state == 2 || state == 8 {
		path = "/payment/reverse"
		payload = map[string]any{"ticket_id": o.Authority, "provider_id": azkiProviderID(o.ID), "reason": "regret"}
	}
	var out azkiEnvelope
	e = b.call(ctx, c, t, "POST", path, payload, &out)
	if e != nil {
		return e
	}
	if out.Code == nil || *out.Code != 0 || (out.Result.Code != 0 && out.Result.Code != 16) {
		return fmt.Errorf("لغو یا بازگشت ازکی‌وام تأیید نشد")
	}
	return nil
}

// BindCallback validates only transport fields. Provider verification remains authoritative.
func BindCallback(o shop.Order, values map[string]string) (shop.Order, error) {
	get := func(key string) string { return values[key] }
	switch o.Gateway {
	case "snappay", "torobpay":
		if get("transactionId") != o.ID || get("state") != "OK" {
			return o, shop.ErrInvalid
		}
		amount, e := strconv.ParseInt(get("amount"), 10, 64)
		if e != nil || amount != o.Quote.Total {
			return o, shop.ErrInvalid
		}
	case "digipay":
		amount, e := strconv.ParseInt(get("amount"), 10, 64)
		if e != nil || amount != o.Quote.Total || get("providerId") != o.ID || get("result") != "SUCCESS" || get("trackingCode") == "" {
			return o, shop.ErrInvalid
		}
		if get("type") != "13" && get("type") != "5" && get("type") != "11" && get("type") != "24" {
			return o, shop.ErrInvalid
		}
		o.Callback = map[string]string{"trackingCode": get("trackingCode"), "type": get("type")}
	case "tara":
		if get("token") != o.Authority || get("result") != "0" {
			return o, shop.ErrInvalid
		}
		o.Callback = map[string]string{"ip": get("ip")}
	case "azkivam":
		if get("ticketId") != o.Authority || get("status") != "Done" {
			return o, shop.ErrInvalid
		}
	case "zibal":
		if get("trackId") != o.Authority {
			return o, shop.ErrInvalid
		}
	case "zarinpal":
		if get("Authority") != o.Authority || get("Status") != "OK" {
			return o, shop.ErrInvalid
		}
	}
	return o, nil
}
func TaraRedirect(g shop.Gateway, authority string) (string, map[string]string, error) {
	var c Credentials
	if json.Unmarshal([]byte(g.Credential), &c) != nil || c.Username == "" || authority == "" {
		return "", nil, shop.ErrInvalid
	}
	return "https://pay.tara360.ir/pay/api/ipgPurchase", map[string]string{"username": c.Username, "token": authority}, nil
}

func (b *BNPL) Callback(o shop.Order, values map[string]string) (map[string]string, error) {
	bound, e := BindCallback(o, values)
	return bound.Callback, e
}
func (g *Gateway) Callback(o shop.Order, values map[string]string) (map[string]string, error) {
	bound, e := BindCallback(o, values)
	return bound.Callback, e
}
func (b *BNPL) Redirect(g shop.Gateway, authority string) (string, map[string]string, error) {
	if b.Kind != "tara" {
		return "", nil, shop.ErrInvalid
	}
	return TaraRedirect(g, authority)
}
