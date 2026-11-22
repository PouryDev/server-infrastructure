package mizan_test

import (
	"fmt"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"sync"
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
)

func (e env) item(t *testing.T, code string, m inventory.Method) inventory.Item {
	t.Helper()
	it, err := e.m.Inventory.CreateItem(e.ctx, inventory.ItemInput{Code: code, Name: code, Unit: "pcs", Method: m, Actor: "tester"})
	must(t, err)
	return it
}

func (e env) receive(t *testing.T, key string, it inventory.Item, qty, cost int64) inventory.Movement {
	t.Helper()
	m, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: key, ItemID: it.ID, Qty: qty, TotalCost: irr(cost), Actor: "tester"})
	must(t, err)
	return m
}

func (e env) issue(key string, it inventory.Item, qty int64) (inventory.Movement, error) {
	return e.m.Inventory.Issue(e.ctx, inventory.IssueInput{IdemKey: key, ItemID: it.ID, Qty: qty, Actor: "tester"})
}

// stockMatchesLedger checks the valuation report against the inventory account of the ledger.
func (e env) stockMatchesLedger(t *testing.T) {
	t.Helper()
	v, err := e.m.Inventory.Valuation(e.ctx)
	must(t, err)
	if b := e.bal(t, mizan.AccInventory); b != v.TotalValue {
		t.Fatalf("the ledger holds %d of inventory but the stock is worth %d", b, v.TotalValue)
	}
	e.balanced(t)
}

func TestInventoryWeightedAverage(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "widget", inventory.WeightedAverage)
	if _, err := e.m.Inventory.CreateItem(e.ctx, inventory.ItemInput{Code: "widget", Name: "dup", Unit: "pcs"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a taken code: %v", err)
	}
	r1 := e.receive(t, "r1", it, 100, 100_000)
	e.receive(t, "r2", it, 100, 120_000)
	if again := e.receive(t, "r1", it, 100, 100_000); again.ID != r1.ID {
		t.Fatal("a retry must return the same movement")
	}
	got, _ := e.m.Inventory.Item(e.ctx, it.ID)
	if got.Qty != 200 || got.Value.Amount() != 220_000 || got.UnitCost() != 1100 {
		t.Fatalf("after two receipts: %+v", got)
	}

	m, err := e.issue("i1", it, 150)
	must(t, err)
	if m.Value != -165_000 || m.QtyAfter != 50 || m.ValueAfter != 55_000 || m.Journal == nil {
		t.Fatalf("issue: %+v", m)
	}
	if e.bal(t, mizan.AccCOGS) != 165_000 || e.bal(t, mizan.AccPayable) != 220_000 {
		t.Fatalf("cogs %d payable %d", e.bal(t, mizan.AccCOGS), e.bal(t, mizan.AccPayable))
	}
	e.stockMatchesLedger(t)

	if _, err := e.issue("i2", it, 51); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an issue above the stock must be refused: %v", err)
	}
	if again, err := e.issue("i1", it, 150); err != nil || again.ID != m.ID {
		t.Fatalf("a retry of an issue must not issue twice: %v", err)
	}
	if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "bad", ItemID: it.ID, Qty: 0, TotalCost: irr(1)}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a zero quantity: %v", err)
	}
	// The last units take the exact remaining value, so stock reaches zero value with zero quantity.
	if _, err := e.issue("i3", it, 50); err != nil {
		t.Fatal(err)
	}
	got, _ = e.m.Inventory.Item(e.ctx, it.ID)
	if got.Qty != 0 || got.Value.Amount() != 0 {
		t.Fatalf("empty stock: %+v", got)
	}
	e.stockMatchesLedger(t)
}

func TestInventoryConcurrentIssuesNeverOversell(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "bolt", inventory.WeightedAverage)
	e.receive(t, "r", it, 100, 100_000)
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.issue(fmt.Sprintf("c%d", i), it, 30)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !apperr.Is(err, apperr.KindConflict):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	got, _ := e.m.Inventory.Item(e.ctx, it.ID)
	if ok != 3 || got.Qty != 10 || got.Value.Amount() != 10_000 {
		t.Fatalf("%d issues succeeded, stock %d worth %d", ok, got.Qty, got.Value.Amount())
	}
	e.stockMatchesLedger(t)
}

