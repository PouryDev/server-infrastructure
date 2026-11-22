package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/vat"
)

func (a *API) mountVAT(r *httpx.Router, p string) {
	r.Route("POST "+p+"/vat/rules", a.addVATRule)
	r.Route("GET "+p+"/vat/rules", a.listVATRules)
	r.Route("GET "+p+"/vat/preview", a.vatPreview)
	r.Route("POST "+p+"/vat/returns", a.fileVAT)
	r.Route("GET "+p+"/vat/returns", a.listVATReturns)
	r.Route("GET "+p+"/vat/returns/{id}", a.getVATReturn)
	r.Route("GET "+p+"/vat/position", a.vatPosition)
	r.Route("POST "+p+"/vat/payments", a.vatPay)
	r.Route("POST "+p+"/vat/refunds", a.vatRefund)
}

type vatRuleDTO struct {
	ID             uuid.UUID `json:"id"`
	EffectiveFrom  string    `json:"effective_from"`
	PeriodMonths   int       `json:"period_months"`
	FilingDays     int       `json:"filing_days,omitempty"`
	FilingMonthEnd bool      `json:"filing_month_end,omitempty"`
	LatePenaltyBP  int       `json:"late_penalty_bp"`
	Source         string    `json:"source"`
}

func (a *API) addVATRule(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		EffectiveFrom  string `json:"effective_from"`
		PeriodMonths   int    `json:"period_months"`
		FilingDays     int    `json:"filing_days"`
		FilingMonthEnd bool   `json:"filing_month_end"`
		LatePenaltyBP  int    `json:"late_penalty_bp"`
		Source         string `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	from, err := optDay(req.EffectiveFrom)
	if err != nil {
		return err
	}
	x, err := a.m.VAT.AddRule(r.Context(), vat.RuleInput{EffectiveFrom: from, PeriodMonths: req.PeriodMonths, FilingDays: req.FilingDays, FilingMonthEnd: req.FilingMonthEnd,
		LatePenaltyBP: req.LatePenaltyBP, Source: req.Source, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, vatRuleDTO{x.ID, x.EffectiveFrom.Format("2006-01-02"), x.PeriodMonths, x.FilingDays, x.FilingMonthEnd, x.LatePenaltyBP, x.Source})
}

func (a *API) listVATRules(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.VAT.Rules(r.Context())
	if err != nil {
		return err
	}
	out := make([]vatRuleDTO, len(rs))
	for i, x := range rs {
		out[i] = vatRuleDTO{x.ID, x.EffectiveFrom.Format("2006-01-02"), x.PeriodMonths, x.FilingDays, x.FilingMonthEnd, x.LatePenaltyBP, x.Source}
	}
	return ok(w, map[string]any{"items": out})
}

func periodParams(r *http.Request) (year, months, no int, err error) {
	q := r.URL.Query()
	if year, err = strconv.Atoi(q.Get("year")); err != nil {
		return 0, 0, 0, apperr.InvalidField("year", "required, a Jalali year")
	}
	if no, err = strconv.Atoi(q.Get("period")); err != nil {
		return 0, 0, 0, apperr.InvalidField("period", "required, the period number in the year")
	}
	if v := q.Get("period_months"); v != "" {
		if months, err = strconv.Atoi(v); err != nil {
			return 0, 0, 0, apperr.InvalidField("period_months", "must be 1, 2 or 3")
		}
	}
	return year, months, no, nil
}

func figuresDTO(f vat.Figures) map[string]any {
	m := map[string]any{"jalali_year": f.Year, "period": f.No, "period_months": f.Months, "from": f.From.Format("2006-01-02"), "to": f.To.Format("2006-01-02"),
		"output_tax": f.Output, "input_tax": f.Input, "net": f.Net, "opening_settlement_balance": f.OpeningBalance}
	if f.DueOn != nil {
		m["due_on"] = f.DueOn.Format("2006-01-02")
		m["due_on_jalali"] = jdate(*f.DueOn)
	}
	return m
}

// vatPreview shows the figures of a period from the books without filing: ?year=&period=[&period_months=].
func (a *API) vatPreview(w http.ResponseWriter, r *http.Request) error {
	year, months, no, err := periodParams(r)
	if err != nil {
		return err
	}
	f, err := a.m.VAT.Preview(r.Context(), year, months, no)
	if err != nil {
		return err
	}
	return ok(w, figuresDTO(f))
}

func returnDTO(x vat.Return) map[string]any {
	m := figuresDTO(x.Figures)
	m["id"], m["filed_on"], m["journal_id"] = x.ID, x.FiledOn.Format("2006-01-02"), x.Journal
	return m
}

func (a *API) fileVAT(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year         int    `json:"jalali_year"`
		Period       int    `json:"period"`
		PeriodMonths int    `json:"period_months"`
		FiledOn      string `json:"filed_on"`
	}](r)
	if err != nil {
		return err
	}
	in := vat.FileInput{IdemKey: key, Year: req.Year, Period: req.Period, PeriodMonths: req.PeriodMonths, Actor: actorOf(r.Context())}
	if in.FiledOn, err = optDay(req.FiledOn); err != nil {
		return err
	}
	x, err := a.m.VAT.File(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, returnDTO(x))
}

func (a *API) listVATReturns(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.VAT.Returns(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(rs))
	for i, x := range rs {
		out[i] = returnDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getVATReturn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.VAT.Return(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, returnDTO(x))
}

// vatPosition is the balance with the tax authority: positive is owed, negative is credit held.
func (a *API) vatPosition(w http.ResponseWriter, r *http.Request) error {
	pos, err := a.m.VAT.Position(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"balance": pos, "owed": max(pos, 0), "credit": max(-pos, 0)})
}

func (a *API) vatMove(w http.ResponseWriter, r *http.Request, pay bool) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount  money.Money `json:"amount"`
		Account string      `json:"account"`
		Date    string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := vat.PayInput{IdemKey: key, Amount: req.Amount, Account: req.Account, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	var jid uuid.UUID
	if pay {
		jid, err = a.m.VAT.Pay(r.Context(), in)
	} else {
		jid, err = a.m.VAT.ReceiveRefund(r.Context(), in)
	}
	if err != nil {
		return err
	}
	return created(w, map[string]any{"journal_id": jid})
}

func (a *API) vatPay(w http.ResponseWriter, r *http.Request) error    { return a.vatMove(w, r, true) }
func (a *API) vatRefund(w http.ResponseWriter, r *http.Request) error { return a.vatMove(w, r, false) }

func (a *API) mountVATData(r *httpx.Router, p string) {
	r.Route("GET "+p+"/vat/rates", a.listVATRates)
	r.Route("POST "+p+"/vat/rates", a.addVATRate)
	r.Route("GET "+p+"/vat/compute", a.vatCompute)
	r.Route("GET "+p+"/vat/exemptions", a.listVATExemptions)
	r.Route("POST "+p+"/vat/exemptions", a.addVATExemption)
	r.Route("POST "+p+"/vat/returns/amend", a.amendVAT)
	r.Route("GET "+p+"/vat/returns/{id}/penalty", a.vatPenalty)
	r.Route("POST "+p+"/vat/apportionments", a.apportionVAT)
	r.Route("GET "+p+"/vat/apportionments", a.listVATApportionments)
}

func rateDTO(x vat.RateVersion) map[string]any {
	m := map[string]any{"id": x.ID, "category": x.Category, "effective_from": x.EffectiveFrom.Format("2006-01-02"), "rate_bp": x.RateBP, "description": x.Description, "verified": x.Verified, "source": x.Source}
	if x.LevyBP != nil {
		m["levy_bp"] = *x.LevyBP
	}
	return m
}

func (a *API) listVATRates(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.VAT.Rates(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(rs))
	for i, x := range rs {
		out[i] = rateDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) addVATRate(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Category      string `json:"category"`
		EffectiveFrom string `json:"effective_from"`
		RateBP        int    `json:"rate_bp"`
		LevyBP        *int   `json:"levy_bp"`
		Description   string `json:"description"`
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
	x, err := a.m.VAT.AddRate(r.Context(), vat.RateInput{Category: req.Category, EffectiveFrom: from, RateBP: req.RateBP, LevyBP: req.LevyBP, Description: req.Description,
		Verified: req.Verified, Source: req.Source, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, rateDTO(x))
}

// vatCompute: ?category=general&base=1000000[&date=YYYY-MM-DD] returns the tax on a base and its state/levy split when known.
func (a *API) vatCompute(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	base, err := strconv.ParseInt(q.Get("base"), 10, 64)
	if err != nil || base < 0 {
		return apperr.InvalidField("base", "required, rials before tax")
	}
	cat := q.Get("category")
	if cat == "" {
		cat = vat.CategoryGeneral
	}
	day, err := optDay(q.Get("date"))
	if err != nil {
		return err
	}
	if day.IsZero() {
		day = a.m.Ledger.Today()
	}
	sp, err := a.m.VAT.Compute(r.Context(), cat, base, day)
	if err != nil {
		return err
	}
	m := map[string]any{"category": cat, "rate_bp": sp.RateBP, "tax": sp.Total, "total": base + sp.Total}
	if sp.LevyKnown {
		m["state_share"], m["municipal_levy"] = sp.State, sp.Levy
	}
	return ok(w, m)
}

func (a *API) listVATExemptions(w http.ResponseWriter, r *http.Request) error {
	on, err := optDay(r.URL.Query().Get("on"))
	if err != nil {
		return err
	}
	es, err := a.m.VAT.Exemptions(r.Context(), on)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(es))
	for i, x := range es {
		out[i] = exemptionDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func exemptionDTO(x vat.Exemption) map[string]any {
	m := map[string]any{"id": x.ID, "code": x.Code, "kind": x.Kind, "description": x.Description, "article": x.Article, "effective_from": x.EffectiveFrom.Format("2006-01-02"), "source": x.Source}
	if x.EffectiveTo != nil {
		m["effective_to"] = x.EffectiveTo.Format("2006-01-02")
	}
	return m
}

func (a *API) addVATExemption(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code          string `json:"code"`
		Kind          string `json:"kind"`
		Description   string `json:"description"`
		Article       string `json:"article"`
		EffectiveFrom string `json:"effective_from"`
		EffectiveTo   string `json:"effective_to"`
		Source        string `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	in := vat.ExemptionInput{Code: req.Code, Kind: req.Kind, Description: req.Description, Article: req.Article, Source: req.Source, Actor: actorOf(r.Context())}
	if in.EffectiveFrom, err = optDay(req.EffectiveFrom); err != nil {
		return err
	}
	if req.EffectiveTo != "" {
		to, err := optDay(req.EffectiveTo)
		if err != nil {
			return err
		}
		in.EffectiveTo = &to
	}
	x, err := a.m.VAT.AddExemption(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, exemptionDTO(x))
}

