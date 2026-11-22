package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountFixedAssets(r *httpx.Router, p string) {
	r.Route("POST "+p+"/fixed-assets/categories", a.createAssetCategory)
	r.Route("GET "+p+"/fixed-assets/categories", a.listAssetCategories)
	r.Route("POST "+p+"/fixed-assets/categories/{id}/{state}", a.setAssetCategoryState)
	r.Route("GET "+p+"/fixed-assets/legal-table", a.legalTable)
	r.Route("POST "+p+"/fixed-assets/legal-table-categories", a.createCategoryFromLegalTable)
	r.Route("POST "+p+"/fixed-assets/assets", a.acquireAsset)
	r.Route("GET "+p+"/fixed-assets/assets", a.listAssets)
	r.Route("GET "+p+"/fixed-assets/assets/{id}", a.getAsset)
	r.Route("GET "+p+"/fixed-assets/assets/{id}/schedule", a.assetSchedule)
	r.Route("GET "+p+"/fixed-assets/assets/{id}/history", a.assetHistory)
	r.Route("POST "+p+"/fixed-assets/assets/{id}/dispose", a.disposeAsset)
	r.Route("POST "+p+"/fixed-assets/depreciation-runs", a.runDepreciation)
}

type assetCategoryDTO struct {
	ID         uuid.UUID `json:"id"`
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Method     string    `json:"method"`
	LifeMonths int       `json:"life_months,omitempty"`
	RateBP     int       `json:"rate_bp,omitempty"`
	SalvageBP  int       `json:"salvage_bp"`
	Source     string    `json:"source"`
	Cost       string    `json:"cost_account"`
	AccDep     string    `json:"accumulated_depreciation_account"`
	Active     bool      `json:"active"`
	IdleExempt bool      `json:"idle_exempt"`
}

func toAssetCategory(c fixedasset.Category) assetCategoryDTO {
	return assetCategoryDTO{c.ID, c.Code, c.Name, string(c.Method), c.LifeMonths, c.RateBP, c.SalvageBP, c.Source, c.CostAccount, c.AccDepAccount, c.Active, c.IdleExempt}
}

func (a *API) createAssetCategory(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code         string `json:"code"`
		Name         string `json:"name"`
		Method       string `json:"method"`
		LifeMonths   int    `json:"life_months"`
		RateBP       int    `json:"rate_bp"`
		SalvageBP    int    `json:"salvage_bp"`
		Source       string `json:"source"`
		CostCoding   string `json:"cost_coding"`
		AccDepCoding string `json:"accumulated_depreciation_coding"`
		IdleExempt   bool   `json:"idle_exempt"`
	}](r)
	if err != nil {
		return err
	}
	c, err := a.m.FixedAssets.CreateCategory(r.Context(), fixedasset.CategoryInput{Code: req.Code, Name: req.Name,
		Terms: fixedasset.Terms{Method: fixedasset.Method(req.Method), LifeMonths: req.LifeMonths, RateBP: req.RateBP}, SalvageBP: req.SalvageBP,
		Source: req.Source, CostCoding: req.CostCoding, AccDepCoding: req.AccDepCoding, IdleExempt: req.IdleExempt}, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toAssetCategory(c))
}

func (a *API) listAssetCategories(w http.ResponseWriter, r *http.Request) error {
	cs, err := a.m.FixedAssets.Categories(r.Context())
	if err != nil {
		return err
	}
	out := make([]assetCategoryDTO, len(cs))
	for i, c := range cs {
		out[i] = toAssetCategory(c)
	}
	return ok(w, map[string]any{"items": out})
}

