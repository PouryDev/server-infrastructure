package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/coupon"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountCoupons(r *httpx.Router, p string) {
	r.Route("POST "+p+"/coupons", a.createCoupon)
	r.Route("GET "+p+"/coupons", a.listCoupons)
	r.Route("GET "+p+"/coupons/{id}", a.getCoupon)
	r.Route("PUT "+p+"/coupons/{id}", a.updateCoupon)
	r.Route("GET "+p+"/coupons/{id}/redemptions", a.couponRedemptions)
	r.Route("POST "+p+"/coupon-actions/preview", a.previewCoupon)
	r.Route("POST "+p+"/coupon-actions/redeem", a.redeemCoupon)
	r.Route("POST "+p+"/coupon-redemptions/{id}/release", a.releaseRedemption)
}

type couponDTO struct {
	ID             uuid.UUID `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	PercentBP      int       `json:"percent_bp,omitempty"`
	Currency       string    `json:"currency"`
	FixedAmount    int64     `json:"fixed_amount,omitempty"`
	MinAmount      int64     `json:"min_amount"`
	ValidFrom      string    `json:"valid_from,omitempty"`
	ValidUntil     string    `json:"valid_until,omitempty"`
	MaxRedemptions *int      `json:"max_redemptions"`
	MaxPerCustomer *int      `json:"max_per_customer"`
	Active         bool      `json:"active"`
	Redeemed       int       `json:"redeemed"`
}

func toCoupon(c coupon.Coupon) couponDTO {
	return couponDTO{c.ID, c.Code, c.Name, string(c.Kind), c.PercentBP, string(c.Currency), c.FixedAmount, c.MinAmount, dayOrEmpty(c.ValidFrom), dayOrEmpty(c.ValidUntil),
		c.MaxRedemptions, c.MaxPerCustomer, c.Active, c.Redeemed}
}

// dateField parses an optional date of a request body (Gregorian or Jalali).
func dateField(s, field string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := parseDate(s)
	if err != nil {
		return nil, apperr.InvalidField(field, "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	return &t, nil
}

// createCoupon: kind is percent (percent_bp) or fixed (fixed_amount, in the currency's minor unit); every limit is optional.
func (a *API) createCoupon(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code           string         `json:"code"`
		Name           string         `json:"name"`
		Kind           string         `json:"kind"`
		PercentBP      int            `json:"percent_bp"`
		Currency       money.Currency `json:"currency"`
		FixedAmount    int64          `json:"fixed_amount"`
		MinAmount      int64          `json:"min_amount"`
		ValidFrom      string         `json:"valid_from"`
		ValidUntil     string         `json:"valid_until"`
		MaxRedemptions *int           `json:"max_redemptions"`
		MaxPerCustomer *int           `json:"max_per_customer"`
	}](r)
	if err != nil {
		return err
	}
	from, err := dateField(req.ValidFrom, "valid_from")
	if err != nil {
		return err
	}
	until, err := dateField(req.ValidUntil, "valid_until")
	if err != nil {
		return err
	}
	if req.Currency == "" {
		req.Currency = money.IRR
	}
	c, err := a.m.Coupons.Create(r.Context(), coupon.Input{Code: req.Code, Name: req.Name, Kind: coupon.Kind(req.Kind), PercentBP: req.PercentBP, Currency: req.Currency,
		FixedAmount: req.FixedAmount, MinAmount: req.MinAmount, ValidFrom: from, ValidUntil: until, MaxRedemptions: req.MaxRedemptions, MaxPerCustomer: req.MaxPerCustomer,
		Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toCoupon(c))
}

func (a *API) listCoupons(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Coupons.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toCoupon))
}

func (a *API) getCoupon(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := a.m.Coupons.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toCoupon(c))
}

// updateCoupon changes the limits: send only what changes; clear_* removes a limit. The kind and the value never change.
func (a *API) updateCoupon(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Name              *string `json:"name"`
		ValidFrom         string  `json:"valid_from"`
		ValidUntil        string  `json:"valid_until"`
		ClearValidFrom    bool    `json:"clear_valid_from"`
		ClearValidUntil   bool    `json:"clear_valid_until"`
		MinAmount         *int64  `json:"min_amount"`
		MaxRedemptions    *int    `json:"max_redemptions"`
		ClearMaxRedeem    bool    `json:"clear_max_redemptions"`
		MaxPerCustomer    *int    `json:"max_per_customer"`
		ClearMaxPerCustom bool    `json:"clear_max_per_customer"`
		Active            *bool   `json:"active"`
	}](r)
	if err != nil {
		return err
	}
	from, err := dateField(req.ValidFrom, "valid_from")
	if err != nil {
		return err
	}
	until, err := dateField(req.ValidUntil, "valid_until")
	if err != nil {
		return err
	}
	c, err := a.m.Coupons.Update(r.Context(), id, coupon.UpdateInput{Name: req.Name, ValidFrom: from, ValidUntil: until, ClearValidFrom: req.ClearValidFrom,
		ClearValidUntil: req.ClearValidUntil, MinAmount: req.MinAmount, MaxRedemptions: req.MaxRedemptions, ClearMaxRedeem: req.ClearMaxRedeem,
		MaxPerCustomer: req.MaxPerCustomer, ClearMaxPerCustom: req.ClearMaxPerCustom, Active: req.Active, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return ok(w, toCoupon(c))
}

type redemptionDTO struct {
	ID             uuid.UUID   `json:"id"`
	CouponID       uuid.UUID   `json:"coupon_id"`
	CustomerRef    string      `json:"customer_ref"`
	Amount         money.Money `json:"amount"`
	Discount       money.Money `json:"discount"`
	Status         string      `json:"status"`
	SubjectType    string      `json:"subject_type,omitempty"`
	SubjectID      string      `json:"subject_id,omitempty"`
	ReleasedReason string      `json:"released_reason,omitempty"`
}

func toRedemption(x coupon.Redemption) redemptionDTO {
	return redemptionDTO{x.ID, x.CouponID, x.CustomerRef, x.Amount, x.Discount, string(x.Status), x.SubjectType, x.SubjectID, x.ReleasedReason}
}

func (a *API) couponRedemptions(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := a.m.Coupons.Redemptions(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]redemptionDTO, len(list))
	for i, x := range list {
		out[i] = toRedemption(x)
	}
	return ok(w, out)
}

type couponUse struct {
	Code        string      `json:"code"`
	CustomerRef string      `json:"customer_ref"`
	Amount      money.Money `json:"amount"`
	SubjectType string      `json:"subject_type"`
	SubjectID   string      `json:"subject_id"`
}

// previewCoupon shows the discount a coupon would give now; it does not use the coupon (only redeem is binding).
func (a *API) previewCoupon(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[couponUse](r)
	if err != nil {
		return err
	}
	d, err := a.m.Coupons.Preview(r.Context(), req.Code, req.CustomerRef, req.Amount)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"discount": d})
}

func (a *API) redeemCoupon(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[couponUse](r)
	if err != nil {
		return err
	}
	x, err := a.m.Coupons.Redeem(r.Context(), coupon.RedeemInput{IdemKey: "redeem:" + key, Code: req.Code, CustomerRef: req.CustomerRef, Amount: req.Amount,
		SubjectType: req.SubjectType, SubjectID: req.SubjectID})
	if err != nil {
		return err
	}
	return created(w, toRedemption(x))
}

func (a *API) releaseRedemption(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	x, err := a.m.Coupons.Release(r.Context(), id, strings.TrimSpace(req.Reason), actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toRedemption(x))
}