// amendVAT files an amended return (اظهارنامه اصلاحی) for a filed period.
func (a *API) amendVAT(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year         int `json:"jalali_year"`
		Period       int `json:"period"`
		PeriodMonths int `json:"period_months"`
	}](r)
	if err != nil {
		return err
	}
	x, err := a.m.VAT.Amend(r.Context(), vat.AmendInput{IdemKey: key, Year: req.Year, Period: req.Period, Months: req.PeriodMonths, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, returnDTO(x))
}

// vatPenalty: ?as_of=YYYY-MM-DD (default today) estimates the late-payment penalty of a return's unpaid tax under the rule in force.
func (a *API) vatPenalty(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	asOf, err := optDay(r.URL.Query().Get("as_of"))
	if err != nil {
		return err
	}
	if asOf.IsZero() {
		asOf = a.m.Ledger.Today()
	}
	p, err := a.m.VAT.PenaltyFor(r.Context(), id, asOf)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"return_id": id, "as_of": asOf.Format("2006-01-02"), "unpaid": p.Unpaid, "months_late": p.Months, "penalty": p.Penalty, "penalty_bp_per_month": p.RateBP,
		"basis": "art. 37 of the VAT law of 1400/03/02: 2% per month; part-months counted as whole (estimate, confirm with the accountant)"})
}

