package mizan_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

var (
	day1405   = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	endOf1405 = time.Date(2027, 3, 20, 0, 0, 0, 0, time.UTC) // Jalali 1405/12/29, the last day
	startOf06 = time.Date(2027, 3, 21, 0, 0, 0, 0, time.UTC) // 1406/01/01
	afterYear = time.Date(2027, 3, 25, 10, 0, 0, 0, time.UTC)
)

func (e env) post(t *testing.T, key string, date time.Time, lines ...ledger.Line) ledger.Journal {
	t.Helper()
	j, err := e.m.Ledger.Post(e.ctx, ledger.Draft{Key: key, Date: date, Description: key, Lines: lines})
	must(t, err)
	return j
}

func (e env) fiscalYear(t *testing.T, name string) ledger.FiscalYear {
	t.Helper()
	all, err := e.m.Ledger.FiscalYears(e.ctx)
	must(t, err)
	for _, f := range all {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("fiscal year %s not found", name)
	return ledger.FiscalYear{}
}

// yearOfBooks posts a small business into 1405: capital, a wallet top-up, a sale, a fee and salaries.
func (e env) yearOfBooks(t *testing.T) {
	t.Helper()
	must(t, e.m.Ledger.Ensure(e.ctx,
		ledger.AccountSpec{Code: "equity.capital", Name: "Capital", Type: ledger.Equity, Currency: "IRR", CodingCode: ""},
		ledger.AccountSpec{Code: "liab.wallet.1", Name: "Wallet 1", Type: ledger.Liability, Currency: "IRR", NonNegative: true},
		ledger.AccountSpec{Code: "asset.accdep", Name: "Accumulated depreciation", Type: ledger.Asset, Currency: "IRR"}))
	e.post(t, "ye:capital", day1405, ledger.Dr(mizan.AccBank, irr(10_000_000)), ledger.Cr("equity.capital", irr(10_000_000)))
	e.post(t, "ye:topup", day1405, ledger.Dr(mizan.AccBank, irr(1_000_000)), ledger.Cr("liab.wallet.1", irr(1_000_000)))
	e.post(t, "ye:sale", day1405, ledger.Dr(mizan.AccBank, irr(3_000_000)), ledger.Cr(mizan.AccRevenue, irr(3_000_000)))
	e.post(t, "ye:fee", day1405, ledger.Dr(mizan.AccBankFees, irr(500_000)), ledger.Cr(mizan.AccBank, irr(500_000)))
	e.post(t, "ye:salary", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(700_000)), ledger.Cr(mizan.AccSalariesPayable, irr(700_000)))
	e.post(t, "ye:dep", day1405, ledger.Dr(mizan.AccAdjustments, irr(200_000)), ledger.Cr("asset.accdep", irr(200_000))) // contra asset: a credit balance
}

func (e env) closeBalances(t *testing.T) map[string]int64 {
	t.Helper()
	end := endOf1405
	tb, err := e.m.Ledger.TrialBalance(e.ctx, ledger.TrialBalanceQuery{To: &end, ExcludeClosing: true})
	must(t, err)
	out := map[string]int64{}
	for _, r := range tb.Rows {
		if r.Type != ledger.Revenue && r.Type != ledger.Expense {
			out[r.Code] = r.Debit - r.Credit
		}
	}
	return out
}

