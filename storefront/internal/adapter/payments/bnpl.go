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
	"strings"
	"time"
)

type Credentials struct {
	Base         string `json:"base"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	ServiceID    string `json:"serviceId"`
	MerchantID   string `json:"merchantId"`
}
type BNPL struct {
	Kind     string
	Client   *http.Client
	TestBase string
}

func (b *BNPL) ID() string      { return b.Kind }
func (b *BNPL) Available() bool { return true }
func (b *BNPL) config(g shop.Gateway) (Credentials, error) {
	var c Credentials
	if json.Unmarshal([]byte(g.Credential), &c) != nil || c.Username == "" || c.Password == "" {
		return c, fmt.Errorf("اطلاعات پذیرندگی کامل نیست")
	}
	switch b.Kind {
	case "snappay":
		u, e := url.Parse(c.Base)
		if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(strings.HasSuffix(u.Hostname(), ".snapppay.ir") || strings.HasSuffix(u.Hostname(), ".snappay.ir")) {
			return c, fmt.Errorf("آدرس پذیرندگی اسنپ‌پی معتبر نیست")
		}
	case "torobpay":
		c.Base = "https://cpg.torobpay.com"
	case "digipay":
		c.Base = "https://api.mydigipay.com/digipay/api"
	case "azkivam":
		c.Base = "https://api.azkivam.com"
	case "tara":
		c.Base = "https://pay.tara360.ir/pay"
	}
	if b.Kind != "tara" && b.Kind != "azkivam" && (c.ClientID == "" || c.ClientSecret == "") {
		return c, fmt.Errorf("اطلاعات OAuth پذیرندگی کامل نیست")
	}
	if b.Kind == "tara" && c.ServiceID == "" {
		return c, fmt.Errorf("شناسه سرویس تارا لازم است")
	}
	if b.TestBase != "" {
		c.Base = b.TestBase
	}
	c.Base = strings.TrimRight(c.Base, "/")
	return c, nil
}
func (b *BNPL) call(ctx context.Context, c Credentials, token, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if b.Kind == "digipay" {
		req.Header.Set("Agent", "WEB")
		req.Header.Set("Digipay-Version", "2026-11-15")
	}
	res, e := b.Client.Do(req)
	if e != nil {
		return fmt.Errorf("پاسخ درگاه دریافت نشد؛ نتیجه پرداخت نیازمند بررسی است")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("درگاه اعتباری درخواست را نپذیرفت")
	}
	if out == nil {
		_, e = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return e
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out) != nil {
		return fmt.Errorf("پاسخ درگاه اعتباری معتبر نیست")
	}
	return nil
}
func (b *BNPL) auth(ctx context.Context, c Credentials) (string, error) {
	if b.Kind == "tara" || b.Kind == "azkivam" {
		var out struct {
			AccessToken string `json:"accessToken"`
		}
		path := "/api/v2/authenticate"
		if b.Kind == "azkivam" {
			path = "/auth/authenticate"
		}
		e := b.call(ctx, c, "", "POST", path, map[string]string{"username": c.Username, "password": c.Password}, &out)
		if e != nil {
			return "", e
		}
		if out.AccessToken == "" {
			return "", fmt.Errorf("احراز پذیرندگی تارا انجام نشد")
		}
		return out.AccessToken, nil
	}
	path := "/api/online/v1/oauth/token"
	f := url.Values{"grant_type": {"password"}, "username": {c.Username}, "password": {c.Password}}
	if b.Kind == "digipay" {
		path = "/oauth/token"
	} else {
		f.Set("scope", "online-merchant")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.Base+path, strings.NewReader(f.Encode()))
	if e != nil {
		return "", e
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, e := b.Client.Do(req)
	if e != nil {
		return "", fmt.Errorf("احراز پذیرندگی در دسترس نیست")
	}
	defer res.Body.Close()
	var out struct {
		Token string `json:"access_token"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out) != nil || out.Token == "" {
		return "", fmt.Errorf("اطلاعات پذیرندگی توسط درگاه تأیید نشد")
	}
	return out.Token, nil
}

