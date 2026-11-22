package treasury

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func TestChequeLifecycleRules(t *testing.T) {
	allowed := []struct {
		d        Direction
		from, to ChequeStatus
		ok       bool
	}{
		{Receivable, InHand, Deposited, true}, {Receivable, InHand, Returned, true}, {Receivable, InHand, Cleared, false},
		{Receivable, Deposited, Cleared, true}, {Receivable, Deposited, Bounced, true}, {Receivable, Deposited, Returned, false},
		{Receivable, Bounced, Cleared, false}, {Receivable, Cleared, Bounced, false}, {Receivable, Returned, Deposited, false},
		{Payable, Issued, Cleared, true}, {Payable, Issued, Bounced, true}, {Payable, Issued, Returned, true},
		{Payable, Bounced, Cleared, true}, {Payable, Cleared, Bounced, false}, {Payable, Issued, Deposited, false},
	}
	for _, a := range allowed {
		c := Cheque{Direction: a.d, Status: a.from}
		if c.CanMoveTo(a.to) != a.ok {
			t.Errorf("%s %s -> %s: got %v, want %v", a.d, a.from, a.to, !a.ok, a.ok)
		}
	}
	if Receivable.Initial() != InHand || Payable.Initial() != Issued {
		t.Fatal("initial states")
	}
	if !(Cheque{Status: Bounced}).Open() || (Cheque{Status: Cleared}).Open() || (Cheque{Status: Returned}).Open() {
		t.Fatal("open means still owed or owing")
	}
}

func TestIBAN(t *testing.T) {
	// A well-formed Iranian IBAN built from the ISO 13616 algorithm (not a real account).
	good := makeIBAN("0170000000123456789012")
	for in, ok := range map[string]bool{
		good:                  true,
		strings.ToLower(good): true,
		good[:4] + " " + good[4:8] + " " + good[8:]: true,
		"":              true,  // optional
		good[:25] + "0": false, // wrong check digits
		"IR12":          false,
		"DE" + good[2:]: false,
	} {
		if _, err := ValidateIBAN(in); (err == nil) != ok {
			t.Errorf("ValidateIBAN(%q): err=%v, want ok=%v", in, err, ok)
		}
	}
}

// makeIBAN computes the two check digits of an IR IBAN for a 22-digit BBAN.
func makeIBAN(bban string) string {
	rest := bban + "1827" + "00" // IR -> 18 27, check digits as 00
	rem := 0
	for _, r := range rest {
		rem = (rem*10 + int(r-'0')) % 97
	}
	check := 98 - rem
	return "IR" + string(rune('0'+check/10)) + string(rune('0'+check%10)) + bban
}

func TestSayadID(t *testing.T) {
	for in, want := range map[string]string{"1234567890123456": "1234567890123456", "۱۲۳۴ ۵۶۷۸-۹۰۱۲۳۴۵۶": "1234567890123456", "": ""} {
		got, err := ValidateSayadID(in)
		if err != nil || got != want {
			t.Errorf("ValidateSayadID(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"123", "12345678901234567", "123456789012345a"} {
		if _, err := ValidateSayadID(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestChequeInputValidation(t *testing.T) {
	acc := uuid.New()
	good := ChequeInput{IdemKey: "k", Direction: Receivable, Number: "123", Amount: money.Must(1000, money.IRR), DueDate: time.Now(), CounterpartyRef: "alice"}
	if _, err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*ChequeInput){
		"no key":              func(c *ChequeInput) { c.IdemKey = "" },
		"bad direction":       func(c *ChequeInput) { c.Direction = "x" },
		"no number":           func(c *ChequeInput) { c.Number = " " },
		"zero amount":         func(c *ChequeInput) { c.Amount = money.Must(0, money.IRR) },
		"no due date":         func(c *ChequeInput) { c.DueDate = time.Time{} },
		"no counterparty":     func(c *ChequeInput) { c.CounterpartyRef = "" },
		"received with bank":  func(c *ChequeInput) { c.BankAccountID = &acc },
		"issued without bank": func(c *ChequeInput) { c.Direction = Payable },
		"bad sayad":           func(c *ChequeInput) { c.SayadID = "12" },
	} {
		c := good
		mut(&c)
		if _, err := c.validate(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestParseStatementCSV(t *testing.T) {
	rows, err := ParseStatementCSV(strings.NewReader("\xef\xbb\xbfDate,Description,Reference,Credit,Debit,Balance\n" +
		"1405/08/18,\"cheque 1234567890123456, Alice\",R1,\"1,200,000\",,5000000\n" +
		"2026-11-10,fee,,,\"۵۰٬۰۰۰\",4950000\n"))
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0].Amount != 1200000 || rows[0].Date.Format("2006-01-02") != "2026-11-09" || rows[0].Reference != "R1" || *rows[0].Balance != 5000000 {
		t.Fatalf("%+v", rows[0])
	}
	if rows[1].Amount != -50000 {
		t.Fatalf("a debit is negative: %+v", rows[1])
	}
	signed, err := ParseStatementCSV(strings.NewReader("date,amount\n2026-11-09,-100\n2026-11-09,250\n"))
	if err != nil || signed[0].Amount != -100 || signed[1].Amount != 250 {
		t.Fatalf("%v %v", signed, err)
	}
	for _, bad := range []string{"", "date\n2026-11-09\n", "date,amount\n", "date,amount\nsoon,5\n", "date,amount\n2026-11-09,x\n", "date,amount\n2026-11-09,0\n",
		"date,credit,debit\n2026-11-09,5,6\n"} {
		if _, err := ParseStatementCSV(strings.NewReader(bad)); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestFingerprintsAreStableAndKeepIdenticalLines(t *testing.T) {
	acc := uuid.New()
	d := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	a := StatementRow{Date: d, Amount: 100, Description: "x"}
	fp := Fingerprints(acc, []StatementRow{a, a, {Date: d, Amount: 200}})
	if fp[0] == fp[1] || fp[0] == fp[2] {
		t.Fatal("two identical lines of one statement are two lines")
	}
	again := Fingerprints(acc, []StatementRow{a, a, {Date: d, Amount: 200}})
	for i := range fp {
		if fp[i] != again[i] {
			t.Fatal("the same statement must give the same fingerprints")
		}
	}
	if other := Fingerprints(uuid.New(), []StatementRow{a}); other[0] == fp[0] {
		t.Fatal("fingerprints are per account")
	}
}

func TestSettlementInputValidation(t *testing.T) {
	good := SettlementInput{Gateway: "zarinpal", Ref: "S1", AccountID: uuid.New(), Gross: money.Must(1000, money.IRR), Fee: money.Must(10, money.IRR)}
	if _, err := good.validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*SettlementInput){
		"no gateway":   func(s *SettlementInput) { s.Gateway = "" },
		"no ref":       func(s *SettlementInput) { s.Ref = "" },
		"no account":   func(s *SettlementInput) { s.AccountID = uuid.Nil },
		"zero gross":   func(s *SettlementInput) { s.Gross = money.Must(0, money.IRR) },
		"negative fee": func(s *SettlementInput) { s.Fee = money.Must(-1, money.IRR) },
		"fee = gross":  func(s *SettlementInput) { s.Fee = money.Must(1000, money.IRR) },
		"fee currency": func(s *SettlementInput) { s.Fee = money.Must(1, money.IRT) },
	} {
		s := good
		mut(&s)
		if _, err := s.validate(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}
