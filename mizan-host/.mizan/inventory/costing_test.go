package inventory_test

import (
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/inventory"
)

func TestAverageIssue(t *testing.T) {
	// 200 units worth 220,000: issuing 150 costs 165,000 and leaves 50 worth 55,000.
	c, err := inventory.AverageIssue(200, 220_000, 150)
	if err != nil || c != 165_000 {
		t.Fatalf("%d %v", c, err)
	}
	// Issuing everything takes the whole value, whatever the rounding.
	if c, _ := inventory.AverageIssue(3, 100, 3); c != 100 {
		t.Fatalf("%d", c)
	}
	// 3 units worth 100: issuing 1 costs 33, then 1 costs 34 (of 67 left, 2 units), then the last takes 33.
	var spent int64
	onHand, value := int64(3), int64(100)
	for i := 0; i < 3; i++ {
		c, err := inventory.AverageIssue(onHand, value, 1)
		if err != nil {
			t.Fatal(err)
		}
		spent += c
		onHand, value = onHand-1, value-c
	}
	if spent != 100 || value != 0 {
		t.Fatalf("spent %d, left %d", spent, value)
	}
	if _, err := inventory.AverageIssue(5, 10, 6); err == nil {
		t.Fatal("more than the stock must be refused")
	}
	if _, err := inventory.AverageIssue(5, 10, 0); err == nil {
		t.Fatal("zero must be refused")
	}
}

func TestFIFOIssue(t *testing.T) {
	layers := []inventory.Layer{{Seq: 1, Qty: 10, Remaining: 10, RemainingValue: 1000}, {Seq: 2, Qty: 10, Remaining: 10, RemainingValue: 2000}}
	cost, after, err := inventory.FIFOIssue(layers, 15)
	if err != nil || cost != 1000+1000 {
		t.Fatalf("%d %v", cost, err)
	}
	if after[0].Remaining != 0 || after[0].RemainingValue != 0 || after[1].Remaining != 5 || after[1].RemainingValue != 1000 {
		t.Fatalf("%+v", after)
	}
	if layers[0].Remaining != 10 {
		t.Fatal("the input must not change")
	}
	if _, _, err := inventory.FIFOIssue(after, 6); err == nil {
		t.Fatal("more than the stock must be refused")
	}
	cost, after, err = inventory.FIFOIssue(after, 5)
	if err != nil || cost != 1000 || after[1].Remaining != 0 {
		t.Fatalf("%d %v %+v", cost, err, after)
	}
}

func TestCutValueKeepsTheTotal(t *testing.T) {
	layers := []inventory.Layer{{Remaining: 3, RemainingValue: 100}, {Remaining: 3, RemainingValue: 200}, {Remaining: 3, RemainingValue: 301}}
	after := inventory.CutValue(layers, 400)
	var total int64
	for _, l := range after {
		total += l.RemainingValue
	}
	if total != 400 {
		t.Fatalf("total %d: %+v", total, after)
	}
	if same := inventory.CutValue(layers, 10_000); same[0].RemainingValue != 100 {
		t.Fatal("a cut above the value changes nothing")
	}
}

func TestTakeLayersAndDerivedRates(t *testing.T) {
	layers := []inventory.Layer{{ID: 1, Remaining: 10, RemainingValue: 1000}, {ID: 2, Remaining: 10, RemainingValue: 2000}}
	takes, after, err := inventory.TakeLayers(layers, 15)
	if err != nil || len(takes) != 2 || takes[0].Cost != 1000 || takes[1].Qty != 5 || takes[1].Cost != 1000 || after[1].RemainingValue != 1000 {
		t.Fatalf("%+v %+v %v", takes, after, err)
	}
	if _, _, err := inventory.TakeLayers(layers, 21); err == nil {
		t.Fatal("more than the stock")
	}
	// Fixed overhead: 120,000,000 budget over a normal capacity of 6,000 machine hours = 20,000 a hour; over 5,000,000,000 of labour cost = 2.4% = 240 bp.
	if r, _ := inventory.DeriveFixedRate(inventory.BasisMachineUnits, 120_000_000, 6000); r != 20_000 {
		t.Fatalf("rate %d", r)
	}
	if r, _ := inventory.DeriveFixedRate(inventory.BasisLaborCost, 120_000_000, 5_000_000_000); r != 240 {
		t.Fatalf("rate %d", r)
	}
	if _, err := inventory.DeriveFixedRate(inventory.BasisMachineUnits, 1, 0); err == nil {
		t.Fatal("zero capacity")
	}
	f, v := inventory.OverheadRate{Basis: inventory.BasisLaborCost, FixedRate: 2000, VariableRate: 500}.Apply(0, 1_000_000, 0, 0) // 20% and 5% of labour
	if f != 200_000 || v != 50_000 {
		t.Fatalf("%d %d", f, v)
	}
	f, v = inventory.OverheadRate{Basis: inventory.BasisMachineUnits, FixedRate: 20_000, VariableRate: 3000}.Apply(0, 0, 10, 0)
	if f != 200_000 || v != 30_000 {
		t.Fatalf("%d %d", f, v)
	}
}
