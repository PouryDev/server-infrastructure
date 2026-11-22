package ledger_test

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func irr(n int64) money.Money { return money.Must(n, money.IRR) }

func draft(lines ...ledger.Line) ledger.Draft {
	return ledger.Draft{Key: "k", Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Description: "d", Lines: lines}
}

func TestDraftValidate(t *testing.T) {
	ok := draft(ledger.Dr("a", irr(100)), ledger.Cr("b", irr(60)), ledger.Cr("c", irr(40)))
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]ledger.Draft{
		"unbalanced":  draft(ledger.Dr("a", irr(100)), ledger.Cr("b", irr(99))),
		"one line":    draft(ledger.Dr("a", irr(100))),
		"zero amount": draft(ledger.Dr("a", irr(0)), ledger.Cr("b", irr(0))),
		"negative":    draft(ledger.Dr("a", irr(-5)), ledger.Cr("b", irr(-5))),
		"mixed money": draft(ledger.Dr("a", irr(10)), ledger.Cr("b", money.Must(10, money.IRT))),
		"no account":  draft(ledger.Dr("", irr(10)), ledger.Cr("b", irr(10))),
		"no side":     draft(ledger.Line{Account: "a", Amount: irr(10)}, ledger.Cr("b", irr(10))),
	}
	for name, d := range bad {
		if err := d.Validate(); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("%s: expected invalid, got %v", name, err)
		}
	}
	noKey := ok
	noKey.Key = ""
	if err := noKey.Validate(); err == nil {
		t.Error("missing key accepted")
	}
}

func TestFingerprintDetectsChanges(t *testing.T) {
	a := draft(ledger.Dr("a", irr(100)), ledger.Cr("b", irr(100)))
	b := draft(ledger.Dr("a", irr(100)), ledger.Cr("b", irr(100)))
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("same content must hash the same")
	}
	c := draft(ledger.Dr("a", irr(101)), ledger.Cr("b", irr(101)))
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatal("different amounts must differ")
	}
}

func TestRollUpWorksWithAndWithoutChart(t *testing.T) {
	rows := []ledger.TrialBalanceRow{
		{Code: "cash", Type: ledger.Asset, CodingCode: "10101", Debit: 500},
		{Code: "bank", Type: ledger.Asset, CodingCode: "10102", Debit: 300, Credit: 100},
		{Code: "sales", Type: ledger.Revenue, CodingCode: "60101", Credit: 700},
		{Code: "misc", Type: ledger.Expense, Debit: 100}, // not mapped: must not break anything
	}
	rep := ledger.RollUp(rows, ledger.IranianCoding())
	got := map[string]int64{}
	for _, n := range rep.Nodes {
		got[n.Code] = n.Balance
	}
	if got["101"] != 700 || got["1"] != 700 || got["6"] != 700 {
		t.Fatalf("roll-up wrong: %v", got)
	}
	if len(rep.Unclassified) != 1 || rep.Unclassified[0].Code != "misc" {
		t.Fatalf("unclassified: %v", rep.Unclassified)
	}
	empty := ledger.RollUp(rows, nil)
	if len(empty.Nodes) != 0 || len(empty.Unclassified) != 4 {
		t.Fatalf("no chart must still work: %+v", empty)
	}
}

func TestIranianCodingIsValid(t *testing.T) {
	if err := ledger.ValidateCoding(ledger.IranianCoding()); err != nil {
		t.Fatal(err)
	}
}