func TestYearEndClosingAndOpening(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	fy := e.fiscalYear(t, "1405")

	// The year is not over: the preview says so and closing is refused.
	p, err := e.m.Ledger.PreviewYearEnd(e.ctx, fy.ID, ledger.YearEndOptions{})
	must(t, err)
	if len(p.Blockers) == 0 || p.NetProfit != 3_000_000-500_000-700_000-200_000 {
		t.Fatalf("preview: %+v", p)
	}
	if _, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("closing a running year must be refused, got %v", err)
	}

	e.set(afterYear)
	before := e.closeBalances(t)
	incomeBefore, err := e.m.Ledger.IncomeStatement(e.ctx, nil, nil, false)
	must(t, err)

	p, err = e.m.Ledger.PreviewYearEnd(e.ctx, fy.ID, ledger.YearEndOptions{})
	must(t, err)
	if len(p.Blockers) != 0 {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	res, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{Actor: "tester"})
	must(t, err)
	if res.AlreadyClosed || len(res.Temporary) != 1 || res.Transfer == nil || len(res.Closing) != 1 || len(res.Opening) != 1 {
		t.Fatalf("result: %+v", res)
	}
	if res.Temporary[0].Kind != ledger.KindClosing || res.Opening[0].Kind != ledger.KindOpening || res.NetProfit != 1_600_000 {
		t.Fatalf("kinds or profit: %+v", res)
	}
	if !res.Closing[0].Date.Equal(endOf1405) || !res.Opening[0].Date.Equal(startOf06) || res.Opening[0].FiscalYearID == fy.ID {
		t.Fatalf("dates: closing %v opening %v", res.Closing[0].Date, res.Opening[0].Date)
	}
	e.balanced(t)

	// Revenue and expense accounts are zero, retained earnings holds the profit, the summary account is empty.
	for _, code := range []string{mizan.AccRevenue, mizan.AccBankFees, mizan.AccSalaryExpense, mizan.AccAdjustments, mizan.AccProfitLossSummary} {
		if b := e.bal(t, code); b != 0 {
			t.Fatalf("%s must be zero after closing, got %d", code, b)
		}
	}
	if b := e.bal(t, mizan.AccRetainedEarnings); b != 1_600_000 {
		t.Fatalf("retained earnings %d", b)
	}
	// The non-negative wallet balance survives the close and the open.
	if b := e.bal(t, "liab.wallet.1"); b != 1_000_000 {
		t.Fatalf("wallet balance %d", b)
	}

	// The income statement of the closed year is unchanged by the closing journals; they show only on request.
	incomeAfter, err := e.m.Ledger.IncomeStatement(e.ctx, nil, nil, false)
	must(t, err)
	if incomeAfter.Net != incomeBefore.Net || incomeAfter.TotalRevenue != incomeBefore.TotalRevenue || incomeAfter.TotalExpense != incomeBefore.TotalExpense || incomeAfter.Net != 1_600_000 {
		t.Fatalf("income statement changed: before %+v after %+v", incomeBefore, incomeAfter)
	}
	withClosing, err := e.m.Ledger.IncomeStatement(e.ctx, nil, nil, true)
	must(t, err)
	if withClosing.Net != 0 {
		t.Fatalf("with the closing journals the result is zero, got %d", withClosing.Net)
	}

	// The new year's opening balances equal the old year's closing balances of the permanent accounts.
	open, close := startOf06, startOf06
	tb, err := e.m.Ledger.TrialBalance(e.ctx, ledger.TrialBalanceQuery{From: &open, To: &close})
	must(t, err)
	got := map[string]int64{}
	for _, r := range tb.Rows {
		got[r.Code] = r.Debit - r.Credit
	}
	retained := before[mizan.AccRetainedEarnings] - 1_600_000 // the profit joined retained earnings (a credit)
	want := map[string]int64{}
	for k, v := range before {
		want[k] = v
	}
	want[mizan.AccRetainedEarnings] = retained
	for code, v := range want {
		if v != 0 && got[code] != v {
			t.Fatalf("opening balance of %s is %d, want %d", code, got[code], v)
		}
	}
	for code, v := range got {
		if v != want[code] {
			t.Fatalf("unexpected opening balance of %s: %d", code, v)
		}
	}
	if !tb.Balanced {
		t.Fatal("the opening trial balance must balance")
	}

	// Nothing can be posted into the closed year; the new year works and starts clean.
	_, err = e.m.Ledger.Post(e.ctx, ledger.Draft{Key: "late", Date: day1405, Description: "late", Lines: []ledger.Line{ledger.Dr(mizan.AccBank, irr(1)), ledger.Cr(mizan.AccRevenue, irr(1))}})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("posting into a closed year must be refused, got %v", err)
	}
	e.post(t, "new-year-sale", afterYear, ledger.Dr(mizan.AccBank, irr(5)), ledger.Cr(mizan.AccRevenue, irr(5)))
	if e.fiscalYear(t, "1405").Status != ledger.Closed || e.fiscalYear(t, "1406").Status != ledger.Open {
		t.Fatal("year states")
	}

	// The closing left an audit entry and an event in the same transaction, once.
	q := e.m.DB.Querier(e.ctx)
	if n, err := postgres.Value[int](e.ctx, q, `SELECT count(*) FROM mz_outbox WHERE topic = 'ledger.fiscal_year_closed'`); err != nil || n != 1 {
		t.Fatalf("closing events: %d %v", n, err)
	}
	if n, err := postgres.Value[int](e.ctx, q, `SELECT count(*) FROM mz_audit_log WHERE action = 'fiscal_year.close' AND actor = 'tester'`); err != nil || n != 1 {
		t.Fatalf("closing audit entries: %d %v", n, err)
	}

	// Closing again returns the same documents and posts nothing.
	again, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)
	if !again.AlreadyClosed || again.Closing[0].ID != res.Closing[0].ID || again.Opening[0].ID != res.Opening[0].ID {
		t.Fatalf("second close: %+v", again)
	}
}

