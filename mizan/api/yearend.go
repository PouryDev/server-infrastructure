package api

import (
	"net/http"
	"strconv"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

func (a *API) mountYearEnd(r *httpx.Router, p string) {
	r.Route("GET "+p+"/accounting/fiscal-years/{id}/close-preview", a.closePreview)
	r.Route("POST "+p+"/accounting/fiscal-years/{id}/close", a.closeYear)
	r.Route("POST "+p+"/accounting/fiscal-years/{id}/reopen", a.reopenYear)
	r.Route("GET "+p+"/accounting/income-statement", a.incomeStatement)
	r.Route("GET "+p+"/accounting/balance-sheet", a.balanceSheet)
}

func fiscalYearID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Invalid("invalid fiscal year id")
	}
	return id, nil
}

type planLineDTO struct {
	Account string `json:"account"`
	Side    string `json:"side"`
	Amount  int64  `json:"amount"`
}

func toPlanLines(ls []ledger.Line) []planLineDTO {
	out := make([]planLineDTO, len(ls))
	for i, l := range ls {
		out[i] = planLineDTO{l.Account, l.Side.String(), l.Amount.Amount()}
	}
	return out
}

// toPlanDocs shows each document of a plan (one unless the accounts were split) with its lines.
func toPlanDocs(docs [][]ledger.Line) [][]planLineDTO {
	out := make([][]planLineDTO, len(docs))
	for i, d := range docs {
		out[i] = toPlanLines(d)
	}
	return out
}

func (a *API) closePreview(w http.ResponseWriter, r *http.Request) error {
	id, err := fiscalYearID(r)
	if err != nil {
		return err
	}
	p, err := a.m.Ledger.PreviewYearEnd(r.Context(), id, ledger.YearEndOptions{RetainedAccount: r.URL.Query().Get("retained_account")})
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"fiscal_year": p.FiscalYear, "next_fiscal_year": p.NextYear, "currency": p.Currency, "retained_account": p.RetainedAccount,
		"summary_account": p.SummaryAccount, "total_revenue": p.TotalRevenue, "total_expense": p.TotalExpense, "net_profit": p.NetProfit,
		"can_close": len(p.Blockers) == 0, "blockers": p.Blockers,
		"temporary_documents": toPlanDocs(p.Temporary), "transfer_lines": toPlanLines(p.Transfer),
		"closing_documents": toPlanDocs(p.Closing), "opening_documents": toPlanDocs(p.Opening),
	})
}

type yearEndDTO struct {
	FiscalYear    string       `json:"fiscal_year"`
	Generation    int          `json:"generation"`
	AlreadyClosed bool         `json:"already_closed"`
	NetProfit     int64        `json:"net_profit"`
	Temporary     []journalDTO `json:"temporary_closing"`
	Transfer      *journalDTO  `json:"retained_earnings_transfer,omitempty"`
	Closing       []journalDTO `json:"closing"`
	Opening       []journalDTO `json:"opening"`
}

func toYearEnd(res ledger.YearEndResult) yearEndDTO {
	conv := func(js []ledger.Journal) []journalDTO {
		out := make([]journalDTO, len(js))
		for i, j := range js {
			out[i] = toJournal(j)
		}
		return out
	}
	d := yearEndDTO{FiscalYear: res.FiscalYear, Generation: res.Generation, AlreadyClosed: res.AlreadyClosed, NetProfit: res.NetProfit,
		Temporary: conv(res.Temporary), Closing: conv(res.Closing), Opening: conv(res.Opening)}
	if res.Transfer != nil {
		t := toJournal(*res.Transfer)
		d.Transfer = &t
	}
	return d
}

// closeYear closes a fiscal year: closing documents, the next year's opening document, then the year is locked.
func (a *API) closeYear(w http.ResponseWriter, r *http.Request) error {
	if err := a.m.RequireNoApproval(); err != nil {
		return err
	}
	id, err := fiscalYearID(r)
	if err != nil {
		return err
	}
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		RetainedAccount string `json:"retained_account"`
	}](r)
	if err != nil {
		return err
	}
	res, err := a.m.Ledger.CloseYear(r.Context(), id, ledger.CloseRequest{RetainedAccount: req.RetainedAccount, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	if res.AlreadyClosed {
		return ok(w, toYearEnd(res))
	}
	return created(w, toYearEnd(res))
}

func (a *API) reopenYear(w http.ResponseWriter, r *http.Request) error {
	id, err := fiscalYearID(r)
	if err != nil {
		return err
	}
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	res, err := a.m.Ledger.ReopenYear(r.Context(), id, ledger.ReopenRequest{Reason: req.Reason, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return ok(w, toYearEnd(res))
}

// incomeStatement (صورت سود و زیان) leaves year-end closing documents out unless ?include_closing=true.
func (a *API) incomeStatement(w http.ResponseWriter, r *http.Request) error {
	from, err := optDate(r, "from")
	if err != nil {
		return err
	}
	to, err := optDate(r, "to")
	if err != nil {
		return err
	}
	st, err := a.m.Ledger.IncomeStatement(r.Context(), from, to, r.URL.Query().Get("include_closing") == "true")
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"revenue": toTB(st.Revenue), "expense": toTB(st.Expense),
		"total_revenue": st.TotalRevenue, "total_expense": st.TotalExpense, "net": st.Net})
}

func toBalanceSection(sec ledger.BalanceSection) map[string]any {
	lines := make([]map[string]any, len(sec.Lines))
	for i, l := range sec.Lines {
		lines[i] = map[string]any{"account": l.Code, "name": l.Name, "coding_code": l.CodingCode, "amount": l.Amount}
	}
	return map[string]any{"lines": lines, "total": sec.Total}
}

// balanceSheet (ترازنامه) as of ?as_of= (default today). Current and non-current follow the chart of accounts.
func (a *API) balanceSheet(w http.ResponseWriter, r *http.Request) error {
	asOf, err := optDate(r, "as_of")
	if err != nil {
		return err
	}
	bs, err := a.m.Ledger.BalanceSheet(r.Context(), asOf)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{
		"as_of": bs.AsOf.Format("2006-01-02"), "as_of_jalali": jdate(bs.AsOf), "fiscal_year": bs.FiscalYear,
		"current_assets": toBalanceSection(bs.CurrentAssets), "non_current_assets": toBalanceSection(bs.NonCurrentAssets),
		"current_liabilities": toBalanceSection(bs.CurrentLiabilities), "non_current_liabilities": toBalanceSection(bs.NonCurrentLiability),
		"equity": toBalanceSection(bs.Equity), "current_result": bs.CurrentResult,
		"total_assets": bs.TotalAssets, "total_liabilities_and_equity": bs.TotalClaims, "balanced": bs.Balanced, "unclassified_accounts": bs.Unclassified,
	})
}
