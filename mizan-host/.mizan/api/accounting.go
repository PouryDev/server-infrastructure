package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountAccounting(r *httpx.Router, p string) {
	r.Route("GET "+p+"/accounting/journals", a.listJournals)
	r.Route("POST "+p+"/accounting/journals", a.postJournal)
	r.Route("GET "+p+"/accounting/journals/{id}", a.getJournal)
	r.Route("POST "+p+"/accounting/journals/{id}/reverse", a.reverseJournal)
	r.Route("GET "+p+"/accounting/trial-balance", a.trialBalance)
	r.Route("GET "+p+"/accounting/accounts/{code}", a.getAccount)
	r.Route("POST "+p+"/accounting/accounts", a.createAccount)
	r.Route("GET "+p+"/accounting/accounts/{code}/statement", a.statement)
	r.Route("PUT "+p+"/accounting/accounts/{code}/coding", a.mapAccount)
	r.Route("GET "+p+"/accounting/coding", a.coding)
	r.Route("POST "+p+"/accounting/coding/iranian", a.enableIranianCoding)
	r.Route("POST "+p+"/accounting/coding/iranian-full", a.enableIranianCodingFull)
	r.Route("GET "+p+"/accounting/fiscal-years", a.fiscalYears)
	r.Route("POST "+p+"/accounting/fiscal-years", a.openFiscalYear)
	a.mountYearEnd(r, p)
	r.Route("POST "+p+"/accounting/periods/{id}/close", func(w http.ResponseWriter, r *http.Request) error { return a.setPeriod(w, r, ledger.Closed) })
	r.Route("POST "+p+"/accounting/periods/{id}/open", func(w http.ResponseWriter, r *http.Request) error { return a.setPeriod(w, r, ledger.Open) })
}

func (a *API) listJournals(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Ledger.ListJournals(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, page)
}

type journalReq struct {
	Date        string `json:"date"`
	Description string `json:"description"`
	Lines       []struct {
		Account string      `json:"account"`
		Side    string      `json:"side"`
		Amount  money.Money `json:"amount"`
		Memo    string      `json:"memo"`
	} `json:"lines"`
}

// postJournal records a manual journal (سند دستی). Automatic documents come from invoices, payments and wallets.
func (a *API) postJournal(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[journalReq](r)
	if err != nil {
		return err
	}
	date := a.m.Ledger.Today()
	if req.Date != "" {
		if date, err = parseDate(req.Date); err != nil {
			return err
		}
	}
	d := ledger.Draft{Key: "manual:" + key, Date: date, Description: req.Description, Actor: actorOf(r.Context()), Ref: ledger.Ref{Type: "manual", ID: key}}
	for _, l := range req.Lines {
		side := ledger.Debit
		switch l.Side {
		case "debit":
		case "credit":
			side = ledger.Credit
		default:
			return apperr.InvalidField("lines.side", "must be debit or credit")
		}
		d.Lines = append(d.Lines, ledger.Line{Account: l.Account, Side: side, Amount: l.Amount, Memo: l.Memo})
	}
	j, err := a.m.Ledger.Post(r.Context(), d)
	if err != nil {
		return err
	}
	return created(w, toJournal(j))
}

func (a *API) getJournal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	j, err := a.m.Ledger.Journal(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toJournal(j))
}