func TestYearEndConcurrentCloseMakesOneSet(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	fy := e.fiscalYear(t, "1405")
	e.set(afterYear)

	var wg sync.WaitGroup
	results := make([]ledger.YearEndResult, 6)
	errs := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{Actor: "tester"})
		}()
	}
	wg.Wait()
	fresh := 0
	for i, err := range errs {
		must(t, err)
		if !results[i].AlreadyClosed {
			fresh++
		}
		if len(results[i].Opening) != 1 || results[i].Opening[0].ID != results[0].Opening[0].ID {
			t.Fatal("every request must see the same opening journal")
		}
	}
	if fresh != 1 {
		t.Fatalf("exactly one request closes the year, %d did", fresh)
	}
	js, err := e.m.Ledger.JournalsFor(e.ctx, ledger.Ref{Type: ledger.RefYearClosing, ID: fmt.Sprintf("%d:1", fy.ID)})
	must(t, err)
	kinds := map[ledger.Kind]int{}
	for _, j := range js {
		kinds[j.Kind]++
	}
	if kinds[ledger.KindClosing] != 3 || kinds[ledger.KindOpening] != 1 || len(js) != 4 {
		t.Fatalf("journals: %v", kinds)
	}
	e.balanced(t)
}

func TestYearEndReopen(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	fy := e.fiscalYear(t, "1405")
	e.set(afterYear)

	if _, err := e.m.Ledger.ReopenYear(e.ctx, fy.ID, ledger.ReopenRequest{}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("reopening a year that was never closed: %v", err)
	}
	_, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)

	// A posting in the new year (besides the opening document) blocks the reopening.
	e.post(t, "n1", afterYear, ledger.Dr(mizan.AccBank, irr(5)), ledger.Cr(mizan.AccRevenue, irr(5)))
	if _, err := e.m.Ledger.ReopenYear(e.ctx, fy.ID, ledger.ReopenRequest{Reason: "x"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("reopening after postings in the next year must be refused: %v", err)
	}
	if e.fiscalYear(t, "1405").Status != ledger.Closed {
		t.Fatal("a refused reopening must change nothing")
	}
}

