package mizan_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/coupon"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/subscription"
)

// clockEnv is an environment whose clock a test can move.
type clockEnv struct {
	env
	mu  sync.Mutex
	cur time.Time
}

func (c *clockEnv) set(t time.Time) { c.mu.Lock(); c.cur = t; c.mu.Unlock() }

func newClockEnv(t *testing.T) *clockEnv {
	t.Helper()
	c := &clockEnv{cur: now}
	m, err := mizan.New(context.Background(), testdb.New(t), mizan.Config{IranianCoding: true}, mizan.WithClock(func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.cur }))
	if err != nil {
		t.Fatal(err)
	}
	c.env = env{m: m, ctx: context.Background()}
	return c
}

func (e env) coupon(t *testing.T, in coupon.Input) coupon.Coupon {
	t.Helper()
	if in.Currency == "" {
		in.Currency = "IRR"
	}
	if in.Name == "" {
		in.Name = "test"
	}
	if in.Kind == "" {
		in.Kind, in.PercentBP = coupon.Percent, 1000
	}
	c, err := e.m.Coupons.Create(e.ctx, in)
	must(t, err)
	return c
}

func (e env) redeem(key, code, customer string, amount int64) (coupon.Redemption, error) {
	return e.m.Coupons.Redeem(e.ctx, coupon.RedeemInput{IdemKey: key, Code: code, CustomerRef: customer, Amount: irr(amount)})
}

