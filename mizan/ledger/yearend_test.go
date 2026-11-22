package ledger_test

import (
	"fmt"
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func row(code string, t ledger.AccountType, dr, cr int64) ledger.TrialBalanceRow {
	return ledger.TrialBalanceRow{Code: code, Type: t, Debit: dr, Credit: cr}
}

func sums(ls []ledger.Line) (dr, cr int64) {
	for _, l := range ls {
		if l.Side == ledger.Debit {
			dr += l.Amount.Amount()
		} else {
			cr += l.Amount.Amount()
		}
	}
	return
}

func flat(docs [][]ledger.Line) []ledger.Line {
	var out []ledger.Line
	for _, d := range docs {
		out = append(out, d...)
	}
	return out
}

func find(ls []ledger.Line, acct string) (ledger.Side, int64, bool) {
	for _, l := range ls {
		if l.Account == acct {
			return l.Side, l.Amount.Amount(), true
		}
	}
	return 0, 0, false
}

func TestPlanYearEndProfit(t *testing.T) {
	p, err := ledger.PlanYearEnd(ledger.PlanInput{Currency: money.IRR, Summary: "sum", Retained: "re", Rows: []ledger.TrialBalanceRow{
		row("bank", ledger.Asset, 1000, 100),  // 900 debit
		row("accdep", ledger.Asset, 0, 50),    // contra asset: credit balance
		row("loan", ledger.Liability, 0, 300), // 300 credit
		row("capital", ledger.Equity, 0, 270), // 270 credit
		row("sales", ledger.Revenue, 0, 700),  // 700 credit
		row("returns", ledger.Revenue, 20, 0), // contra revenue: debit balance
		row("salary", ledger.Expense, 400, 0), // 400 debit
		row("zero", ledger.Expense, 5, 5),     // nets to zero: skipped
		row("sum", ledger.Equity, 0, 0),       // never carried
	}})
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalRevenue != 700 || p.TotalExpense != 420 || p.NetProfit != 280 {
		t.Fatalf("totals: %+v", p)
	}
	if dr, cr := sums(flat(p.Temporary)); dr != cr {
		t.Fatalf("temporary journal unbalanced %d/%d", dr, cr)
	}
	if _, _, ok := find(flat(p.Temporary), "zero"); ok {
		t.Fatal("zero-balance account must be skipped")
	}
	if s, a, _ := find(flat(p.Temporary), "sales"); s != ledger.Debit || a != 700 {
		t.Fatalf("sales must be debited 700, got %v %d", s, a)
	}
	if s, a, _ := find(flat(p.Temporary), "returns"); s != ledger.Credit || a != 20 {
		t.Fatalf("returns must be credited 20, got %v %d", s, a)
	}
	if s, a, _ := find(p.Transfer, "re"); s != ledger.Credit || a != 280 {
		t.Fatalf("profit goes to retained earnings as a credit: %v %d", s, a)
	}
	// The closing document carries the retained earnings after the transfer, and the summary is not carried.
	if s, a, _ := find(flat(p.Closing), "re"); s != ledger.Debit || a != 280 {
		t.Fatalf("closing must debit retained earnings 280, got %v %d", s, a)
	}
	if s, a, _ := find(flat(p.Closing), "accdep"); s != ledger.Debit || a != 50 {
		t.Fatalf("contra asset closes on its own side: %v %d", s, a)
	}
	if _, _, ok := find(flat(p.Closing), "sum"); ok {
		t.Fatal("summary must not be carried")
	}
	cd, cc := sums(flat(p.Closing))
	od, oc := sums(flat(p.Opening))
	if cd != cc || od != oc || cd != oc || cc != od {
		t.Fatalf("closing %d/%d opening %d/%d", cd, cc, od, oc)
	}
	for _, c := range flat(p.Closing) {
		s, a, ok := find(flat(p.Opening), c.Account)
		if !ok || s != c.Side.Opposite() || a != c.Amount.Amount() {
			t.Fatalf("opening of %s is not the mirror of closing", c.Account)
		}
	}
}

func TestPlanYearEndLossAndEmpty(t *testing.T) {
	p, _ := ledger.PlanYearEnd(ledger.PlanInput{Currency: money.IRR, Summary: "sum", Retained: "re", Rows: []ledger.TrialBalanceRow{
		row("bank", ledger.Asset, 500, 0), row("capital", ledger.Equity, 0, 600), row("cost", ledger.Expense, 100, 0),
	}})
	if p.NetProfit != -100 {
		t.Fatal(p.NetProfit)
	}
	if s, a, _ := find(p.Transfer, "re"); s != ledger.Debit || a != 100 {
		t.Fatalf("loss debits retained earnings: %v %d", s, a)
	}
	if s, a, _ := find(flat(p.Closing), "re"); s != ledger.Credit || a != 100 {
		t.Fatalf("closing credits the accumulated loss: %v %d", s, a)
	}

	e, _ := ledger.PlanYearEnd(ledger.PlanInput{Currency: money.IRR, Summary: "sum", Retained: "re"})
	if len(e.Temporary)+len(e.Transfer)+len(e.Closing)+len(e.Opening) != 0 || len(e.Blockers) != 0 {
		t.Fatalf("an empty year posts nothing: %+v", e)
	}
}

func TestPlanYearEndSplitsLargeDocuments(t *testing.T) {
	var rows []ledger.TrialBalanceRow
	for i := 0; i < 25; i++ {
		rows = append(rows, row(fmt.Sprintf("a%02d", i), ledger.Asset, 10, 0))
	}
	rows = append(rows, row("cap", ledger.Equity, 0, 200), row("sales", ledger.Revenue, 0, 70), row("cost", ledger.Expense, 20, 0))
	p, err := ledger.PlanYearEnd(ledger.PlanInput{Currency: money.IRR, Summary: "sum", Retained: "re", ClosingTransit: "ct", OpeningTransit: "ot", ChunkSize: 8, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if !p.NeedsTransit() || len(p.Closing) < 2 || len(p.Opening) < 2 {
		t.Fatalf("expected several documents: closing %d opening %d", len(p.Closing), len(p.Opening))
	}
	check := func(name, transit string, docs [][]ledger.Line) (net int64) {
		accounts := 0
		for _, d := range docs {
			if len(d) > 8 {
				t.Fatalf("%s document has %d lines", name, len(d))
			}
			dr, cr := sums(d)
			if dr != cr {
				t.Fatalf("%s document unbalanced %d/%d", name, dr, cr)
			}
			for _, l := range d {
				if l.Account == transit {
					if l.Side == ledger.Debit {
						net += l.Amount.Amount()
					} else {
						net -= l.Amount.Amount()
					}
				} else {
					accounts++
				}
			}
		}
		if accounts != 27 { // 25 assets, capital and retained earnings
			t.Fatalf("%s documents carry %d accounts", name, accounts)
		}
		return net
	}
	if n := check("closing", "ct", p.Closing); n != 0 {
		t.Fatalf("closing transit nets %d", n)
	}
	if n := check("opening", "ot", p.Opening); n != 0 {
		t.Fatalf("opening transit nets %d", n)
	}
	// Every carried account is the exact mirror between closing and opening.
	cl, op := flat(p.Closing), flat(p.Opening)
	for _, c := range cl {
		if c.Account == "ct" {
			continue
		}
		if s, a, ok := find(op, c.Account); !ok || s != c.Side.Opposite() || a != c.Amount.Amount() {
			t.Fatalf("opening of %s is not the mirror of the closing", c.Account)
		}
	}
}

func TestIranianCodingFull(t *testing.T) {
	nodes := ledger.IranianCodingFull()
	if err := ledger.ValidateCoding(nodes); err != nil {
		t.Fatal(err)
	}
	levels := map[int]int{}
	byCode := map[string]ledger.CodingNode{}
	for _, n := range nodes {
		levels[n.Level]++
		byCode[n.Code] = n
	}
	if levels[1] != 12 || levels[2] != 60 || levels[3] != 290 {
		t.Fatalf("shape %v", levels)
	}
	for code, hint := range map[string]ledger.AccountType{"111005": ledger.Asset, "121101": ledger.Asset, "211208": ledger.Liability, "311301": ledger.Equity, "411001": ledger.Revenue, "611001": ledger.Expense, "921101": ""} {
		n, ok := byCode[code]
		if !ok || n.TypeHint != hint {
			t.Fatalf("%s: %+v", code, n)
		}
	}
	if byCode["311301"].Parent != "3113" || byCode["3113"].Parent != "31" || byCode["31"].Parent != "" {
		t.Fatal("hierarchy")
	}
}