func TestYearEndReopenCorrectAndCloseAgain(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	fy := e.fiscalYear(t, "1405")
	e.set(afterYear)
	first, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)

	res, err := e.m.Ledger.ReopenYear(e.ctx, fy.ID, ledger.ReopenRequest{Reason: "missed an invoice", Actor: "tester"})
	must(t, err)
	if res.Generation != 1 || e.fiscalYear(t, "1405").Status != ledger.Open {
		t.Fatalf("reopen: %+v", res)
	}
	e.balanced(t)
	// Reversing everything restores the books as they were before the closing, and the originals are untouched.
	if b := e.bal(t, mizan.AccRevenue); b != 3_000_000 {
		t.Fatalf("revenue after reopening %d", b)
	}
	if b := e.bal(t, "liab.wallet.1"); b != 1_000_000 {
		t.Fatalf("wallet balance after reopening %d", b)
	}
	old, err := e.m.Ledger.Journal(e.ctx, first.Closing[0].ID)
	must(t, err)
	if old.Kind != ledger.KindClosing {
		t.Fatal("the closing journal must stay")
	}
	income, err := e.m.Ledger.IncomeStatement(e.ctx, nil, nil, false)
	must(t, err)
	if income.Net != 1_600_000 {
		t.Fatalf("income statement after reopening %d", income.Net)
	}

	// Correct the year, then close again: new documents, the new numbers.
	e.post(t, "ye:missed", endOf1405, ledger.Dr(mizan.AccBank, irr(400_000)), ledger.Cr(mizan.AccRevenue, irr(400_000)))
	second, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)
	if second.Generation != 2 || second.AlreadyClosed || second.NetProfit != 2_000_000 || second.Closing[0].ID == first.Closing[0].ID {
		t.Fatalf("second closing: %+v", second)
	}
	if b := e.bal(t, mizan.AccRetainedEarnings); b != 2_000_000 {
		t.Fatalf("retained earnings %d", b)
	}
	if b := e.bal(t, "liab.wallet.1"); b != 1_000_000 {
		t.Fatalf("wallet balance %d", b)
	}
	e.balanced(t)
	income, err = e.m.Ledger.IncomeStatement(e.ctx, nil, nil, false)
	must(t, err)
	if income.Net != 2_000_000 {
		t.Fatalf("income statement after the second closing %d", income.Net)
	}
}

func TestYearEndWithoutChartAndWithManyAccounts(t *testing.T) {
	db := testdb.New(t)
	cur := now
	// A small chunk size forces the documents to be split over several journals.
	m, err := mizan.New(context.Background(), db, mizan.Config{YearEndChunk: 100}, mizan.WithClock(func() time.Time { return cur }))
	must(t, err)
	e := env{m: m, ctx: context.Background()}

	const n = 600
	specs := make([]ledger.AccountSpec, 0, n)
	for i := 0; i < n; i++ {
		specs = append(specs, ledger.AccountSpec{Code: fmt.Sprintf("asset.many.%04d", i), Name: "Many", Type: ledger.Asset, Currency: "IRR"})
	}
	specs = append(specs, ledger.AccountSpec{Code: "equity.capital", Name: "Capital", Type: ledger.Equity, Currency: "IRR"})
	must(t, m.Ledger.Ensure(e.ctx, specs...))
	lines := []ledger.Line{ledger.Cr("equity.capital", irr(int64(n)*10))}
	for i := 0; i < n; i++ {
		lines = append(lines, ledger.Dr(specs[i].Code, irr(10)))
	}
	e.post(t, "many", day1405, lines...)
	e.post(t, "sale", day1405, ledger.Dr(specs[0].Code, irr(100)), ledger.Cr(mizan.AccRevenue, irr(100)))
	cur = afterYear

	fy := e.fiscalYear(t, "1405")
	p, err := m.Ledger.PreviewYearEnd(e.ctx, fy.ID, ledger.YearEndOptions{})
	must(t, err)
	if len(p.Blockers) != 0 || len(p.Closing) != 7 || len(p.Opening) != 7 { // 602 accounts in documents of 99 + transit
		t.Fatalf("preview: closing %d opening %d blockers %v", len(p.Closing), len(p.Opening), p.Blockers)
	}
	res, err := m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)
	// The system retained-earnings account is used without a chart; nothing needs a coding code.
	acct, err := m.Ledger.Account(e.ctx, mizan.AccRetainedEarnings)
	must(t, err)
	if acct.CodingCode != "" {
		t.Fatalf("no chart, no coding code: %q", acct.CodingCode)
	}
	if len(res.Closing) != 7 || len(res.Opening) != 7 {
		t.Fatalf("documents: closing %d opening %d", len(res.Closing), len(res.Opening))
	}
	carried := 0
	for _, j := range res.Opening {
		if len(j.Lines) > 100 {
			t.Fatalf("opening document with %d lines", len(j.Lines))
		}
		for _, l := range j.Lines {
			if l.AccountCode != mizan.AccOpeningBalance {
				carried++
			}
		}
	}
	if carried != n+2 { // every asset, capital and retained earnings
		t.Fatalf("opening carries %d accounts", carried)
	}
	e.balanced(t)
	// The transit accounts net to zero over all parts, and the balances are intact.
	for _, code := range []string{mizan.AccClosingBalance, mizan.AccOpeningBalance} {
		if b := e.bal(t, code); b != 0 {
			t.Fatalf("%s must net to zero, got %d", code, b)
		}
	}
	if b := e.bal(t, specs[0].Code); b != 110 {
		t.Fatalf("balance %d", b)
	}
	open := startOf06
	tb, err := m.Ledger.TrialBalance(e.ctx, ledger.TrialBalanceQuery{From: &open, To: &open})
	must(t, err)
	if !tb.Balanced {
		t.Fatal("the opening day must balance")
	}

	// Reopening reverses every part.
	_, err = m.Ledger.ReopenYear(e.ctx, fy.ID, ledger.ReopenRequest{Reason: "check", Actor: "tester"})
	must(t, err)
	e.balanced(t)
	if b := e.bal(t, mizan.AccRevenue); b != 100 {
		t.Fatalf("revenue after reopening %d", b)
	}
}

