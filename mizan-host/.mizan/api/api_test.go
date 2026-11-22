package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/api"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/gateway/gatewaytest"
)

type env struct {
	srv *httptest.Server
	gw  *gatewaytest.Gateway
}

// testAuth stands in for httpx.Authenticate: the user and roles come from headers.
func testAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.Header.Get("X-User")
		if u == "" {
			httpx.WriteError(w, r, apperr.Unauthorized("missing credentials"))
			return
		}
		roles := strings.Fields(r.Header.Get("X-Roles"))
		next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), &auth.Claims{Subject: u, Roles: roles})))
	})
}

func setup(t *testing.T) env {
	t.Helper()
	return setupAt(t, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
}

func setupAt(t *testing.T, clock func() time.Time) env {
	t.Helper()
	db := testdb.New(t)
	gw := gatewaytest.New()
	m, err := mizan.New(context.Background(), db, mizan.Config{IranianCoding: true,
		Payment: payment.Config{CallbackBaseURL: "https://api.test/v1/gateway-callbacks", AllowedReturnHosts: []string{"shop.test"}}},
		mizan.WithGateway(gw), mizan.WithClock(clock))
	if err != nil {
		t.Fatal(err)
	}
	r := httpx.NewRouter()
	api.Mount(r, m, api.Options{Authenticate: testAuth})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return env{srv, gw}
}

type resp struct {
	status int
	body   map[string]any
	header http.Header
}

func (e env) do(t *testing.T, method, path, user, roles, key string, body any) resp {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.Header.Set("X-User", user)
		req.Header.Set("X-Roles", roles)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return resp{res.StatusCode, m, res.Header}
}

func (e env) raw(t *testing.T, method, path, user, roles string) string {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, nil)
	req.Header.Set("X-User", user)
	req.Header.Set("X-Roles", roles)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(res.Body)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("status %d, type %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	return buf.String()
}

func want(t *testing.T, r resp, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status %d, want %d: %v", r.status, status, r.body)
	}
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func TestAuthorizationBoundaries(t *testing.T) {
	e := setup(t)
	want(t, e.do(t, "GET", "/v1/accounting/trial-balance", "", "", "", nil), 401)
	want(t, e.do(t, "GET", "/v1/accounting/trial-balance", "u1", "customer", "", nil), 403)
	want(t, e.do(t, "GET", "/v1/accounting/trial-balance", "u9", "finance", "", nil), 200)
	want(t, e.do(t, "GET", "/v1/me/wallet", "", "", "", nil), 401)
	want(t, e.do(t, "POST", "/v1/invoices", "u9", "finance", "", map[string]any{}), 400) // no Idempotency-Key
}

func TestFullFlowOverHTTP(t *testing.T) {
	e := setup(t)
	const staff, finance = "staff-1", "finance"

	inv := e.do(t, "POST", "/v1/invoices", staff, finance, "inv-1", map[string]any{
		"customer_ref": "alice", "issue": true,
		"items": []map[string]any{{"description": "plan", "quantity": 2, "unit_price": 50000}},
	})
	want(t, inv, 201)
	id := str(inv.body, "id")
	if str(inv.body, "status") != "issued" || inv.body["number"].(float64) != 1 || inv.body["issue_date_jalali"] != "1405/08/18" {
		t.Fatalf("%v", inv.body)
	}
	// Retrying the same request returns the same invoice.
	again := e.do(t, "POST", "/v1/invoices", staff, finance, "inv-1", map[string]any{
		"customer_ref": "alice", "issue": true,
		"items": []map[string]any{{"description": "plan", "quantity": 2, "unit_price": 50000}},
	})
	if str(again.body, "id") != id {
		t.Fatal("a retry created a second invoice")
	}

	// Another customer cannot see it; the owner can.
	want(t, e.do(t, "GET", "/v1/me/invoices/"+id, "bob", "customer", "", nil), 404)
	want(t, e.do(t, "GET", "/v1/me/invoices/"+id, "alice", "customer", "", nil), 200)
	list := e.do(t, "GET", "/v1/me/invoices?filter[customer_ref]=bob", "alice", "customer", "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("a customer must only ever see their own invoices: %v", list.body)
	}

	// Alice starts a payment, pays at the "gateway", and the browser lands on the callback.
	pay := e.do(t, "POST", "/v1/me/invoices/"+id+"/payments", "alice", "customer", "try-1", map[string]any{"return_url": "https://shop.test/thanks"})
	want(t, pay, 201)
	payID := str(pay.body, "id")
	if !strings.HasPrefix(str(pay.body, "pay_url"), "https://pay.example/") {
		t.Fatalf("%v", pay.body)
	}
	want(t, e.do(t, "POST", "/v1/me/invoices/"+id+"/payments", "alice", "customer", "try-2", map[string]any{"return_url": "https://evil.test/x"}), 400)

	ref := strings.TrimPrefix(str(pay.body, "pay_url"), "https://pay.example/")
	e.gw.Pay(ref)
	cb := e.do(t, "GET", "/v1/gateway-callbacks/fake?ref="+ref+"&status=OK", "", "", "", nil)
	want(t, cb, http.StatusSeeOther)
	loc := cb.header.Get("Location")
	if !strings.HasPrefix(loc, "https://shop.test/thanks?") || !strings.Contains(loc, "status=paid") || !strings.Contains(loc, payID) {
		t.Fatalf("redirect %q", loc)
	}
	want(t, e.do(t, "GET", "/v1/gateway-callbacks/fake?ref="+ref+"&status=OK", "", "", "", nil), http.StatusSeeOther) // duplicate

	got := e.do(t, "GET", "/v1/invoices/"+id, staff, finance, "", nil)
	want(t, got, 200)
	if str(got.body["invoice"].(map[string]any), "status") != "paid" {
		t.Fatalf("%v", got.body)
	}

	// The books: balanced, receivable cleared, VAT booked, report by coding has no unclassified accounts.
	tb := e.do(t, "GET", "/v1/accounting/trial-balance", staff, finance, "", nil)
	if tb.body["balanced"] != true {
		t.Fatalf("%v", tb.body)
	}
	byCoding := e.do(t, "GET", "/v1/accounting/trial-balance?by=coding", staff, finance, "", nil)
	if u, _ := byCoding.body["unclassified"].([]any); len(u) != 0 {
		t.Fatalf("%v", byCoding.body)
	}
	st := e.do(t, "GET", "/v1/accounting/accounts/receivable.customers/statement?from=1405/08/01&to=1405/08/30", staff, finance, "", nil)
	want(t, st, 200)
	if rows, _ := st.body["rows"].([]any); len(rows) != 2 || rows[1].(map[string]any)["balance"].(float64) != 0 {
		t.Fatalf("statement %v", st.body)
	}

	// Refund through the API.
	rf := e.do(t, "POST", "/v1/payments/"+payID+"/refunds", staff, finance, "rf-1", map[string]any{"reason": "test"})
	want(t, rf, 201)
	if str(rf.body, "status") != "done" {
		t.Fatalf("%v", rf.body)
	}
}

func TestManualJournalAndReverse(t *testing.T) {
	e := setup(t)
	body := map[string]any{"description": "capital", "date": "1405/08/18", "lines": []map[string]any{
		{"account": "bank.main", "side": "debit", "amount": map[string]any{"amount": 1000000, "currency": "IRR"}},
		{"account": "revenue.sales", "side": "credit", "amount": map[string]any{"amount": 1000000, "currency": "IRR"}},
	}}
	j := e.do(t, "POST", "/v1/accounting/journals", "s", "finance", "j-1", body)
	want(t, j, 201)
	if j.body["date"] != "2026-11-09" || j.body["date_jalali"] != "1405/08/18" {
		t.Fatalf("%v", j.body)
	}
	bad := map[string]any{"description": "x", "lines": []map[string]any{
		{"account": "bank.main", "side": "debit", "amount": map[string]any{"amount": 10, "currency": "IRR"}},
		{"account": "revenue.sales", "side": "credit", "amount": map[string]any{"amount": 9, "currency": "IRR"}},
	}}
	want(t, e.do(t, "POST", "/v1/accounting/journals", "s", "finance", "j-2", bad), 400)
	rev := e.do(t, "POST", "/v1/accounting/journals/"+str(j.body, "id")+"/reverse", "s", "finance", "r-1", map[string]any{"reason": "oops"})
	want(t, rev, 201)
	want(t, e.do(t, "POST", "/v1/accounting/journals/"+str(j.body, "id")+"/reverse", "s", "finance", "r-2", map[string]any{}), 409)
	want(t, e.do(t, "PUT", "/v1/accounting/journals/"+str(j.body, "id"), "s", "finance", "", map[string]any{}), 405) // no edit route exists
	want(t, e.do(t, "DELETE", "/v1/accounting/journals/"+str(j.body, "id"), "s", "finance", "", nil), 405)
}

func TestManualRefundWorkflowOverHTTP(t *testing.T) {
	e := setup(t)
	e.gw.NoRefunds = true
	const finance = "finance"
	inv := e.do(t, "POST", "/v1/invoices", "s", finance, "inv", map[string]any{"customer_ref": "alice", "issue": true,
		"items": []map[string]any{{"description": "plan", "quantity": 1, "unit_price": 100000, "tax_bp": 0}}})
	want(t, inv, 201)
	pay := e.do(t, "POST", "/v1/me/invoices/"+str(inv.body, "id")+"/payments", "alice", "customer", "try", map[string]any{})
	want(t, pay, 201)
	ref := strings.TrimPrefix(str(pay.body, "pay_url"), "https://pay.example/")
	e.gw.Pay(ref)
	want(t, e.do(t, "GET", "/v1/gateway-callbacks/fake?ref="+ref+"&status=OK", "", "", "", nil), 200)
	payID := str(pay.body, "id")

	rf := e.do(t, "POST", "/v1/payments/"+payID+"/refunds", "s", finance, "rf-1", map[string]any{"amount": map[string]any{"amount": 30000, "currency": "IRR"}, "reason": "returned"})
	want(t, rf, 202)
	id := str(rf.body, "id")
	if str(rf.body, "method") != "manual_card_to_card" || str(rf.body, "status") != "pending" {
		t.Fatalf("%v", rf.body)
	}

	// The queue: only finance sees it; oldest first; pending and processing only.
	q := "/v1/refunds?filter[method]=manual_card_to_card&filter[status][in]=pending,processing"
	want(t, e.do(t, "GET", q, "alice", "customer", "", nil), 403)
	list := e.do(t, "GET", q, "ann", finance, "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != id {
		t.Fatalf("queue: %v", list.body)
	}

	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/claim", "ann", "customer", "", nil), 403)
	claimed := e.do(t, "POST", "/v1/refunds/"+id+"/claim", "ann", finance, "", nil)
	want(t, claimed, 200)
	if str(claimed.body, "status") != "processing" || str(claimed.body, "claimed_by") != "ann" {
		t.Fatalf("the agent is the authenticated user: %v", claimed.body)
	}
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/claim", "bob", finance, "", nil), 409)
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/release", "bob", finance, "", map[string]any{}), 409)
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/release", "boss", finance, "", map[string]any{"force": true}), 200)
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/claim", "ann", finance, "", nil), 200)

	body := map[string]any{"tracking_number": "۱۲۳۴۵۶۷۸۹", "paid_at": "1405/08/18", "destination_card_masked": "603799******1234", "note": "ok"}
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/complete", "ann", finance, "", body), 400) // Idempotency-Key is required
	full := map[string]any{"tracking_number": "123456789", "paid_at": "1405/08/18", "destination_card_masked": "6037997012345675"}
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/complete", "ann", finance, "c-0", full), 400) // a full card number is refused
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/complete", "ann", finance, "c-0", map[string]any{"tracking_number": "123456789", "paid_at": "soon", "destination_card_masked": "603799******1234"}), 400)
	done := e.do(t, "POST", "/v1/refunds/"+id+"/complete", "ann", finance, "c-1", body)
	want(t, done, 200)
	if str(done.body, "status") != "done" || str(done.body, "tracking_number") != "123456789" || str(done.body, "destination_card_masked") != "603799******1234" || str(done.body, "journal_id") == "" {
		t.Fatalf("%v", done.body)
	}
	want(t, e.do(t, "POST", "/v1/refunds/"+id+"/complete", "ann", finance, "c-1", body), 200) // a retry is a no-op

	// A second refund cannot reuse the tracking number.
	rf2 := e.do(t, "POST", "/v1/payments/"+payID+"/refunds", "s", finance, "rf-2", map[string]any{"reason": "rest"})
	want(t, rf2, 202)
	id2 := str(rf2.body, "id")
	want(t, e.do(t, "POST", "/v1/refunds/"+id2+"/claim", "ann", finance, "", nil), 200)
	want(t, e.do(t, "POST", "/v1/refunds/"+id2+"/complete", "ann", finance, "c-2", body), 409)
	want(t, e.do(t, "POST", "/v1/refunds/"+id2+"/fail", "ann", finance, "", map[string]any{"reason": "x"}), 400) // key required
	failed := e.do(t, "POST", "/v1/refunds/"+id2+"/fail", "ann", finance, "f-1", map[string]any{"reason": "destination card blocked"})
	want(t, failed, 200)
	if str(failed.body, "status") != "failed" || str(failed.body, "failure") != "destination card blocked" {
		t.Fatalf("%v", failed.body)
	}
	got := e.do(t, "GET", "/v1/refunds/"+id2, "ann", finance, "", nil)
	want(t, got, 200)
	want(t, e.do(t, "GET", "/v1/refunds/"+uuidZero, "ann", finance, "", nil), 404)

	// Every step is audited with the agent.
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/audit/refund/"+id, nil)
	req.Header.Set("X-User", "ann")
	req.Header.Set("X-Roles", finance)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var rows []struct{ Actor, Action string }
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{}
	for _, r := range rows {
		actions[r.Action] = r.Actor
	}
	if actions["refund.manual_requested"] != "s" || actions["refund.claimed"] != "ann" || actions["refund.released"] != "boss" || actions["refund.completed"] != "ann" {
		t.Fatalf("every step is audited with who did it: %v", actions)
	}
}

const uuidZero = "00000000-0000-0000-0000-000000000000"

func TestTreasuryOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/treasury/position", "alice", "customer", "", nil), 403)
	accs := e.do(t, "POST", "/v1/bank-accounts", "ann", finance, "", map[string]any{"name": "Savings", "bank_name": "Bank B", "iban": "IR12"})
	want(t, accs, 400) // not a valid IBAN
	sav := e.do(t, "POST", "/v1/bank-accounts", "ann", finance, "", map[string]any{"name": "Savings", "bank_name": "Bank B"})
	want(t, sav, 201)
	savID := str(sav.body, "id")

	// A cheque received (Jalali due date), deposited and cleared.
	body := map[string]any{"direction": "receivable", "sayad_id": "1234567890123456", "number": "555", "amount": irr(900000), "due_date": "1405/09/10", "counterparty_ref": "alice"}
	want(t, e.do(t, "POST", "/v1/cheques", "ann", finance, "", body), 400) // Idempotency-Key is required
	ch := e.do(t, "POST", "/v1/cheques", "ann", finance, "ch-1", body)
	want(t, ch, 201)
	if str(ch.body, "status") != "in_hand" || str(ch.body, "due_date") != "2026-12-01" || str(ch.body, "due_date_jalali") != "1405/09/10" {
		t.Fatalf("%v", ch.body)
	}
	id := str(ch.body, "id")
	if again := e.do(t, "POST", "/v1/cheques", "ann", finance, "ch-1", body); str(again.body, "id") != id {
		t.Fatal("a retry created a second cheque")
	}
	body2 := map[string]any{"direction": "receivable", "sayad_id": "1234567890123456", "number": "556", "amount": irr(1), "due_date": "2026-12-01", "counterparty_ref": "bob"}
	want(t, e.do(t, "POST", "/v1/cheques", "ann", finance, "ch-2", body2), 409) // the same Sayad id
	want(t, e.do(t, "POST", "/v1/cheques/"+id+"/clear", "ann", finance, "", map[string]any{}), 409)
	want(t, e.do(t, "POST", "/v1/cheques/"+id+"/deposit", "ann", finance, "", map[string]any{"bank_account_id": savID}), 200)
	got := e.do(t, "GET", "/v1/cheques/"+id, "ann", finance, "", nil)
	want(t, got, 200)
	if hist, _ := got.body["history"].([]any); len(hist) != 2 {
		t.Fatalf("%v", got.body)
	}

	// The statement of the savings account arrives as CSV and settles the cheque by its Sayad id.
	csv := "date,description,reference,credit,debit\n1405/08/18,\"Cheque 1234567890123456\",R1,\"900,000\",\n1405/08/18,Account charge,R2,,2500\n"
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/bank-accounts/"+savID+"/statements", strings.NewReader(csv))
	req.Header.Set("Content-Type", "text/csv")
	req.Header.Set("X-User", "ann")
	req.Header.Set("X-Roles", finance)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var imp map[string]float64
	_ = json.NewDecoder(res.Body).Decode(&imp)
	res.Body.Close()
	if res.StatusCode != 201 || imp["inserted"] != 2 {
		t.Fatalf("import: %d %v", res.StatusCode, imp)
	}
	auto := e.do(t, "POST", "/v1/bank-accounts/"+savID+"/auto-match", "ann", finance, "", nil)
	want(t, auto, 200)
	if auto.body["matched"].(float64) != 1 {
		t.Fatalf("%v", auto.body)
	}
	lines := e.do(t, "GET", "/v1/statement-lines?filter[status]=unmatched&filter[bank_account_id]="+savID, "ann", finance, "", nil)
	want(t, lines, 200)
	items, _ := lines.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["amount"].(float64) != -2500 {
		t.Fatalf("%v", lines.body)
	}
	lineID := items[0].(map[string]any)["id"].(string)
	want(t, e.do(t, "POST", "/v1/statement-lines/"+lineID+"/match", "ann", finance, "", map[string]any{"kind": "fee"}), 200)
	if cl := e.do(t, "GET", "/v1/cheques/"+id, "ann", finance, "", nil); str(cl.body["cheque"].(map[string]any), "status") != "cleared" {
		t.Fatalf("%v", cl.body)
	}

	// A bounced issued cheque with a bank fee, then the position.
	out := e.do(t, "POST", "/v1/cheques", "ann", finance, "ch-3", map[string]any{"direction": "payable", "number": "9", "amount": irr(50000),
		"due_date": "2026-12-20", "counterparty_ref": "supplier", "bank_account_id": savID})
	want(t, out, 201)
	want(t, e.do(t, "POST", "/v1/cheques/"+str(out.body, "id")+"/bounce", "ann", finance, "", map[string]any{"reason": "no funds", "fee": irr(100)}), 200)
	pos := e.do(t, "GET", "/v1/treasury/position", "ann", finance, "", nil)
	want(t, pos, 200)
	banks := pos.body["banks"].(map[string]any)["amount"].(float64)
	out2 := pos.body["cheques_out"].(map[string]any)["amount"].(float64)
	if banks != 900000-2500-100 || out2 != 50000 {
		t.Fatalf("position %v", pos.body)
	}

	// Settlement validation: the clearing account is empty here, so a payout is refused; a header is required.
	set := map[string]any{"gateway": "zarinpal", "ref": "S1", "bank_account_id": savID, "gross": irr(1000)}
	want(t, e.do(t, "POST", "/v1/gateway-settlements", "ann", finance, "", set), 400)
	want(t, e.do(t, "POST", "/v1/gateway-settlements", "ann", finance, "s-1", set), 409)
	want(t, e.do(t, "GET", "/v1/gateway-settlements", "ann", finance, "", nil), 200)
}

func TestPayrollOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/payroll/employees", "alice", "customer", "", nil), 403)
	emp := e.do(t, "POST", "/v1/payroll/employees", "ann", finance, "", map[string]any{"code": "E1", "name": "Ali", "national_id": "۰۰۱۲۳۴۵۶۷۸"})
	want(t, emp, 201)
	want(t, e.do(t, "POST", "/v1/payroll/employees", "ann", finance, "", map[string]any{"code": "E1", "name": "Dup"}), 409)
	want(t, e.do(t, "POST", "/v1/payroll/employees", "ann", finance, "", map[string]any{"code": "E2", "name": "Bad", "national_id": "12"}), 400)
	id := str(emp.body, "id")

	// contract with a Jalali start date and a taxable, insurable housing allowance
	con := e.do(t, "POST", "/v1/payroll/employees/"+id+"/contracts", "ann", finance, "", map[string]any{
		"base_salary": irr(300_000_000), "insured": true, "start": "1404/10/11",
		"allowances": []map[string]any{{"name": "Housing", "amount": irr(30_000_000), "taxable": true, "insurable": false}}})
	want(t, con, 201)
	want(t, e.do(t, "POST", "/v1/payroll/employees/"+id+"/contracts", "ann", finance, "", map[string]any{"base_salary": irr(1), "start": "1405/01/01"}), 409)

	// the rules are data: the starting version is there, a new one is added through the API
	rules := e.do(t, "GET", "/v1/payroll/rule-versions", "ann", finance, "", nil)
	want(t, rules, 200)
	bad := map[string]any{"name": "bad", "effective_from": "1406/01/01", "employee_bp": 700, "brackets": []map[string]any{{"up_to": 5, "rate_bp": 0}}}
	want(t, e.do(t, "POST", "/v1/payroll/rule-versions", "ann", finance, "", bad), 400) // the last bracket must be unlimited
	v := e.do(t, "POST", "/v1/payroll/rule-versions", "ann", finance, "", map[string]any{"name": "1406", "effective_from": "1406/01/01", "employee_bp": 700,
		"employer_bp": 2000, "unemployment_bp": 300, "brackets": []map[string]any{{"up_to": 500_000_000, "rate_bp": 0}, {"up_to": 0, "rate_bp": 1000}}, "source": "test"})
	want(t, v, 201)
	if str(v.body, "effective_from") != "2027-03-21" || str(v.body, "effective_from_jalali") != "1406/01/01" {
		t.Fatalf("%v", v.body)
	}
	at := e.do(t, "GET", "/v1/payroll/rule-versions/at?date=1405/08/01", "ann", finance, "", nil)
	want(t, at, 200)
	if str(at.body, "name") != "Iran 1405" {
		t.Fatalf("%v", at.body)
	}
	want(t, e.do(t, "DELETE", "/v1/payroll/rule-versions/"+str(v.body, "id"), "ann", finance, "", nil), 204)

	// preview does not book; issuing does and is idempotent per (employee, month)
	req := map[string]any{"employee_id": id, "year": 1405, "month": 8}
	pv := e.do(t, "POST", "/v1/payroll/payslips/preview", "ann", finance, "", req)
	want(t, pv, 200)
	if pv.body["net"].(map[string]any)["amount"].(float64) != 330_000_000-21_000_000 {
		t.Fatalf("housing is not insurable, so the 7%% is on the base only: %v", pv.body)
	}
	slip := e.do(t, "POST", "/v1/payroll/payslips", "ann", finance, "", req)
	want(t, slip, 201)
	slipID := str(slip.body, "id")
	if again := e.do(t, "POST", "/v1/payroll/payslips", "ann", finance, "", req); str(again.body, "id") != slipID {
		t.Fatal("a retry issued a second payslip")
	}
	want(t, e.do(t, "POST", "/v1/payroll/payslips", "ann", finance, "", map[string]any{"employee_id": id, "year": 1405, "month": 13}), 400)
	list := e.do(t, "GET", "/v1/payroll/payslips?filter[status][in]=issued&filter[year]=1405", "ann", finance, "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}

	// payment run: needs a key; paying twice moves nothing
	run := map[string]any{"year": 1405, "month": 8}
	want(t, e.do(t, "POST", "/v1/payroll/runs", "ann", finance, "", run), 400)
	r1 := e.do(t, "POST", "/v1/payroll/runs", "ann", finance, "run-1", run)
	want(t, r1, 201)
	if r1.body["count"].(float64) != 1 || r1.body["total"].(map[string]any)["amount"].(float64) != 309_000_000 {
		t.Fatalf("%v", r1.body)
	}
	if again := e.do(t, "POST", "/v1/payroll/runs", "ann", finance, "run-1", run); str(again.body, "id") != str(r1.body, "id") {
		t.Fatal("a retry created a second run")
	}
	want(t, e.do(t, "POST", "/v1/payroll/runs", "ann", finance, "run-2", run), 409)
	want(t, e.do(t, "POST", "/v1/payroll/payslips/"+slipID+"/void", "ann", finance, "", map[string]any{"reason": "x"}), 409) // paid
	got := e.do(t, "GET", "/v1/payroll/payslips/"+slipID, "ann", finance, "", nil)
	want(t, got, 200)
	if str(got.body, "status") != "paid" {
		t.Fatalf("%v", got.body)
	}
}

func TestPayrollLoansRemittancesAndTermsOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	emp := e.do(t, "POST", "/v1/payroll/employees", "ann", finance, "", map[string]any{"code": "E1", "name": "Ali"})
	want(t, emp, 201)
	id := str(emp.body, "id")
	want(t, e.do(t, "POST", "/v1/payroll/employees/"+id+"/contracts", "ann", finance, "", map[string]any{
		"base_salary": irr(1_500_000_000), "insured": true, "start": "1404/10/11",
		"allowances": []map[string]any{{"name": "Seniority", "amount": irr(0), "wage_base": true}}}), 201)

	// a loan: needs a key, repeats safely, shows up in the payslip as a deduction
	loan := map[string]any{"principal": irr(100_000_000), "installment": irr(30_000_000), "start_year": 1405, "start_month": 8}
	want(t, e.do(t, "POST", "/v1/payroll/employees/"+id+"/loans", "ann", finance, "", loan), 400)
	l := e.do(t, "POST", "/v1/payroll/employees/"+id+"/loans", "ann", finance, "ln-1", loan)
	want(t, l, 201)
	if again := e.do(t, "POST", "/v1/payroll/employees/"+id+"/loans", "ann", finance, "ln-1", loan); str(again.body, "id") != str(l.body, "id") {
		t.Fatal("a retry granted a second loan")
	}
	req := map[string]any{"employee_id": id, "year": 1405, "month": 8, "hours": map[string]any{"overtime_minutes": 60}, "leave_days": 2}
	slip := e.do(t, "POST", "/v1/payroll/payslips", "ann", finance, "", req)
	want(t, slip, 201)
	if slip.body["deduction_total"].(map[string]any)["amount"].(float64) != 30_000_000 || len(slip.body["deductions"].([]any)) != 1 {
		t.Fatalf("%v", slip.body)
	}
	// overtime 1h = 1.4 x 1.5B / 219.9 = 9,549,795; leave 2 days = 100M
	codes := map[string]float64{}
	for _, ln := range slip.body["lines"].([]any) {
		m := ln.(map[string]any)
		codes[str(m, "code")] = m["amount"].(map[string]any)["amount"].(float64)
	}
	if codes["overtime"] != 9_549_795 || codes["leave_payout"] != 100_000_000 {
		t.Fatalf("%v", codes)
	}
	loans := e.do(t, "GET", "/v1/payroll/employees/"+id+"/loans", "ann", finance, "", nil)
	want(t, loans, 200)

	// remittances: tax is owed; paying more than that is refused
	rem := map[string]any{"kind": "tax", "amount": irr(1_000_000), "period_year": 1405, "period_month": 8, "reference": "T-77"}
	want(t, e.do(t, "POST", "/v1/payroll/remittances", "ann", finance, "", rem), 400)
	r1 := e.do(t, "POST", "/v1/payroll/remittances", "ann", finance, "rm-1", rem)
	want(t, r1, 201)
	if again := e.do(t, "POST", "/v1/payroll/remittances", "ann", finance, "rm-1", rem); str(again.body, "id") != str(r1.body, "id") {
		t.Fatal("a retry paid twice")
	}
	rem["amount"] = irr(900_000_000_000)
	want(t, e.do(t, "POST", "/v1/payroll/remittances", "ann", finance, "rm-2", rem), 409)
	rem["kind"] = "vat"
	want(t, e.do(t, "POST", "/v1/payroll/remittances", "ann", finance, "rm-3", rem), 400)
	list := e.do(t, "GET", "/v1/payroll/remittances?kind=tax", "ann", finance, "", nil)
	want(t, list, 200)

	// a rule version carries its terms through the API
	at := e.do(t, "GET", "/v1/payroll/rule-versions/at?date=1405/08/01", "ann", finance, "", nil)
	want(t, at, 200)
	terms := at.body["terms"].(map[string]any)
	if terms["days_per_month"].(float64) != 30 || terms["overtime"].(map[string]any)["bp"].(float64) != 14000 {
		t.Fatalf("%v", terms)
	}
}

func TestPayoutOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/payout/sellers", "alice", "customer", "", nil), 403)
	sel := e.do(t, "POST", "/v1/payout/sellers", "ann", finance, "", map[string]any{"code": "S1", "name": "Shop"})
	want(t, sel, 201)
	want(t, e.do(t, "POST", "/v1/payout/sellers", "ann", finance, "", map[string]any{"code": "S1", "name": "Dup"}), 409)
	want(t, e.do(t, "POST", "/v1/payout/sellers", "ann", finance, "", map[string]any{"code": "S2", "name": "Bad", "sheba": "IR12"}), 400)
	id := str(sel.body, "id")

	// the terms are data: a starting default (10%, 7 days) is seeded; new versions are added through the API in Jalali or Gregorian dates
	sale := map[string]any{"seller_id": id, "gross": irr(1_000_000), "reference": "o-1", "category": "books"}
	seeded := e.do(t, "GET", "/v1/payout/terms", "ann", finance, "", nil)
	want(t, seeded, 200)
	want(t, e.do(t, "POST", "/v1/payout/terms", "ann", finance, "", map[string]any{"name": "same day", "effective_from": "1405/01/01", "rate_bp": 900}), 409)
	want(t, e.do(t, "POST", "/v1/payout/terms", "ann", finance, "", map[string]any{"name": "bad", "effective_from": "1405/01/01", "rate_bp": 20000}), 400)
	def := e.do(t, "POST", "/v1/payout/terms", "ann", finance, "", map[string]any{"name": "default", "effective_from": "1405/02/01", "rate_bp": 1000, "hold_days": 7, "source": "test"})
	want(t, def, 201)
	if str(def.body, "effective_from") != "2026-04-21" || str(def.body, "scope") != "default" {
		t.Fatalf("%v", def.body)
	}
	cat := e.do(t, "POST", "/v1/payout/terms", "ann", finance, "", map[string]any{"name": "books", "scope": "category", "scope_key": "books", "effective_from": "1405/01/01", "rate_bp": 500, "hold_days": 3})
	want(t, cat, 201)
	at := e.do(t, "GET", "/v1/payout/terms/at?seller_id="+id+"&category=books&date=2026-11-01", "ann", finance, "", nil)
	want(t, at, 200)
	if at.body["rate_bp"].(float64) != 500 {
		t.Fatalf("%v", at.body)
	}

	// sale: needs the key, books the split, is idempotent, and can be listed with a filter
	want(t, e.do(t, "POST", "/v1/payout/sales", "ann", finance, "", sale), 400)
	s1 := e.do(t, "POST", "/v1/payout/sales", "ann", finance, "s-1", sale)
	want(t, s1, 201)
	if s1.body["phase"] != "awaiting_delivery" || s1.body["commission"].(map[string]any)["amount"].(float64) != 50_000 || s1.body["vat"].(map[string]any)["amount"].(float64) != 5_000 || s1.body["net"].(map[string]any)["amount"].(float64) != 945_000 {
		t.Fatalf("%v", s1.body)
	}
	if again := e.do(t, "POST", "/v1/payout/sales", "ann", finance, "s-1", sale); str(again.body, "id") != str(s1.body, "id") {
		t.Fatal("a retry created a second sale")
	}
	list := e.do(t, "GET", "/v1/payout/sales?filter[status][in]=held,paid&filter[seller_id]="+id, "ann", finance, "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}

	// nothing is payable before delivery; deliver today, release early, then pay
	want(t, e.do(t, "POST", "/v1/payout/batches", "ann", finance, "b-1", map[string]any{}), 409)
	sid := str(s1.body, "id")
	del := e.do(t, "POST", "/v1/payout/sales/"+sid+"/deliver", "ann", finance, "", map[string]any{})
	want(t, del, 200)
	if del.body["phase"] != "held" || del.body["release_at"] != "2026-11-12" {
		t.Fatalf("%v", del.body)
	}
	want(t, e.do(t, "POST", "/v1/payout/sales/"+sid+"/release", "ann", finance, "", nil), 200)
	bal := e.do(t, "GET", "/v1/payout/sellers/"+id+"/balance", "ann", finance, "", nil)
	want(t, bal, 200)
	if bal.body["available"].(map[string]any)["amount"].(float64) != 945_000 {
		t.Fatalf("%v", bal.body)
	}

	// a blocked seller is skipped; unblocking lets the batch pay
	want(t, e.do(t, "POST", "/v1/payout/sellers/"+id+"/block", "ann", finance, "", map[string]any{}), 400)
	want(t, e.do(t, "POST", "/v1/payout/sellers/"+id+"/block", "ann", finance, "", map[string]any{"reason": "dispute"}), 200)
	want(t, e.do(t, "POST", "/v1/payout/batches", "ann", finance, "b-0", map[string]any{}), 409)
	want(t, e.do(t, "POST", "/v1/payout/sellers/"+id+"/unblock", "ann", finance, "", nil), 200)

	b := e.do(t, "POST", "/v1/payout/batches", "ann", finance, "b-1", map[string]any{})
	want(t, b, 201)
	if b.body["total"].(map[string]any)["amount"].(float64) != 945_000 || len(b.body["items"].([]any)) != 1 {
		t.Fatalf("%v", b.body)
	}
	if again := e.do(t, "POST", "/v1/payout/batches", "ann", finance, "b-1", map[string]any{}); str(again.body, "id") != str(b.body, "id") {
		t.Fatal("a retry created a second batch")
	}
	want(t, e.do(t, "GET", "/v1/payout/batches/"+str(b.body, "id"), "ann", finance, "", nil), 200)
	want(t, e.do(t, "GET", "/v1/payout/batches", "ann", finance, "", nil), 200)
	csv := e.raw(t, "GET", "/v1/payout/batches/"+str(b.body, "id")+"/csv", "ann", finance)
	if !strings.HasPrefix(csv, "seller_code,seller_name,sheba,amount,currency,sale_count\n") || !strings.Contains(csv, "S1,Shop,,945000,IRR,1") {
		t.Fatalf("%q", csv)
	}
	// paid sales cannot be returned; terms a sale used cannot be deleted
	want(t, e.do(t, "POST", "/v1/payout/sales/"+sid+"/return", "ann", finance, "", map[string]any{"reason": "late"}), 409)
	want(t, e.do(t, "DELETE", "/v1/payout/terms/"+str(def.body, "id"), "ann", finance, "", nil), 204) // the sale used the category terms, not the default
	want(t, e.do(t, "DELETE", "/v1/payout/terms/"+str(cat.body, "id"), "ann", finance, "", nil), 409)
}

