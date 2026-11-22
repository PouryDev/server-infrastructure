//nolint:staticcheck // ZWNJ is correct Persian orthography
package ledger

import (
	"context"
	"strings"
	"time"
)

// Official layouts of the financial statements.
//
// SOURCE (read from the official PDFs of the Iranian accounting standards, revised 1397, via https://thdorsan.com/storage/content/421-s1p.pdf and 421-s2p.pdf):
//   - Standard 1 (ارائه صورت‌های مالی) para. 9: the complete set is the statement of financial position, profit and loss, comprehensive income, changes in equity, cash flows and notes.
//     Para. 55: the statement of financial position must show AT LEAST: fixed tangible assets, investment property, intangible assets, financial assets, equity-method investments,
//     biological assets, inventories, trade and other receivables, orders and prepayments, cash and cash equivalents, assets held for sale, trade and other payables, provisions, financial
//     liabilities, current tax liabilities and assets, deferred tax, prepayments received, liabilities held for sale, non-controlling interests, issued capital and reserves.
//     Para. 59: no particular order or format is imposed. Paras. 62, 68, 71: assets and liabilities are split into current and non-current (current = expected to be realised or settled in the
//     operating cycle or within twelve months of the reporting date, or held for trading, or cash). Para. 83: the profit and loss statement must show at least operating revenue, finance costs, share of
//     equity-method results, tax expense, discontinued operations and profit or loss. Paras. 86-88: sub-totals are allowed when made of recognised items and presented clearly: gross profit and
//     operating profit below are such sub-totals (a presentation choice, not required lines).
//   - Standard 2 (صورت جریان‌های نقدی) paras. 6, 9-19: cash flows are classified into operating, investing and financing activities (this revision has no separate "returns on investment"
//     or "tax" sections); operating flows may be reported by the direct or indirect method; the indirect method starts from the profit and adjusts for inventory/receivable/payable
//     changes, non-cash items (depreciation, provisions, deferred tax, FX), and items whose cash effect is investing or financing.
//
// MAPPING: accounts are mapped to lines by their LEDGER CODE (the system accounts of the module have fixed codes) through the data table below (LayoutVersion). An account the table does not know
// is NOT hidden: it falls back to its type, the chart's current/non-current group (as the existing balance sheet does) and is listed in Unclassified so the accountant maps it.
// The tax-depreciation, interest and dividend classification choices of standard 2 (paras. 20 ff., not read) are therefore: dividends paid and share capital movements are financing, interest
// and tax follow the account's mapping (income tax: operating).

// LayoutVersion identifies the mapping data and the standards' revision it follows.
const LayoutVersion = "std1+std2-1397/layout-1"

// Statement line codes.
const (
	LineFixedAssets    = "fixed_assets"
	LineInventories    = "inventories"
	LineReceivables    = "receivables"
	LinePrepayments    = "prepayments"
	LineCash           = "cash"
	LineTaxAssets      = "tax_assets"
	LinePayables       = "payables"
	LineTaxLiabilities = "tax_liabilities"
	LineFinancial      = "financial_liabilities"
	LineDividends      = "dividends_payable"
	LineCapital        = "capital"
	LineReserves       = "reserves"
	LineRetained       = "retained"
	LineRevenue        = "operating_revenue"
	LineCOGS           = "cost_of_sales"
	LineOpExpense      = "operating_expense"
	LineOtherOp        = "other_operating"
	LineFinanceCost    = "finance_costs"
	LineTaxExpense     = "tax_expense"
)

var lineTitles = map[string]string{
	LineFixedAssets: "دارایی‌های ثابت مشهود (خالص)", LineInventories: "موجودی مواد و کالا", LineReceivables: "دریافتنی‌های تجاری و سایر دریافتنی‌ها",
	LinePrepayments: "سفارشات و پیش‌پرداخت‌ها", LineCash: "نقد و معادل‌های نقد", LineTaxAssets: "دارایی‌های مالیات جاری",
	LinePayables: "پرداختنی‌های تجاری و سایر پرداختنی‌ها", LineTaxLiabilities: "بدهی‌های مالیات جاری", LineFinancial: "بدهی‌های مالی", LineDividends: "سود سهام پرداختنی",
	LineCapital: "سرمایه", LineReserves: "اندوخته‌ها", LineRetained: "سود (زیان) انباشته",
	LineRevenue: "درآمدهای عملیاتی", LineCOGS: "بهای تمام‌شده کالای فروش‌رفته و خدمات ارائه‌شده", LineOpExpense: "هزینه‌های عملیاتی", LineOtherOp: "سایر اقلام عملیاتی",
	LineFinanceCost: "هزینه‌های مالی", LineTaxExpense: "هزینه مالیات بر درآمد",
}