func (a *API) reverseJournal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string `json:"reason"`
		Date   string `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	rr := ledger.ReverseRequest{Key: "reverse:" + key, Reason: req.Reason, Actor: actorOf(r.Context())}
	if req.Date != "" {
		if rr.Date, err = parseDate(req.Date); err != nil {
			return err
		}
	}
	j, err := a.m.Ledger.Reverse(r.Context(), id, rr)
	if err != nil {
		return err
	}
	return created(w, toJournal(j))
}

type tbRowDTO struct {
	Code    string             `json:"code"`
	Name    string             `json:"name"`
	Type    ledger.AccountType `json:"type"`
	Coding  string             `json:"coding_code,omitempty"`
	Debit   int64              `json:"debit"`
	Credit  int64              `json:"credit"`
	Balance int64              `json:"balance"`
}

// trialBalance returns the trial balance (تراز آزمایشی); ?by=coding rolls it up along the chart of accounts.
func (a *API) trialBalance(w http.ResponseWriter, r *http.Request) error {
	from, err := optDate(r, "from")
	if err != nil {
		return err
	}
	to, err := optDate(r, "to")
	if err != nil {
		return err
	}
	q := ledger.TrialBalanceQuery{From: from, To: to, IncludeZero: r.URL.Query().Get("include_zero") == "true"}
	if r.URL.Query().Get("by") == "coding" {
		rep, err := a.m.Ledger.CodingTrialBalance(r.Context(), q)
		if err != nil {
			return err
		}
		return ok(w, map[string]any{"nodes": rep.Nodes, "unclassified": toTB(rep.Unclassified)})
	}
	tb, err := a.m.Ledger.TrialBalance(r.Context(), q)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"rows": toTB(tb.Rows), "debit": tb.Debit, "credit": tb.Credit, "balanced": tb.Balanced})
}

func toTB(rows []ledger.TrialBalanceRow) []tbRowDTO {
	out := make([]tbRowDTO, len(rows))
	for i, r := range rows {
		out[i] = tbRowDTO{Code: r.Code, Name: r.Name, Type: r.Type, Coding: r.CodingCode, Debit: r.Debit, Credit: r.Credit, Balance: r.Balance()}
	}
	return out
}

func (a *API) getAccount(w http.ResponseWriter, r *http.Request) error {
	acc, err := a.m.Ledger.Account(r.Context(), r.PathValue("code"))
	if err != nil {
		return err
	}
	bal, err := a.m.Ledger.Balance(r.Context(), acc.Code, nil)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"code": acc.Code, "name": acc.Name, "type": acc.Type, "currency": acc.Currency, "non_negative": acc.NonNegative,
		"coding_code": acc.CodingCode, "active": acc.Active, "balance": bal})
}

func (a *API) createAccount(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code     string             `json:"code"`
		Name     string             `json:"name"`
		Type     ledger.AccountType `json:"type"`
		Currency money.Currency     `json:"currency"`
		Coding   string             `json:"coding_code"`
	}](r)
	if err != nil {
		return err
	}
	if req.Currency == "" {
		req.Currency = money.IRR
	}
	if err := a.m.Ledger.Ensure(r.Context(), ledger.AccountSpec{Code: req.Code, Name: req.Name, Type: req.Type, Currency: req.Currency}); err != nil {
		return err
	}
	if req.Coding != "" {
		if err := a.m.Ledger.MapAccount(r.Context(), req.Code, req.Coding); err != nil {
			return err
		}
	}
	return created(w, map[string]string{"code": req.Code})
}

func (a *API) statement(w http.ResponseWriter, r *http.Request) error {
	from, err := optDate(r, "from")
	if err != nil {
		return err
	}
	to, err := optDate(r, "to")
	if err != nil {
		return err
	}
	q := ledger.StatementQuery{From: from, To: to}
	if v := r.URL.Query().Get("after"); v != "" {
		if q.AfterLineID, err = strconv.ParseInt(v, 10, 64); err != nil || q.AfterLineID < 0 {
			return apperr.InvalidField("after", "must be a line id")
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if q.Limit, err = strconv.Atoi(v); err != nil {
			return apperr.InvalidField("limit", "must be a number")
		}
	}
	st, err := a.m.Ledger.Statement(r.Context(), r.PathValue("code"), q)
	if err != nil {
		return err
	}
	type row struct {
		LineID      int64     `json:"line_id"`
		JournalID   uuid.UUID `json:"journal_id"`
		Number      int64     `json:"number"`
		FiscalYear  string    `json:"fiscal_year"`
		Date        string    `json:"date"`
		DateJalali  string    `json:"date_jalali"`
		Description string    `json:"description"`
		Memo        string    `json:"memo,omitempty"`
		RefType     string    `json:"ref_type,omitempty"`
		RefID       string    `json:"ref_id,omitempty"`
		Debit       int64     `json:"debit"`
		Credit      int64     `json:"credit"`
		Balance     int64     `json:"balance"`
	}
	rows := make([]row, len(st.Rows))
	for i, x := range st.Rows {
		rows[i] = row{x.LineID, x.JournalID, x.Number, x.FiscalYear, x.Date.Format("2006-01-02"), jdate(x.Date), x.Description, x.Memo, x.Ref.Type, x.Ref.ID, x.Debit, x.Credit, x.Running}
	}
	return ok(w, map[string]any{"account": st.Account.Code, "currency": st.Account.Currency, "opening_balance": st.Opening, "rows": rows, "next_after": st.NextAfter})
}

func (a *API) mapAccount(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Coding string `json:"coding_code"`
	}](r)
	if err != nil {
		return err
	}
	if err := a.m.Ledger.MapAccount(r.Context(), r.PathValue("code"), req.Coding); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (a *API) coding(w http.ResponseWriter, r *http.Request) error {
	nodes, err := a.m.Ledger.Coding(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"nodes": nodes})
}

func (a *API) enableIranianCoding(w http.ResponseWriter, r *http.Request) error {
	if err := a.m.EnableIranianCoding(r.Context()); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (a *API) enableIranianCodingFull(w http.ResponseWriter, r *http.Request) error {
	if err := a.m.EnableIranianCodingFull(r.Context()); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (a *API) fiscalYears(w http.ResponseWriter, r *http.Request) error {
	fys, err := a.m.Ledger.FiscalYears(r.Context())
	if err != nil {
		return err
	}
	type period struct {
		ID     int64         `json:"id"`
		Number int           `json:"number"`
		From   string        `json:"from"`
		To     string        `json:"to"`
		Status ledger.Status `json:"status"`
	}
	type year struct {
		ID      int64         `json:"id"`
		Name    string        `json:"name"`
		From    string        `json:"from"`
		To      string        `json:"to"`
		Status  ledger.Status `json:"status"`
		Periods []period      `json:"periods"`
	}
	out := make([]year, len(fys))
	for i, f := range fys {
		out[i] = year{ID: f.ID, Name: f.Name, From: f.StartsOn.Format("2006-01-02"), To: f.EndsBefore.AddDate(0, 0, -1).Format("2006-01-02"), Status: f.Status}
		for _, p := range f.Periods {
			out[i].Periods = append(out[i].Periods, period{p.ID, p.Number, p.StartsOn.Format("2006-01-02"), p.EndsBefore.AddDate(0, 0, -1).Format("2006-01-02"), p.Status})
		}
	}
	return ok(w, out)
}

func (a *API) openFiscalYear(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		JalaliYear int `json:"jalali_year"`
	}](r)
	if err != nil {
		return err
	}
	fy, err := a.m.Ledger.OpenFiscalYear(r.Context(), req.JalaliYear)
	if err != nil {
		return err
	}
	return created(w, map[string]any{"id": fy.ID, "name": fy.Name})
}

func (a *API) setPeriod(w http.ResponseWriter, r *http.Request, st ledger.Status) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return apperr.Invalid("invalid period id")
	}
	if err := a.m.Ledger.SetPeriodStatus(r.Context(), id, st); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
