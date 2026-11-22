// Package zarinpal adapts ZarinPal's v4 payment API to payment.Gateway.
package zarinpal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway"
)

// Config of the adapter.
type Config struct {
	MerchantID string
	Sandbox    bool
	HTTP       *http.Client
	// BaseURL and StartPayURL override the endpoints (tests).
	BaseURL     string
	StartPayURL string
}

// Gateway implements payment.Gateway.
type Gateway struct {
	cfg    Config
	client gateway.Client
	base   string
	start  string
}

var _ payment.Gateway = (*Gateway)(nil)

// New creates the adapter.
func New(cfg Config) (*Gateway, error) {
	if len(cfg.MerchantID) < 10 {
		return nil, errors.New("zarinpal: merchant id is required")
	}
	g := &Gateway{cfg: cfg, client: gateway.Client{HTTP: cfg.HTTP}}
	g.base, g.start = "https://api.zarinpal.com/pg/v4/payment", "https://www.zarinpal.com/pg/StartPay/"
	if cfg.Sandbox {
		g.base, g.start = "https://sandbox.zarinpal.com/pg/v4/payment", "https://sandbox.zarinpal.com/pg/StartPay/"
	}
	if cfg.BaseURL != "" {
		g.base = cfg.BaseURL
	}
	if cfg.StartPayURL != "" {
		g.start = cfg.StartPayURL
	}
	return g, nil
}

func (g *Gateway) Name() string { return "zarinpal" }

type envelope struct {
	Data   json.RawMessage `json:"data"`
	Errors json.RawMessage `json:"errors"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// parse splits the answer into its data object or the error ZarinPal reported (errors is [] on success).
func parse(status int, body []byte) (json.RawMessage, *apiError, error) {
	if status >= 500 || status == http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("zarinpal: http %d", status)
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, nil, fmt.Errorf("zarinpal: unreadable response (http %d)", status)
	}
	if len(env.Errors) > 0 && env.Errors[0] == '{' {
		var e apiError
		if err := json.Unmarshal(env.Errors, &e); err == nil && e.Code != 0 {
			return nil, &e, nil
		}
	}
	if len(env.Data) == 0 || env.Data[0] != '{' {
		return nil, nil, fmt.Errorf("zarinpal: empty response (http %d)", status)
	}
	return env.Data, nil, nil
}

func (g *Gateway) Request(ctx context.Context, in payment.RequestInput) (payment.RequestResult, error) {
	md := map[string]string{}
	if in.Mobile != "" {
		md["mobile"] = in.Mobile
	}
	if in.Email != "" {
		md["email"] = in.Email
	}
	status, body, err := g.client.PostJSON(ctx, g.base+"/request.json", map[string]any{
		"merchant_id": g.cfg.MerchantID, "amount": in.Amount.Amount(), "currency": string(in.Amount.Currency()),
		"callback_url": in.CallbackURL, "description": in.Description, "metadata": md,
	})
	if err != nil {
		return payment.RequestResult{}, err
	}
	data, apiErr, err := parse(status, body)
	if err != nil {
		return payment.RequestResult{}, err
	}
	if apiErr != nil {
		return payment.RequestResult{}, &payment.RejectedError{Code: strconv.Itoa(apiErr.Code), Message: apiErr.Message}
	}
	var d struct {
		Code      int    `json:"code"`
		Authority string `json:"authority"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Code != 100 || d.Authority == "" {
		return payment.RequestResult{}, &payment.RejectedError{Code: strconv.Itoa(d.Code), Message: "unexpected response"}
	}
	return payment.RequestResult{Ref: d.Authority, PayURL: g.start + d.Authority}, nil
}

func (g *Gateway) ParseCallback(p map[string]string) (payment.Callback, error) {
	a := p["Authority"]
	if a == "" {
		return payment.Callback{}, errors.New("zarinpal: callback without Authority")
	}
	return payment.Callback{Ref: a, ClaimedPaid: p["Status"] == "OK", EventKey: "zarinpal:" + a + ":" + p["Status"]}, nil
}

// notPaid are the verify codes that mean "this authority was not paid", as opposed to "ask again later".
var notPaid = map[int]bool{-50: true, -51: true, -53: true, -54: true}

func (g *Gateway) Verify(ctx context.Context, in payment.VerifyInput) (payment.VerifyResult, error) {
	status, body, err := g.client.PostJSON(ctx, g.base+"/verify.json", map[string]any{
		"merchant_id": g.cfg.MerchantID, "amount": in.Amount.Amount(), "authority": in.Ref,
	})
	if err != nil {
		return payment.VerifyResult{}, err
	}
	data, apiErr, err := parse(status, body)
	if err != nil {
		return payment.VerifyResult{}, err
	}
	if apiErr != nil {
		if notPaid[apiErr.Code] {
			return payment.VerifyResult{Code: strconv.Itoa(apiErr.Code), Message: apiErr.Message}, nil
		}
		return payment.VerifyResult{}, fmt.Errorf("zarinpal verify: code %d: %s", apiErr.Code, apiErr.Message)
	}
	var d struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		RefID   json.Number     `json:"ref_id"`
		CardPAN string          `json:"card_pan"`
		Fee     json.RawMessage `json:"fee"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return payment.VerifyResult{}, errors.New("zarinpal: unreadable verify data")
	}
	if d.Code != 100 && d.Code != 101 { // 101: already verified, still a successful payment
		return payment.VerifyResult{Code: strconv.Itoa(d.Code), Message: d.Message}, nil
	}
	amt := money.Must(in.Amount.Amount(), in.Amount.Currency()) // ZarinPal verifies against the amount we send
	return payment.VerifyResult{Paid: true, TxnRef: d.RefID.String(), CardPAN: d.CardPAN, Amount: &amt, Code: strconv.Itoa(d.Code)}, nil
}
