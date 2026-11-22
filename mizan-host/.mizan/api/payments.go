package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
)

func (a *API) mountPayments(r *httpx.Router, p string) {
	r.Route("GET "+p+"/payments", a.listPayments)
	r.Route("GET "+p+"/payments/unsettled", a.unsettled)
	r.Route("POST "+p+"/payments/sweep", a.sweep)
	r.Route("GET "+p+"/payments/{id}", a.getPayment)
	r.Route("POST "+p+"/payments/{id}/refunds", a.refund)
	r.Route("GET "+p+"/refunds", a.listRefunds)
	r.Route("GET "+p+"/refunds/{id}", a.getRefund)
	r.Route("POST "+p+"/refunds/{id}/claim", a.claimRefund)
	r.Route("POST "+p+"/refunds/{id}/release", a.releaseRefund)
	r.Route("POST "+p+"/refunds/{id}/complete", a.completeRefund)
	r.Route("POST "+p+"/refunds/{id}/fail", a.failRefund)
	r.Route("POST "+p+"/refunds/{id}/cancel", a.cancelRefund)
}

// callback receives the payer's browser (and the gateway's retries). It stores the raw event, lets the
// service verify with the gateway, then sends the payer back to the merchant's return URL.
func (a *API) callback(w http.ResponseWriter, r *http.Request) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		return apperr.Invalid("unreadable callback")
	}
	if err := r.ParseForm(); err != nil && len(body) == 0 { // a form body was already consumed above on POST
		return apperr.Invalid("malformed callback")
	}
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		params[k] = v[0]
	}
	if r.Method == http.MethodPost && len(body) > 0 {
		if form, err := parseForm(string(body)); err == nil {
			for k, v := range form {
				params[k] = v
			}
		}
	}
	raw := r.Method + " " + r.URL.RequestURI() + "\n" + string(body)
	res, err := a.m.Payments.HandleCallback(r.Context(), r.PathValue("gateway"), params, raw, httpx.ClientIP(r))
	if res.Payment.ID != uuid.Nil {
		if ret := res.Payment.Metadata["return_url"]; ret != "" {
			status := string(res.Payment.Status)
			if err != nil && apperr.Is(err, apperr.KindUnavailable) {
				status = "pending" // being verified; the sweeper will finish it
			}
			http.Redirect(w, r, returnURL(ret, map[string]string{"payment_id": res.Payment.ID.String(), "status": status}), http.StatusSeeOther)
			return nil
		}
	}
	if err != nil {
		return err
	}
	return ok(w, toPayment(res.Payment))
}

func (a *API) listPayments(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Payments.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toPayment))
}

func (a *API) getPayment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := a.m.Payments.Get(r.Context(), id)
	if err != nil {
		return err
	}
	hist, err := a.m.Payments.History(r.Context(), id)
	if err != nil {
		return err
	}
	refunds, err := a.m.Payments.Refunds(r.Context(), id)
	if err != nil {
		return err
	}
	type tr struct {
		From   payment.Status `json:"from"`
		To     payment.Status `json:"to"`
		Reason string         `json:"reason"`
		At     string         `json:"at"`
	}
	trs := make([]tr, len(hist))
	for i, h := range hist {
		trs[i] = tr{h.From, h.To, h.Reason, h.At.UTC().Format("2006-01-02T15:04:05Z")}
	}
	rs := make([]refundDTO, len(refunds))
	for i, x := range refunds {
		rs[i] = toRefund(x)
	}
	return ok(w, map[string]any{"payment": toPayment(p), "transitions": trs, "refunds": rs})
}

