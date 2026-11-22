package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/subscription"
)

func (a *API) mountSubscriptions(r *httpx.Router, p string) {
	r.Route("POST "+p+"/subscription-plans", a.addPlan)
	r.Route("GET "+p+"/subscription-plans", a.listPlans)
	r.Route("GET "+p+"/subscription-plans/{id}", a.getPlan)
	r.Route("PUT "+p+"/subscription-plans/{id}", a.updatePlan)

	r.Route("POST "+p+"/subscriptions", a.subscribe)
	r.Route("GET "+p+"/subscriptions", a.listSubscriptions)
	r.Route("POST "+p+"/subscriptions/bill", a.billSubscriptions)
	r.Route("GET "+p+"/subscriptions/{id}", a.getSubscription)
	r.Route("GET "+p+"/subscriptions/{id}/periods", a.subscriptionPeriods)
	r.Route("POST "+p+"/subscriptions/{id}/pause", a.pauseSubscription)
	r.Route("POST "+p+"/subscriptions/{id}/resume", a.resumeSubscription)
	r.Route("POST "+p+"/subscriptions/{id}/cancel", a.cancelSubscription)
}

type planDTO struct {
	ID        uuid.UUID   `json:"id"`
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	Price     money.Money `json:"price"`
	Unit      string      `json:"unit"`
	Count     int         `json:"count"`
	TrialDays int         `json:"trial_days"`
	TaxBP     *int        `json:"tax_bp"` // null: follows the accounting VAT setting
	Active    bool        `json:"active"`
}

func toPlan(p subscription.Plan) planDTO {
	return planDTO{p.ID, p.Code, p.Name, p.Price, string(p.Unit), p.Count, p.TrialDays, p.TaxBP, p.Active}
}

func (a *API) addPlan(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code      string      `json:"code"`
		Name      string      `json:"name"`
		Price     money.Money `json:"price"`
		Unit      string      `json:"unit"`
		Count     int         `json:"count"`
		TrialDays int         `json:"trial_days"`
		TaxBP     *int        `json:"tax_bp"`
	}](r)
	if err != nil {
		return err
	}
	if req.Count == 0 {
		req.Count = 1
	}
	p, err := a.m.Subscribe.AddPlan(r.Context(), subscription.PlanInput{Code: req.Code, Name: req.Name, Price: req.Price, Unit: subscription.Unit(req.Unit), Count: req.Count,
		TrialDays: req.TrialDays, TaxBP: req.TaxBP, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toPlan(p))
}

func (a *API) listPlans(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Subscribe.Plans(r.Context())
	if err != nil {
		return err
	}
	out := make([]planDTO, len(list))
	for i, p := range list {
		out[i] = toPlan(p)
	}
	return ok(w, out)
}

func (a *API) getPlan(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := a.m.Subscribe.Plan(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toPlan(p))
}

