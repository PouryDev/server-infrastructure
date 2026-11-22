package vat_test

import (
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/vat"
)

func TestPenaltyEstimate(t *testing.T) {
	due := time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC)
	r := vat.Rule{LatePenaltyBP: 200}
	// Not late: nothing. One day late: one started month = 2%. 31 days late: two started months = 4%.
	if vat.PenaltyEstimate(r, due, due, 1_000_000) != 0 {
		t.Fatal("on the due date there is no penalty")
	}
	if got := vat.PenaltyEstimate(r, due, due.AddDate(0, 0, 1), 1_000_000); got != 20_000 {
		t.Fatalf("one day late: %d", got)
	}
	if got := vat.PenaltyEstimate(r, due, due.AddDate(0, 0, 31), 1_000_000); got != 40_000 {
		t.Fatalf("31 days late: %d", got)
	}
	if vat.PenaltyEstimate(vat.Rule{}, due, due.AddDate(0, 0, 90), 1_000_000) != 0 || vat.PenaltyEstimate(r, due, due.AddDate(0, 0, 90), 0) != 0 {
		t.Fatal("no rate or nothing unpaid means no penalty")
	}
}