func TestEInvoiceOverHTTP(t *testing.T) {
	e := setup(t) // no provider configured: invoices queue, sending is refused until one is
	const finance = "finance"
	want(t, e.do(t, "GET", "/v1/einvoices", "alice", "customer", "", nil), 403)
	draft := e.do(t, "POST", "/v1/invoices", "ann", finance, "ei-d", map[string]any{"customer_ref": "alice", "items": []map[string]any{{"description": "x", "quantity": 1, "unit_price": 1000}}})
	want(t, draft, 201)
	want(t, e.do(t, "POST", "/v1/einvoice-requests/invoice/"+str(draft.body, "id"), "ann", finance, "", nil), 409) // not issued yet
	inv := e.do(t, "POST", "/v1/invoices", "ann", finance, "ei-1", map[string]any{"customer_ref": "alice", "issue": true,
		"items": []map[string]any{{"description": "plan", "quantity": 1, "unit_price": 50000}}})
	want(t, inv, 201)
	q := e.do(t, "POST", "/v1/einvoice-requests/invoice/"+str(inv.body, "id"), "ann", finance, "", nil)
	want(t, q, 201)
	if str(q.body, "status") != "queued" || str(q.body, "source_id") != str(inv.body, "id") {
		t.Fatalf("%v", q.body)
	}
	want(t, e.do(t, "GET", "/v1/einvoices/"+str(q.body, "id"), "ann", finance, "", nil), 200)
	list := e.do(t, "GET", "/v1/einvoices?filter[status][in]=queued,sent", "ann", finance, "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}
	want(t, e.do(t, "POST", "/v1/einvoices/send", "ann", finance, "", nil), 409)
	want(t, e.do(t, "POST", "/v1/einvoices/"+str(q.body, "id")+"/requeue", "ann", finance, "", nil), 409) // only a rejected one
}

func TestCouponsAndSubscriptionsOverHTTP(t *testing.T) {
	e := setup(t) // the test clock is 2026-11-09 (1405/08/18)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/coupons", "alice", "customer", "", nil), 403)
	want(t, e.do(t, "POST", "/v1/coupons", "ann", finance, "", map[string]any{"code": "x", "name": "bad", "kind": "percent", "percent_bp": 1000}), 400) // code too short
	want(t, e.do(t, "POST", "/v1/coupons", "ann", finance, "", map[string]any{"code": "BIG", "name": "bad", "kind": "percent", "percent_bp": 20000}), 400)
	cp := e.do(t, "POST", "/v1/coupons", "ann", finance, "", map[string]any{"code": "first20", "name": "First month", "kind": "percent", "percent_bp": 2000,
		"valid_until": "1405/12/29", "max_redemptions": 2, "max_per_customer": 1, "min_amount": 1000})
	want(t, cp, 201)
	if str(cp.body, "code") != "FIRST20" || str(cp.body, "valid_until") != "2027-03-20" {
		t.Fatalf("%v", cp.body)
	}
	want(t, e.do(t, "POST", "/v1/coupons", "ann", finance, "", map[string]any{"code": "FIRST20", "name": "dup", "kind": "fixed", "fixed_amount": 5}), 409)
	cid := str(cp.body, "id")

	use := map[string]any{"code": "FIRST20", "customer_ref": "alice", "amount": irr(100_000)}
	pv := e.do(t, "POST", "/v1/coupon-actions/preview", "ann", finance, "", use)
	want(t, pv, 200)
	if pv.body["discount"].(map[string]any)["amount"].(float64) != 20_000 {
		t.Fatalf("%v", pv.body)
	}
	want(t, e.do(t, "POST", "/v1/coupon-actions/redeem", "ann", finance, "", use), 400) // Idempotency-Key required
	rd := e.do(t, "POST", "/v1/coupon-actions/redeem", "ann", finance, "r-1", use)
	want(t, rd, 201)
	if again := e.do(t, "POST", "/v1/coupon-actions/redeem", "ann", finance, "r-1", use); str(again.body, "id") != str(rd.body, "id") {
		t.Fatal("a retry made a second redemption")
	}
	want(t, e.do(t, "POST", "/v1/coupon-actions/redeem", "ann", finance, "r-2", use), 409) // alice already used it
	want(t, e.do(t, "GET", "/v1/coupons/"+cid+"/redemptions", "ann", finance, "", nil), 200)
	want(t, e.do(t, "POST", "/v1/coupon-redemptions/"+str(rd.body, "id")+"/release", "ann", finance, "", map[string]any{}), 400) // reason required
	want(t, e.do(t, "POST", "/v1/coupon-redemptions/"+str(rd.body, "id")+"/release", "ann", finance, "", map[string]any{"reason": "voided"}), 200)
	up := e.do(t, "PUT", "/v1/coupons/"+cid, "ann", finance, "", map[string]any{"max_redemptions": 5, "clear_max_per_customer": true, "active": true})
	want(t, up, 200)
	if up.body["max_redemptions"].(float64) != 5 || up.body["max_per_customer"] != nil {
		t.Fatalf("%v", up.body)
	}
	list := e.do(t, "GET", "/v1/coupons?filter[active]=true", "ann", finance, "", nil)
	want(t, list, 200)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}

	// plans and subscriptions
	want(t, e.do(t, "POST", "/v1/subscription-plans", "ann", finance, "", map[string]any{"code": "pro", "name": "Pro", "price": irr(0), "unit": "month"}), 400)
	pl := e.do(t, "POST", "/v1/subscription-plans", "ann", finance, "", map[string]any{"code": "pro", "name": "Pro", "price": irr(1_000_000), "unit": "month"})
	want(t, pl, 201)
	want(t, e.do(t, "POST", "/v1/subscription-plans", "ann", finance, "", map[string]any{"code": "pro", "name": "Dup", "price": irr(5), "unit": "month"}), 409)
	pid := str(pl.body, "id")
	sub := map[string]any{"plan_id": pid, "customer_ref": "alice", "coupon_code": "FIRST20", "buyer": map[string]any{"name": "Alice"}}
	want(t, e.do(t, "POST", "/v1/subscriptions", "ann", finance, "", sub), 400) // Idempotency-Key required
	s1 := e.do(t, "POST", "/v1/subscriptions", "ann", finance, "sub-1", sub)
	want(t, s1, 201)
	if str(s1.body, "status") != "active" || str(s1.body, "next_bill_date") != "2026-11-09" || str(s1.body, "next_bill_date_jalali") != "1405/08/18" {
		t.Fatalf("%v", s1.body)
	}
	if again := e.do(t, "POST", "/v1/subscriptions", "ann", finance, "sub-1", sub); str(again.body, "id") != str(s1.body, "id") {
		t.Fatal("a retry created a second subscription")
	}
	sid := str(s1.body, "id")
	bill := e.do(t, "POST", "/v1/subscriptions/bill", "ann", finance, "", nil)
	want(t, bill, 200)
	if bill.body["periods"].(float64) != 1 || bill.body["invoiced"].(float64) != 1 {
		t.Fatalf("%v", bill.body)
	}
	periods := e.do(t, "GET", "/v1/subscriptions/"+sid+"/periods", "ann", finance, "", nil)
	_ = periods // the list endpoint answers an array; its status is checked below through the invoice
	invs := e.do(t, "GET", "/v1/invoices?filter[customer_ref]=alice", "ann", finance, "", nil)
	want(t, invs, 200)
	items, _ := invs.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["total"].(float64) != 880_000 { // 1,000,000 - 20% = 800,000, VAT 10% = 880,000
		t.Fatalf("%v", invs.body)
	}
	want(t, e.do(t, "POST", "/v1/subscriptions/"+sid+"/pause", "ann", finance, "", nil), 200)
	want(t, e.do(t, "POST", "/v1/subscriptions/"+sid+"/pause", "ann", finance, "", nil), 409)
	want(t, e.do(t, "POST", "/v1/subscriptions/"+sid+"/resume", "ann", finance, "", nil), 200)
	cancel := e.do(t, "POST", "/v1/subscriptions/"+sid+"/cancel", "ann", finance, "", map[string]any{"at_period_end": false})
	want(t, cancel, 200)
	if str(cancel.body, "status") != "canceled" {
		t.Fatalf("%v", cancel.body)
	}
	sl := e.do(t, "GET", "/v1/subscriptions?filter[status][in]=canceled,paused", "ann", finance, "", nil)
	want(t, sl, 200)
	if items, _ := sl.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", sl.body)
	}
	want(t, e.do(t, "GET", "/v1/subscription-plans", "ann", finance, "", nil), 200)
	want(t, e.do(t, "PUT", "/v1/subscription-plans/"+pid, "ann", finance, "", map[string]any{"price": irr(1_200_000)}), 200)
}

func TestYearEndOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}

	want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, "j1", map[string]any{"date": "2026-11-09", "description": "capital",
		"lines": []any{line("bank.main", "debit", 1000), line("equity.retained_earnings", "credit", 1000)}}), 201)
	want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, "j2", map[string]any{"date": "2026-11-09", "description": "sale",
		"lines": []any{line("bank.main", "debit", 300), line("revenue.sales", "credit", 300)}}), 201)
	fyID := ""
	if rows, ok := e.list(t, "/v1/accounting/fiscal-years", finance); ok {
		for _, r := range rows {
			if m := r.(map[string]any); m["name"] == "1405" {
				fyID = fmt.Sprint(int64(m["id"].(float64)))
			}
		}
	}
	if fyID == "" {
		t.Fatal("fiscal year 1405 not found")
	}
	base := "/v1/accounting/fiscal-years/" + fyID

	want(t, e.do(t, "GET", base+"/close-preview", "u", "customer", "", nil), 403)
	pv := e.do(t, "GET", base+"/close-preview", "ann", finance, "", nil)
	want(t, pv, 200)
	if pv.body["can_close"] != false || pv.body["net_profit"].(float64) != 300 {
		t.Fatalf("%v", pv.body)
	}
	want(t, e.do(t, "POST", base+"/close", "ann", finance, "", map[string]any{}), 400)    // Idempotency-Key required
	want(t, e.do(t, "POST", base+"/close", "ann", finance, "c1", map[string]any{}), 409)  // the year is not over
	want(t, e.do(t, "POST", base+"/reopen", "ann", finance, "r0", map[string]any{}), 409) // never closed
	want(t, e.do(t, "POST", "/v1/accounting/fiscal-years/abc/close", "ann", finance, "c1", map[string]any{}), 400)

	mu.Lock()
	cur = time.Date(2027, 3, 25, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	pv = e.do(t, "GET", base+"/close-preview", "ann", finance, "", nil)
	if pv.body["can_close"] != true || len(pv.body["closing_documents"].([]any)) != 1 || len(pv.body["opening_documents"].([]any)) != 1 {
		t.Fatalf("%v", pv.body)
	}
	cl := e.do(t, "POST", base+"/close", "ann", finance, "c2", map[string]any{})
	want(t, cl, 201)
	if cl.body["net_profit"].(float64) != 300 || len(cl.body["opening"].([]any)) != 1 || cl.body["already_closed"] != false {
		t.Fatalf("%v", cl.body)
	}
	if again := e.do(t, "POST", base+"/close", "ann", finance, "c3", map[string]any{}); again.status != 200 || again.body["already_closed"] != true {
		t.Fatalf("a second close must return the first: %v", again.body)
	}
	// The income statement of the year ignores the closing documents unless asked.
	is := e.do(t, "GET", "/v1/accounting/income-statement?from=2026-03-21&to=2027-03-20", "ann", finance, "", nil)
	want(t, is, 200)
	if is.body["net"].(float64) != 300 {
		t.Fatalf("%v", is.body)
	}
	is = e.do(t, "GET", "/v1/accounting/income-statement?include_closing=true", "ann", finance, "", nil)
	if is.body["net"].(float64) != 0 {
		t.Fatalf("%v", is.body)
	}
	bsr := e.do(t, "GET", "/v1/accounting/balance-sheet?as_of=2027-03-20", "ann", finance, "", nil)
	want(t, bsr, 200)
	if bsr.body["balanced"] != true || bsr.body["total_assets"].(float64) != 1300 || bsr.body["current_result"].(float64) != 300 {
		t.Fatalf("%v", bsr.body)
	}
	want(t, e.do(t, "POST", base+"/reopen", "ann", finance, "r1", map[string]any{"reason": "late invoice"}), 200)
	want(t, e.do(t, "POST", base+"/reopen", "ann", finance, "r2", map[string]any{}), 409) // not closed any more
}