func (a *API) refund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount *money.Money `json:"amount"` // omitted: everything that is left
		Reason string       `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	in := payment.RefundInput{PaymentID: id, IdemKey: "refund:" + id.String() + ":" + key, Reason: req.Reason, Actor: actorOf(r.Context())}
	if req.Amount != nil {
		in.Amount = *req.Amount
	}
	rf, err := a.m.Payments.Refund(r.Context(), in)
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if rf.Status == payment.RefundPending {
		status = http.StatusAccepted // waiting for the gateway or a manual payout
	}
	httpx.JSON(w, status, toRefund(rf))
	return nil
}

func (a *API) listRefunds(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Payments.ListRefunds(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toRefund))
}

func (a *API) getRefund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rf, err := a.m.Payments.RefundByID(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toRefund(rf))
}

// The manual refund workflow: an agent claims a pending refund, pays it card-to-card in the bank's app and
// records the transfer. The agent is the authenticated subject, never a field of the request.

func (a *API) claimRefund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rf, err := a.m.Payments.ClaimRefund(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toRefund(rf))
}

func (a *API) releaseRefund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Force bool `json:"force"` // a supervisor frees another agent's claim
	}](r)
	if err != nil {
		return err
	}
	rf, err := a.m.Payments.ReleaseRefund(r.Context(), id, actorOf(r.Context()), req.Force)
	if err != nil {
		return err
	}
	return ok(w, toRefund(rf))
}

// completeRefund records the card-to-card transfer and applies the refund. The Idempotency-Key is required
// like on every money-moving call; safety against a double booking comes from the refund's own state.
func (a *API) completeRefund(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		TrackingNumber        string `json:"tracking_number"`
		PaidAt                string `json:"paid_at"` // RFC 3339, or a date (Gregorian or Jalali) meaning the start of that day (UTC)
		DestinationCardMasked string `json:"destination_card_masked"`
		Note                  string `json:"note"`
	}](r)
	if err != nil {
		return err
	}
	paidAt, err := parseMoment(req.PaidAt)
	if err != nil {
		return apperr.InvalidField("paid_at", "RFC 3339 time (2026-11-09T10:30:00+03:30) or a date")
	}
	rf, err := a.m.Payments.CompleteManualRefund(r.Context(), id, payment.CompleteManualInput{Agent: actorOf(r.Context()),
		TrackingNumber: req.TrackingNumber, PaidAt: paidAt, DestinationCardMasked: req.DestinationCardMasked, Note: req.Note})
	if err != nil {
		return err
	}
	return ok(w, toRefund(rf))
}

func (a *API) failRefund(w http.ResponseWriter, r *http.Request) error {
	return a.endRefund(w, r, a.m.Payments.FailRefund)
}

func (a *API) cancelRefund(w http.ResponseWriter, r *http.Request) error {
	return a.endRefund(w, r, a.m.Payments.CancelRefund)
}

func (a *API) endRefund(w http.ResponseWriter, r *http.Request, end func(context.Context, uuid.UUID, string, string, bool) (payment.Refund, error)) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string `json:"reason"`
		Force  bool   `json:"force"`
	}](r)
	if err != nil {
		return err
	}
	rf, err := end(r.Context(), id, actorOf(r.Context()), req.Reason, req.Force)
	if err != nil {
		return err
	}
	return ok(w, toRefund(rf))
}

func (a *API) unsettled(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payments.UnsettledPaid(r.Context(), 200)
	if err != nil {
		return err
	}
	out := make([]paymentDTO, len(list))
	for i, p := range list {
		out[i] = toPayment(p)
	}
	return ok(w, out)
}

// sweep verifies open payments now instead of waiting for the background runner.
func (a *API) sweep(w http.ResponseWriter, r *http.Request) error {
	res, err := a.m.Payments.Sweep(r.Context(), 0, 200)
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"checked": res.Checked, "paid": res.Paid, "failed": res.Failed, "expired": res.Expired, "unknown": res.Unknown})
}

// parseMoment accepts an RFC 3339 time, or a date (Gregorian or Jalali) which stands for the start of that day (UTC).
func parseMoment(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := parseDate(s)
	if err != nil {
		return time.Time{}, err
	}
	return d, nil
}