func TestYearEndFullChart(t *testing.T) {
	db := testdb.New(t)
	m, err := mizan.New(context.Background(), db, mizan.Config{IranianCodingFull: true}, mizan.WithClock(func() time.Time { return now }))
	must(t, err)
	e := env{m: m, ctx: context.Background()}
	for code, want := range map[string]string{mizan.AccBank: "111005", mizan.AccRetainedEarnings: "311301", mizan.AccProfitLossSummary: "311401", mizan.AccPayrollTaxPayable: "211202", mizan.AccClosingBalance: ""} {
		a, err := m.Ledger.Account(e.ctx, code)
		must(t, err)
		if a.CodingCode != want {
			t.Fatalf("%s is mapped to %q, want %q", code, a.CodingCode, want)
		}
	}
	if nodes, err := m.Ledger.Coding(e.ctx); err != nil || len(nodes) != 362 {
		t.Fatalf("chart: %d nodes, %v", len(nodes), err)
	}
	// The coding trial balance rolls up along the chart.
	e.post(t, "x", day1405, ledger.Dr(mizan.AccBank, irr(50)), ledger.Cr(mizan.AccRevenue, irr(50)))
	rep, err := m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	found := false
	for _, n := range rep.Nodes {
		if n.Code == "11" && n.Debit == 50 {
			found = true
		}
	}
	if !found || len(rep.Unclassified) != 0 {
		t.Fatalf("rollup: %+v unclassified %v", rep.Nodes, rep.Unclassified)
	}
}

func TestYearEndRefusesInactiveAccountWithBalance(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	must(t, e.m.Ledger.SetAccountActive(e.ctx, "liab.wallet.1", false))
	e.set(afterYear)
	fy := e.fiscalYear(t, "1405")
	_, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an inactive account with a balance must block the closing: %v", err)
	}
	if e.fiscalYear(t, "1405").Status != ledger.Open {
		t.Fatal("nothing may change when the closing is refused")
	}
}

