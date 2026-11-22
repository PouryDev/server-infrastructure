package mizan_test

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/query"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/treasury"
)

func (e env) mainAccount(t *testing.T) treasury.BankAccount {
	t.Helper()
	list, err := e.m.Treasury.BankAccounts(e.ctx)
	must(t, err)
	for _, a := range list {
		if a.LedgerCode == mizan.AccBank {
			return a
		}
	}
	t.Fatal("the main bank account must exist after New")
	return treasury.BankAccount{}
}

func (e env) secondAccount(t *testing.T) treasury.BankAccount {
	t.Helper()
	a, err := e.m.Treasury.OpenBankAccount(e.ctx, treasury.OpenAccountInput{Name: "Savings", BankName: "Bank B", Actor: "tester"})
	must(t, err)
	return a
}

func (e env) cheque(t *testing.T, key string, d treasury.Direction, amount int64, acc *uuid.UUID) treasury.Cheque {
	t.Helper()
	c, err := e.m.Treasury.RegisterCheque(e.ctx, treasury.ChequeInput{IdemKey: key, Direction: d, SayadID: sayad(key), Number: "N-" + key, Amount: irr(amount),
		DueDate: now.AddDate(0, 0, 30), CounterpartyRef: "alice", BankAccountID: acc, Actor: "tester"})
	must(t, err)
	return c
}

// sayad derives a stable 16-digit id from a key.
func sayad(key string) string {
	n := 0
	for _, r := range key {
		n = n*31 + int(r)
	}
	return fmt.Sprintf("%016d", n%1_000_000_000_000_0000)
}

func TestSystemBankAccountAndCodingAreOptional(t *testing.T) {
	for _, coding := range []bool{false, true} {
		e := setup(t, coding)
		main := e.mainAccount(t)
		if main.Currency != "IRR" || !main.Active {
			t.Fatalf("%+v", main)
		}
		// Opening twice returns the same account.
		again, err := e.m.Treasury.OpenBankAccount(e.ctx, treasury.OpenAccountInput{Name: "x", LedgerCode: mizan.AccBank})
		must(t, err)
		if again.ID != main.ID {
			t.Fatal("the ledger code identifies a bank account")
		}
		e.secondAccount(t)
		if coding {
			rep, err := e.m.Ledger.CodingTrialBalance(e.ctx, ledger.TrialBalanceQuery{})
			must(t, err)
			if len(rep.Unclassified) != 0 {
				t.Fatalf("unclassified: %+v", rep.Unclassified)
			}
		}
	}
}

func TestReceivedChequeFullLifecycle(t *testing.T) {
	e := setup(t, true)
	main := e.mainAccount(t)
	c := e.cheque(t, "c1", treasury.Receivable, 5000, nil)
	if c.Status != treasury.InHand || c.OpenJournal == nil {
		t.Fatalf("%+v", c)
	}
	if e.bal(t, mizan.AccChequesInHand) != 5000 || e.bal(t, mizan.AccReceivable) != -5000 {
		t.Fatalf("receipt: in hand=%d receivable=%d", e.bal(t, mizan.AccChequesInHand), e.bal(t, mizan.AccReceivable))
	}
	// Not deposited yet: cannot clear.
	if _, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, time.Time{}, "ann"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("clearing an in-hand cheque: %v", err)
	}
	dep, err := e.m.Treasury.DepositCheque(e.ctx, c.ID, main.ID, time.Time{}, "ann")
	must(t, err)
	if dep.Status != treasury.Deposited || dep.BankAccountID == nil || e.bal(t, mizan.AccChequesInHand) != 0 || e.bal(t, mizan.AccChequesInCollection) != 5000 {
		t.Fatalf("deposit: %+v", dep)
	}
	if e.bal(t, mizan.AccBank) != 0 {
		t.Fatal("the bank is not paid until the cheque clears")
	}
	cl, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, now.AddDate(0, 0, -1), "ann")
	must(t, err)
	if cl.Status != treasury.Cleared || e.bal(t, mizan.AccBank) != 5000 || e.bal(t, mizan.AccChequesInCollection) != 0 {
		t.Fatalf("clear: %+v bank=%d", cl, e.bal(t, mizan.AccBank))
	}
	if _, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, time.Time{}, "ann"); err != nil || e.bal(t, mizan.AccBank) != 5000 {
		t.Fatalf("clearing twice must be a no-op: %v bank=%d", err, e.bal(t, mizan.AccBank))
	}
	if _, err := e.m.Treasury.BounceCheque(e.ctx, c.ID, treasury.BounceInput{Reason: "x"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a cleared cheque cannot bounce: %v", err)
	}
	if _, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, now.AddDate(0, 0, 3), "ann"); apperr.KindOf(err) == 0 {
		t.Fatal("a future date must be refused")
	}
	hist, err := e.m.Treasury.ChequeHistory(e.ctx, c.ID)
	must(t, err)
	if len(hist) != 3 || hist[0].To != treasury.InHand || hist[1].To != treasury.Deposited || hist[2].To != treasury.Cleared || hist[2].Actor != "ann" || hist[2].Journal == nil {
		t.Fatalf("history %+v", hist)
	}
	e.balanced(t)
}

