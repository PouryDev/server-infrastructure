package reconcile_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/reconcile"
)

func TestCompareFindsEveryKindOfDifference(t *testing.T) {
	ids := map[string]uuid.UUID{}
	l := func(ref string, amount int64, status string) reconcile.Local {
		ids[ref] = uuid.New()
		return reconcile.Local{ID: ids[ref], GatewayRef: ref, Amount: amount, Status: status}
	}
	local := []reconcile.Local{
		l("ok", 1000, "paid"),
		l("amount", 1000, "paid"),
		l("only-here", 500, "paid"),
		l("lost-callback", 700, "pending"),
		l("we-say-paid", 900, "paid"),
		l("abandoned", 100, "expired"),
		l("refunded", 300, "refunded"),
	}
	remote := []reconcile.Remote{
		{GatewayRef: "ok", Amount: 1000, Paid: true},
		{GatewayRef: "amount", Amount: 999, Paid: true},
		{GatewayRef: "ghost", Amount: 40, Paid: true},
		{GatewayRef: "lost-callback", Amount: 700, Paid: true},
		{GatewayRef: "we-say-paid", Amount: 900, Paid: false},
		{GatewayRef: "abandoned", Amount: 100, Paid: false},
		{GatewayRef: "refunded", Amount: 300, Paid: true},
	}
	got := map[string]reconcile.Kind{}
	for _, m := range reconcile.Compare(local, remote) {
		got[m.GatewayRef] = m.Kind
	}
	want := map[string]reconcile.Kind{
		"amount": reconcile.AmountMismatch, "ghost": reconcile.MissingInternal, "only-here": reconcile.MissingGateway,
		"lost-callback": reconcile.StatusMismatch, "we-say-paid": reconcile.StatusMismatch,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestParseCSV(t *testing.T) {
	items, err := reconcile.ParseCSV(strings.NewReader("ref,amount,status,txn_ref,time\nA1,\"1,500\",paid,T1,2026-11-09T10:00:00Z\nA2,200,failed,,\n"))
	if err != nil || len(items) != 2 || items[0].Amount != 1500 || !items[0].Paid || items[1].Paid || items[0].At.IsZero() {
		t.Fatalf("%+v %v", items, err)
	}
	for _, bad := range []string{"", "ref,amount\nA,1\n", "ref,amount,status\nA,x,paid\n", "ref,amount,status\n,5,paid\n"} {
		if _, err := reconcile.ParseCSV(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