// list reads an endpoint that answers with a JSON array.
func (e env) list(t *testing.T, path, roles string) ([]any, bool) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
	req.Header.Set("X-User", "ann")
	req.Header.Set("X-Roles", roles)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out []any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, false
	}
	return out, res.StatusCode == 200
}

func TestFixedAssetsOverHTTP(t *testing.T) {
	e := setup(t) // the test clock is 2026-11-09 (1405/08/18)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/fixed-assets/assets", "u", "customer", "", nil), 403)
	lt := e.do(t, "GET", "/v1/fixed-assets/legal-table?group=13", "ann", finance, "", nil)
	want(t, lt, 200)
	if items := lt.body["items"].([]any); len(items) != 9 || str(lt.body, "version") == "" {
		t.Fatalf("legal table: %v", lt.body)
	}
	want(t, e.do(t, "GET", "/v1/fixed-assets/legal-table?group=99", "ann", finance, "", nil), 400)
	lc := e.do(t, "POST", "/v1/fixed-assets/legal-table-categories", "ann", finance, "", map[string]any{"row_id": "g05-r2", "code": "it"})
	want(t, lc, 201)
	if lc.body["life_months"].(float64) != 36 {
		t.Fatalf("%v", lc.body)
	}
	want(t, e.do(t, "POST", "/v1/fixed-assets/categories", "ann", finance, "", map[string]any{"code": "furniture", "name": "Furniture", "method": "straight_line", "life_months": 120}), 400) // no source
	cat := e.do(t, "POST", "/v1/fixed-assets/categories", "ann", finance, "", map[string]any{"code": "furniture", "name": "Furniture", "method": "straight_line",
		"life_months": 120, "salvage_bp": 500, "source": "accountant decision, 10 years"})
	want(t, cat, 201)
	want(t, e.do(t, "POST", "/v1/fixed-assets/categories", "ann", finance, "", map[string]any{"code": "furniture", "name": "dup", "method": "straight_line", "life_months": 12, "source": "x"}), 409)

	body := map[string]any{"code": "F-1", "name": "Desk", "category_id": str(cat.body, "id"), "cost": irr(12_000_000)}
	want(t, e.do(t, "POST", "/v1/fixed-assets/assets", "ann", finance, "", body), 400) // Idempotency-Key required
	as := e.do(t, "POST", "/v1/fixed-assets/assets", "ann", finance, "fa-1", body)
	want(t, as, 201)
	if as.body["salvage"].(float64) != 600_000 || as.body["book_value"].(float64) != 12_000_000 {
		t.Fatalf("%v", as.body)
	}
	if again := e.do(t, "POST", "/v1/fixed-assets/assets", "ann", finance, "fa-1", body); str(again.body, "id") != str(as.body, "id") {
		t.Fatal("a retry made a second asset")
	}
	id := str(as.body, "id")
	sc := e.do(t, "GET", "/v1/fixed-assets/assets/"+id+"/schedule", "ann", finance, "", nil)
	want(t, sc, 200)
	if items := sc.body["items"].([]any); len(items) != 120 || items[0].(map[string]any)["jalali_month"].(float64) != 9 {
		t.Fatalf("schedule: %d rows, %v", len(items), items[0])
	}
	sp := e.do(t, "POST", "/v1/fixed-assets/assets/"+id+"/idle-spans", "ann", finance, "", map[string]any{"from_jalali_year": 1406, "from_jalali_month": 1, "to_jalali_year": 1406, "to_jalali_month": 7})
	want(t, sp, 201)
	want(t, e.do(t, "POST", "/v1/fixed-assets/assets/"+id+"/idle-spans", "ann", finance, "", map[string]any{"from_jalali_year": 1406, "from_jalali_month": 1, "to_jalali_year": 1407, "to_jalali_month": 1}), 400)
	if l := e.do(t, "GET", "/v1/fixed-assets/assets/"+id+"/idle-spans", "ann", finance, "", nil); len(l.body["items"].([]any)) != 1 || str(l.body, "source") == "" {
		t.Fatalf("%v", l.body)
	}
	want(t, e.do(t, "DELETE", "/v1/fixed-assets/assets/"+id+"/idle-spans/"+str(sp.body, "id"), "ann", finance, "", nil), 200)
	want(t, e.do(t, "POST", "/v1/fixed-assets/depreciation-runs", "ann", finance, "", map[string]any{"jalali_year": 1405, "jalali_month": 8}), 400)
	want(t, e.do(t, "POST", "/v1/fixed-assets/depreciation-runs", "ann", finance, "run-1", map[string]any{"jalali_year": 1405, "jalali_month": 8}), 409) // month not over
	want(t, e.do(t, "POST", "/v1/fixed-assets/assets/"+id+"/dispose", "ann", finance, "", map[string]any{"proceeds": irr(0)}), 400)
	ds := e.do(t, "POST", "/v1/fixed-assets/assets/"+id+"/dispose", "ann", finance, "d-1", map[string]any{"proceeds": irr(0)})
	want(t, ds, 200)
	if ds.body["status"] != "disposed" {
		t.Fatalf("%v", ds.body)
	}
	list := e.do(t, "GET", "/v1/fixed-assets/assets?status=disposed", "ann", finance, "", nil)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}
}

func TestInventoryOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	want(t, e.do(t, "GET", "/v1/inventory/items", "u", "customer", "", nil), 403)
	want(t, e.do(t, "POST", "/v1/inventory/items", "ann", finance, "", map[string]any{"code": "tea", "name": "Tea"}), 400) // no unit
	it := e.do(t, "POST", "/v1/inventory/items", "ann", finance, "", map[string]any{"code": "tea", "name": "Tea", "unit": "g", "method": "fifo"})
	want(t, it, 201)
	want(t, e.do(t, "POST", "/v1/inventory/items", "ann", finance, "", map[string]any{"code": "tea", "name": "dup", "unit": "g"}), 409)
	id := str(it.body, "id")

	rc := map[string]any{"item_id": id, "quantity": 1000, "total_cost": irr(50_000)}
	want(t, e.do(t, "POST", "/v1/inventory/receipts", "ann", finance, "", rc), 400) // Idempotency-Key required
	want(t, e.do(t, "POST", "/v1/inventory/receipts", "ann", finance, "rc-1", rc), 201)
	if again := e.do(t, "POST", "/v1/inventory/receipts", "ann", finance, "rc-1", rc); again.status != 201 {
		t.Fatalf("a retry: %v", again.body)
	}
	is := e.do(t, "POST", "/v1/inventory/issues", "ann", finance, "is-1", map[string]any{"item_id": id, "quantity": 400, "ref": "order-1"})
	want(t, is, 201)
	if is.body["value"].(float64) != -20_000 || is.body["quantity_after"].(float64) != 600 {
		t.Fatalf("%v", is.body)
	}
	want(t, e.do(t, "POST", "/v1/inventory/issues", "ann", finance, "is-2", map[string]any{"item_id": id, "quantity": 601}), 409)
	val := e.do(t, "GET", "/v1/inventory/valuation", "ann", finance, "", nil)
	want(t, val, 200)
	if val.body["total_value"].(float64) != 30_000 {
		t.Fatalf("%v", val.body)
	}
	ct := e.do(t, "POST", "/v1/inventory/counts", "ann", finance, "ct-1", map[string]any{"item_id": id, "counted_quantity": 500})
	want(t, ct, 201)
	if ct.body["kind"] != "count_loss" {
		t.Fatalf("%v", ct.body)
	}
	want(t, e.do(t, "POST", "/v1/inventory/write-downs", "ann", finance, "wd-1", map[string]any{"item_id": id, "net_realizable_value": irr(10_000)}), 201)
	mv := e.do(t, "GET", "/v1/inventory/items/"+id+"/movements", "ann", finance, "", nil)
	if items, _ := mv.body["items"].([]any); len(items) != 4 {
		t.Fatalf("%v", mv.body)
	}
}

func TestProfitAppropriationOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}
	post := func(key string, lines ...any) {
		want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, key, map[string]any{"date": "2026-11-09", "description": key, "lines": lines}), 201)
	}
	post("a1", line("bank.main", "debit", 100_000_000), line("equity.capital", "credit", 100_000_000))
	post("a2", line("bank.main", "debit", 10_000_000), line("revenue.sales", "credit", 10_000_000))
	post("a3", line("payroll.salary_expense", "debit", 4_000_000), line("bank.main", "credit", 4_000_000))
	fyID := ""
	if rows, ok := e.list(t, "/v1/accounting/fiscal-years", finance); ok {
		for _, r := range rows {
			if m := r.(map[string]any); m["name"] == "1405" {
				fyID = fmt.Sprint(int64(m["id"].(float64)))
			}
		}
	}
	want(t, e.do(t, "GET", "/v1/accounting/profit-appropriation/preview/"+fyID, "ann", finance, "", nil), 409) // not closed
	mu.Lock()
	cur = time.Date(2027, 3, 25, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	want(t, e.do(t, "POST", "/v1/accounting/fiscal-years/"+fyID+"/close", "ann", finance, "c1", map[string]any{}), 201)

	pv := e.do(t, "GET", "/v1/accounting/profit-appropriation/preview/"+fyID+"?public_company=true", "ann", finance, "", nil)
	want(t, pv, 200)
	if pv.body["legal_reserve_required"].(float64) != 300_000 || pv.body["mandatory_minimum_dividend"].(float64) != 600_000 ||
		pv.body["max_directors_bonus_basis_points_of_dividend"].(float64) != 300 {
		t.Fatalf("%v", pv.body)
	}
	body := map[string]any{"fiscal_year_id": 0, "dividend": irr(599_999)}
	id, _ := strconv.Atoi(fyID)
	body["fiscal_year_id"] = id
	want(t, e.do(t, "POST", "/v1/accounting/profit-appropriation", "ann", finance, "", body), 400)   // Idempotency-Key required
	want(t, e.do(t, "POST", "/v1/accounting/profit-appropriation", "ann", finance, "d1", body), 409) // below the mandatory 10%
	body["dividend"] = irr(600_000)
	dc := e.do(t, "POST", "/v1/accounting/profit-appropriation", "ann", finance, "d2", body)
	want(t, dc, 201)
	if dc.body["dividend"].(float64) != 600_000 || dc.body["legal_reserve"].(float64) != 300_000 || dc.body["outstanding"].(float64) != 600_000 {
		t.Fatalf("%v", dc.body)
	}
	pay := e.do(t, "POST", "/v1/accounting/profit-appropriation/"+str(dc.body, "id")+"/payments", "ann", finance, "p1", map[string]any{"amount": irr(600_000)})
	want(t, pay, 200)
	if pay.body["outstanding"].(float64) != 0 {
		t.Fatalf("%v", pay.body)
	}
	want(t, e.do(t, "POST", "/v1/accounting/profit-appropriation/"+str(dc.body, "id")+"/payments", "ann", finance, "p2", map[string]any{"amount": irr(1)}), 409)
}

func TestVATOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}
	post := func(key string, lines ...any) {
		want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, key, map[string]any{"date": "2026-11-09", "description": key, "lines": lines}), 201)
	}
	post("s", line("bank.main", "debit", 1_100_000), line("revenue.sales", "credit", 1_000_000), line("vat.output", "credit", 100_000))
	post("b", line("payroll.salary_expense", "debit", 500_000), line("vat.input", "debit", 50_000), line("bank.main", "credit", 550_000))

	want(t, e.do(t, "GET", "/v1/vat/rules", "u", "customer", "", nil), 403)
	want(t, e.do(t, "POST", "/v1/vat/rules", "ann", finance, "", map[string]any{"effective_from": "2026-03-21", "period_months": 3, "filing_days": 15}), 400) // no source
	rule := e.do(t, "POST", "/v1/vat/rules", "ann", finance, "", map[string]any{"effective_from": "2026-03-21", "period_months": 3, "filing_month_end": true, "late_penalty_bp": 200, "source": "test"})
	want(t, rule, 201)
	pv := e.do(t, "GET", "/v1/vat/preview?year=1405&period=3", "ann", finance, "", nil)
	want(t, pv, 200)
	if pv.body["output_tax"].(float64) != 100_000 || pv.body["input_tax"].(float64) != 50_000 || pv.body["net"].(float64) != 50_000 || str(pv.body, "due_on") != "2027-01-20" {
		t.Fatalf("%v", pv.body)
	}
	want(t, e.do(t, "GET", "/v1/vat/preview?year=1405&period=9", "ann", finance, "", nil), 400)
	body := map[string]any{"jalali_year": 1405, "period": 3}
	want(t, e.do(t, "POST", "/v1/vat/returns", "ann", finance, "", body), 400)   // Idempotency-Key required
	want(t, e.do(t, "POST", "/v1/vat/returns", "ann", finance, "v1", body), 409) // the period is not over
	mu.Lock()
	cur = time.Date(2026, 12, 25, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	rt := e.do(t, "POST", "/v1/vat/returns", "ann", finance, "v2", body)
	want(t, rt, 201)
	if rt.body["net"].(float64) != 50_000 {
		t.Fatalf("%v", rt.body)
	}
	want(t, e.do(t, "POST", "/v1/vat/returns", "ann", finance, "v3", body), 409) // already filed
	pos := e.do(t, "GET", "/v1/vat/position", "ann", finance, "", nil)
	if pos.body["owed"].(float64) != 50_000 {
		t.Fatalf("%v", pos.body)
	}
	want(t, e.do(t, "POST", "/v1/vat/payments", "ann", finance, "pay1", map[string]any{"amount": irr(60_000)}), 409)
	want(t, e.do(t, "POST", "/v1/vat/payments", "ann", finance, "pay2", map[string]any{"amount": irr(50_000)}), 201)
	want(t, e.do(t, "POST", "/v1/vat/refunds", "ann", finance, "ref1", map[string]any{"amount": irr(1)}), 409)
	list := e.do(t, "GET", "/v1/vat/returns", "ann", finance, "", nil)
	if items, _ := list.body["items"].([]any); len(items) != 1 {
		t.Fatalf("%v", list.body)
	}
}

func TestVATLawDataOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}
	post := func(key string, lines ...any) {
		want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, key, map[string]any{"date": "2026-11-09", "description": key, "lines": lines}), 201)
	}
	post("s", line("bank.main", "debit", 1_100_000), line("revenue.sales", "credit", 1_000_000), line("vat.output", "credit", 100_000))
	post("b", line("payroll.salary_expense", "debit", 500_000), line("vat.input", "debit", 50_000), line("bank.main", "credit", 550_000))

	want(t, e.do(t, "GET", "/v1/vat/rates", "u", "customer", "", nil), 403)
	rates := e.do(t, "GET", "/v1/vat/rates", "ann", finance, "", nil)
	if items, _ := rates.body["items"].([]any); len(items) < 8 {
		t.Fatalf("seeded rates: %v", rates.body)
	}
	c := e.do(t, "GET", "/v1/vat/compute?base=1000000", "ann", finance, "", nil)
	if c.body["tax"].(float64) != 100_000 || c.body["municipal_levy"].(float64) != 30_000 {
		t.Fatalf("compute: %v", c.body)
	}
	want(t, e.do(t, "GET", "/v1/vat/compute?category=nope&base=1", "ann", finance, "", nil), 404)
	want(t, e.do(t, "POST", "/v1/vat/rates", "ann", finance, "", map[string]any{"category": "general", "effective_from": "2027-03-21", "rate_bp": 1100}), 400) // no source
	want(t, e.do(t, "POST", "/v1/vat/rates", "ann", finance, "", map[string]any{"category": "general", "effective_from": "2027-03-21", "rate_bp": 1100, "source": "test"}), 201)
	ex := e.do(t, "GET", "/v1/vat/exemptions?on=2026-11-09", "ann", finance, "", nil)
	if items, _ := ex.body["items"].([]any); len(items) < 25 {
		t.Fatalf("exemptions: %v", ex.body)
	}
	want(t, e.do(t, "POST", "/v1/vat/exemptions", "ann", finance, "", map[string]any{"code": "t1", "kind": "goods", "description": "x", "article": "y", "effective_from": "2027-01-01", "source": "z"}), 201)

	// Apportion (before filing), file, a late sale, amend, penalty.
	want(t, e.do(t, "POST", "/v1/vat/apportionments", "ann", finance, "", map[string]any{"jalali_year": 1405, "period": 3}), 400) // Idempotency-Key required
	want(t, e.do(t, "POST", "/v1/vat/apportionments", "ann", finance, "ap1", map[string]any{"jalali_year": 1405, "period": 3, "direct_taxable": 30_000, "direct_exempt": 20_000}), 201)
	mu.Lock()
	cur = time.Date(2026, 12, 25, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	body := map[string]any{"jalali_year": 1405, "period": 3}
	rt := e.do(t, "POST", "/v1/vat/returns", "ann", finance, "r1", body)
	want(t, rt, 201)
	if rt.body["input_tax"].(float64) != 30_000 {
		t.Fatalf("return: %v", rt.body)
	}
	want(t, e.do(t, "POST", "/v1/vat/returns/amend", "ann", finance, "am0", body), 409) // nothing to amend yet
	post("late", line("bank.main", "debit", 110_000), line("revenue.sales", "credit", 100_000), line("vat.output", "credit", 10_000))
	am := e.do(t, "POST", "/v1/vat/returns/amend", "ann", finance, "am1", body)
	want(t, am, 201)
	if am.body["output_tax"].(float64) != 110_000 {
		t.Fatalf("amended: %v", am.body)
	}
	mu.Lock()
	cur = time.Date(2027, 3, 10, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	pen := e.do(t, "GET", "/v1/vat/returns/"+am.body["id"].(string)+"/penalty", "ann", finance, "", nil)
	if pen.body["unpaid"].(float64) != 80_000 || pen.body["penalty"].(float64) != 3_200 {
		t.Fatalf("penalty: %v", pen.body)
	}
}

func TestIncomeTaxOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}
	post := func(key string, lines ...any) {
		want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, key, map[string]any{"date": "2026-11-09", "description": key, "lines": lines}), 201)
	}
	post("t1", line("bank.main", "debit", 10_000_000), line("revenue.sales", "credit", 10_000_000))
	post("t2", line("payroll.salary_expense", "debit", 4_000_000), line("bank.main", "credit", 4_000_000))
	fyID := ""
	if rows, ok := e.list(t, "/v1/accounting/fiscal-years", finance); ok {
		for _, r := range rows {
			if m := r.(map[string]any); m["name"] == "1405" {
				fyID = fmt.Sprint(int64(m["id"].(float64)))
			}
		}
	}
	want(t, e.do(t, "GET", "/v1/incometax/rules", "u", "customer", "", nil), 403)
	rules := e.do(t, "GET", "/v1/incometax/rules", "ann", finance, "", nil)
	if items, _ := rules.body["items"].([]any); len(items) != 1 {
		t.Fatalf("seeded rule: %v", rules.body)
	}
	body := map[string]any{"add_backs": []any{map[string]any{"label": "fines", "amount": 1_000_000}}, "credits": 100_000}
	pv := e.do(t, "POST", "/v1/incometax/preview/"+fyID, "ann", finance, "", body)
	want(t, pv, 200)
	if pv.body["taxable_income"].(float64) != 7_000_000 || pv.body["tax"].(float64) != 1_750_000 {
		t.Fatalf("preview: %v", pv.body)
	}
	want(t, e.do(t, "POST", "/v1/incometax/assessments/"+fyID, "ann", finance, "", body), 400) // Idempotency-Key required
	d := e.do(t, "POST", "/v1/incometax/assessments/"+fyID, "ann", finance, "i1", body)
	want(t, d, 201)
	if d.body["payable"].(float64) != 1_650_000 || d.body["due_on"] != "2027-07-22" {
		t.Fatalf("declared: %v", d.body)
	}
	want(t, e.do(t, "POST", "/v1/incometax/assessments/"+fyID, "ann", finance, "i2", body), 409)
	id := d.body["id"].(string)
	want(t, e.do(t, "POST", "/v1/incometax/assessments/"+id+"/payments", "ann", finance, "pp1", map[string]any{"amount": irr(2_000_000)}), 409)
	pay := e.do(t, "POST", "/v1/incometax/assessments/"+id+"/payments", "ann", finance, "pp2", map[string]any{"amount": irr(650_000)})
	want(t, pay, 200)
	if pay.body["outstanding"].(float64) != 1_000_000 {
		t.Fatalf("paid: %v", pay.body)
	}
	want(t, e.do(t, "GET", "/v1/incometax/assessments/"+id, "ann", finance, "", nil), 200)
	want(t, e.do(t, "GET", "/v1/incometax/losses", "ann", finance, "", nil), 200)
}