type onlineEnvelope struct {
	Successful bool `json:"successful"`
	Response   struct {
		Eligible    bool   `json:"eligible"`
		Title       string `json:"title_message"`
		Description string `json:"description"`
		Token       string `json:"paymentToken"`
		Page        string `json:"paymentPageUrl"`
		Status      string `json:"status"`
	} `json:"response"`
}

func (b *BNPL) Eligibility(ctx context.Context, g shop.Gateway, amount int64) (bool, string, string, error) {
	if b.Kind != "snappay" && b.Kind != "torobpay" {
		return true, "", "", nil
	}
	c, e := b.config(g)
	if e != nil {
		return false, "", "", e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return false, "", "", e
	}
	var out onlineEnvelope
	e = b.call(ctx, c, t, "GET", "/api/online/offer/v1/eligible?amount="+strconv.FormatInt(amount, 10), nil, &out)
	if e != nil {
		return false, "", "", e
	}
	return out.Successful && out.Response.Eligible, out.Response.Title, out.Response.Description, nil
}
func cartPayload(in commerce.PaymentRequest, kind string) map[string]any {
	items := []map[string]any{}
	sum := int64(0)
	for i, l := range in.Lines {
		sum += l.Price * int64(l.Quantity)
		item := map[string]any{"id": i + 1, "name": l.Name + " / " + l.Variant, "count": l.Quantity, "amount": l.Price, "category": "storefront", "commissionType": 100}
		if kind == "torobpay" {
			item["product_id"] = l.ProductID
			item["page_url"] = in.Origin + "/products/" + l.Slug
		}
		items = append(items, item)
	}
	total := in.Amount
	shipmentIncluded := true
	if kind == "snappay" {
		total = sum + in.Shipping
		shipmentIncluded = false
	}
	cart := map[string]any{"cartId": 1, "totalAmount": total, "cartItems": items, "isShipmentIncluded": shipmentIncluded, "isTaxIncluded": true, "shippingAmount": in.Shipping, "taxAmount": 0}
	p := map[string]any{"amount": in.Amount, "discountAmount": in.Discount, "externalSourceAmount": 0, "mobile": "0" + strings.TrimPrefix(in.Phone, "+98"), "returnURL": in.Callback, "transactionId": in.OrderID, "cartList": []any{cart}}
	if kind == "torobpay" {
		p["paymentMethodTypeDto"] = "INSTALLMENT"
	}
	return p
}
func (b *BNPL) Request(ctx context.Context, g shop.Gateway, in commerce.PaymentRequest) (commerce.PaymentResult, error) {
	c, e := b.config(g)
	if e != nil {
		return commerce.PaymentResult{}, e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return commerce.PaymentResult{}, e
	}
	phone := "0" + strings.TrimPrefix(in.Phone, "+98")
	switch b.Kind {
	case "azkivam":
		return b.azkiRequest(ctx, c, t, in)
	case "snappay", "torobpay":
		var out onlineEnvelope
		e = b.call(ctx, c, t, "POST", "/api/online/payment/v1/token", cartPayload(in, b.Kind), &out)
		if e != nil {
			return commerce.PaymentResult{}, e
		}
		if !out.Successful || out.Response.Token == "" || !safeRedirect(out.Response.Page) {
			return commerce.PaymentResult{}, fmt.Errorf("درگاه اعتباری توکن معتبری صادر نکرد")
		}
		return commerce.PaymentResult{Authority: out.Response.Token, URL: out.Response.Page}, nil
	case "digipay":
		items := []map[string]any{}
		for _, l := range in.Lines {
			items = append(items, map[string]any{"productCode": l.VariantID, "brand": "storefront", "productType": 1, "count": l.Quantity})
		}
		var out struct {
			Result *struct {
				Status int `json:"status"`
			} `json:"result"`
			Ticket   string `json:"ticket"`
			Redirect string `json:"redirectUrl"`
		}
		e = b.call(ctx, c, t, "POST", "/tickets/business?type=11", map[string]any{"cellNumber": phone, "amount": in.Amount, "providerId": in.OrderID, "callbackUrl": in.Callback, "basketDetailsDto": map[string]any{"basketId": strings.ReplaceAll(in.OrderID, "-", "")[:30], "items": items}}, &out)
		if e != nil {
			return commerce.PaymentResult{}, e
		}
		if out.Result == nil || out.Result.Status != 0 || out.Ticket == "" || !safeRedirect(out.Redirect) {
			return commerce.PaymentResult{}, fmt.Errorf("دیجی‌پی توکن معتبری صادر نکرد")
		}
		return commerce.PaymentResult{Authority: out.Ticket, URL: out.Redirect}, nil
	case "tara":
		items := []map[string]any{}
		for i, item := range azkiItems(in) {
			items = append(items, map[string]any{"name": item["name"], "code": fmt.Sprintf("item-%d", i), "count": item["count"], "unit": 5, "fee": item["amount"], "data": item["name"]})
		}
		var out struct {
			Token string `json:"token"`
		}
		e = b.call(ctx, c, t, "POST", "/api/getToken", map[string]any{"additionalData": in.OrderID, "mobile": phone, "callBackUrl": in.Callback, "amount": in.Amount, "vat": 0, "serviceAmountList": []any{map[string]any{"serviceId": c.ServiceID, "amount": in.Amount}}, "taraInvoiceItemList": items, "ip": in.IP}, &out)
		if e != nil {
			return commerce.PaymentResult{}, e
		}
		if out.Token == "" {
			return commerce.PaymentResult{}, fmt.Errorf("تارا توکن معتبری صادر نکرد")
		}
		return commerce.PaymentResult{Authority: out.Token, URL: in.Origin + "/payment/redirect/" + in.OrderID}, nil
	}
	return commerce.PaymentResult{}, shop.ErrInvalid
}
func safeRedirect(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
func (b *BNPL) onlineStatus(ctx context.Context, c Credentials, t, authority string) (string, error) {
	var out onlineEnvelope
	e := b.call(ctx, c, t, "GET", "/api/online/payment/v1/status?paymentToken="+url.QueryEscape(authority), nil, &out)
	if e != nil {
		return "", e
	}
	if !out.Successful {
		return "", fmt.Errorf("وضعیت پرداخت اعتباری نامشخص است")
	}
	return out.Response.Status, nil
}
func (b *BNPL) Verify(ctx context.Context, g shop.Gateway, o shop.Order) (string, error) {
	c, e := b.config(g)
	if e != nil {
		return "", e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return "", e
	}
	switch b.Kind {
	case "azkivam":
		return b.azkiVerify(ctx, c, t, o)
	case "snappay", "torobpay":
		state, e := b.onlineStatus(ctx, c, t, o.Authority)
		if e != nil {
			return "", e
		}
		if state == "SETTLE" {
			return o.Authority, nil
		}
		if state == "PENDING" {
			var out onlineEnvelope
			e = b.call(ctx, c, t, "POST", "/api/online/payment/v1/verify", map[string]string{"paymentToken": o.Authority}, &out)
			if e != nil || !out.Successful {
				state, e = b.onlineStatus(ctx, c, t, o.Authority)
				if e != nil || state != "VERIFY" {
					return "", fmt.Errorf("تأیید پرداخت اعتباری کامل نشد")
				}
			} else {
				state = "VERIFY"
			}
		}
		if state != "VERIFY" {
			return "", fmt.Errorf("پرداخت اعتباری تأیید نشده است")
		}
		var out onlineEnvelope
		e = b.call(ctx, c, t, "POST", "/api/online/payment/v1/settle", map[string]string{"paymentToken": o.Authority}, &out)
		if e != nil || !out.Successful {
			state, e = b.onlineStatus(ctx, c, t, o.Authority)
			if e != nil || state != "SETTLE" {
				return "", fmt.Errorf("تسویه پرداخت اعتباری کامل نشد؛ نیازمند پیگیری است")
			}
		}
		return o.Authority, nil
	case "digipay":
		tracking := o.Callback["trackingCode"]
		kind := o.Callback["type"]
		if tracking == "" || (kind != "13" && kind != "5" && kind != "11" && kind != "24") {
			return "", fmt.Errorf("اطلاعات برگشت دیجی‌پی معتبر نیست")
		}
		var out struct {
			Result *struct {
				Status int `json:"status"`
			} `json:"result"`
			Amount     int64       `json:"amount"`
			ProviderID string      `json:"providerId"`
			Tracking   json.Number `json:"trackingCode"`
		}
		e = b.call(ctx, c, t, "POST", "/purchases/verify?type="+kind, map[string]string{"trackingCode": tracking, "providerId": o.ID}, &out)
		if e != nil {
			return "", e
		}
		if out.Result == nil || out.Result.Status != 0 || out.Amount != o.Quote.Total || out.ProviderID != o.ID {
			return "", fmt.Errorf("تأیید دیجی‌پی با سفارش همخوانی ندارد")
		}
		return tracking, nil
	case "tara":
		var out struct {
			Result *int        `json:"result"`
			RRN    json.Number `json:"rrn"`
		}
		e = b.call(ctx, c, t, "POST", "/api/purchaseVerify", map[string]string{"token": o.Authority, "ip": o.Callback["ip"]}, &out)
		if e != nil {
			return "", e
		}
		if out.Result == nil || *out.Result != 0 || out.RRN == "" {
			return "", fmt.Errorf("تارا پرداخت را تأیید نکرد")
		}
		return out.RRN.String(), nil
	}
	return "", shop.ErrInvalid
}
func (b *BNPL) Cancel(ctx context.Context, g shop.Gateway, o shop.Order) error {
	if b.Kind == "azkivam" {
		c, e := b.config(g)
		if e != nil {
			return e
		}
		t, e := b.auth(ctx, c)
		if e != nil {
			return e
		}
		return b.azkiCancel(ctx, c, t, o)
	}
	if b.Kind != "snappay" && b.Kind != "torobpay" {
		return fmt.Errorf("لغو این پرداخت به رسیدگی پذیرنده نیاز دارد")
	}
	c, e := b.config(g)
	if e != nil {
		return e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return e
	}
	var out onlineEnvelope
	e = b.call(ctx, c, t, "POST", "/api/online/payment/v1/cancel", map[string]string{"paymentToken": o.Authority}, &out)
	if e != nil {
		return e
	}
	if !out.Successful {
		return fmt.Errorf("لغو پرداخت اعتباری تأیید نشد")
	}
	return nil
}
func (b *BNPL) Update(ctx context.Context, g shop.Gateway, o shop.Order) error {
	if b.Kind != "snappay" && b.Kind != "torobpay" {
		return fmt.Errorf("اصلاح این پرداخت پشتیبانی نمی‌شود")
	}
	c, e := b.config(g)
	if e != nil {
		return e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return e
	}
	in := cartPayload(commerce.PaymentRequest{OrderID: o.ID, Amount: o.Quote.Total, Shipping: o.Quote.Shipping, Discount: o.Quote.Discount, Phone: o.Phone, Lines: o.Quote.Lines}, b.Kind)
	in["paymentToken"] = o.Authority
	var out onlineEnvelope
	e = b.call(ctx, c, t, "POST", "/api/online/payment/v1/update", in, &out)
	if e != nil {
		return e
	}
	if !out.Successful {
		return fmt.Errorf("اصلاح پرداخت اعتباری تأیید نشد")
	}
	return nil
}
func (b *BNPL) Deliver(ctx context.Context, g shop.Gateway, o shop.Order) error {
	if b.Kind != "digipay" {
		return nil
	}
	kind := o.Callback["type"]
	if kind != "13" && kind != "5" {
		return nil
	}
	c, e := b.config(g)
	if e != nil {
		return e
	}
	t, e := b.auth(ctx, c)
	if e != nil {
		return e
	}
	products := []string{}
	for _, l := range o.Quote.Lines {
		products = append(products, l.Name)
	}
	var out struct {
		Result *struct {
			Status int `json:"status"`
		} `json:"result"`
	}
	e = b.call(ctx, c, t, "POST", "/purchases/deliver?type="+kind, map[string]any{"deliveryDate": time.Now().UnixMilli(), "invoiceNumber": o.ID, "trackingCode": o.Reference, "products": products}, &out)
	if e != nil {
		return e
	}
	if out.Result == nil || out.Result.Status != 0 {
		return fmt.Errorf("گزارش تحویل دیجی‌پی تأیید نشد")
	}
	return nil
}

func (b *BNPL) ValidateConfiguration(g shop.Gateway) error { _, e := b.config(g); return e }