func TestCouponRedemptionLimitsAndIdempotency(t *testing.T) {
	e := setup(t, false)
	from, until := d(2026, 11, 1), d(2026, 11, 30)
	one, two := 1, 2
	c := e.coupon(t, coupon.Input{Code: "save10", MinAmount: 1000, ValidFrom: &from, ValidUntil: &until, MaxRedemptions: &two, MaxPerCustomer: &one})

	r1, err := e.redeem("k1", "SAVE10", "alice", 50_000)
	must(t, err)
	if r1.Discount != irr(5000) || r1.Status != coupon.Redeemed {
		t.Fatalf("%+v", r1)
	}
	again, err := e.redeem("k1", "save10", "alice", 50_000) // the code is case-insensitive; the same key is the same redemption
	must(t, err)
	if again.ID != r1.ID {
		t.Fatal("the same key must return the same redemption")
	}
	if _, err := e.redeem("k1", "SAVE10", "alice", 60_000); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("the same key with other data: %v", err)
	}
	got, _ := e.m.Coupons.Get(e.ctx, c.ID)
	if got.Redeemed != 1 {
		t.Fatalf("a retry must count once, counted %d", got.Redeemed)
	}
	if _, err := e.redeem("k2", "SAVE10", "alice", 50_000); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("one use per customer: %v", err)
	}
	if _, err := e.redeem("k3", "SAVE10", "bob", 999); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("below the minimum: %v", err)
	}
	if _, err := e.redeem("k4", "SAVE10", "bob", 20_000); err != nil {
		t.Fatal(err)
	}
	if _, err := e.redeem("k5", "SAVE10", "carol", 20_000); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("used up: %v", err)
	}
	if _, err := e.redeem("k6", "NOPE", "carol", 20_000); !apperr.Is(err, apperr.KindNotFound) {
		t.Fatalf("%v", err)
	}

	// releasing a redemption frees its use; the total limit cannot be lowered below what is in force
	if _, err := e.m.Coupons.Release(e.ctx, r1.ID, "invoice voided", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Coupons.Release(e.ctx, r1.ID, "again", "tester"); err != nil {
		t.Fatalf("releasing twice is idempotent: %v", err)
	}
	got, _ = e.m.Coupons.Get(e.ctx, c.ID)
	if got.Redeemed != 1 {
		t.Fatalf("%d", got.Redeemed)
	}
	if _, err := e.redeem("k7", "SAVE10", "alice", 20_000); err != nil {
		t.Fatalf("alice may use it again after her redemption was released: %v", err)
	}
	if _, err := e.m.Coupons.Update(e.ctx, c.ID, coupon.UpdateInput{MaxRedemptions: &one, Actor: "tester"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("2 are in force, the limit cannot become 1: %v", err)
	}
	three := 3
	up, err := e.m.Coupons.Update(e.ctx, c.ID, coupon.UpdateInput{MaxRedemptions: &three, ClearValidUntil: true, Actor: "tester"})
	must(t, err)
	if *up.MaxRedemptions != 3 || up.ValidUntil != nil {
		t.Fatalf("%+v", up)
	}
	if _, err := e.m.Coupons.Preview(e.ctx, "SAVE10", "dave", irr(10_000)); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := e.m.Coupons.Update(e.ctx, c.ID, coupon.UpdateInput{Active: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.redeem("k8", "SAVE10", "dave", 20_000); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a disabled coupon: %v", err)
	}
}

func TestCouponLimitsHoldUnderConcurrency(t *testing.T) {
	e := setup(t, false)
	five, one := 5, 1
	e.coupon(t, coupon.Input{Code: "TOTAL5", MaxRedemptions: &five})
	e.coupon(t, coupon.Input{Code: "ONCE", MaxPerCustomer: &one})
	e.coupon(t, coupon.Input{Code: "SAMEKEY"})

	run := func(n int, fn func(i int) error) (ok int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := fn(i); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !apperr.Is(err, apperr.KindConflict) {
					t.Errorf("worker %d: %v", i, err)
				}
			}()
		}
		wg.Wait()
		return ok
	}
	if got := run(20, func(i int) error {
		_, err := e.redeem(fmt.Sprintf("t-%d", i), "TOTAL5", fmt.Sprintf("c%d", i), 10_000)
		return err
	}); got != 5 {
		t.Fatalf("20 customers, total limit 5: %d redeemed", got)
	}
	if got := run(10, func(i int) error { _, err := e.redeem(fmt.Sprintf("o-%d", i), "ONCE", "alice", 10_000); return err }); got != 1 {
		t.Fatalf("one customer, per-customer limit 1: %d redeemed", got)
	}
	ids := make([]uuid.UUID, 8)
	if got := run(8, func(i int) error {
		r, err := e.redeem("same", "SAMEKEY", "alice", 10_000)
		ids[i] = r.ID
		return err
	}); got != 8 {
		t.Fatalf("the same key from 8 workers must succeed for all: %d", got)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("one redemption expected: %v", ids)
		}
	}
	c, err := e.m.Coupons.List(e.ctx, query.Params{})
	must(t, err)
	for _, x := range c.Items {
		want := map[string]int{"TOTAL5": 5, "ONCE": 1, "SAMEKEY": 1}[x.Code]
		if x.Redeemed != want {
			t.Errorf("%s counted %d, want %d", x.Code, x.Redeemed, want)
		}
	}
}

func (c *clockEnv) plan(t *testing.T, code string, price int64, trial int) subscription.Plan {
	t.Helper()
	p, err := c.m.Subscribe.AddPlan(c.ctx, subscription.PlanInput{Code: code, Name: "Plan " + code, Price: irr(price), Unit: subscription.Month, Count: 1, TrialDays: trial, Actor: "tester"})
	must(t, err)
	return p
}

func (c *clockEnv) subscribe(t *testing.T, key string, p subscription.Plan, customer, couponCode string) subscription.Subscription {
	t.Helper()
	s, err := c.m.Subscribe.Subscribe(c.ctx, subscription.SubscribeInput{IdemKey: key, PlanID: p.ID, CustomerRef: customer, CouponCode: couponCode, Actor: "tester"})
	must(t, err)
	return s
}

func (c *clockEnv) invoicesOf(t *testing.T, customer string) []invoice.Summary {
	t.Helper()
	page, err := c.m.Invoices.List(c.ctx, query.Params{Filters: []query.Filter{{Field: "customer_ref", Op: query.OpEq, Values: []string{customer}}}, PageSize: 100})
	must(t, err)
	return page.Items
}