// layoutRule maps account codes (exact, or by prefix when it ends with a dot or underscore) to a line.
type layoutRule struct{ match, line string }

var layoutRules = []layoutRule{
	{"bank.", LineCash}, {"cheques.receivable", LineReceivables}, {"receivable.", LineReceivables}, {"payroll.employee_loans", LineReceivables}, {"vat.input", LineReceivables},
	{"prepaid.income_tax", LineTaxAssets}, {"inventory.", LineInventories}, {"fixedasset.", LineFixedAssets},
	{"payable.income_tax", LineTaxLiabilities}, {"payable.dividends", LineDividends}, {"payable.", LinePayables}, {"cheques.payable", LinePayables}, {"vat.output", LinePayables}, {"vat.settlement", LinePayables},
	{"payroll.", LinePayables}, {"wallet.", LinePayables},
	{"equity.capital", LineCapital}, {"equity.legal_reserve", LineReserves}, {"equity.optional_reserve", LineReserves}, {"equity.retained_earnings", LineRetained},
	{"revenue.", LineRevenue}, {"expense.cogs", LineCOGS}, {"expense.income_tax", LineTaxExpense}, {"expense.interest", LineFinanceCost}, {"expense.asset_disposal", LineOtherOp},
	{"expense.", LineOpExpense}, {"payroll.salary_expense", LineOpExpense}, {"payroll.employer_insurance_expense", LineOpExpense},
}

// LineOf returns the line an account code is mapped to; ok is false for an unknown account.
func LineOf(code string) (string, bool) {
	// "payroll.salary_expense" style expense accounts must win over the generic "payroll." payable prefix.
	if code == "payroll.salary_expense" || code == "payroll.employer_insurance_expense" {
		return LineOpExpense, true
	}
	for _, r := range layoutRules {
		if code == r.match || (strings.HasSuffix(r.match, ".") && strings.HasPrefix(code, r.match)) || (!strings.HasSuffix(r.match, ".") && strings.HasPrefix(code, r.match+".")) {
			return r.line, true
		}
	}
	return "", false
}

// StatementLine is one titled line of an official statement.
type StatementLine struct {
	Code    string // line code
	Title   string // Persian title
	Amount  int64
	Current *bool // balance sheet lines: current (true) or non-current (false)
	Items   []BalanceLine
}

// OfficialBalanceSheet is the statement of financial position (صورت وضعیت مالی) in the layout of standard 1.
type OfficialBalanceSheet struct {
	AsOf                                                                  time.Time
	FiscalYear                                                            string
	LayoutVersion                                                         string
	CurrentAssets                                                         []StatementLine
	NonCurrentAssets                                                      []StatementLine
	CurrentLiabilities                                                    []StatementLine
	NonCurrentLiabilities                                                 []StatementLine
	Equity                                                                []StatementLine
	TotalCurrentAssets, TotalNonCurrentAssets, TotalAssets                int64
	TotalCurrentLiabilities, TotalNonCurrentLiabilities, TotalLiabilities int64
	CurrentResult                                                         int64 // the year's profit not yet closed into retained earnings
	TotalEquity                                                           int64 // equity lines + current result
	TotalClaims                                                           int64
	Balanced                                                              bool
	Unclassified                                                          []string
}

func titleOf(code string, fallback string) string {
	if t, ok := lineTitles[code]; ok {
		return t
	}
	return fallback
}

type lineBuilder struct {
	order []string
	byKey map[string]*StatementLine
}

func (b *lineBuilder) add(code, title string, bl BalanceLine) {
	if b.byKey == nil {
		b.byKey = map[string]*StatementLine{}
	}
	l, ok := b.byKey[code]
	if !ok {
		l = &StatementLine{Code: code, Title: title}
		b.byKey[code] = l
		b.order = append(b.order, code)
	}
	l.Amount += bl.Amount
	l.Items = append(l.Items, bl)
}

func (b *lineBuilder) lines() ([]StatementLine, int64) {
	var out []StatementLine
	var total int64
	for _, k := range b.order {
		out = append(out, *b.byKey[k])
		total += b.byKey[k].Amount
	}
	return out, total
}

