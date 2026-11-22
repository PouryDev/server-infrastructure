package mizan_test

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

func TestOfficialStatements(t *testing.T) {
	e := newClockEnv(t)
	from := time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC)
	// Capital 100M in cash; inventory bought on credit 30M; sales 50M cash with 5M VAT; cost of goods 20M; salaries 8M cash; gateway fee 1M;
	// income tax 2M booked, 1M of it paid, 1M still payable; dividends declared 0.
	e.post(t, "s:capital", day1405, ledger.Dr(mizan.AccBank, irr(100_000_000)), ledger.Cr(mizan.AccCapital, irr(100_000_000)))
	e.post(t, "s:stock", day1405, ledger.Dr(mizan.AccInventory, irr(30_000_000)), ledger.Cr(mizan.AccPayable, irr(30_000_000)))
	e.post(t, "s:sale", day1405, ledger.Dr(mizan.AccBank, irr(55_000_000)), ledger.Cr(mizan.AccRevenue, irr(50_000_000)), ledger.Cr(mizan.AccVATPayable, irr(5_000_000)))
	e.post(t, "s:cogs", day1405, ledger.Dr(mizan.AccCOGS, irr(20_000_000)), ledger.Cr(mizan.AccInventory, irr(20_000_000)))
	e.post(t, "s:pay", day1405, ledger.Dr(mizan.AccSalaryExpense, irr(8_000_000)), ledger.Dr(mizan.AccGatewayFees, irr(1_000_000)), ledger.Cr(mizan.AccBank, irr(9_000_000)))
	e.post(t, "s:tax", day1405, ledger.Dr(mizan.AccIncomeTaxExpense, irr(2_000_000)), ledger.Cr(mizan.AccIncomeTaxPayable, irr(1_000_000)), ledger.Cr(mizan.AccBank, irr(1_000_000)))
	to := day1405

	bs, err := e.m.Ledger.OfficialBalanceSheet(e.ctx, &to)
	must(t, err)
	// profit: 50 - 20 - 8 - 1 - 2 = 19M
	if !bs.Balanced || bs.CurrentResult != 19_000_000 {
		t.Fatalf("balance sheet: %+v", bs)
	}
	if bs.TotalAssets != 100_000_000+55_000_000-9_000_000-1_000_000+10_000_000 { // cash 145M, inventory 10M
		t.Fatalf("assets %d", bs.TotalAssets)
	}
	byCode := map[string]int64{}
	for _, l := range append(append([]ledger.StatementLine{}, bs.CurrentAssets...), bs.CurrentLiabilities...) {
		byCode[l.Code] = l.Amount
	}
	if byCode[ledger.LineCash] != 145_000_000 || byCode[ledger.LineInventories] != 10_000_000 || byCode[ledger.LinePayables] != 35_000_000 || byCode[ledger.LineTaxLiabilities] != 1_000_000 {
		t.Fatalf("lines: %v", byCode)
	}

	is, err := e.m.Ledger.OfficialIncomeStatement(e.ctx, &from, &to)
	must(t, err)
	if is.GrossProfit != 30_000_000 || is.OperatingExpenses.Amount != 9_000_000 || is.OperatingProfit != 21_000_000 || is.TaxExpense.Amount != 2_000_000 || is.NetProfit != 19_000_000 {
		t.Fatalf("income statement: %+v", is)
	}

	eq, err := e.m.Ledger.ChangesInEquity(e.ctx, from, to)
	must(t, err)
	if eq.NetProfit != 19_000_000 || eq.Opening != 0 || eq.Closing != 119_000_000 {
		t.Fatalf("equity: %+v", eq)
	}

	cf, err := e.m.Ledger.CashFlow(e.ctx, from, to)
	must(t, err)
	// operating: profit 19 + payables 35 (suppliers 30 + VAT 5) + tax payable 1 - inventory 10 = 45M; financing: capital 100M; cash 145M
	if !cf.Reconciled || cf.Operating != 45_000_000 || cf.Financing != 100_000_000 || cf.Investing != 0 || cf.ClosingCash != 145_000_000 || cf.NetChange != 145_000_000 {
		t.Fatalf("cash flow: %+v", cf)
	}
}