func TestSubscriptionBillsEachPeriodOnceAndFollowsTheJalaliCalendar(t *testing.T) {
	c := newClockEnv(t) // 1405/08/18
	p := c.plan(t, "pro", 1_000_000, 0)
	s := c.subscribe(t, "s1", p, "alice", "")
	if s.Status != subscription.Active || s.NextBill == nil || !s.NextBill.Equal(d(2026, 11, 9)) {
		t.Fatalf("%+v", s)
	}
	if again := c.subscribe(t, "s1", p, "alice", ""); again.ID != s.ID {
		t.Fatal("the same key must return the same subscription")
	}

	res, err := c.m.BillSubscriptions(c.ctx)
	must(t, err)
	if res.Periods != 1 || res.Invoiced != 1 || res.Failed != 0 {
		t.Fatalf("%+v", res)
	}
	inv := c.invoicesOf(t, "alice")
	if len(inv) != 1 || inv[0].Total != 1_100_000 || inv[0].Status != invoice.Issued || inv[0].Number == nil { // 1,000,000 + 10% VAT (the accounting setting)
		t.Fatalf("%+v", inv)
	}
	if got := c.bal(t, mizan.AccReceivable); got != 1_100_000 {
		t.Fatalf("the invoice is booked: receivable %d", got)
	}
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Periods+res.Invoiced != 0 {
		t.Fatalf("nothing is due again today: %+v", res)
	}

	// a month later (Jalali: 1405/09/18) period 2 is billed; many instances at once still bill it once
	c.set(d(2026, 12, 9))
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.m.BillSubscriptions(c.ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if inv := c.invoicesOf(t, "alice"); len(inv) != 2 {
		t.Fatalf("6 concurrent runs must bill period 2 once: %d invoices", len(inv))
	}
	periods, err := c.m.Subscribe.Periods(c.ctx, s.ID)
	must(t, err)
	if len(periods) != 2 || periods[1].No != 2 || periods[1].Status != subscription.PeriodInvoiced || !periods[1].Start.Equal(d(2026, 12, 9)) || !periods[1].End.Equal(d(2027, 1, 7)) {
		t.Fatalf("%+v", periods)
	}
	c.balanced(t)
}

func TestSubscriptionTrialPauseResumeAndCancel(t *testing.T) {
	c := newClockEnv(t)
	trial := c.plan(t, "trial", 500_000, 7)
	s := c.subscribe(t, "t1", trial, "bob", "")
	if s.Status != subscription.Trialing || !s.NextBill.Equal(d(2026, 11, 16)) {
		t.Fatalf("%+v", s)
	}
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Periods != 0 {
		t.Fatalf("nothing is billed during the trial: %+v", res)
	}
	c.set(d(2026, 11, 16))
	res, err := c.m.BillSubscriptions(c.ctx)
	must(t, err)
	got, _ := c.m.Subscribe.Get(c.ctx, s.ID)
	if res.Invoiced != 1 || got.Status != subscription.Active || got.PeriodsBilled != 1 {
		t.Fatalf("%+v %+v", res, got)
	}

	// paused time is not billed; resuming starts a new period today
	if _, err := c.m.Subscribe.Pause(c.ctx, s.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.m.Subscribe.Pause(c.ctx, s.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("%v", err)
	}
	c.set(d(2027, 2, 1))
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Periods != 0 {
		t.Fatalf("a paused subscription is not billed: %+v", res)
	}
	resumed, err := c.m.Subscribe.Resume(c.ctx, s.ID, "tester")
	must(t, err)
	if resumed.Status != subscription.Active || !resumed.NextBill.Equal(d(2027, 2, 1)) {
		t.Fatalf("%+v", resumed)
	}
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Invoiced != 1 {
		t.Fatalf("%+v", res)
	}
	periods, _ := c.m.Subscribe.Periods(c.ctx, s.ID)
	if len(periods) != 2 || periods[1].No != 2 || !periods[1].Start.Equal(d(2027, 2, 1)) {
		t.Fatalf("%+v", periods)
	}

	// cancel at the end of the paid period: the next run ends it without a new invoice
	if _, err := c.m.Subscribe.Cancel(c.ctx, s.ID, true, "tester"); err != nil {
		t.Fatal(err)
	}
	c.set(d(2027, 3, 5))
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Invoiced != 0 {
		t.Fatalf("%+v", res)
	}
	end, _ := c.m.Subscribe.Get(c.ctx, s.ID)
	if end.Status != subscription.Canceled || end.NextBill != nil || len(c.invoicesOf(t, "bob")) != 2 {
		t.Fatalf("%+v", end)
	}
	if _, err := c.m.Subscribe.Resume(c.ctx, s.ID, "tester"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("a canceled subscription cannot resume: %v", err)
	}

	// immediate cancel
	s2 := c.subscribe(t, "t2", c.plan(t, "p2", 100_000, 0), "carol", "")
	if x, err := c.m.Subscribe.Cancel(c.ctx, s2.ID, false, "tester"); err != nil || x.Status != subscription.Canceled {
		t.Fatalf("%+v %v", x, err)
	}
	if res, _ := c.m.BillSubscriptions(c.ctx); res.Periods != 0 {
		t.Fatalf("%+v", res)
	}
	c.balanced(t)
}

func TestSubscriptionCouponAppliesToTheFirstInvoiceOnly(t *testing.T) {
	c := newClockEnv(t)
	one := 1
	c.coupon(t, coupon.Input{Code: "FIRST20", Kind: coupon.Percent, PercentBP: 2000, MaxRedemptions: &one})
	p := c.plan(t, "pro", 1_000_000, 0)
	s := c.subscribe(t, "s1", p, "alice", "FIRST20")
	late := c.subscribe(t, "s2", p, "bob", "FIRST20") // the coupon is used up by the time bob is billed
	if _, err := c.m.BillSubscriptions(c.ctx); err != nil {
		t.Fatal(err)
	}
	a, b := c.invoicesOf(t, "alice"), c.invoicesOf(t, "bob")
	// one use only: whoever is billed first gets 1,000,000 - 20% = 800,000, VAT 10% on the discounted price = 880,000; the other pays in
	// full, 1,100,000: a coupon that is no longer good must not stop the billing
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("alice %+v bob %+v", a, b)
	}
	if got := a[0].Total + b[0].Total; got != 880_000+1_100_000 || a[0].Total == b[0].Total {
		t.Fatalf("exactly one customer gets the coupon: %d and %d", a[0].Total, b[0].Total)
	}
	winner := "alice"
	if b[0].Total == 880_000 {
		winner = "bob"
	}
	c.set(d(2026, 12, 9))
	if _, err := c.m.BillSubscriptions(c.ctx); err != nil {
		t.Fatal(err)
	}
	if inv := c.invoicesOf(t, winner); len(inv) != 2 || inv[0].Total+inv[1].Total != 880_000+1_100_000 {
		t.Fatalf("the coupon applies to the first period only: %+v", inv)
	}
	_ = s
	_ = late
	c.balanced(t)
}

func TestPlanPriceChangeDoesNotTouchRunningSubscriptions(t *testing.T) {
	c := newClockEnv(t)
	p := c.plan(t, "pro", 1_000_000, 0)
	s := c.subscribe(t, "s1", p, "alice", "")
	np := irr(2_000_000)
	if _, err := c.m.Subscribe.UpdatePlan(c.ctx, p.ID, subscription.PlanUpdate{Price: &np, Actor: "tester"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.m.BillSubscriptions(c.ctx); err != nil {
		t.Fatal(err)
	}
	if inv := c.invoicesOf(t, "alice"); len(inv) != 1 || inv[0].Total != 1_100_000 {
		t.Fatalf("the subscription keeps the price it started with: %+v", inv)
	}
	if fresh := c.subscribe(t, "s2", p, "bob", ""); fresh.Price != np {
		t.Fatalf("a new subscription gets the new price: %+v", fresh.Price)
	}
	off := false
	if _, err := c.m.Subscribe.UpdatePlan(c.ctx, p.ID, subscription.PlanUpdate{Active: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.m.Subscribe.Subscribe(c.ctx, subscription.SubscribeInput{IdemKey: "s3", PlanID: p.ID, CustomerRef: "carol"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an inactive plan takes no new subscribers: %v", err)
	}
	_ = s
}