func TestOfficialStatementsOverHTTP(t *testing.T) {
	e := setupAt(t, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	line := func(acct, side string, n int) map[string]any {
		return map[string]any{"account": acct, "side": side, "amount": irr(n)}
	}
	post := func(key string, lines ...any) {
		want(t, e.do(t, "POST", "/v1/accounting/journals", "ann", finance, key, map[string]any{"date": "2026-11-09", "description": key, "lines": lines}), 201)
	}
	post("o1", line("bank.main", "debit", 10_000_000), line("equity.capital", "credit", 10_000_000))
	post("o2", line("bank.main", "debit", 5_000_000), line("revenue.sales", "credit", 5_000_000))
	want(t, e.do(t, "GET", "/v1/accounting/official/balance-sheet", "u", "customer", "", nil), 403)
	bs := e.do(t, "GET", "/v1/accounting/official/balance-sheet?as_of=2026-11-09", "ann", finance, "", nil)
	want(t, bs, 200)
	if bs.body["balanced"] != true || bs.body["total_assets"].(float64) != 15_000_000 {
		t.Fatalf("balance sheet: %v", bs.body)
	}
	is := e.do(t, "GET", "/v1/accounting/official/income-statement?from=2026-03-21&to=2026-11-09", "ann", finance, "", nil)
	want(t, is, 200)
	if is.body["net_profit"].(float64) != 5_000_000 || is.body["gross_profit"].(float64) != 5_000_000 {
		t.Fatalf("income statement: %v", is.body)
	}
	want(t, e.do(t, "GET", "/v1/accounting/official/income-statement?from=2026-03-21", "ann", finance, "", nil), 400)
	want(t, e.do(t, "GET", "/v1/accounting/official/changes-in-equity?from=2026-03-21&to=2026-11-09", "ann", finance, "", nil), 200)
	cf := e.do(t, "GET", "/v1/accounting/official/cash-flow?from=2026-03-21&to=2026-11-09", "ann", finance, "", nil)
	want(t, cf, 200)
	if cf.body["reconciled"] != true || cf.body["financing"].(float64) != 10_000_000 {
		t.Fatalf("cash flow: %v", cf.body)
	}
}

func TestApprovalsOverHTTP(t *testing.T) {
	var mu sync.Mutex
	cur := time.Date(2027, 3, 15, 10, 0, 0, 0, time.UTC)
	e := setupAt(t, func() time.Time { mu.Lock(); defer mu.Unlock(); return cur })
	const finance = "finance"
	fyID := int64(0)
	if rows, ok := e.list(t, "/v1/accounting/fiscal-years", finance); ok {
		for _, r := range rows {
			if m := r.(map[string]any); m["name"] == "1405" {
				fyID = int64(m["id"].(float64))
			}
		}
	}
	body := map[string]any{"kind": "year_close", "payload": map[string]any{"fiscal_year_id": fyID}}
	want(t, e.do(t, "POST", "/v1/approvals", "u", "customer", "r1", body), 403)
	want(t, e.do(t, "POST", "/v1/approvals", "ann", finance, "", body), 400) // Idempotency-Key required
	want(t, e.do(t, "POST", "/v1/approvals", "ann", finance, "r0", map[string]any{"kind": "other", "payload": map[string]any{}}), 400)
	rq := e.do(t, "POST", "/v1/approvals", "ann", finance, "r1", body)
	want(t, rq, 201)
	id := rq.body["id"].(string)
	want(t, e.do(t, "POST", "/v1/approvals/"+id+"/approve", "ann", finance, "", nil), 409) // the preparer cannot approve
	want(t, e.do(t, "POST", "/v1/approvals/"+id+"/approve", "bob", finance, "", nil), 409) // the year is not over: the request stays pending
	mu.Lock()
	cur = time.Date(2027, 3, 25, 10, 0, 0, 0, time.UTC)
	mu.Unlock()
	ok2 := e.do(t, "POST", "/v1/approvals/"+id+"/approve", "bob", finance, "", nil)
	want(t, ok2, 200)
	if ok2.body["status"] != "executed" || ok2.body["decided_by"] != "bob" {
		t.Fatalf("approved: %v", ok2.body)
	}
	want(t, e.do(t, "GET", "/v1/approvals?status=executed", "ann", finance, "", nil), 200)
	want(t, e.do(t, "GET", "/v1/approvals/"+id, "ann", finance, "", nil), 200)
}

func TestInventoryProductionOverHTTP(t *testing.T) {
	e := setup(t)
	const finance = "finance"
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }
	wh := e.do(t, "POST", "/v1/inventory/warehouses", "ann", finance, "", map[string]any{"code": "W2", "name": "Second"})
	want(t, wh, 201)
	want(t, e.do(t, "POST", "/v1/inventory/warehouses", "ann", finance, "", map[string]any{"code": "W2", "name": "dup"}), 409)
	want(t, e.do(t, "GET", "/v1/inventory/warehouses", "ann", "customer", "", nil), 403)
	item := e.do(t, "POST", "/v1/inventory/items", "ann", finance, "", map[string]any{"code": "milk", "name": "Milk", "unit": "l", "method": "fifo", "fefo": true})
	want(t, item, 201)
	rc := e.do(t, "POST", "/v1/inventory/receipts", "ann", finance, "r-1", map[string]any{"item_id": str(item.body, "id"), "quantity": 10, "total_cost": irr(1000),
		"warehouse_id": str(wh.body, "id"), "lot_no": "L1", "expires_on": "2026-12-20"})
	want(t, rc, 201)
	if rc.body["lot_id"] == nil {
		t.Fatalf("%v", rc.body)
	}
	if l := e.do(t, "GET", "/v1/inventory/expiring-lots?on_or_before=2026-12-31", "ann", finance, "", nil); len(l.body["items"].([]any)) != 1 {
		t.Fatalf("%v", l.body)
	}
	want(t, e.do(t, "GET", "/v1/inventory/expiring-lots", "ann", finance, "", nil), 400)
	rate := e.do(t, "POST", "/v1/production/overhead-rates", "ann", finance, "", map[string]any{"effective_from": "2026-03-21", "basis": "direct_labor_cost", "budget_fixed": 600, "normal_capacity": 1200, "variable_rate": 1000, "source": "test"})
	want(t, rate, 201)
	if rate.body["fixed_rate"].(float64) != 5000 {
		t.Fatalf("%v", rate.body)
	}
	ord := e.do(t, "POST", "/v1/production/orders", "ann", finance, "", map[string]any{"code": "PO-1", "product_item_id": str(item.body, "id"), "planned_quantity": 5})
	want(t, ord, 201)
	want(t, e.do(t, "POST", "/v1/production/orders/"+str(ord.body, "id")+"/labor", "ann", finance, "", map[string]any{"amount": irr(100)}), 400) // Idempotency-Key required
	lb := e.do(t, "POST", "/v1/production/orders/"+str(ord.body, "id")+"/labor", "ann", finance, "lab-1", map[string]any{"amount": irr(100)})
	want(t, lb, 200)
	if lb.body["work_in_process"].(float64) != 100 {
		t.Fatalf("%v", lb.body)
	}
	cp := e.do(t, "POST", "/v1/production/orders/"+str(ord.body, "id")+"/complete", "ann", finance, "done-1", map[string]any{"output_quantity": 5})
	want(t, cp, 200)
	if cp.body["finished_cost"].(float64) != 160 { // 100 labour + 50 fixed + 10 variable
		t.Fatalf("%v", cp.body)
	}
	want(t, e.do(t, "GET", "/v1/production/work-in-process", "ann", finance, "", nil), 200)
}