func TestBounceAndReturn(t *testing.T) {
	e := setup(t, false)
	main := e.mainAccount(t)

	b := e.cheque(t, "b1", treasury.Receivable, 8000, nil)
	_, err := e.m.Treasury.DepositCheque(e.ctx, b.ID, main.ID, time.Time{}, "ann")
	must(t, err)
	if _, err := e.m.Treasury.BounceCheque(e.ctx, b.ID, treasury.BounceInput{Reason: ""}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("a reason is required: %v", err)
	}
	bounced, err := e.m.Treasury.BounceCheque(e.ctx, b.ID, treasury.BounceInput{Reason: "insufficient funds", Fee: irr(300), Actor: "ann"})
	must(t, err)
	if bounced.Status != treasury.Bounced || bounced.Reason != "insufficient funds" {
		t.Fatalf("%+v", bounced)
	}
	// The customer owes again (the receipt had made the receivable -8,000; the bounce brings it back to 0), the bank charged 300.
	if e.bal(t, mizan.AccReceivable) != 0 || e.bal(t, mizan.AccChequesInCollection) != 0 || e.bal(t, mizan.AccBank) != -300 || e.bal(t, mizan.AccBankFees) != 300 {
		t.Fatalf("receivable=%d collection=%d bank=%d fees=%d", e.bal(t, mizan.AccReceivable), e.bal(t, mizan.AccChequesInCollection), e.bal(t, mizan.AccBank), e.bal(t, mizan.AccBankFees))
	}
	if again, err := e.m.Treasury.BounceCheque(e.ctx, b.ID, treasury.BounceInput{Reason: "insufficient funds", Fee: irr(300)}); err != nil || again.Status != treasury.Bounced || e.bal(t, mizan.AccBankFees) != 300 {
		t.Fatalf("bouncing twice must book nothing more: %v", err)
	}

	// Returned: the receipt is reversed, as if the cheque had never been taken.
	r := e.cheque(t, "r1", treasury.Receivable, 2000, nil)
	ret, err := e.m.Treasury.ReturnCheque(e.ctx, r.ID, "customer paid cash instead", "ann")
	must(t, err)
	if ret.Status != treasury.Returned || e.bal(t, mizan.AccChequesInHand) != 0 || e.bal(t, mizan.AccReceivable) != 0 {
		t.Fatalf("%+v in hand=%d receivable=%d", ret, e.bal(t, mizan.AccChequesInHand), e.bal(t, mizan.AccReceivable))
	}
	if _, err := e.m.Treasury.DepositCheque(e.ctx, r.ID, main.ID, time.Time{}, "ann"); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a returned cheque cannot be deposited: %v", err)
	}
	e.balanced(t)
}

