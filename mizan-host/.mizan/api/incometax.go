package api

import (
	"net/http"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/incometax"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountIncomeTax(r *httpx.Router, p string) {
	r.Route("GET "+p+"/incometax/rules", a.listITRules)
	r.Route("POST "+p+"/incometax/rules", a.addITRule)
	r.Route("POST "+p+"/incometax/preview/{fiscal_year_id}", a.itPreview)
	r.Route("POST "+p+"/incometax/assessments/{fiscal_year_id}", a.itDeclare)
	r.Route("GET "+p+"/incometax/assessments", a.listITAssessments)
	r.Route("GET "+p+"/incometax/assessments/{id}", a.getITAssessment)
	r.Route("POST "+p+"/incometax/assessments/{id}/payments", a.itPay)
	r.Route("GET "+p+"/incometax/losses", a.itLosses)
}

func itRuleDTO(x incometax.Rule) map[string]any {
	m := map[string]any{"id": x.ID, "effective_from": x.EffectiveFrom.Format("2006-01-02"), "rate_bp": x.RateBP, "cooperative_discount_bp": x.CooperativeDiscountBP,
		"filing_months": x.FilingMonths, "source": x.Source}
	if x.LossCarryYears != nil {
		m["loss_carry_years"] = *x.LossCarryYears
	}
	return m
}

func (a *API) listITRules(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.IncomeTax.Rules(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(rs))
	for i, x := range rs {
		out[i] = itRuleDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) addITRule(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		EffectiveFrom         string `json:"effective_from"`
		RateBP                int    `json:"rate_bp"`
		CooperativeDiscountBP int    `json:"cooperative_discount_bp"`
		LossCarryYears        *int   `json:"loss_carry_years"`
		FilingMonths          int    `json:"filing_months"`
		Source                string `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	from, err := optDay(req.EffectiveFrom)
	if err != nil {
		return err
	}
	x, err := a.m.IncomeTax.AddRule(r.Context(), incometax.RuleInput{EffectiveFrom: from, RateBP: req.RateBP, CooperativeDiscountBP: req.CooperativeDiscountBP,
		LossCarryYears: req.LossCarryYears, FilingMonths: req.FilingMonths, Source: req.Source, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, itRuleDTO(x))
}

type itAdjustmentsReq struct {
	AddBacks        []incometax.Item `json:"add_backs"`
	Deductions      []incometax.Item `json:"deductions"`
	DepreciationAdj int64            `json:"depreciation_adj"`
	Cooperative     bool             `json:"cooperative"`
	RateReductionBP int              `json:"rate_reduction_bp"`
	Credits         int64            `json:"credits"`
}

func (q itAdjustmentsReq) adj() incometax.Adjustments {
	return incometax.Adjustments{AddBacks: q.AddBacks, Deductions: q.Deductions, DepreciationAdj: q.DepreciationAdj, Cooperative: q.Cooperative, RateReductionBP: q.RateReductionBP, Credits: q.Credits}
}

func resultDTO(x incometax.Result) map[string]any {
	uses := make([]map[string]any, len(x.LossUsed))
	for i, u := range x.LossUsed {
		uses[i] = map[string]any{"loss_assessment_id": u.AssessmentID, "fiscal_year": u.FiscalYear, "amount": u.Amount}
	}
	return map[string]any{"accounting_profit": x.Profit, "add_back_total": x.AddBackTotal, "deduction_total": x.DeductionTotal, "depreciation_adjustment": x.DepreciationAdj,
		"taxable_before_loss": x.TaxableBeforeLoss, "losses_used": uses, "loss_used_total": x.LossUsedTotal, "new_loss": x.NewLoss, "taxable_income": x.Taxable,
		"effective_rate_bp": x.RateBP, "tax": x.Tax}
}

// itPreview: POST (it takes the adjustments as a body) the computation for a fiscal year, nothing is booked.
func (a *API) itPreview(w http.ResponseWriter, r *http.Request) error {
	id, err := yearIDParam(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[itAdjustmentsReq](r)
	if err != nil {
		return err
	}
	f, err := a.m.IncomeTaxFacts(r.Context(), id)
	if err != nil {
		return err
	}
	res, err := a.m.IncomeTax.Preview(r.Context(), f, req.adj())
	if err != nil {
		return err
	}
	m := resultDTO(res)
	m["fiscal_year"] = f.FiscalYear
	return ok(w, m)
}

func (a *API) itDeclare(w http.ResponseWriter, r *http.Request) error {
	id, err := yearIDParam(r)
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[itAdjustmentsReq](r)
	if err != nil {
		return err
	}
	x, err := a.m.DeclareIncomeTax(r.Context(), id, req.adj(), key, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, a.toAssessment(x))
}

func (a *API) toAssessment(x incometax.Assessment) map[string]any {
	m := resultDTO(x.Result)
	m["id"], m["fiscal_year"], m["from"], m["to"] = x.ID, x.FiscalYear, x.From.Format("2006-01-02"), x.To.Format("2006-01-02")
	m["credits"], m["payable"], m["paid"], m["outstanding"] = x.Credits, x.Payable, x.Paid, x.Outstanding()
	m["due_on"], m["due_on_jalali"], m["overdue"], m["journal_id"] = x.DueOn.Format("2006-01-02"), jdate(x.DueOn), a.m.IncomeTax.Overdue(x), x.Journal
	m["add_backs"], m["deductions"] = x.Adj.AddBacks, x.Adj.Deductions
	return m
}

func (a *API) listITAssessments(w http.ResponseWriter, r *http.Request) error {
	xs, err := a.m.IncomeTax.Assessments(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(xs))
	for i, x := range xs {
		out[i] = a.toAssessment(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getITAssessment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.IncomeTax.Assessment(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, a.toAssessment(x))
}

func (a *API) itPay(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
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
	in := incometax.PayInput{IdemKey: key, Amount: req.Amount, Account: req.Account, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	x, err := a.m.IncomeTax.Pay(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, a.toAssessment(x))
}

func (a *API) itLosses(w http.ResponseWriter, r *http.Request) error {
	ls, err := a.m.IncomeTax.OpenLosses(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(ls))
	for i, l := range ls {
		out[i] = map[string]any{"assessment_id": l.AssessmentID, "fiscal_year": l.FiscalYear, "remaining": l.Remaining}
	}
	return ok(w, map[string]any{"items": out})
}
