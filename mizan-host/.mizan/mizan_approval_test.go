package mizan_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

func TestTwoPersonApprovalOfYearClose(t *testing.T) {
	c := &clockEnv{cur: now}
	m, err := mizan.New(context.Background(), testdb.New(t), mizan.Config{IranianCoding: true, RequireApproval: true}, mizan.WithClock(func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.cur }))
	must(t, err)
	c.env = env{m: m, ctx: context.Background()}
	e := c
	e.post(t, "ap:sale", day1405, ledger.Dr(mizan.AccBank, irr(10_000_000)), ledger.Cr(mizan.AccRevenue, irr(10_000_000)))
	e.set(afterYear)
	fy := e.fiscalYear(t, "1405")
	if !m.ApprovalRequired() || m.RequireNoApproval() == nil {
		t.Fatal("approval must be required")
	}
	payload, _ := json.Marshal(mizan.YearClosePayload{FiscalYearID: fy.ID})
	if _, err := m.Approvals.Request(e.ctx, "nope", "x", payload, "k0", "ann"); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	a, err := m.Approvals.Request(e.ctx, mizan.ApprovalYearClose, fmt.Sprintf("fiscal_year:%d", fy.ID), payload, "k1", "ann")
	must(t, err)
	if again, err := m.Approvals.Request(e.ctx, mizan.ApprovalYearClose, a.Subject, payload, "k1", "ann"); err != nil || again.ID != a.ID {
		t.Fatalf("idempotent request: %v %v", again.ID, err)
	}
	if _, err := m.Approvals.Request(e.ctx, mizan.ApprovalYearClose, a.Subject, payload, "k2", "ann"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a second pending request for the same year: %v", err)
	}
	if _, err := m.Approvals.Approve(e.ctx, a.ID, "ann"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("the preparer approving: %v", err)
	}
	if y := e.fiscalYear(t, "1405"); y.Status == ledger.Closed {
		t.Fatal("closed before approval")
	}
	// Concurrent approvals by two different people: one execution.
	var wg sync.WaitGroup
	res := make([]error, 4)
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, res[i] = m.Approvals.Approve(e.ctx, a.ID, fmt.Sprintf("bob%d", i))
		}()
	}
	wg.Wait()
	for _, err := range res {
		if err != nil {
			t.Fatalf("approval: %v", err)
		}
	}
	done, err := m.Approvals.Get(e.ctx, a.ID)
	must(t, err)
	if done.Status != "executed" || done.DecidedBy == "" || done.RequestedBy != "ann" {
		t.Fatalf("approval: %+v", done)
	}
	if y := e.fiscalYear(t, "1405"); y.Status != ledger.Closed {
		t.Fatal("the year was not closed by the approval")
	}
	// Rejecting: a new request, then withdrawn; an executed one cannot be rejected.
	if _, err := m.Approvals.Reject(e.ctx, a.ID, "ann", "late"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("rejecting an executed request: %v", err)
	}
	b, err := m.Approvals.Request(e.ctx, mizan.ApprovalAppropriation, "fiscal_year:99", json.RawMessage(`{"fiscal_year_id":99}`), "k3", "ann")
	must(t, err)
	r, err := m.Approvals.Reject(e.ctx, b.ID, "carl", "not now")
	must(t, err)
	if r.Status != "rejected" || r.Reason != "not now" {
		t.Fatalf("rejected: %+v", r)
	}
	if _, err := m.Approvals.Approve(e.ctx, b.ID, "dave"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("approving a rejected request: %v", err)
	}
	// A failing action leaves the request pending.
	f, err := m.Approvals.Request(e.ctx, mizan.ApprovalAppropriation, "fiscal_year:98", json.RawMessage(`{"fiscal_year_id":98}`), "k4", "ann")
	must(t, err)
	if _, err := m.Approvals.Approve(e.ctx, f.ID, "dave"); err == nil {
		t.Fatal("an appropriation of an unknown year must fail")
	}
	if st, _ := m.Approvals.Get(e.ctx, f.ID); st.Status != "pending" {
		t.Fatalf("status after a failed run: %s", st.Status)
	}
}

func TestDirectEndpointsStayUsableWithoutApprovalSwitch(t *testing.T) {
	e := newClockEnv(t)
	if e.m.ApprovalRequired() || e.m.RequireNoApproval() != nil {
		t.Fatal("approval is off by default")
	}
}
