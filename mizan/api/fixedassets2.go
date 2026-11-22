package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountFixedAssets2(r *httpx.Router, p string) {
	r.Route("POST "+p+"/fixed-assets/assets/{id}/usage", a.recordUsage)
	r.Route("POST "+p+"/fixed-assets/assets/{id}/partial-disposal", a.partialDispose)
	r.Route("POST "+p+"/fixed-assets/component-assets", a.acquireComponents)
	r.Route("POST "+p+"/fixed-assets/depreciation-runs/void", a.voidRun)
	r.Route("POST "+p+"/fixed-assets/assets/{id}/idle-spans", a.addIdleSpan)
	r.Route("GET "+p+"/fixed-assets/assets/{id}/idle-spans", a.listIdleSpans)
	r.Route("DELETE "+p+"/fixed-assets/assets/{id}/idle-spans/{span}", a.removeIdleSpan)
	r.Route("GET "+p+"/fixed-assets/thresholds", a.listThresholds)
	r.Route("POST "+p+"/fixed-assets/thresholds", a.addThreshold)
	r.Route("GET "+p+"/fixed-assets/expense-limit", a.expenseLimit)
	r.Route("POST "+p+"/fixed-assets/small-assets", a.expenseSmall)
}

// recordUsage stores the units an asset produced in a Jalali month (units-of-production assets).
func (a *API) recordUsage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year  int   `json:"jalali_year"`
		Month int   `json:"jalali_month"`
		Units int64 `json:"units"`
	}](r)
	if err != nil {
		return err
	}
	if err := a.m.FixedAssets.RecordUsage(r.Context(), id, req.Year, req.Month, req.Units, actorOf(r.Context())); err != nil {
		return err
	}
	return ok(w, map[string]any{"asset_id": id, "jalali_year": req.Year, "jalali_month": req.Month, "units": req.Units})
}

func (a *API) partialDispose(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		PortionBP       int         `json:"portion_bp"`
		Date            string      `json:"date"`
		Proceeds        money.Money `json:"proceeds"`
		ProceedsAccount string      `json:"proceeds_account"`
	}](r)
	if err != nil {
		return err
	}
	in := fixedasset.PartialDisposeInput{IdemKey: key, PortionBP: req.PortionBP, Proceeds: req.Proceeds, ProceedsAccount: req.ProceedsAccount, Actor: actorOf(r.Context())}
	if req.Date != "" {
		if in.Date, err = parseDate(req.Date); err != nil {
			return apperr.InvalidField("date", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
		}
	}
	x, err := a.m.FixedAssets.DisposePartial(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toAsset(x))
}

// acquireComponents registers a composite asset as separately depreciated parts (regulation art. 5).
func (a *API) acquireComponents(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Code        string      `json:"code"`
		Name        string      `json:"name"`
		AcquiredOn  string      `json:"acquired_on"`
		InServiceOn string      `json:"in_service_on"`
		VAT         money.Money `json:"vat"`
		PaidFrom    string      `json:"paid_from"`
		Components  []struct {
			Suffix     string      `json:"suffix"`
			CategoryID uuid.UUID   `json:"category_id"`
			Cost       money.Money `json:"cost"`
		} `json:"components"`
	}](r)
	if err != nil {
		return err
	}
	in := fixedasset.AcquireComponentsInput{IdemKey: key, Code: req.Code, Name: req.Name, VAT: req.VAT, PaidFrom: req.PaidFrom, Actor: actorOf(r.Context())}
	if in.AcquiredOn, err = optDay(req.AcquiredOn); err != nil {
		return err
	}
	if in.InServiceOn, err = optDay(req.InServiceOn); err != nil {
		return err
	}
	for _, c := range req.Components {
		in.Components = append(in.Components, fixedasset.Component{Suffix: c.Suffix, CategoryID: c.CategoryID, Cost: c.Cost})
	}
	xs, err := a.m.FixedAssets.AcquireComponents(r.Context(), in)
	if err != nil {
		return err
	}
	out := make([]assetDTO, len(xs))
	for i, x := range xs {
		out[i] = toAsset(x)
	}
	return created(w, map[string]any{"items": out})
}

