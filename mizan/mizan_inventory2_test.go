package mizan_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
)

func (e env) wh(t *testing.T, code string) inventory.Warehouse {
	t.Helper()
	w, err := e.m.Inventory.CreateWarehouse(e.ctx, code, code, "tester")
	must(t, err)
	return w
}

func (e env) stockIn(t *testing.T, it inventory.Item, wh uuid.UUID) int64 {
	t.Helper()
	rows, err := e.m.Inventory.StockOf(e.ctx, it.ID)
	must(t, err)
	for _, r := range rows {
		if r.WarehouseID == wh {
			return r.Qty
		}
	}
	return 0
}

func TestInventorySpecificIdentification(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "unit-a", inventory.SpecificID)
	a := e.receive(t, "a", it, 2, 1_000_000) // two identical-looking cars bought at different prices
	b := e.receive(t, "b", it, 1, 3_000_000)
	if a.LotID == nil || b.LotID == nil {
		t.Fatal("a receipt makes a lot")
	}
	if _, err := e.issue("x0", it, 1); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("no lot named: %v", err)
	}
	m, err := e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: "x1", ItemID: it.ID, Qty: 1, LotID: *b.LotID, Actor: "tester"})
	must(t, err)
	if m.Value != -3_000_000 { // the cost of THAT lot, whatever the average or the order
		t.Fatalf("specific cost %d", m.Value)
	}
	m, err = e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: "x2", ItemID: it.ID, Qty: 1, LotID: *a.LotID, Actor: "tester"})
	must(t, err)
	if m.Value != -500_000 {
		t.Fatalf("one of two units of lot a: %d", m.Value)
	}
	if _, err := e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: "x3", ItemID: it.ID, Qty: 1, LotID: *b.LotID, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an empty lot: %v", err)
	}
	if _, err := e.m.Inventory.Count(e.ctx, inventory.CountInput{IdemKey: "c", ItemID: it.ID, CountedQty: 0, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a shortage of a specific item: %v", err)
	}
	e.stockMatchesLedger(t)
}

func TestInventoryWarehousesAndTransfer(t *testing.T) {
	e := setup(t, true)
	a, b := e.wh(t, "A"), e.wh(t, "B")
	if _, err := e.m.Inventory.CreateWarehouse(e.ctx, "A", "dup", "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken code: %v", err)
	}
	fifo := e.item(t, "f", inventory.FIFO)
	wa := e.item(t, "w", inventory.WeightedAverage)
	for _, it := range []inventory.Item{fifo, wa} {
		if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "r1" + it.Code, ItemID: it.ID, Qty: 10, TotalCost: irr(1000), WarehouseID: a.ID, Actor: "tester"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "r2" + it.Code, ItemID: it.ID, Qty: 10, TotalCost: irr(3000), WarehouseID: a.ID, Actor: "tester"}); err != nil {
			t.Fatal(err)
		}
		// 15 units move to B: the FIFO item carries the cost of its oldest layers (1000 + half of 3000), the average item the average (2000 per 10 -> 3000).
		if _, err := e.m.Inventory.Transfer(e.ctx, inventory.TransferInput{IdemKey: "t" + it.Code, ItemID: it.ID, From: a.ID, To: b.ID, Qty: 15, Actor: "tester"}); err != nil {
			t.Fatal(err)
		}
		if e.stockIn(t, it, a.ID) != 5 || e.stockIn(t, it, b.ID) != 15 {
			t.Fatalf("%s: A=%d B=%d", it.Code, e.stockIn(t, it, a.ID), e.stockIn(t, it, b.ID))
		}
		if _, err := e.m.Inventory.Transfer(e.ctx, inventory.TransferInput{IdemKey: "tx" + it.Code, ItemID: it.ID, From: a.ID, To: b.ID, Qty: 6, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("more than the warehouse holds: %v", err)
		}
		// The main warehouse is empty: an issue from it is refused even though the item has stock elsewhere.
		if _, err := e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: "i0" + it.Code, ItemID: it.ID, Qty: 1, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
			t.Fatalf("issue from an empty warehouse: %v", err)
		}
	}
	// FIFO: the transferred stock in B keeps the cost of the oldest units: 10 at 100 + 2 at 300 (the second layer cost 3000 for 10).
	m, err := e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: "ib", ItemID: fifo.ID, Qty: 12, WarehouseID: b.ID, Actor: "tester"})
	must(t, err)
	if m.Value != -(1000 + 2*300) {
		t.Fatalf("fifo issue from B: %d", m.Value)
	}
	// A count is per warehouse.
	c, err := e.m.Inventory.Count(e.ctx, inventory.CountInput{IdemKey: "cnt", ItemID: wa.ID, CountedQty: 4, WarehouseID: b.ID, Actor: "tester"})
	must(t, err)
	if c.Qty != -11 || e.stockIn(t, wa, b.ID) != 4 {
		t.Fatalf("count: %+v B=%d", c, e.stockIn(t, wa, b.ID))
	}
	e.stockMatchesLedger(t)
}