func TestInventoryFIFOCountAndWriteDown(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "cable", inventory.FIFO)
	e.receive(t, "r1", it, 10, 1000)
	e.receive(t, "r2", it, 10, 2000)
	m, err := e.issue("i1", it, 15) // 10 at 100 + 5 at 200
	must(t, err)
	if m.Value != -2000 || m.ValueAfter != 1000 {
		t.Fatalf("fifo issue: %+v", m)
	}

	// A count finds 3 of the 5 left: the shortage of 2 is issued from the oldest layer at its cost (200 each).
	c, err := e.m.Inventory.Count(e.ctx, inventory.CountInput{IdemKey: "k1", ItemID: it.ID, CountedQty: 3, Reason: "stock count", Actor: "tester"})
	must(t, err)
	if c.Kind != inventory.CountLoss || c.Qty != -2 || c.Value != -400 || e.bal(t, mizan.AccInventoryAdjustment) != 400 {
		t.Fatalf("count loss: %+v adjustment %d", c, e.bal(t, mizan.AccInventoryAdjustment))
	}
	// A later count finds 6: the surplus of 3 comes in at the average cost (600/3 = 200 each).
	c, err = e.m.Inventory.Count(e.ctx, inventory.CountInput{IdemKey: "k2", ItemID: it.ID, CountedQty: 6, Actor: "tester"})
	must(t, err)
	if c.Kind != inventory.CountGain || c.Value != 600 || c.ValueAfter != 1200 || e.bal(t, mizan.AccInventoryAdjustment) != 400-600 {
		t.Fatalf("count gain: %+v", c)
	}
	e.stockMatchesLedger(t)

	// Lower of cost and net realizable value: the stock can only be sold for 500.
	if _, err := e.m.Inventory.WriteDown(e.ctx, inventory.WriteDownInput{IdemKey: "w0", ItemID: it.ID, NetRealizableValue: irr(1200), Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a write-down that is not below the cost must be refused: %v", err)
	}
	w, err := e.m.Inventory.WriteDown(e.ctx, inventory.WriteDownInput{IdemKey: "w1", ItemID: it.ID, NetRealizableValue: irr(500), Reason: "damaged", Actor: "tester"})
	must(t, err)
	if w.Value != -700 || w.ValueAfter != 500 || w.QtyAfter != 6 {
		t.Fatalf("write-down: %+v", w)
	}
	e.stockMatchesLedger(t)
	// FIFO layers follow the write-down: issuing everything now takes exactly the remaining 500.
	last, err := e.issue("i2", it, 6)
	must(t, err)
	if last.Value != -500 {
		t.Fatalf("last issue: %+v", last)
	}
	e.stockMatchesLedger(t)

	ms, err := e.m.Inventory.Movements(e.ctx, it.ID)
	must(t, err)
	if len(ms) != 7 {
		t.Fatalf("%d movements", len(ms))
	}
}

func TestInventorySurplusNeedsACostBasis(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "ghost", inventory.WeightedAverage)
	if _, err := e.m.Inventory.Count(e.ctx, inventory.CountInput{IdemKey: "k", ItemID: it.ID, CountedQty: 5, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a surplus of an item with no stock has no cost: %v", err)
	}
}

func TestInventoryAndAssetPurchasesBookDeductibleVAT(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "tax-item", inventory.WeightedAverage)
	_, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "tv", ItemID: it.ID, Qty: 10, TotalCost: irr(100_000), VAT: irr(10_000), Actor: "tester"})
	must(t, err)
	got, _ := e.m.Inventory.Item(e.ctx, it.ID)
	if got.Value.Amount() != 100_000 { // the tax is not part of the stock's cost
		t.Fatalf("stock value %d", got.Value.Amount())
	}
	if e.bal(t, mizan.AccVATInput) != 10_000 || e.bal(t, mizan.AccPayable) != 110_000 || e.bal(t, mizan.AccInventory) != 100_000 {
		t.Fatalf("input VAT %d, payable %d, inventory %d", e.bal(t, mizan.AccVATInput), e.bal(t, mizan.AccPayable), e.bal(t, mizan.AccInventory))
	}
	if _, err := e.m.Inventory.Receive(e.ctx, inventory.ReceiveInput{IdemKey: "tv2", ItemID: it.ID, Qty: 1, TotalCost: irr(10), VAT: irr(-1)}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("a negative tax: %v", err)
	}
	e.stockMatchesLedger(t)
}