// OfficialBalanceSheet rebuilds BalanceSheet as the grouped lines of standard 1 para. 55, each split into current and non-current. Anything the mapping does not know goes to a
// line titled "other assets/liabilities" and is listed in Unclassified.
func (s *Service) OfficialBalanceSheet(ctx context.Context, asOf *time.Time) (OfficialBalanceSheet, error) {
	bs, err := s.BalanceSheet(ctx, asOf)
	if err != nil {
		return OfficialBalanceSheet{}, err
	}
	out := OfficialBalanceSheet{AsOf: bs.AsOf, FiscalYear: bs.FiscalYear, LayoutVersion: LayoutVersion, CurrentResult: bs.CurrentResult, Unclassified: bs.Unclassified}
	build := func(sec BalanceSection, other string, nonCurrent bool) ([]StatementLine, int64) {
		var b lineBuilder
		for _, l := range sec.Lines {
			code, known := LineOf(l.Code)
			title := titleOf(code, "")
			if !known {
				code, title = other, "سایر "+map[bool]string{false: "جاری", true: "غیرجاری"}[nonCurrent]
				out.Unclassified = appendUnique(out.Unclassified, l.Code)
			}
			b.add(code, title, l)
		}
		return b.lines()
	}
	out.CurrentAssets, out.TotalCurrentAssets = build(bs.CurrentAssets, "other_assets", false)
	out.NonCurrentAssets, out.TotalNonCurrentAssets = build(bs.NonCurrentAssets, "other_assets", true)
	out.CurrentLiabilities, out.TotalCurrentLiabilities = build(bs.CurrentLiabilities, "other_liabilities", false)
	out.NonCurrentLiabilities, out.TotalNonCurrentLiabilities = build(bs.NonCurrentLiability, "other_liabilities", true)
	var eq lineBuilder
	for _, l := range bs.Equity.Lines {
		code, known := LineOf(l.Code)
		title := titleOf(code, "")
		if !known {
			code, title = "other_equity", "سایر اقلام حقوق مالکانه"
			out.Unclassified = appendUnique(out.Unclassified, l.Code)
		}
		eq.add(code, title, l)
	}
	out.Equity, out.TotalEquity = eq.lines()
	out.TotalEquity += bs.CurrentResult
	out.TotalAssets = out.TotalCurrentAssets + out.TotalNonCurrentAssets
	out.TotalLiabilities = out.TotalCurrentLiabilities + out.TotalNonCurrentLiabilities
	out.TotalClaims = out.TotalLiabilities + out.TotalEquity
	out.Balanced = out.TotalAssets == out.TotalClaims
	return out, nil
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// OfficialIncomeStatement is the profit and loss statement (صورت سود و زیان) with the items of standard 1 para. 83 and the optional sub-totals of paras. 86-88.
type OfficialIncomeStatement struct {
	From, To          *time.Time
	LayoutVersion     string
	Revenue           StatementLine
	CostOfSales       StatementLine
	GrossProfit       int64 // sub-total: revenue - cost of sales
	OperatingExpenses StatementLine
	OtherOperating    StatementLine // natural balance: an expense is positive; the line is subtracted
	OperatingProfit   int64         // sub-total
	FinanceCosts      StatementLine
	ProfitBeforeTax   int64 // sub-total
	TaxExpense        StatementLine
	NetProfit         int64
	Unclassified      []string
}

// OfficialIncomeStatement builds the multi-step statement from the books for the range (closing documents left out).
func (s *Service) OfficialIncomeStatement(ctx context.Context, from, to *time.Time) (OfficialIncomeStatement, error) {
	is, err := s.IncomeStatement(ctx, from, to, false)
	if err != nil {
		return OfficialIncomeStatement{}, err
	}
	out := OfficialIncomeStatement{From: from, To: to, LayoutVersion: LayoutVersion}
	set := func(l *StatementLine, code string) { l.Code, l.Title = code, lineTitles[code] }
	set(&out.Revenue, LineRevenue)
	set(&out.CostOfSales, LineCOGS)
	set(&out.OperatingExpenses, LineOpExpense)
	set(&out.OtherOperating, LineOtherOp)
	set(&out.FinanceCosts, LineFinanceCost)
	set(&out.TaxExpense, LineTaxExpense)
	for _, r := range is.Revenue {
		bl := BalanceLine{Code: r.Code, Name: r.Name, CodingCode: r.CodingCode, Amount: r.Balance()}
		if code, ok := LineOf(r.Code); !ok || code != LineRevenue {
			out.Unclassified = appendUnique(out.Unclassified, r.Code) // other income: kept in revenue, flagged
		}
		out.Revenue.Amount += bl.Amount
		out.Revenue.Items = append(out.Revenue.Items, bl)
	}
	for _, r := range is.Expense {
		bl := BalanceLine{Code: r.Code, Name: r.Name, CodingCode: r.CodingCode, Amount: r.Balance()}
		code, ok := LineOf(r.Code)
		if !ok {
			code = LineOpExpense
			out.Unclassified = appendUnique(out.Unclassified, r.Code)
		}
		var tgt *StatementLine
		switch code {
		case LineCOGS:
			tgt = &out.CostOfSales
		case LineOtherOp:
			tgt = &out.OtherOperating
		case LineFinanceCost:
			tgt = &out.FinanceCosts
		case LineTaxExpense:
			tgt = &out.TaxExpense
		default:
			tgt = &out.OperatingExpenses
		}
		tgt.Amount += bl.Amount
		tgt.Items = append(tgt.Items, bl)
	}
	out.GrossProfit = out.Revenue.Amount - out.CostOfSales.Amount
	out.OperatingProfit = out.GrossProfit - out.OperatingExpenses.Amount - out.OtherOperating.Amount
	out.ProfitBeforeTax = out.OperatingProfit - out.FinanceCosts.Amount
	out.NetProfit = out.ProfitBeforeTax - out.TaxExpense.Amount
	return out, nil
}

// EquityComponent is one column of the statement of changes in equity (standard 1 paras. 108-113).
type EquityComponent struct {
	Code, Title string
	Opening     int64
	Profit      int64 // retained earnings only: the profit (loss) of the period
	Other       int64 // other movements: capital injections, appropriations to reserves, dividends declared...
	Closing     int64
}

// ChangesInEquity is the statement of changes in equity for a period.
type ChangesInEquity struct {
	From, To         time.Time
	LayoutVersion    string
	Components       []EquityComponent
	NetProfit        int64
	Opening, Closing int64
}

// balancesAt returns the natural balances of every account at the end of a day, inception to date, with ALL closing and opening documents left out, so profit accumulates in the
// revenue and expense accounts and equity shows only what was posted to it by appropriations and capital movements.
func (s *Service) balancesAt(ctx context.Context, day time.Time) (map[string]TrialBalanceRow, error) {
	d := s.DateOf(day)
	rows, err := s.store.TrialBalance(ctx, TrialBalanceQuery{To: &d, ExcludeClosing: true, IncludeZero: true})
	if err != nil {
		return nil, err
	}
	m := make(map[string]TrialBalanceRow, len(rows))
	for _, r := range rows {
		m[r.Code] = r
	}
	return m, nil
}

func cumulativeProfit(m map[string]TrialBalanceRow) int64 {
	var p int64
	for _, r := range m {
		switch r.Type {
		case Revenue:
			p += r.Balance()
		case Expense:
			p -= r.Balance()
		}
	}
	return p
}

// ChangesInEquity builds the statement from two cumulative positions (the day before from, and to).
func (s *Service) ChangesInEquity(ctx context.Context, from, to time.Time) (ChangesInEquity, error) {
	start, err := s.balancesAt(ctx, from.AddDate(0, 0, -1))
	if err != nil {
		return ChangesInEquity{}, err
	}
	end, err := s.balancesAt(ctx, to)
	if err != nil {
		return ChangesInEquity{}, err
	}
	out := ChangesInEquity{From: from, To: to, LayoutVersion: LayoutVersion}
	comp := map[string]*EquityComponent{}
	var order []string
	get := func(code, title string) *EquityComponent {
		c, ok := comp[code]
		if !ok {
			c = &EquityComponent{Code: code, Title: title}
			comp[code] = c
			order = append(order, code)
		}
		return c
	}
	get(LineCapital, lineTitles[LineCapital])
	get(LineReserves, lineTitles[LineReserves])
	get(LineRetained, lineTitles[LineRetained])
	add := func(m map[string]TrialBalanceRow, opening bool) {
		for code, r := range m {
			if r.Type != Equity {
				continue
			}
			line, known := LineOf(code)
			title := lineTitles[line]
			if !known || (line != LineCapital && line != LineReserves && line != LineRetained) {
				line, title = "other_equity", "سایر اقلام حقوق مالکانه"
			}
			c := get(line, title)
			if opening {
				c.Opening += r.Balance()
			} else {
				c.Closing += r.Balance()
			}
		}
	}
	add(start, true)
	add(end, false)
	ps, pe := cumulativeProfit(start), cumulativeProfit(end)
	out.NetProfit = pe - ps
	ret := get(LineRetained, lineTitles[LineRetained])
	ret.Opening += ps
	ret.Closing += pe
	ret.Profit = out.NetProfit
	for _, k := range order {
		c := comp[k]
		c.Other = c.Closing - c.Opening - c.Profit
		out.Components = append(out.Components, *c)
		out.Opening += c.Opening
		out.Closing += c.Closing
	}
	return out, nil
}

// CashFlow is the statement of cash flows by the indirect method (standard 2 paras. 9, 17-19).
type CashFlow struct {
	From, To      time.Time
	LayoutVersion string
	NetProfit     int64
	// Operating: adjustments to the profit.
	Depreciation             int64 // added back
	DisposalLoss             int64 // loss (positive) or gain (negative) on disposal of assets, added back
	ReceivablesChange        int64 // minus the increase
	InventoriesChange        int64
	PrepaymentsChange        int64 // prepayments and tax assets
	PayablesChange           int64 // plus the increase of operating payables and tax liabilities
	OtherOperating           int64
	Operating                int64
	Investing                int64 // purchases (negative) and proceeds from fixed assets
	Financing                int64 // capital, reserves and dividends paid, financial liabilities
	NetChange                int64
	OpeningCash, ClosingCash int64
	Reconciled               bool // net change equals the change of the cash accounts
	Unclassified             []string
}

// CashFlow computes the statement from the change of every account between the day before from and to (see the file comment for the classification). Dividends paid and
// capital movements are financing; fixed-asset movements are investing; all other non-cash accounts adjust the operating flow, and unknown ones are flagged.
func (s *Service) CashFlow(ctx context.Context, from, to time.Time) (CashFlow, error) {
	start, err := s.balancesAt(ctx, from.AddDate(0, 0, -1))
	if err != nil {
		return CashFlow{}, err
	}
	end, err := s.balancesAt(ctx, to)
	if err != nil {
		return CashFlow{}, err
	}
	cf := CashFlow{From: from, To: to, LayoutVersion: LayoutVersion, NetProfit: cumulativeProfit(end) - cumulativeProfit(start)}
	chart, err := s.store.Coding(ctx)
	if err != nil {
		return CashFlow{}, err
	}
	nonCurrent := nonCurrentGroups(chart)
	codes := map[string]bool{}
	for c := range start {
		codes[c] = true
	}
	for c := range end {
		codes[c] = true
	}
	for code := range codes {
		e, st := end[code], start[code]
		typ := e.Type
		if typ == "" {
			typ = st.Type
		}
		delta := e.Balance() - st.Balance()
		line, known := LineOf(code)
		switch typ {
		case Revenue, Expense:
			if code == AccDepreciationCode {
				cf.Depreciation += delta
			}
			if code == AccDisposalCode {
				cf.DisposalLoss += delta
			}
		case Asset:
			switch {
			case line == LineCash:
				cf.OpeningCash += st.Balance()
				cf.ClosingCash += e.Balance()
			case line == LineFixedAssets:
				cf.Investing -= delta
			case line == LineReceivables:
				cf.ReceivablesChange -= delta
			case line == LineInventories:
				cf.InventoriesChange -= delta
			case line == LinePrepayments || line == LineTaxAssets:
				cf.PrepaymentsChange -= delta
			default:
				if !known {
					cf.Unclassified = appendUnique(cf.Unclassified, code)
				}
				if nonCurrent[e.CodingCode] || nonCurrent[st.CodingCode] { // an unknown non-current asset is an investment
					cf.Investing -= delta
				} else {
					cf.OtherOperating -= delta
				}
			}
		case Liability:
			switch {
			case line == LineDividends || line == LineFinancial:
				cf.Financing += delta
			case line == LinePayables || line == LineTaxLiabilities:
				cf.PayablesChange += delta
			default:
				if !known {
					cf.Unclassified = appendUnique(cf.Unclassified, code)
				}
				if nonCurrent[e.CodingCode] || nonCurrent[st.CodingCode] {
					cf.Financing += delta
				} else {
					cf.OtherOperating += delta
				}
			}
		case Equity:
			cf.Financing += delta
		}
	}
	// The profit already includes depreciation and disposal results; add them back (their cash effect is in investing).
	cf.Operating = cf.NetProfit + cf.Depreciation + cf.DisposalLoss + cf.ReceivablesChange + cf.InventoriesChange + cf.PrepaymentsChange + cf.PayablesChange + cf.OtherOperating
	cf.Investing -= cf.Depreciation + cf.DisposalLoss
	cf.NetChange = cf.Operating + cf.Investing + cf.Financing
	cf.Reconciled = cf.NetChange == cf.ClosingCash-cf.OpeningCash
	return cf, nil
}

// Codes of the accounts the cash-flow adjustments need (the system accounts of the module that root package accounts.go defines).
const (
	AccDepreciationCode = "expense.depreciation"
	AccDisposalCode     = "expense.asset_disposal"
)