func TestYearEndRetainedAccountChoice(t *testing.T) {
	e := newClockEnv(t)
	e.yearOfBooks(t)
	e.set(afterYear)
	fy := e.fiscalYear(t, "1405")
	_, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{RetainedAccount: "revenue.sales"})
	if !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("retained earnings must be an equity account: %v", err)
	}
	must(t, e.m.Ledger.Ensure(e.ctx, ledger.AccountSpec{Code: "equity.reserve", Name: "Reserve", Type: ledger.Equity, Currency: "IRR"}))
	res, err := e.m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{RetainedAccount: "equity.reserve"})
	must(t, err)
	if res.NetProfit != 1_600_000 || e.bal(t, "equity.reserve") != 1_600_000 || e.bal(t, mizan.AccRetainedEarnings) != 0 {
		t.Fatal("the chosen account must receive the result")
	}
}

func TestBalanceSheetClassifiesAndSurvivesTheClosing(t *testing.T) {
	db := testdb.New(t)
	cur := now
	m, err := mizan.New(context.Background(), db, mizan.Config{IranianCodingFull: true}, mizan.WithClock(func() time.Time { return cur }))
	must(t, err)
	e := env{m: m, ctx: context.Background()}
	must(t, m.Ledger.Ensure(e.ctx,
		ledger.AccountSpec{Code: "equity.capital", Name: "Capital", Type: ledger.Equity, Currency: "IRR"},
		ledger.AccountSpec{Code: "asset.plant", Name: "Plant", Type: ledger.Asset, Currency: "IRR"},
		ledger.AccountSpec{Code: "asset.plant_accdep", Name: "Accumulated depreciation", Type: ledger.Asset, Currency: "IRR"},
		ledger.AccountSpec{Code: "liab.loan", Name: "Long-term loan", Type: ledger.Liability, Currency: "IRR"}))
	for acct, code := range map[string]string{"equity.capital": "311001", "asset.plant": "121003", "asset.plant_accdep": "121103", "liab.loan": "221201"} {
		must(t, m.Ledger.MapAccount(e.ctx, acct, code))
	}
	e.post(t, "b1", day1405, ledger.Dr(mizan.AccBank, irr(1000)), ledger.Cr("equity.capital", irr(1000)))
	e.post(t, "b2", day1405, ledger.Dr("asset.plant", irr(400)), ledger.Cr(mizan.AccBank, irr(400)))
	e.post(t, "b3", day1405, ledger.Dr(mizan.AccAdjustments, irr(40)), ledger.Cr("asset.plant_accdep", irr(40)))
	e.post(t, "b4", day1405, ledger.Dr(mizan.AccBank, irr(300)), ledger.Cr(mizan.AccRevenue, irr(300)))
	e.post(t, "b5", day1405, ledger.Dr(mizan.AccBank, irr(200)), ledger.Cr("liab.loan", irr(200)))

	check := func(label string, asOf time.Time, retained, result int64) {
		t.Helper()
		bs, err := m.Ledger.BalanceSheet(e.ctx, &asOf)
		must(t, err)
		if !bs.Balanced || bs.TotalAssets != 1460 {
			t.Fatalf("%s: %+v", label, bs)
		}
		if bs.CurrentAssets.Total != 1100 || bs.NonCurrentAssets.Total != 360 || bs.NonCurrentLiability.Total != 200 || bs.CurrentLiabilities.Total != 0 {
			t.Fatalf("%s: classification %+v", label, bs)
		}
		if bs.Equity.Total != 1000+retained || bs.CurrentResult != result || len(bs.Unclassified) != 0 {
			t.Fatalf("%s: equity %d result %d unclassified %v", label, bs.Equity.Total, bs.CurrentResult, bs.Unclassified)
		}
	}
	check("running year", day1405, 0, 260)

	cur = afterYear
	fy := e.fiscalYear(t, "1405")
	_, err = m.Ledger.CloseYear(e.ctx, fy.ID, ledger.CloseRequest{})
	must(t, err)
	check("last day of the closed year", endOf1405, 0, 260) // its own closing documents are left out
	check("first day of the next year", startOf06, 260, 0)  // earlier closing and opening cancel each other
	check("today", afterYear, 260, 0)
}