func TestIssuedCheque(t *testing.T) {
	e := setup(t, true)
	main := e.mainAccount(t)
	if _, err := e.m.Treasury.RegisterCheque(e.ctx, treasury.ChequeInput{IdemKey: "no-bank", Direction: treasury.Payable, Number: "1", Amount: irr(10),
		DueDate: now, CounterpartyRef: "supplier"}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("an issued cheque names its bank account: %v", err)
	}
	c := e.cheque(t, "p1", treasury.Payable, 4000, &main.ID)
	if c.Status != treasury.Issued || e.bal(t, mizan.AccChequesPayable) != 4000 || e.bal(t, mizan.AccPayable) != -4000 {
		t.Fatalf("issue: %+v payable cheques=%d suppliers=%d", c, e.bal(t, mizan.AccChequesPayable), e.bal(t, mizan.AccPayable))
	}
	// Our cheque bounces: the liability stays, only the fee is booked.
	b, err := e.m.Treasury.BounceCheque(e.ctx, c.ID, treasury.BounceInput{Reason: "no funds in our account", Fee: irr(100)})
	must(t, err)
	if b.Status != treasury.Bounced || e.bal(t, mizan.AccChequesPayable) != 4000 || e.bal(t, mizan.AccBank) != -100 {
		t.Fatalf("%+v cheques=%d bank=%d", b, e.bal(t, mizan.AccChequesPayable), e.bal(t, mizan.AccBank))
	}
	// Paid after all.
	cl, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, time.Time{}, "ann")
	must(t, err)
	if cl.Status != treasury.Cleared || e.bal(t, mizan.AccChequesPayable) != 0 || e.bal(t, mizan.AccBank) != -4100 {
		t.Fatalf("%+v cheques=%d bank=%d", cl, e.bal(t, mizan.AccChequesPayable), e.bal(t, mizan.AccBank))
	}
	// Taking an issued cheque back reverses the issue.
	r := e.cheque(t, "p2", treasury.Payable, 700, &main.ID)
	if _, err := e.m.Treasury.ReturnCheque(e.ctx, r.ID, "supplier gave it back", "ann"); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccChequesPayable) != 0 || e.bal(t, mizan.AccPayable) != -4000 {
		t.Fatalf("cheques=%d suppliers=%d", e.bal(t, mizan.AccChequesPayable), e.bal(t, mizan.AccPayable))
	}
	e.balanced(t)
}