func TestInventoryLotsFEFOAndExpiry(t *testing.T) {
	e := setup(t, true)
	it, err := e.m.Inventory.CreateItem(e.ctx, inventory.ItemInput{Code: "milk", Name: "milk", Unit: "l", Method: inventory.FIFO, FEFO: true, Actor: "tester"})
	must(t, err)
	if _, err := e.m.Inventory.CreateItem(e.ctx, inventory.ItemInput{Code: "bad", Name: "bad", Unit: "l", Method: inventory.WeightedAverage, FEFO: true}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("fefo on an average item: %v", err)
	}
	day := func(d int) *time.Time { x := time.Date(2026, 12, d, 0, 0, 0, 0, time.UTC); return &x }
	// The later purchase expires first.
	if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "l1", ItemID: it.ID, Qty: 10, TotalCost: irr(1000), LotNo: "L1", ExpiresOn: day(20), Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "l2", ItemID: it.ID, Qty: 10, TotalCost: irr(5000), LotNo: "L2", ExpiresOn: day(5), Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	m, err := e.issue("f1", it, 10)
	must(t, err)
	if m.Value != -5000 { // FEFO took L2 although L1 is older
		t.Fatalf("FEFO issue %d", m.Value)
	}
	lots, err := e.m.Inventory.ExpiringLots(e.ctx, *day(31))
	must(t, err)
	if len(lots) != 1 || lots[0].LotNo != "L1" || lots[0].Remaining != 10 {
		t.Fatalf("%+v", lots)
	}
	avg := e.item(t, "plain", inventory.WeightedAverage)
	if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "lx", ItemID: avg.ID, Qty: 1, TotalCost: irr(1), LotNo: "Z", Actor: "tester"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a lot on an average item: %v", err)
	}
}

func TestInventoryTransferConcurrency(t *testing.T) {
	e := setup(t, true)
	a, b := e.wh(t, "A"), e.wh(t, "B")
	it := e.item(t, "c", inventory.WeightedAverage)
	_, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "r", ItemID: it.ID, Qty: 10, TotalCost: irr(1000), WarehouseID: a.ID, Actor: "tester"})
	must(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.m.Inventory.Transfer(e.ctx, inventory.TransferInput{IdemKey: fmt.Sprintf("t%d", i), ItemID: it.ID, From: a.ID, To: b.ID, Qty: 4, Actor: "tester"})
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 2 || e.stockIn(t, it, a.ID) != 2 || e.stockIn(t, it, b.ID) != 8 {
		t.Fatalf("%d transfers went through, A=%d B=%d", ok, e.stockIn(t, it, a.ID), e.stockIn(t, it, b.ID))
	}
}

