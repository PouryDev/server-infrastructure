// Package idpay adapts IDPay's v1.1 API to payment.Gateway. IDPay only deals in rials.
package idpay

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
	APIKey  string
	Sandbox bool
	HTTP    *http.Client
	BaseURL string // override (tests)
}

// Gateway implements payment.Gateway.
type Gateway struct {
	client gateway.Client
	base   string
}

var _ payment.Gateway = (*Gateway)(nil)

// New creates the adapter.
func New(cfg Config) (*Gateway, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("idpay: api key is required")
	}
	h := map[string]string{"X-API-KEY": cfg.APIKey}
	if cfg.Sandbox {
		h["X-SANDBOX"] = "1"
	}
	base := "https://api.idpay.ir/v1.1"
	if cfg.BaseURL != "" {
		base = cfg.BaseURL
	}
	return &Gateway{client: gateway.Client{HTTP: cfg.HTTP, Headers: h}, base: base}, nil
}

func (g *Gateway) Name() string { return "idpay" }

type apiError struct {
	Code    int    `json:"error_code"`
	Message string `json:"error_message"`
}

func (g *Gateway) Request(ctx context.Context, in payment.RequestInput) (payment.RequestResult, error) {
	irr, err := in.Amount.ToIRR()
	if err != nil {
		return payment.RequestResult{}, err
	}
	status, body, err := g.client.PostJSON(ctx, g.base+"/payment", map[string]any{
		"order_id": in.PaymentID, "amount": irr.Amount(), "phone": in.Mobile, "mail": in.Email, "desc": in.Description, "callback": in.CallbackURL,
	})
	if err != nil {
		return payment.RequestResult{}, err
	}
	if status >= 500 || status == http.StatusTooManyRequests {
		return payment.RequestResult{}, fmt.Errorf("idpay: http %d", status)
	}
	if status != http.StatusCreated {
		var e apiError
		_ = json.Unmarshal(body, &e)
		return payment.RequestResult{}, &payment.RejectedError{Code: strconv.Itoa(e.Code), Message: e.Message}
	}
	var r struct {
		ID   string `json:"id"`
		Link string `json:"link"`
	}
	if err := json.Unmarshal(body, &r); err != nil || r.ID == "" {
		return payment.RequestResult{}, errors.New("idpay: unreadable response")
	}
	return payment.RequestResult{Ref: r.ID, PayURL: r.Link}, nil
}

func (g *Gateway) ParseCallback(p map[string]string) (payment.Callback, error) {
	id := p["id"]
	if id == "" {
		return payment.Callback{}, errors.New("idpay: callback without id")
	}
	// status 10 means "paid, waiting for verification"; everything else is a failure or a cancel.
	return payment.Callback{Ref: id, ClaimedPaid: p["status"] == "10", EventKey: "idpay:" + id + ":" + p["status"]}, nil
}

// notPaid are the verify errors that mean the payment did not (or can no longer) succeed.
var notPaid = map[int]bool{51: true, 52: true, 53: true, 54: true}

func (g *Gateway) Verify(ctx context.Context, in payment.VerifyInput) (payment.VerifyResult, error) {
	status, body, err := g.client.PostJSON(ctx, g.base+"/payment/verify", map[string]any{"id": in.Ref, "order_id": in.PaymentID})
	if err != nil {
		return payment.VerifyResult{}, err
	}
	if status >= 500 || status == http.StatusTooManyRequests {
		return payment.VerifyResult{}, fmt.Errorf("idpay: http %d", status)
	}
	if status >= 400 {
		var e apiError
		_ = json.Unmarshal(body, &e)
		if notPaid[e.Code] {
			return payment.VerifyResult{Code: strconv.Itoa(e.Code), Message: e.Message}, nil
		}
		return payment.VerifyResult{}, fmt.Errorf("idpay verify: code %d: %s", e.Code, e.Message)
	}
	var v struct {
		Status  int         `json:"status"`
		TrackID json.Number `json:"track_id"`
		Amount  json.Number `json:"amount"`
		Payment struct {
			TrackID json.Number `json:"track_id"`
			Amount  json.Number `json:"amount"`
			CardNo  string      `json:"card_no"`
		} `json:"payment"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return payment.VerifyResult{}, errors.New("idpay: unreadable verify response")
	}
	if v.Status != 100 && v.Status != 101 { // 101: already verified, still paid
		return payment.VerifyResult{Code: strconv.Itoa(v.Status), Message: "payment not completed"}, nil
	}
	res := payment.VerifyResult{Paid: true, TxnRef: v.Payment.TrackID.String(), CardPAN: v.Payment.CardNo, Code: strconv.Itoa(v.Status)}
	if res.TxnRef == "" {
		res.TxnRef = v.TrackID.String()
	}
	amount := v.Payment.Amount
	if amount == "" {
		amount = v.Amount
	}
	if n, err := amount.Int64(); err == nil {
		m := money.Must(n, money.IRR)
		if in.Amount.Currency() == money.IRT {
			if m, err = m.ToIRT(); err != nil {
				return payment.VerifyResult{}, err
			}
		}
		res.Amount = &m
	}
	return res, nil
}
