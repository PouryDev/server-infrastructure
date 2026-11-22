package mizan_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice/einvoicetest"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

var seller = einvoice.Party{Name: "Shop Ltd", NationalID: "10100302746", EconomicCode: "14003778990"}

func einvoiceEnv(t *testing.T, p einvoice.Provider) env {
	t.Helper()
	db := testdb.New(t)
	opts := []mizan.Option{mizan.WithClock(func() time.Time { return now })}
	if p != nil {
		opts = append(opts, mizan.WithEInvoice(p, "A11216", seller))
	}
	m, err := mizan.New(context.Background(), db, mizan.Config{}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return env{m: m, db: db, ctx: context.Background()}
}

func (e env) paidInvoiceNumber(t *testing.T, key string, price int64) invoice.Invoice {
	t.Helper()
	inv, err := e.m.Invoices.Create(e.ctx, invoice.CreateInput{IdemKey: key, CustomerRef: "u1", Currency: money.IRR,
		Buyer: invoice.Buyer{Name: "Ali", NationalID: "0012345678"}, Items: []invoice.ItemInput{{Description: "plan", Quantity: 2, UnitPrice: price}}})
	must(t, err)
	inv, err = e.m.Invoices.Issue(e.ctx, inv.ID, "tester")
	must(t, err)
	return inv
}

func TestEInvoiceEnqueueMapsTheInvoiceAndIsIdempotent(t *testing.T) {
	e := einvoiceEnv(t, einvoicetest.New())
	draft, err := e.m.Invoices.Create(e.ctx, invoice.CreateInput{IdemKey: "d", CustomerRef: "u1", Currency: money.IRR, Items: []invoice.ItemInput{{Description: "x", Quantity: 1, UnitPrice: 1000}}})
	must(t, err)
	if _, err := e.m.EnqueueInvoice(e.ctx, draft.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a draft invoice must not go to the authority: %v", err)
	}
	inv := e.paidInvoiceNumber(t, "i1", 50_000) // 2 x 50,000, VAT 10% = 10,000, total 110,000
	d, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
	must(t, err)
	if d.Status != einvoice.Queued || d.FiscalID != "A11216" || d.Invoice.Number != "1" || d.Invoice.Total != 110_000 || d.Invoice.VAT != 10_000 ||
		d.Invoice.Seller != seller || d.Invoice.Buyer.NationalID != "0012345678" || len(d.Invoice.Lines) != 1 || d.Invoice.Lines[0].VATBP != 1000 {
		t.Fatalf("%+v", d)
	}
	again, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
	must(t, err)
	if again.ID != d.ID || again.TraceID != d.TraceID {
		t.Fatal("an invoice has one e-invoice")
	}
}

func TestEInvoiceCancellationIsANewDocument(t *testing.T) {
	e := einvoiceEnv(t, einvoicetest.New())
	inv := e.paidInvoiceNumber(t, "i1", 50_000)
	orig, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
	must(t, err)
	const ref = "A1121604C220002F095011"
	opt := mizan.EInvoiceOptions{Subject: 3, RefTaxID: ref}
	c1, err := e.m.EnqueueInvoiceWith(e.ctx, inv.ID, "tester", opt)
	must(t, err)
	c2, err := e.m.EnqueueInvoiceWith(e.ctx, inv.ID, "tester", opt)
	must(t, err)
	if c1.ID == orig.ID || c1.ID != c2.ID || c1.Invoice.Subject != 3 || c1.Invoice.RefTaxID != ref {
		t.Fatalf("a cancellation is its own idempotent document: %+v", c1)
	}
	if rq, err := e.m.RequeueEInvoice(e.ctx, c1.ID, "tester"); err != nil && !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("requeue keeps the options: %v", err)
	} else if err == nil && rq.Invoice.Subject != 3 {
		t.Fatalf("%+v", rq.Invoice)
	}
}

func TestEInvoiceProviderDetailsAreStored(t *testing.T) {
	e := einvoiceEnv(t, einvoicetest.New())
	inv := e.paidInvoiceNumber(t, "i1", 50_000)
	d, err := e.m.EnqueueInvoiceWith(e.ctx, inv.ID, "tester", mizan.EInvoiceOptions{Pattern: 3, Details: &einvoice.Invoice{BillID: "123", Buyer: einvoice.Party{Kind: 2},
		Lines: []einvoice.Line{{WageFee: 7, SellerProfit: 3, OtherTaxSubject: "levy", OtherTaxBP: 100}}}})
	must(t, err)
	l := d.Invoice.Lines[0]
	if d.Invoice.Pattern != 3 || d.Invoice.BillID != "123" || d.Invoice.Buyer.Kind != 2 || l.WageFee != 7 || l.SellerProfit != 3 || l.OtherTaxBP != 100 || l.UnitPrice != 50_000 {
		t.Fatalf("%+v", d.Invoice)
	}
}