func TestProductionCosting(t *testing.T) {
	e := setup(t, true)
	steel := e.item(t, "steel", inventory.WeightedAverage)
	e.receive(t, "s1", steel, 100, 1_000_000) // 10,000 a unit
	prod := e.item(t, "chair", inventory.FIFO)
	o, err := e.m.Inventory.OpenOrder(e.ctx, "PO-1", prod.ID, 10, "tester")
	must(t, err)
	if _, err := e.m.Inventory.OpenOrder(e.ctx, "PO-1", prod.ID, 10, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken code: %v", err)
	}
	// No rate yet: completing is refused unless it says so.
	mat, err := e.m.Inventory.IssueMaterial(e.ctx, o.ID, inventory.IssueMaterialInput{IdemKey: "m1", ItemID: steel.ID, Qty: 30, Actor: "tester"})
	must(t, err)
	if mat.Value != -300_000 {
		t.Fatalf("material %d", mat.Value)
	}
	if again, err := e.m.Inventory.IssueMaterial(e.ctx, o.ID, inventory.IssueMaterialInput{IdemKey: "m1", ItemID: steel.ID, Qty: 30, Actor: "tester"}); err != nil || again.ID != mat.ID {
		t.Fatalf("a retry: %v", err)
	}
	must(t, e.m.Inventory.AddLabor(e.ctx, o.ID, inventory.AddLaborInput{IdemKey: "l1", Amount: irr(200_000), Actor: "tester"}))
	must(t, e.m.Inventory.AddLabor(e.ctx, o.ID, inventory.AddLaborInput{IdemKey: "l1", Amount: irr(200_000), Actor: "tester"})) // retry: counted once
	got, _ := e.m.Inventory.Order(e.ctx, o.ID)
	if got.MaterialCost != 300_000 || got.LaborCost != 200_000 {
		t.Fatalf("order %+v", got)
	}
	if w, _ := e.m.Inventory.WorkInProcess(e.ctx); w != 500_000 || e.bal(t, mizan.AccWIP) != 500_000 {
		t.Fatalf("WIP %d / ledger %d", w, e.bal(t, mizan.AccWIP))
	}
	if _, err := e.m.Inventory.CompleteOrder(e.ctx, o.ID, inventory.CompleteInput{IdemKey: "c0", OutputQty: 10, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("no overhead rate: %v", err)
	}
	// Overhead: 50% of labour cost fixed (derived from a budget) + 10% variable.
	rate, err := e.m.Inventory.AddOverheadRate(e.ctx, inventory.OverheadRate{EffectiveFrom: time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), Basis: inventory.BasisLaborCost,
		BudgetFixed: 600_000_000, NormalCap: 1_200_000_000, VariableRate: 1000, Source: "owner's budget for 1405 (test)"}, "tester")
	must(t, err)
	if rate.FixedRate != 5000 {
		t.Fatalf("derived fixed rate %d", rate.FixedRate)
	}
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	c, err := e.m.Inventory.CompleteOrder(e.ctx, o.ID, inventory.CompleteInput{IdemKey: "c1", OutputQty: 10, AbnormalLoss: 20_000, LotNo: "B1", ExpiresOn: &exp, Actor: "tester"})
	must(t, err)
	// overhead = 100,000 fixed + 20,000 variable; total 620,000; loss 20,000; finished 600,000
	if c.FixedOverhead != 100_000 || c.VarOverhead != 20_000 || c.FinishedCost != 600_000 || c.Status != inventory.OrderCompleted {
		t.Fatalf("completed %+v", c)
	}
	if again, err := e.m.Inventory.CompleteOrder(e.ctx, o.ID, inventory.CompleteInput{IdemKey: "c1", OutputQty: 10, Actor: "tester"}); err != nil || again.FinishedCost != 600_000 {
		t.Fatalf("a retry of the completion: %v", err)
	}
	if _, err := e.m.Inventory.CompleteOrder(e.ctx, o.ID, inventory.CompleteInput{IdemKey: "other", OutputQty: 10, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("completing twice: %v", err)
	}
	if e.bal(t, mizan.AccWIP) != 0 || e.bal(t, mizan.AccInventoryAdjustment) != 20_000 {
		t.Fatalf("WIP %d", e.bal(t, mizan.AccWIP))
	}
	chair, _ := e.m.Inventory.Item(e.ctx, prod.ID)
	if chair.Qty != 10 || chair.Value.Amount() != 600_000 {
		t.Fatalf("finished goods %+v", chair)
	}
	e.stockMatchesLedger(t)

	// The pool: 100,000 of the 150,000 actually incurred overhead was applied (120,000): 30,000 is the cost of unused capacity.
	must(t, e.m.Inventory.RecordOverhead(e.ctx, "oh1", irr(150_000), "", "rent", time.Time{}, "tester"))
	must(t, e.m.Inventory.RecordOverhead(e.ctx, "oh1", irr(150_000), "", "rent", time.Time{}, "tester")) // retry
	st, err := e.m.Inventory.SettleOverhead(e.ctx, "set1", time.Time{}, "tester")
	must(t, err)
	if st.Actual != 150_000 || st.Applied != 120_000 || st.Journal == nil {
		t.Fatalf("settlement %+v", st)
	}
	if e.bal(t, mizan.AccProductionOverhead) != 0 || e.bal(t, mizan.AccProductionVariance) != 30_000 {
		t.Fatalf("pool %d variance %d", e.bal(t, mizan.AccProductionOverhead), e.bal(t, mizan.AccProductionVariance))
	}
	if again, err := e.m.Inventory.SettleOverhead(e.ctx, "set1", time.Time{}, "tester"); err != nil || again.Journal == nil {
		t.Fatalf("a retry of the settlement: %v", err)
	}
	e.balanced(t)
}