func TestChequeIdempotencyAndConcurrency(t *testing.T) {
	e := setup(t, false)
	main := e.mainAccount(t)
	c := e.cheque(t, "dup", treasury.Receivable, 1000, nil)
	again := e.cheque(t, "dup", treasury.Receivable, 1000, nil)
	if again.ID != c.ID || e.bal(t, mizan.AccChequesInHand) != 1000 {
		t.Fatal("a retried registration must return the same cheque and book once")
	}
	if _, err := e.m.Treasury.RegisterCheque(e.ctx, treasury.ChequeInput{IdemKey: "dup", Direction: treasury.Receivable, SayadID: sayad("dup"), Number: "N-dup",
		Amount: irr(2000), DueDate: now, CounterpartyRef: "alice"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("the same key with another amount: %v", err)
	}
	if _, err := e.m.Treasury.RegisterCheque(e.ctx, treasury.ChequeInput{IdemKey: "other-key", Direction: treasury.Receivable, SayadID: sayad("dup"), Number: "N-2",
		Amount: irr(1000), DueDate: now, CounterpartyRef: "alice"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("the same Sayad id twice must conflict: %v", err)
	}

	_, err := e.m.Treasury.DepositCheque(e.ctx, c.ID, main.ID, time.Time{}, "ann")
	must(t, err)
	var wg sync.WaitGroup
	var failed atomic.Int32
	for range 10 { // the agent double-clicks, the bank statement matcher runs at the same time
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, time.Time{}, "ann"); err != nil {
				failed.Add(1)
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if e.bal(t, mizan.AccBank) != 1000 {
		t.Fatalf("the cheque was cleared %d times", e.bal(t, mizan.AccBank)/1000)
	}
	e.balanced(t)

	// Concurrent opposite outcomes: exactly one wins.
	d := e.cheque(t, "race", treasury.Receivable, 500, nil)
	_, err = e.m.Treasury.DepositCheque(e.ctx, d.ID, main.ID, time.Time{}, "ann")
	must(t, err)
	var wins atomic.Int32
	for _, f := range []func() error{
		func() error { _, err := e.m.Treasury.ClearCheque(e.ctx, d.ID, time.Time{}, "a"); return err },
		func() error {
			_, err := e.m.Treasury.BounceCheque(e.ctx, d.ID, treasury.BounceInput{Reason: "no funds"})
			return err
		},
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f() == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("a cheque clears or bounces, never both: %d winners", wins.Load())
	}
	e.balanced(t)
}

// payInvoice puts money into the gateway clearing account through a real payment.
func (e env) collect(t *testing.T, key string, amount int64) {
	t.Helper()
	inv := e.issuedInvoice(t, "inv-"+key, invoice.ItemInput{Description: "a", Quantity: 1, UnitPrice: amount, TaxBP: ptr(0)})
	p, err := e.m.StartInvoicePayment(e.ctx, mizan.StartInvoicePaymentInput{InvoiceID: inv.ID, IdemKey: key})
	must(t, err)
	e.payThroughGateway(t, p)
}

func TestGatewaySettlement(t *testing.T) {
	e := setup(t, true)
	main := e.mainAccount(t)
	e.collect(t, "a", 100000)
	e.collect(t, "b", 50000)
	if e.bal(t, mizan.AccGatewayClearing) != 150000 {
		t.Fatal("setup")
	}
	in := treasury.SettlementInput{Gateway: "zarinpal", Ref: "SETTLE-0001", AccountID: main.ID, Gross: irr(100000), Fee: irr(1500), Actor: "ann"}
	st, err := e.m.Treasury.RecordSettlement(e.ctx, in)
	must(t, err)
	if st.Net.Amount() != 98500 || e.bal(t, mizan.AccBank) != 98500 || e.bal(t, mizan.AccGatewayFees) != 1500 || e.bal(t, mizan.AccGatewayClearing) != 50000 {
		t.Fatalf("%+v bank=%d fees=%d clearing=%d", st, e.bal(t, mizan.AccBank), e.bal(t, mizan.AccGatewayFees), e.bal(t, mizan.AccGatewayClearing))
	}
	again, err := e.m.Treasury.RecordSettlement(e.ctx, in)
	if err != nil || again.ID != st.ID || e.bal(t, mizan.AccBank) != 98500 {
		t.Fatalf("a retried settlement must not book twice: %v", err)
	}
	// A fee with VAT: 1,500 kept, 136 of it VAT (the rest is the expense).
	e.collect(t, "vat", 20000)
	withVAT := treasury.SettlementInput{Gateway: "zarinpal", Ref: "SETTLE-VAT", AccountID: main.ID, Gross: irr(20000), Fee: irr(1500), FeeVAT: irr(136), Actor: "ann"}
	if _, err := e.m.Treasury.RecordSettlement(e.ctx, withVAT); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccGatewayFees) != 1500+1364 || e.bal(t, mizan.AccVATInput) != 136 || e.bal(t, mizan.AccGatewayClearing) != 50000 {
		t.Fatalf("fees %d input VAT %d clearing %d", e.bal(t, mizan.AccGatewayFees), e.bal(t, mizan.AccVATInput), e.bal(t, mizan.AccGatewayClearing))
	}
	bad := withVAT
	bad.Ref, bad.FeeVAT = "SETTLE-BAD", irr(1501)
	if _, err := e.m.Treasury.RecordSettlement(e.ctx, bad); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("VAT above the fee: %v", err)
	}
	other := in
	other.Gross = irr(90000)
	if _, err := e.m.Treasury.RecordSettlement(e.ctx, other); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("the same reference with other amounts: %v", err)
	}
	big := treasury.SettlementInput{Gateway: "zarinpal", Ref: "SETTLE-0002", AccountID: main.ID, Gross: irr(50001)}
	if _, err := e.m.Treasury.RecordSettlement(e.ctx, big); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a payout larger than the clearing balance must be refused: %v", err)
	}
	if e.bal(t, mizan.AccGatewayClearing) != 50000 {
		t.Fatal("a refused payout books nothing")
	}

	// The same payout recorded by ten requests at once: one journal.
	rest := treasury.SettlementInput{Gateway: "zarinpal", Ref: "SETTLE-0003", AccountID: main.ID, Gross: irr(50000), Fee: irr(500)}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.m.Treasury.RecordSettlement(e.ctx, rest); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if e.bal(t, mizan.AccBank) != 98500+18500+49500 || e.bal(t, mizan.AccGatewayClearing) != 0 || e.bal(t, mizan.AccGatewayFees) != 2000+1364 {
		t.Fatalf("bank=%d clearing=%d fees=%d", e.bal(t, mizan.AccBank), e.bal(t, mizan.AccGatewayClearing), e.bal(t, mizan.AccGatewayFees))
	}
	e.balanced(t)
}