// updatePlan changes name, price, trial days or active; a new price applies to subscriptions started afterwards.
func (a *API) updatePlan(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Name      *string      `json:"name"`
		Price     *money.Money `json:"price"`
		TrialDays *int         `json:"trial_days"`
		Active    *bool        `json:"active"`
	}](r)
	if err != nil {
		return err
	}
	p, err := a.m.Subscribe.UpdatePlan(r.Context(), id, subscription.PlanUpdate{Name: req.Name, Price: req.Price, TrialDays: req.TrialDays, Active: req.Active, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return ok(w, toPlan(p))
}

type subscriptionDTO struct {
	ID                uuid.UUID   `json:"id"`
	PlanID            uuid.UUID   `json:"plan_id"`
	PlanName          string      `json:"plan_name"`
	CustomerRef       string      `json:"customer_ref"`
	Status            string      `json:"status"`
	Price             money.Money `json:"price"`
	Unit              string      `json:"unit"`
	Count             int         `json:"count"`
	CouponCode        string      `json:"coupon_code,omitempty"`
	StartDate         string      `json:"start_date"`
	NextBillDate      string      `json:"next_bill_date,omitempty"`
	NextBillJalali    string      `json:"next_bill_date_jalali,omitempty"`
	PeriodsBilled     int         `json:"periods_billed"`
	CancelAtPeriodEnd bool        `json:"cancel_at_period_end"`
}

func toSubscription(s subscription.Subscription) subscriptionDTO {
	d := subscriptionDTO{ID: s.ID, PlanID: s.PlanID, PlanName: s.PlanName, CustomerRef: s.CustomerRef, Status: string(s.Status), Price: s.Price, Unit: string(s.Unit),
		Count: s.Count, CouponCode: s.CouponCode, StartDate: s.StartDate.Format("2006-01-02"), PeriodsBilled: s.PeriodsBilled, CancelAtPeriodEnd: s.CancelAtPeriodEnd}
	if s.NextBill != nil {
		d.NextBillDate, d.NextBillJalali = s.NextBill.Format("2006-01-02"), jdate(*s.NextBill)
	}
	return d
}

// subscribe starts a subscription (Idempotency-Key): the plan's price, interval and VAT are copied; start is Gregorian or Jalali.
func (a *API) subscribe(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		PlanID      uuid.UUID `json:"plan_id"`
		CustomerRef string    `json:"customer_ref"`
		Buyer       struct {
			Name         string `json:"name"`
			NationalID   string `json:"national_id"`
			EconomicCode string `json:"economic_code"`
			PostalCode   string `json:"postal_code"`
			Phone        string `json:"phone"`
			Address      string `json:"address"`
		} `json:"buyer"`
		Start      string `json:"start"`
		CouponCode string `json:"coupon_code"`
	}](r)
	if err != nil {
		return err
	}
	start, err := optDateBody(req.Start, "start")
	if err != nil {
		return err
	}
	s, err := a.m.Subscribe.Subscribe(r.Context(), subscription.SubscribeInput{IdemKey: "subscribe:" + key, PlanID: req.PlanID, CustomerRef: req.CustomerRef,
		Buyer: subscription.Buyer(req.Buyer), Start: start, CouponCode: req.CouponCode, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toSubscription(s))
}

func (a *API) listSubscriptions(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Subscribe.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toSubscription))
}

func (a *API) getSubscription(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Subscribe.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toSubscription(s))
}

func (a *API) subscriptionPeriods(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := a.m.Subscribe.Periods(r.Context(), id)
	if err != nil {
		return err
	}
	type periodDTO struct {
		No        int         `json:"no"`
		Start     string      `json:"start"`
		End       string      `json:"end"`
		StartJal  string      `json:"start_jalali"`
		EndJal    string      `json:"end_jalali"`
		Amount    money.Money `json:"amount"`
		Status    string      `json:"status"`
		InvoiceID *uuid.UUID  `json:"invoice_id,omitempty"`
		Attempts  int         `json:"attempts"`
		LastError string      `json:"last_error,omitempty"`
	}
	out := make([]periodDTO, len(list))
	for i, p := range list {
		out[i] = periodDTO{p.No, p.Start.Format("2006-01-02"), p.End.Format("2006-01-02"), jdate(p.Start), jdate(p.End), p.Amount, string(p.Status), p.InvoiceID, p.Attempts, p.LastError}
	}
	return ok(w, out)
}

func (a *API) pauseSubscription(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Subscribe.Pause(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSubscription(s))
}

func (a *API) resumeSubscription(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Subscribe.Resume(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSubscription(s))
}

// cancelSubscription ends a subscription now, or at the end of the period already billed with at_period_end.
func (a *API) cancelSubscription(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		AtPeriodEnd bool `json:"at_period_end"`
	}](r)
	if err != nil {
		return err
	}
	s, err := a.m.Subscribe.Cancel(r.Context(), id, req.AtPeriodEnd, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSubscription(s))
}

// billSubscriptions runs one billing round on demand (the background runner does the same every few minutes).
func (a *API) billSubscriptions(w http.ResponseWriter, r *http.Request) error {
	res, err := a.m.BillSubscriptions(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"periods": res.Periods, "invoiced": res.Invoiced, "failed": res.Failed})
}