func TestEInvoiceSendFollowAndRequeue(t *testing.T) {
	p := einvoicetest.New()
	e := einvoiceEnv(t, p)
	inv := e.paidInvoiceNumber(t, "i1", 50_000)
	d, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
	must(t, err)

	n, err := e.m.EInvoice.SendQueued(e.ctx)
	must(t, err)
	got, _ := e.m.EInvoice.Get(e.ctx, d.ID)
	if n != 1 || got.Status != einvoice.Sent || got.ReferenceNumber == "" || got.SentAt == nil || got.Attempts != 1 || len(p.Packets) != 1 || p.Packets[0].TraceID != d.TraceID.String() {
		t.Fatalf("%d %+v", n, got)
	}
	if n, _ := e.m.EInvoice.SendQueued(e.ctx); n != 0 || len(p.Packets) != 1 {
		t.Fatal("a sent invoice must not be sent again")
	}

	// the verdict is pending until the authority decides
	if n, err := e.m.EInvoice.RefreshSent(e.ctx); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	p.Decide(d.TraceID.String(), einvoice.Result{Outcome: einvoice.OutcomeFailed, Errors: []einvoice.Issue{{Code: "012802", Message: "bad", Type: "ERROR"}}})
	if n, err := e.m.EInvoice.RefreshSent(e.ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	got, _ = e.m.EInvoice.Get(e.ctx, d.ID)
	if got.Status != einvoice.Failed || len(got.Errors) != 1 || got.Errors[0].Code != "012802" {
		t.Fatalf("%+v", got)
	}

	// requeue sends it under a new trace id; a document that is not failed cannot be requeued
	again, err := e.m.RequeueEInvoice(e.ctx, d.ID, "tester")
	must(t, err)
	if again.Status != einvoice.Queued || again.TraceID == d.TraceID || again.ReferenceNumber != "" || len(again.Errors) != 0 {
		t.Fatalf("%+v", again)
	}
	if _, err := e.m.RequeueEInvoice(e.ctx, d.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("%v", err)
	}
	_, err = e.m.EInvoice.SendQueued(e.ctx)
	must(t, err)
	p.Decide(again.TraceID.String(), einvoice.Result{Outcome: einvoice.OutcomeSuccess, Warnings: []einvoice.Issue{{Code: "111208", Message: "w", Type: "WARNING"}}})
	_, err = e.m.EInvoice.RefreshSent(e.ctx)
	must(t, err)
	got, _ = e.m.EInvoice.Get(e.ctx, d.ID)
	if got.Status != einvoice.Success || len(got.Warnings) != 1 || len(p.Packets) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestEInvoiceProviderFailureKeepsTheQueue(t *testing.T) {
	p := einvoicetest.New()
	e := einvoiceEnv(t, p)
	inv := e.paidInvoiceNumber(t, "i1", 1000)
	d, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
	must(t, err)
	p.SubmitErr = errors.New("authority is down")
	if _, err := e.m.EInvoice.SendQueued(e.ctx); err == nil {
		t.Fatal("the provider failure must be reported")
	}
	got, _ := e.m.EInvoice.Get(e.ctx, d.ID)
	if got.Status != einvoice.Queued || got.Attempts != 1 || got.LastError != "authority is down" {
		t.Fatalf("%+v", got)
	}
	p.SubmitErr = nil
	if n, err := e.m.EInvoice.SendQueued(e.ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestEInvoiceWithoutAProviderOnlyQueues(t *testing.T) {
	e := einvoiceEnv(t, nil)
	inv := e.paidInvoiceNumber(t, "i1", 1000)
	if _, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.EInvoice.SendQueued(e.ctx); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("%v", err)
	}
}

func TestEInvoiceConcurrentSendersSendEachInvoiceOnce(t *testing.T) {
	p := einvoicetest.New()
	p.Lim.Submit = 3 // small batches so several rounds and workers overlap
	e := einvoiceEnv(t, p)
	ids := map[uuid.UUID]bool{}
	for i := 0; i < 15; i++ {
		inv := e.paidInvoiceNumber(t, fmt.Sprintf("i%d", i), int64(1000+i))
		d, err := e.m.EnqueueInvoice(e.ctx, inv.ID, "tester")
		must(t, err)
		ids[d.ID] = true
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := e.m.EInvoice.SendQueued(e.ctx)
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	seen := map[string]int{}
	for _, pk := range p.Packets {
		seen[pk.TraceID]++
	}
	if len(p.Packets) != 15 || len(seen) != 15 {
		t.Fatalf("%d packets for %d distinct invoices", len(p.Packets), len(seen))
	}
}