const csvStatement = `date,description,reference,credit,debit
1405/08/18,"Cheque 1234567890123456 Alice",REF1,"5,000",
1405/08/18,"Transfer from ZarinPal SETTLE-0001",REF2,"98,500",
1405/08/18,"Cash deposit",REF3,"5,000",
1405/08/18,"Monthly account charge",REF4,,300
1405/08/18,"Payment to supplier",REF5,,4000
`

func TestStatementImportAndMatching(t *testing.T) {
	e := setup(t, false)
	main := e.mainAccount(t)
	other := e.secondAccount(t)

	// Money on its way: a deposited cheque (Sayad id in the statement), a payout, an issued cheque.
	rc := e.cheque(t, "rc", treasury.Receivable, 5000, nil)
	rc2, err := e.m.Treasury.RegisterCheque(e.ctx, treasury.ChequeInput{IdemKey: "rc-x", Direction: treasury.Receivable, SayadID: "9999888877776666", Number: "N-x",
		Amount: irr(5000), DueDate: now, CounterpartyRef: "bob"})
	must(t, err)
	for _, c := range []treasury.Cheque{rc, rc2} {
		_, err := e.m.Treasury.DepositCheque(e.ctx, c.ID, main.ID, time.Time{}, "ann")
		must(t, err)
	}
	// rc's Sayad id is in the statement text; rc2's is not, so with the amount alone it stays a manual decision.
	sayadOfRC := sayad("rc")
	text := strings.Replace(csvStatement, "1234567890123456", sayadOfRC, 1)
	e.collect(t, "s", 100000)
	_, err = e.m.Treasury.RecordSettlement(e.ctx, treasury.SettlementInput{Gateway: "zarinpal", Ref: "SETTLE-0001", AccountID: main.ID, Gross: irr(100000), Fee: irr(1500)})
	must(t, err)
	pay := e.cheque(t, "pay", treasury.Payable, 4000, &main.ID)

	rows, err := treasury.ParseStatementCSV(strings.NewReader(text))
	must(t, err)
	res, err := e.m.Treasury.ImportStatement(e.ctx, main.ID, rows, "ann")
	must(t, err)
	if res.Read != 5 || res.Inserted != 5 || res.Skipped != 0 {
		t.Fatalf("%+v", res)
	}
	res, err = e.m.Treasury.ImportStatement(e.ctx, main.ID, rows, "ann")
	must(t, err)
	if res.Inserted != 0 || res.Skipped != 5 {
		t.Fatalf("importing the same statement twice must add nothing: %+v", res)
	}
	// An overlapping statement adds only the new line.
	more, _ := treasury.ParseStatementCSV(strings.NewReader("date,description,reference,credit,debit\n" + strings.SplitN(text, "\n", 3)[1] + "\n1405/08/19,late,REF9,10,\n"))
	if res, err = e.m.Treasury.ImportStatement(e.ctx, main.ID, more, "ann"); err != nil || res.Inserted != 1 {
		t.Fatalf("overlap: %+v %v", res, err)
	}
	if _, err := e.m.Treasury.ImportStatement(e.ctx, uuid.New(), rows, "ann"); apperr.KindOf(err) != apperr.KindNotFound {
		t.Fatalf("unknown account: %v", err)
	}

	lines := e.lines(t, main.ID)
	byRef := map[string]treasury.Line{}
	for _, l := range lines {
		byRef[l.Reference] = l
	}
	// Suggestions: the cheque whose Sayad id is named is High; the amount-only line sees both cheques, none High.
	sug, err := e.m.Treasury.Suggest(e.ctx, byRef["REF1"].ID)
	must(t, err)
	highs := 0
	for _, c := range sug {
		if c.High {
			highs++
			if c.ID != rc.ID {
				t.Fatalf("the wrong cheque is High: %+v", c)
			}
		}
	}
	if len(sug) != 2 || highs != 1 {
		t.Fatalf("suggestions for REF1: %+v", sug)
	}
	if sug, _ := e.m.Treasury.Suggest(e.ctx, byRef["REF3"].ID); len(sug) != 2 || sug[0].High || sug[1].High {
		t.Fatalf("amount alone is never High: %+v", sug)
	}

	auto, err := e.m.Treasury.AutoMatch(e.ctx, main.ID, 100, "robot")
	must(t, err)
	if auto.Matched != 2 || auto.Failed != 0 { // the cheque with its Sayad id, and the payout with its reference
		t.Fatalf("auto: %+v", auto)
	}
	if got, _ := e.m.Treasury.Cheque(e.ctx, rc.ID); got.Status != treasury.Cleared {
		t.Fatalf("the named cheque must be cleared: %+v", got)
	}
	if got, _ := e.m.Treasury.Cheque(e.ctx, rc2.ID); got.Status != treasury.Deposited {
		t.Fatalf("an amount-only candidate must stay untouched: %+v", got)
	}
	l2, _ := e.m.Treasury.Line(e.ctx, byRef["REF2"].ID)
	if l2.Status != treasury.LineMatched || l2.MatchKind != treasury.MatchSettlement || l2.MatchedBy != "robot" {
		t.Fatalf("%+v", l2)
	}
	if auto2, _ := e.m.Treasury.AutoMatch(e.ctx, main.ID, 100, "robot"); auto2.Matched != 0 {
		t.Fatalf("a second pass finds nothing new: %+v", auto2)
	}

	// A person decides the rest.
	l3 := byRef["REF3"]
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: l3.ID, Kind: treasury.MatchCheque, ItemID: pay.ID, Actor: "ann"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a payable cheque cannot match money coming in: %v", err)
	}
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: l3.ID, Kind: treasury.MatchIgnored, Actor: "ann"}); apperr.KindOf(err) != apperr.KindInvalid {
		t.Fatalf("ignoring needs a reason: %v", err)
	}
	m3, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: l3.ID, Kind: treasury.MatchCheque, ItemID: rc2.ID, Actor: "ann"})
	must(t, err)
	if m3.Status != treasury.LineMatched || m3.JournalID == nil {
		t.Fatalf("%+v", m3)
	}
	if again, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: l3.ID, Kind: treasury.MatchCheque, ItemID: rc2.ID, Actor: "ann"}); err != nil || again.ID != m3.ID {
		t.Fatalf("matching again is a no-op: %v", err)
	}
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: l3.ID, Kind: treasury.MatchFee, Actor: "ann"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a matched line cannot become something else: %v", err)
	}
	// The cheque belongs to the main account, not to the second one.
	otherRows, _ := treasury.ParseStatementCSV(strings.NewReader("date,amount,reference\n2026-11-09,-4000,O1\n"))
	_, err = e.m.Treasury.ImportStatement(e.ctx, other.ID, otherRows, "ann")
	must(t, err)
	ol := e.lines(t, other.ID)[0]
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: ol.ID, Kind: treasury.MatchCheque, ItemID: pay.ID, Actor: "ann"}); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("a line of another account cannot clear the cheque: %v", err)
	}
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: byRef["REF5"].ID, Kind: treasury.MatchCheque, ItemID: pay.ID, Actor: "ann"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.m.Treasury.Cheque(e.ctx, pay.ID); got.Status != treasury.Cleared {
		t.Fatalf("%+v", got)
	}
	bankBefore := e.bal(t, mizan.AccBank)
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: byRef["REF4"].ID, Kind: treasury.MatchFee, Actor: "ann"}); err != nil {
		t.Fatal(err)
	}
	if e.bal(t, mizan.AccBank) != bankBefore-300 || e.bal(t, mizan.AccBankFees) != 300 {
		t.Fatalf("bank charge: bank=%d fees=%d", e.bal(t, mizan.AccBank), e.bal(t, mizan.AccBankFees))
	}
	if _, err := e.m.Treasury.MatchLine(e.ctx, treasury.MatchInput{LineID: byRef["REF9"].ID, Kind: treasury.MatchIgnored, Note: "rounding", Actor: "ann"}); err != nil {
		t.Fatal(err)
	}
	if open := e.lines(t, main.ID); countStatus(open, treasury.LineUnmatched) != 0 {
		t.Fatalf("every line is handled: %+v", open)
	}
	// The bank account's ledger balance now equals what the statement says arrived and left.
	if want := int64(5000 + 98500 + 5000 - 300 - 4000); e.bal(t, mizan.AccBank) != want {
		t.Fatalf("bank balance %d, want %d", e.bal(t, mizan.AccBank), want)
	}
	e.balanced(t)
}