func apportionDTO(x vat.Apportionment) map[string]any {
	return map[string]any{"id": x.ID, "jalali_year": x.Year, "period": x.No, "period_months": x.Mths, "input_total": x.InputTotal, "direct_taxable": x.DirectTaxable, "direct_exempt": x.DirectExempt,
		"machinery": x.Machinery, "common": x.Common, "taxable_sales": x.TaxableSales, "exempt_sales": x.ExemptSales, "recoverable": x.Recoverable, "non_recoverable": x.NonRecoverable, "journal_id": x.Journal}
}

func (a *API) apportionVAT(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year          int   `json:"jalali_year"`
		Period        int   `json:"period"`
		PeriodMonths  int   `json:"period_months"`
		DirectTaxable int64 `json:"direct_taxable"`
		DirectExempt  int64 `json:"direct_exempt"`
		Machinery     int64 `json:"machinery"`
		Common        int64 `json:"common"`
		TaxableSales  int64 `json:"taxable_sales"`
		ExemptSales   int64 `json:"exempt_sales"`
	}](r)
	if err != nil {
		return err
	}
	x, err := a.m.VAT.Apportion(r.Context(), vat.ApportionInput{IdemKey: key, Year: req.Year, Period: req.Period, Months: req.PeriodMonths, DirectTaxable: req.DirectTaxable,
		DirectExempt: req.DirectExempt, Machinery: req.Machinery, Common: req.Common, TaxableSales: req.TaxableSales, ExemptSales: req.ExemptSales, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, apportionDTO(x))
}

func (a *API) listVATApportionments(w http.ResponseWriter, r *http.Request) error {
	xs, err := a.m.VAT.Apportionments(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(xs))
	for i, x := range xs {
		out[i] = apportionDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}