func TestInventoryWriteDownReversal(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "tile", inventory.FIFO)
	e.receive(t, "r1", it, 10, 1000)
	if _, err := e.m.Inventory.ReverseWriteDown(e.ctx, inventory.ReverseWriteDownInput{IdemKey: "x0", ItemID: it.ID, NetRealizableValue: irr(1500), Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("nothing was written down: %v", err)
	}
	_, err := e.m.Inventory.WriteDown(e.ctx, inventory.WriteDownInput{IdemKey: "w1", ItemID: it.ID, NetRealizableValue: irr(600), Actor: "tester"}) // 400 written down
	must(t, err)
	// NRV recovers to 5,000 but the reversal stops at the earlier reduction: back to the cost 1,000.
	m, err := e.m.Inventory.ReverseWriteDown(e.ctx, inventory.ReverseWriteDownInput{IdemKey: "x1", ItemID: it.ID, NetRealizableValue: irr(5000), Reason: "prices recovered", Actor: "tester"})
	must(t, err)
	if m.Kind != inventory.ReverseWriteDown || m.Value != 400 || m.ValueAfter != 1000 {
		t.Fatalf("reversal: %+v", m)
	}
	if again, err := e.m.Inventory.ReverseWriteDown(e.ctx, inventory.ReverseWriteDownInput{IdemKey: "x1", ItemID: it.ID, NetRealizableValue: irr(5000), Actor: "tester"}); err != nil || again.ID != m.ID {
		t.Fatalf("idempotent: %v", err)
	}
	if _, err := e.m.Inventory.ReverseWriteDown(e.ctx, inventory.ReverseWriteDownInput{IdemKey: "x2", ItemID: it.ID, NetRealizableValue: irr(5000), Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("the write-down is used up: %v", err)
	}
	e.stockMatchesLedger(t)
	if e.bal(t, mizan.AccInventoryAdjustment) != 0 {
		t.Fatalf("adjustment account %d", e.bal(t, mizan.AccInventoryAdjustment))
	}
	// A write-down, then half the stock leaves: only half of the reduction can ever be reversed.
	_, err = e.m.Inventory.WriteDown(e.ctx, inventory.WriteDownInput{IdemKey: "w2", ItemID: it.ID, NetRealizableValue: irr(600), Actor: "tester"})
	must(t, err)
	if _, err := e.issue("i1", it, 5); err != nil {
		t.Fatal(err)
	}
	m, err = e.m.Inventory.ReverseWriteDown(e.ctx, inventory.ReverseWriteDownInput{IdemKey: "x3", ItemID: it.ID, NetRealizableValue: irr(5000), Actor: "tester"})
	must(t, err)
	if m.Value != 200 {
		t.Fatalf("half the reduction: %+v", m)
	}
	e.stockMatchesLedger(t)
}

func TestIssueStockForInvoice(t *testing.T) {
	e := setup(t, true)
	it := e.item(t, "pipe", inventory.WeightedAverage)
	e.receive(t, "r1", it, 10, 1000)
	draft, err := e.m.Invoices.Create(e.ctx, invoice.CreateInput{IdemKey: "ivs", CustomerRef: "u1", Currency: money.IRR, Items: []invoice.ItemInput{{Description: "pipe", Quantity: 4, UnitPrice: 500}}})
	must(t, err)
	if _, err := e.m.IssueStockForInvoice(e.ctx, draft.ID, []mizan.InvoiceStockLine{{ItemID: it.ID, Qty: 4}}, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a draft invoice: %v", err)
	}
	inv, err := e.m.Invoices.Issue(e.ctx, draft.ID, "tester")
	must(t, err)
	mv, err := e.m.IssueStockForInvoice(e.ctx, inv.ID, []mizan.InvoiceStockLine{{ItemID: it.ID, Qty: 4}}, "tester")
	must(t, err)
	if len(mv) != 1 || mv[0].Value != -400 {
		t.Fatalf("movements: %+v", mv)
	}
	again, err := e.m.IssueStockForInvoice(e.ctx, inv.ID, []mizan.InvoiceStockLine{{ItemID: it.ID, Qty: 4}}, "tester")
	must(t, err)
	if again[0].ID != mv[0].ID || e.bal(t, mizan.AccCOGS) != 400 {
		t.Fatalf("a retry must not issue twice: cogs %d", e.bal(t, mizan.AccCOGS))
	}
	e.stockMatchesLedger(t)
}