// setAssetCategoryState handles /categories/{id}/activate and /deactivate.
func (a *API) setAssetCategoryState(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var active bool
	switch r.PathValue("state") {
	case "activate":
		active = true
	case "deactivate":
	default:
		return apperr.NotFound("route")
	}
	if err := a.m.FixedAssets.SetCategoryActive(r.Context(), id, active); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

type assetDTO struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	CategoryID  uuid.UUID `json:"category_id"`
	AcquiredOn  string    `json:"acquired_on"`
	InServiceOn string    `json:"in_service_on"`
	Cost        int64     `json:"cost"`
	Salvage     int64     `json:"salvage"`
	Method      string    `json:"method"`
	LifeMonths  int       `json:"life_months,omitempty"`
	RateBP      int       `json:"rate_bp,omitempty"`
	Accumulated int64     `json:"accumulated_depreciation"`
	BookValue   int64     `json:"book_value"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	DisposedOn  string    `json:"disposed_on,omitempty"`
}

func toAsset(x fixedasset.Asset) assetDTO {
	d := assetDTO{x.ID, x.Code, x.Name, x.CategoryID, x.AcquiredOn.Format("2006-01-02"), x.InServiceOn.Format("2006-01-02"), x.Cost.Amount(), x.Salvage.Amount(),
		string(x.Method), x.LifeMonths, x.RateBP, x.Accumulated.Amount(), x.BookValue(), string(x.Cost.Currency()), string(x.Status), ""}
	if x.DisposedOn != nil {
		d.DisposedOn = x.DisposedOn.Format("2006-01-02")
	}
	return d
}

func (a *API) acquireAsset(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Code               string       `json:"code"`
		Name               string       `json:"name"`
		CategoryID         uuid.UUID    `json:"category_id"`
		AcquiredOn         string       `json:"acquired_on"`
		InServiceOn        string       `json:"in_service_on"`
		Cost               money.Money  `json:"cost"`
		VAT                money.Money  `json:"vat"`
		Salvage            *money.Money `json:"salvage"`
		PaidFrom           string       `json:"paid_from"`
		AlreadyBooked      bool         `json:"already_booked"`
		OpeningAccumulated *money.Money `json:"opening_accumulated"`
		MonthsDone         int          `json:"months_done"`
	}](r)
	if err != nil {
		return err
	}
	in := fixedasset.AcquireInput{IdemKey: key, Code: req.Code, Name: req.Name, CategoryID: req.CategoryID, Cost: req.Cost, VAT: req.VAT, Salvage: req.Salvage, PaidFrom: req.PaidFrom,
		AlreadyBooked: req.AlreadyBooked, OpeningAccumulated: req.OpeningAccumulated, MonthsDone: req.MonthsDone, Actor: actorOf(r.Context())}
	if req.AcquiredOn != "" {
		if in.AcquiredOn, err = parseDate(req.AcquiredOn); err != nil {
			return apperr.InvalidField("acquired_on", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
		}
	}
	if req.InServiceOn != "" {
		if in.InServiceOn, err = parseDate(req.InServiceOn); err != nil {
			return apperr.InvalidField("in_service_on", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
		}
	}
	x, err := a.m.FixedAssets.Acquire(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toAsset(x))
}

func (a *API) listAssets(w http.ResponseWriter, r *http.Request) error {
	xs, err := a.m.FixedAssets.Assets(r.Context(), fixedasset.Status(r.URL.Query().Get("status")))
	if err != nil {
		return err
	}
	out := make([]assetDTO, len(xs))
	for i, x := range xs {
		out[i] = toAsset(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.FixedAssets.Asset(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toAsset(x))
}

func (a *API) assetSchedule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := a.m.FixedAssets.Schedule(r.Context(), id)
	if err != nil {
		return err
	}
	type row struct {
		Year        int   `json:"jalali_year"`
		Month       int   `json:"jalali_month"`
		Amount      int64 `json:"amount"`
		Accumulated int64 `json:"accumulated"`
		BookValue   int64 `json:"book_value"`
	}
	out := make([]row, len(rows))
	for i, x := range rows {
		out[i] = row{x.Year, x.Month, x.Amount, x.Accumulated, x.BookValue}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) assetHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	items, err := a.m.FixedAssets.History(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		y, m := fixedasset.PeriodOf(it.PeriodIndex)
		out[i] = map[string]any{"jalali_year": y, "jalali_month": m, "amount": it.Amount.Amount()}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) disposeAsset(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Date            string      `json:"date"`
		Proceeds        money.Money `json:"proceeds"`
		ProceedsAccount string      `json:"proceeds_account"`
	}](r)
	if err != nil {
		return err
	}
	in := fixedasset.DisposeInput{IdemKey: key, Proceeds: req.Proceeds, ProceedsAccount: req.ProceedsAccount, Actor: actorOf(r.Context())}
	if req.Date != "" {
		if in.Date, err = parseDate(req.Date); err != nil {
			return apperr.InvalidField("date", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
		}
	}
	x, err := a.m.FixedAssets.Dispose(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toAsset(x))
}

// runDepreciation books the depreciation of a finished Jalali month. Months must be run in order.
func (a *API) runDepreciation(w http.ResponseWriter, r *http.Request) error {
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
	run, err := a.m.FixedAssets.RunDepreciation(r.Context(), req.Year, req.Month, actorOf(r.Context()))
	if err != nil {
		return err
	}
	items := make([]map[string]any, len(run.Items))
	for i, it := range run.Items {
		items[i] = map[string]any{"asset_id": it.AssetID, "asset_code": it.AssetCode, "amount": it.Amount.Amount()}
	}
	return created(w, map[string]any{"jalali_year": run.Year, "jalali_month": run.Month, "journal_id": run.Journal, "total": run.Total.Amount(), "items": items})
}

// legalTable lists the rows of the legal depreciation table (article 149), optionally of one ?group=.
func (a *API) legalTable(w http.ResponseWriter, r *http.Request) error {
	group := 0
	if v := r.URL.Query().Get("group"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 25 {
			return apperr.InvalidField("group", "must be 1 to 25")
		}
		group = n
	}
	var items []map[string]any
	for _, row := range fixedasset.LegalTable() {
		if group != 0 && row.Group != group {
			continue
		}
		item := map[string]any{"id": row.ID, "group": row.Group, "group_title": row.GroupTitle, "row": row.Row, "description": row.Description,
			"selectable": row.Selectable, "note": row.Note}
		if row.Selectable {
			item["method"], item["value"] = row.Method, row.Value // years for straight_line, percent for declining_balance
		}
		items = append(items, item)
	}
	return ok(w, map[string]any{"version": fixedasset.LegalTableVersion, "items": items})
}

// createCategoryFromLegalTable makes a category from a row of the legal table; the row's citation becomes the category's source.
func (a *API) createCategoryFromLegalTable(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		RowID        string `json:"row_id"`
		Code         string `json:"code"`
		Name         string `json:"name"`
		CostCoding   string `json:"cost_coding"`
		AccDepCoding string `json:"accumulated_depreciation_coding"`
	}](r)
	if err != nil {
		return err
	}
	c, err := a.m.FixedAssets.CreateCategoryFromLegalTable(r.Context(), req.RowID, req.Code, req.Name,
		fixedasset.CategoryInput{CostCoding: req.CostCoding, AccDepCoding: req.AccDepCoding}, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toAssetCategory(c))
}