func countStatus(ls []treasury.Line, s treasury.LineStatus) int {
	n := 0
	for _, l := range ls {
		if l.Status == s {
			n++
		}
	}
	return n
}

func (e env) lines(t *testing.T, account uuid.UUID) []treasury.Line {
	t.Helper()
	params, err := query.ParseValues(url.Values{"filter[bank_account_id]": {account.String()}, "page_size": {"200"}})
	must(t, err)
	page, err := e.m.Treasury.ListLines(e.ctx, params)
	must(t, err)
	return page.Items
}

func TestCashPosition(t *testing.T) {
	e := setup(t, false)
	main := e.mainAccount(t)
	e.collect(t, "g", 20000)
	c := e.cheque(t, "in", treasury.Receivable, 5000, nil)
	e.cheque(t, "in2", treasury.Receivable, 3000, nil)
	_, err := e.m.Treasury.DepositCheque(e.ctx, c.ID, main.ID, time.Time{}, "ann")
	must(t, err)
	e.cheque(t, "out", treasury.Payable, 7000, &main.ID)
	_, err = e.m.Treasury.RecordSettlement(e.ctx, treasury.SettlementInput{Gateway: "zp", Ref: "S1", AccountID: main.ID, Gross: irr(12000), Fee: irr(200)})
	must(t, err)

	p, err := e.m.Treasury.Position(e.ctx)
	must(t, err)
	// bank 11,800; cheques in 8,000; gateway pending 8,000; cheques out 7,000.
	if p.Banks.Amount() != 11800 || p.ChequesIn.Amount() != 8000 || p.GatewayPending.Amount() != 8000 || p.ChequesOut.Amount() != 7000 || p.Projected.Amount() != 20800 {
		t.Fatalf("%+v", p)
	}
	if len(p.Accounts) != 1 || p.Accounts[0].Balance.Amount() != 11800 {
		t.Fatalf("%+v", p.Accounts)
	}
}