// voidRun reverses the latest depreciation run of a Jalali month.
func (a *API) voidRun(w http.ResponseWriter, r *http.Request) error {
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year  int `json:"jalali_year"`
		Month int `json:"jalali_month"`
	}](r)
	if err != nil {
		return err
	}
	run, err := a.m.FixedAssets.VoidRun(r.Context(), req.Year, req.Month, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"jalali_year": run.Year, "jalali_month": run.Month, "voided_total": run.Total.Amount(), "assets": len(run.Items)})
}

func thresholdDTO(x fixedasset.Threshold) map[string]any {
	return map[string]any{"id": x.ID, "effective_from": x.EffectiveFrom.Format("2006-01-02"), "small_limit": x.SmallLimit, "verified": x.Verified, "source": x.Source}
}

func (a *API) listThresholds(w http.ResponseWriter, r *http.Request) error {
	ts, err := a.m.FixedAssets.Thresholds(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(ts))
	for i, x := range ts {
		out[i] = thresholdDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) addThreshold(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		EffectiveFrom string `json:"effective_from"`
		SmallLimit    int64  `json:"small_limit"`
		Verified      bool   `json:"verified"`
		Source        string `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	from, err := optDay(req.EffectiveFrom)
	if err != nil {
		return err
	}
	x, err := a.m.FixedAssets.AddThreshold(r.Context(), from, req.SmallLimit, req.Verified, req.Source, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, thresholdDTO(x))
}

// expenseLimit: ?date= shows the cost below which an asset may be expensed (art. 12) and the limit it comes from.
func (a *API) expenseLimit(w http.ResponseWriter, r *http.Request) error {
	day, err := optDay(r.URL.Query().Get("date"))
	if err != nil {
		return err
	}
	if day.IsZero() {
		day = a.m.Ledger.Today()
	}
	limit, th, err := a.m.FixedAssets.ExpenseLimit(r.Context(), day)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"expense_below": limit, "threshold": thresholdDTO(th)})
}

// expenseSmall books a small asset straight to expense when its cost is below the art. 12 limit.
func (a *API) expenseSmall(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Name     string      `json:"name"`
		Date     string      `json:"date"`
		Cost     money.Money `json:"cost"`
		VAT      money.Money `json:"vat"`
		PaidFrom string      `json:"paid_from"`
	}](r)
	if err != nil {
		return err
	}
	in := fixedasset.ExpenseSmallInput{IdemKey: key, Name: req.Name, Cost: req.Cost, VAT: req.VAT, PaidFrom: req.PaidFrom, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	jid, err := a.m.FixedAssets.ExpenseSmall(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, map[string]any{"journal_id": jid})
}

type idleSpanDTO struct {
	ID        uuid.UUID `json:"id"`
	FromYear  int       `json:"from_jalali_year"`
	FromMonth int       `json:"from_jalali_month"`
	ToYear    int       `json:"to_jalali_year"`
	ToMonth   int       `json:"to_jalali_month"`
	Note      string    `json:"note,omitempty"`
}

func toIdleSpan(sp fixedasset.IdleSpan) idleSpanDTO {
	fy, fm := fixedasset.PeriodOf(sp.From)
	ty, tm := fixedasset.PeriodOf(sp.To)
	return idleSpanDTO{sp.ID, fy, fm, ty, tm, sp.Note}
}

// addIdleSpan records months an asset was idle (regulation art. 6 remark: more than six consecutive months in a fiscal year -> 30% depreciation for them).
func (a *API) addIdleSpan(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[idleSpanDTO](r)
	if err != nil {
		return err
	}
	sp, err := a.m.FixedAssets.AddIdleSpan(r.Context(), id, req.FromYear, req.FromMonth, req.ToYear, req.ToMonth, req.Note, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toIdleSpan(sp))
}

func (a *API) listIdleSpans(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	sps, err := a.m.FixedAssets.IdleSpans(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]idleSpanDTO, len(sps))
	for i, sp := range sps {
		out[i] = toIdleSpan(sp)
	}
	return ok(w, map[string]any{"items": out, "source": fixedasset.IdleRuleSource})
}

func (a *API) removeIdleSpan(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	span, err := pathID(r, "span")
	if err != nil {
		return err
	}
	if err := a.m.FixedAssets.RemoveIdleSpan(r.Context(), id, span, actorOf(r.Context())); err != nil {
		return err
	}
	return ok(w, map[string]any{"removed": span})
}
