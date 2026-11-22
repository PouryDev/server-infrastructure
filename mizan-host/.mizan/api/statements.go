package api

import (
	"net/http"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

func (a *API) mountStatements(r *httpx.Router, p string) {
	r.Route("GET "+p+"/accounting/official/balance-sheet", a.officialBalanceSheet)
	r.Route("GET "+p+"/accounting/official/income-statement", a.officialIncomeStatement)
	r.Route("GET "+p+"/accounting/official/changes-in-equity", a.officialEquity)
	r.Route("GET "+p+"/accounting/official/cash-flow", a.officialCashFlow)
}

func officialLine(l ledger.StatementLine) map[string]any {
	items := make([]map[string]any, len(l.Items))
	for i, x := range l.Items {
		items[i] = map[string]any{"account": x.Code, "name": x.Name, "coding_code": x.CodingCode, "amount": x.Amount}
	}
	return map[string]any{"code": l.Code, "title": l.Title, "amount": l.Amount, "accounts": items}
}

func officialLines(ls []ledger.StatementLine) []map[string]any {
	out := make([]map[string]any, len(ls))
	for i, l := range ls {
		out[i] = officialLine(l)
	}
	return out
}

func rangeParams(r *http.Request) (from, to time.Time, err error) {
	q := r.URL.Query()
	if from, err = parseDate(q.Get("from")); err != nil {
		return from, to, apperr.InvalidField("from", "required, YYYY-MM-DD")
	}
	if to, err = parseDate(q.Get("to")); err != nil {
		return from, to, apperr.InvalidField("to", "required, YYYY-MM-DD")
	}
	if to.Before(from) {
		return from, to, apperr.InvalidField("to", "must not be before from")
	}
	return from, to, nil
}

// officialBalanceSheet: the statement of financial position in the layout of accounting standard 1 (revised 1397): ?as_of=YYYY-MM-DD (default today).
func (a *API) officialBalanceSheet(w http.ResponseWriter, r *http.Request) error {
	var asOf *time.Time
	if v := r.URL.Query().Get("as_of"); v != "" {
		d, err := parseDate(v)
		if err != nil {
			return err
		}
		asOf = &d
	}
	b, err := a.m.Ledger.OfficialBalanceSheet(r.Context(), asOf)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"as_of": b.AsOf.Format("2006-01-02"), "fiscal_year": b.FiscalYear, "layout_version": b.LayoutVersion,
		"current_assets": officialLines(b.CurrentAssets), "total_current_assets": b.TotalCurrentAssets,
		"non_current_assets": officialLines(b.NonCurrentAssets), "total_non_current_assets": b.TotalNonCurrentAssets, "total_assets": b.TotalAssets,
		"current_liabilities": officialLines(b.CurrentLiabilities), "total_current_liabilities": b.TotalCurrentLiabilities,
		"non_current_liabilities": officialLines(b.NonCurrentLiabilities), "total_non_current_liabilities": b.TotalNonCurrentLiabilities, "total_liabilities": b.TotalLiabilities,
		"equity": officialLines(b.Equity), "current_result": b.CurrentResult, "total_equity": b.TotalEquity, "total_liabilities_and_equity": b.TotalClaims,
		"balanced": b.Balanced, "unclassified_accounts": b.Unclassified})
}

// officialIncomeStatement: the profit and loss statement, multi-step: ?from=&to=.
func (a *API) officialIncomeStatement(w http.ResponseWriter, r *http.Request) error {
	from, to, err := rangeParams(r)
	if err != nil {
		return err
	}
	s, err := a.m.Ledger.OfficialIncomeStatement(r.Context(), &from, &to)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "layout_version": s.LayoutVersion,
		"operating_revenue": officialLine(s.Revenue), "cost_of_sales": officialLine(s.CostOfSales), "gross_profit": s.GrossProfit,
		"operating_expenses": officialLine(s.OperatingExpenses), "other_operating": officialLine(s.OtherOperating), "operating_profit": s.OperatingProfit,
		"finance_costs": officialLine(s.FinanceCosts), "profit_before_tax": s.ProfitBeforeTax, "tax_expense": officialLine(s.TaxExpense), "net_profit": s.NetProfit,
		"unclassified_accounts": s.Unclassified,
		"note":                  "gross profit, operating profit and profit before tax are optional sub-totals (standard 1 paras. 86-88); the required items are para. 83"})
}

// officialEquity: the statement of changes in equity: ?from=&to=.
func (a *API) officialEquity(w http.ResponseWriter, r *http.Request) error {
	from, to, err := rangeParams(r)
	if err != nil {
		return err
	}
	c, err := a.m.Ledger.ChangesInEquity(r.Context(), from, to)
	if err != nil {
		return err
	}
	comps := make([]map[string]any, len(c.Components))
	for i, x := range c.Components {
		comps[i] = map[string]any{"code": x.Code, "title": x.Title, "opening": x.Opening, "profit": x.Profit, "other_movements": x.Other, "closing": x.Closing}
	}
	return ok(w, map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "layout_version": c.LayoutVersion, "components": comps,
		"net_profit": c.NetProfit, "opening_total": c.Opening, "closing_total": c.Closing})
}

// officialCashFlow: the statement of cash flows, indirect method (standard 2): ?from=&to=.
func (a *API) officialCashFlow(w http.ResponseWriter, r *http.Request) error {
	from, to, err := rangeParams(r)
	if err != nil {
		return err
	}
	c, err := a.m.Ledger.CashFlow(r.Context(), from, to)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"from": from.Format("2006-01-02"), "to": to.Format("2006-01-02"), "layout_version": c.LayoutVersion, "method": "indirect",
		"operating": map[string]any{"net_profit": c.NetProfit, "depreciation": c.Depreciation, "disposal_loss": c.DisposalLoss, "receivables_change": c.ReceivablesChange,
			"inventories_change": c.InventoriesChange, "prepayments_and_tax_assets_change": c.PrepaymentsChange, "payables_change": c.PayablesChange, "other": c.OtherOperating, "total": c.Operating},
		"investing": c.Investing, "financing": c.Financing, "net_change_in_cash": c.NetChange, "opening_cash": c.OpeningCash, "closing_cash": c.ClosingCash,
		"reconciled": c.Reconciled, "unclassified_accounts": c.Unclassified})
}