func TestChequeBouncedIsPresentedAgainAndEndorsed(t *testing.T) {
	e := setup(t, true)
	acc := e.mainAccount(t)
	c := e.cheque(t, "re-1", treasury.Receivable, 5_000, nil)
	_, err := e.m.Treasury.DepositCheque(e.ctx, c.ID, acc.ID, time.Time{}, "ann")
	must(t, err)
	_, err = e.m.Treasury.BounceCheque(e.ctx, c.ID, treasury.BounceInput{Reason: "no funds", Actor: "ann"})
	must(t, err)
	if e.bal(t, mizan.AccReceivable) != 0 || e.bal(t, mizan.AccChequesInCollection) != 0 {
		t.Fatalf("after the bounce: receivable %d in collection %d", e.bal(t, mizan.AccReceivable), e.bal(t, mizan.AccChequesInCollection))
	}
	// presented again, bounces again, presented a third time and clears: every cycle has its own journals
	for i := 0; i < 2; i++ {
		_, err = e.m.Treasury.DepositCheque(e.ctx, c.ID, acc.ID, time.Time{}, "ann")
		must(t, err)
		if e.bal(t, mizan.AccChequesInCollection) != 5_000 || e.bal(t, mizan.AccReceivable) != -5_000 {
			t.Fatalf("presented again: collection %d receivable %d", e.bal(t, mizan.AccChequesInCollection), e.bal(t, mizan.AccReceivable))
		}
		if i == 0 {
			_, err = e.m.Treasury.BounceCheque(e.ctx, c.ID, treasury.BounceInput{Reason: "again", Actor: "ann"})
			must(t, err)
		}
	}
	got, err := e.m.Treasury.ClearCheque(e.ctx, c.ID, time.Time{}, "ann")
	must(t, err)
	if got.Status != treasury.Cleared || e.bal(t, mizan.AccChequesInCollection) != 0 {
		t.Fatalf("%+v", got)
	}
	e.balanced(t)

	// A bounced cheque the customer replaces another way is returned without a second reversal.
	r := e.cheque(t, "re-2", treasury.Receivable, 700, nil)
	_, err = e.m.Treasury.DepositCheque(e.ctx, r.ID, acc.ID, time.Time{}, "ann")
	must(t, err)
	_, err = e.m.Treasury.BounceCheque(e.ctx, r.ID, treasury.BounceInput{Reason: "x", Actor: "ann"})
	must(t, err)
	before := e.bal(t, mizan.AccReceivable)
	if got, err := e.m.Treasury.ReturnCheque(e.ctx, r.ID, "customer pays by transfer", "ann"); err != nil || got.Status != treasury.Returned || e.bal(t, mizan.AccReceivable) != before {
		t.Fatalf("%v %+v", err, got)
	}

	// Endorsement: the cheque in hand pays a supplier; it can come back and be endorsed again; only an in-hand received cheque can be endorsed.
	h := e.cheque(t, "en-1", treasury.Receivable, 3_000, nil)
	inHand := e.bal(t, mizan.AccChequesInHand)
	payable := e.bal(t, mizan.AccPayable)
	end, err := e.m.Treasury.EndorseCheque(e.ctx, h.ID, treasury.EndorseInput{To: "supplier-1", Actor: "ann"})
	must(t, err)
	if end.Status != treasury.Endorsed || e.bal(t, mizan.AccChequesInHand) != inHand-3_000 || e.bal(t, mizan.AccPayable) != payable-3_000 {
		t.Fatalf("endorsed: %+v hand %d payable %d (was %d, %d)", end, e.bal(t, mizan.AccChequesInHand), e.bal(t, mizan.AccPayable), inHand, payable)
	}
	if _, err := e.m.Treasury.DepositCheque(e.ctx, h.ID, acc.ID, time.Time{}, "ann"); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an endorsed cheque cannot be deposited: %v", err)
	}
	back, err := e.m.Treasury.TakeBackCheque(e.ctx, h.ID, "ann")
	must(t, err)
	if back.Status != treasury.InHand || e.bal(t, mizan.AccChequesInHand) != inHand || e.bal(t, mizan.AccPayable) != payable {
		t.Fatalf("taken back: %+v", back)
	}
	if _, err := e.m.Treasury.EndorseCheque(e.ctx, h.ID, treasury.EndorseInput{To: "supplier-2", Actor: "ann"}); err != nil {
		t.Fatalf("endorsed again: %v", err)
	}
	issued := e.cheque(t, "en-2", treasury.Payable, 100, &acc.ID)
	if _, err := e.m.Treasury.EndorseCheque(e.ctx, issued.ID, treasury.EndorseInput{To: "x"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("an issued cheque cannot be endorsed: %v", err)
	}
	e.balanced(t)
}
