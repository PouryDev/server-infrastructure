package moadian

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

type built struct {
	Header   map[string]any   `json:"header"`
	Body     []map[string]any `json:"body"`
	Payments []map[string]any `json:"payments"`
}

func build(t *testing.T, b *InvoiceBuilder, inv einvoice.Invoice) built {
	t.Helper()
	raw, err := b.Build(inv, "t")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var out built
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func refuse(t *testing.T, b *InvoiceBuilder, inv einvoice.Invoice, why string) {
	t.Helper()
	if _, err := b.Build(inv, "t"); err == nil {
		t.Errorf("must be refused: %s", why)
	}
}

// The worked example of the technical paper RC_TICS.IS_v1.3 (a pattern 1 credit sale): the unique tax number and every figure are the paper's.
func TestOfficialSampleOfTheTechnicalPaper(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624", UnitCode: "164", Credit: true}
	inv := einvoice.Invoice{Number: "49321217", IssuedAt: time.UnixMilli(1683997837988).UTC(), Seller: einvoice.Party{EconomicCode: "14003778990"},
		Buyer: einvoice.Party{NationalID: "10100302746", EconomicCode: "10100302746"}, Lines: []einvoice.Line{{Quantity: 2, UnitPrice: 10_000, Discount: 500, VATBP: 900}}}
	got := build(t, b, inv)
	h := got.Header
	if h["taxid"] != "A1121604C220002F095011" || h["inty"] != 1.0 || h["inp"] != 1.0 || h["ins"] != 1.0 || h["tob"] != 2.0 || h["tins"] != "14003778990" || h["bid"] != "10100302746" || h["tinb"] != "10100302746" ||
		h["tprdis"] != 20000.0 || h["tdis"] != 500.0 || h["tadis"] != 19500.0 || h["tvam"] != 1755.0 || h["todam"] != 0.0 || h["tbill"] != 21255.0 || h["setm"] != 2.0 || h["indatim"] != 1683997837988.0 {
		t.Fatalf("header: %v", h)
	}
	l := got.Body[0]
	if l["sstid"] != "2710000138624" || l["mu"] != "164" || l["am"] != 2.0 || l["fee"] != 10000.0 || l["prdis"] != 20000.0 || l["dis"] != 500.0 || l["adis"] != 19500.0 || l["vra"] != 9.0 || l["vam"] != 1755.0 || l["tsstam"] != 21255.0 {
		t.Fatalf("body: %v", l)
	}
	// The paper's sample prints the internal number "inno" as the decimal of the serial; the paper's field table says a 10-character hex string: the builder follows the table.
}

func TestGoldPattern(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	inv := einvoice.Invoice{Number: "1", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Pattern: 3, Seller: einvoice.Party{EconomicCode: "14003778990"},
		Buyer: einvoice.Party{NationalID: "0012345678", PostalCode: "1234567890"},
		Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 10_000_000, VATBP: 900, WageFee: 500_000, SellerProfit: 300_000}}}
	got := build(t, b, inv)
	l, h := got.Body[0], got.Header
	// tcpbs = 800,000; adis = 10,000,000 + 800,000; vam = 80,000 (10% of the wage, profit and commission) + 900,000 (9% of the gold)
	if l["tcpbs"] != 800000.0 || l["consfee"] != 500000.0 || l["spro"] != 300000.0 || l["bros"] != 0.0 || l["adis"] != 10800000.0 || l["vam"] != 980000.0 || l["tsstam"] != 11780000.0 || h["inp"] != 3.0 || h["tbill"] != 11780000.0 {
		t.Fatalf("gold: %v %v", l, h)
	}
	inv.Lines[0].WageFee = 10_000_000
	refuse(t, b, inv, "the wage must be below the line amount")
	inv.Lines[0].WageFee, inv.Lines[0].SellerProfit = 0, 0
	refuse(t, b, inv, "a gold line without wage, profit or commission")
}

func TestContractingAndExport(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	inv := einvoice.Invoice{Number: "2", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Pattern: 4, Seller: einvoice.Party{EconomicCode: "14003778990"},
		Buyer: einvoice.Party{NationalID: "10100302746", EconomicCode: "10100302746"}, Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 1_000_000, VATBP: 900}}}
	refuse(t, b, inv, "contracting needs the contract number")
	inv.ContractNo = "123456"
	if got := build(t, b, inv); got.Header["crn"] != "123456" || got.Header["inp"] != 4.0 {
		t.Fatalf("%v", got.Header)
	}
	inv.ContractNo = "12345678901234"
	refuse(t, b, inv, "contract number longer than 12 digits")

	exp := einvoice.Invoice{Number: "3", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Pattern: 7, Seller: einvoice.Party{EconomicCode: "14003778990"},
		Customs: &einvoice.Customs{CottageNo: "123456", CottageDate: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC), OfficeCode: "12345"},
		Lines:   []einvoice.Line{{Quantity: 10, Export: &einvoice.ExportLine{Currency: "USD", ForeignValueE4: 125_0000, RialValue: 5_000_000, RateE8: 40_000_00000000}}}}
	got := build(t, b, exp)
	l, h := got.Body[0], got.Header
	if l["ssrv"] != 5000000.0 || l["sscv"] != 125.0 || l["cut"] != "USD" || l["exr"] != 40000.0 || l["vra"] != 0.0 || l["vam"] != 0.0 || l["tsstam"] != 5000000.0 ||
		h["torv"] != 5000000.0 || h["tocv"] != 125.0 || h["tbill"] != 5000000.0 || h["tvam"] != 0.0 || h["inp"] != 7.0 || h["cdcn"] != "123456" || h["cdcd"] != float64(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC).Unix()/86400) || h["scc"] != "12345" {
		t.Fatalf("export: %v %v", l, h)
	}
	if _, ok := h["tprdis"]; ok {
		t.Fatal("an export has no tprdis")
	}
	exp.Lines[0].VATBP = 900
	refuse(t, b, exp, "export VAT must be 0")
	exp.Lines[0].VATBP, exp.Lines[0].Export.Currency = 0, "US"
	refuse(t, b, exp, "bad currency")
	exp.Lines[0].Export.Currency, exp.Subject, exp.RefTaxID = "USD", 4, "A1121604C220002F095011"
	refuse(t, b, exp, "no return for an export")
}

func TestSubjectsAndReference(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	const ref = "A1121604C220002F095011" // the paper's sample tax id: valid
	inv := einvoice.Invoice{Number: "5", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Seller: einvoice.Party{EconomicCode: "14003778990"},
		Buyer: einvoice.Party{NationalID: "0012345678", PostalCode: "1234567890"}, Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 1000, VATBP: 900}}}
	for _, ins := range []int{2, 3, 4} {
		inv.Subject, inv.RefTaxID = ins, ""
		refuse(t, b, inv, "a subject 2-4 needs irtaxid")
		inv.RefTaxID = "A1121604C220002F095012" // wrong check digit
		refuse(t, b, inv, "an invalid irtaxid")
		inv.RefTaxID = ref
		if got := build(t, b, inv); got.Header["ins"] != float64(ins) || got.Header["irtaxid"] != ref {
			t.Fatalf("subject %d: %v", ins, got.Header)
		}
	}
	inv.Subject = 1
	refuse(t, b, inv, "an original with a reference")
	inv.Subject, inv.RefTaxID = 9, ""
	refuse(t, b, inv, "unknown subject")
	inv.Subject = 0
	inv.Pattern = 8
	refuse(t, b, inv, "waybill pattern is not built")
	inv.Pattern = 10
	refuse(t, b, inv, "there is no pattern 10")
}

func TestMixedSettlementAndPayments(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	paid := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	inv := einvoice.Invoice{Number: "6", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Seller: einvoice.Party{EconomicCode: "14003778990"}, Settlement: 3, CashAmount: 600_000,
		Buyer: einvoice.Party{NationalID: "0012345678", PostalCode: "1234567890"}, Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 1_000_000, VATBP: 900}},
		Payments: []einvoice.Payment{{Method: 4, Amount: 690_000, PaidAt: paid, TraceNo: "123456", CardNo: "6037991234567890", TerminalNo: "12345678"}}}
	got := build(t, b, inv)
	// tbill 1,090,000; cap + insp = tbill - tvam - todam = 1,000,000 -> insp 400,000
	if h := got.Header; h["setm"] != 3.0 || h["cap"] != 600000.0 || h["insp"] != 400000.0 || h["tbill"] != 1090000.0 {
		t.Fatalf("%v", h)
	}
	if p := got.Payments[0]; p["pmt"] != 4.0 || p["pv"] != 690000.0 || p["pdt"] != float64(paid.UnixMilli()) || p["trn"] != "123456" || p["pcn"] != "6037991234567890" {
		t.Fatalf("%v", p)
	}
	inv.CashAmount = 1_000_000
	refuse(t, b, inv, "insp must stay positive")
	inv.CashAmount = 0
	refuse(t, b, inv, "setm 3 needs cap")
	inv.CashAmount, inv.Payments[0].Amount = 600_000, 2_000_000
	refuse(t, b, inv, "pv above tbill")
	inv.Payments[0].Amount, inv.Settlement = 100, 2
	refuse(t, b, inv, "pv of a credit sale must be 0")
	inv.Settlement, inv.Payments[0].Method = 1, 9
	refuse(t, b, inv, "bad payment method")
	inv.Payments[0].Method, inv.Payments[0].CardNo = 4, "12ab"
	refuse(t, b, inv, "non-numeric card")
	if !strings.Contains(string(mustBuild(t, b, einvoice.Invoice{Number: "7", IssuedAt: paid, Seller: einvoice.Party{EconomicCode: "14003778990"}, Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 10, VATBP: 0}}})), `"setm":1`) {
		t.Fatal("default is a cash sale")
	}
}

func mustBuild(t *testing.T, b *InvoiceBuilder, inv einvoice.Invoice) []byte {
	t.Helper()
	raw, err := b.Build(inv, "t")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func baseInv(pattern int) einvoice.Invoice {
	return einvoice.Invoice{Number: "9", IssuedAt: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), Pattern: pattern, Seller: einvoice.Party{EconomicCode: "14003778990"},
		Buyer: einvoice.Party{NationalID: "0012345678", PostalCode: "1234567890"}, Lines: []einvoice.Line{{Quantity: 1, UnitPrice: 1_000_000, VATBP: 900}}}
}

func TestUtilityAirlineInsuranceChain(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624", UnitCode: "1627"}
	u := baseInv(5)
	refuse(t, b, u, "utility needs billid")
	u.BillID = "1234567890123456789"
	if h := build(t, b, u).Header; h["billid"] != "1234567890123456789" || h["inp"] != 5.0 || h["inty"] != 1.0 {
		t.Fatalf("%v", h)
	}
	u.BillID = "12345678901234567890"
	refuse(t, b, u, "billid over 19 digits")

	a := baseInv(6)
	refuse(t, b, a, "airline needs the flight type")
	a.FlightType = 1
	if got := build(t, b, a); got.Header["ft"] != 1.0 || got.Body[0]["prdis"] != 1000000.0 {
		t.Fatalf("%v", got.Header)
	}
	a.Lines[0].Quantity = 2
	refuse(t, b, a, "airline quantity must be 1")
	a.Lines[0].Quantity, a.Buyer = 1, einvoice.Party{}
	refuse(t, b, a, "airline needs a passenger identity")
	a.Buyer = einvoice.Party{PassportNo: "P1234567", Kind: 4}
	a.FlightType, a.TravelAgencyCode = 2, "14003778990"
	if h := build(t, b, a).Header; h["bpn"] != "P1234567" || h["tinc"] != "14003778990" || h["tob"] != 4.0 {
		t.Fatalf("%v", h)
	}

	in := baseInv(13)
	refuse(t, b, in, "insurance needs the policy id")
	in.PolicyNo = "123456789"
	in.Buyer = einvoice.Party{}
	if h := build(t, b, in).Header; h["in"] != "123456789" || h["inty"] != 2.0 { // insurance is allowed as a type 2 invoice
		t.Fatalf("%v", h)
	}
	in.EndorsementNo = "12"
	refuse(t, b, in, "bad endorsement id")

	c := baseInv(14)
	c.Lines[0].VATBP = 900
	refuse(t, b, c, "chain needs the buyer's economic code")
	c.Buyer.EconomicCode, c.Lines[0].VATBP = "14003778990", 0
	if got := build(t, b, c); got.Header["tinb"] != "14003778990" || got.Body[0]["vra"] != 0.0 {
		t.Fatalf("%v", got.Header)
	}
	c.Lines[0].VATBP = 900
	refuse(t, b, c, "chain VAT rate is 0")
}

func TestCommodityExchangeAndPetroleum(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624", UnitCode: "1627"}
	x := baseInv(11)
	x.Lines[0].VATBP = 0
	refuse(t, b, x, "commodity exchange needs the notice")
	x.ExchangeNoticeNo, x.ExchangeNoticeDate = "555", time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	got := build(t, b, x)
	if _, has := got.Body[0]["prdis"]; has || got.Header["tadis"] != 1000000.0 || got.Header["tbill"] != 1000000.0 || got.Header["asn"] != "555" || got.Header["asd"] != float64(x.ExchangeNoticeDate.Unix()/86400) {
		t.Fatalf("%v %v", got.Header, got.Body[0])
	}
	if _, has := got.Header["tprdis"]; has {
		t.Fatal("no tprdis on a commodity-exchange invoice")
	}
	x.Subject, x.RefTaxID = 4, "A1121604C220002F095011"
	refuse(t, b, x, "no return for a commodity-exchange invoice")
	x.Subject, x.RefTaxID = 1, ""
	x.Lines = append(x.Lines, x.Lines[0])
	refuse(t, b, x, "one row only")

	p := baseInv(9)
	p.Lines = []einvoice.Line{{Quantity: 1000, UnitPrice: 5_000, PetroBaseE4: 1_5000}} // 1,000 litres, VAT base 1.5 rials a litre
	got = build(t, b, p)
	if l := got.Body[0]; l["vba"] != 1.5 || l["vam"] != 1500.0 || l["tsstam"] != 5001500.0 {
		t.Fatalf("%v", l)
	}
}

func TestCurrencySale(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	c := baseInv(2)
	// 100 USD at 1,000,000 rials sold, bought at 990,000: margin 1,000,000, whole amount 100,000,000; VAT base 1,000,000 at 9%.
	c.Lines = []einvoice.Line{{Quantity: 1, VATBP: 900, FX: &einvoice.FXLine{Currency: "USD", AmountE4: 100_0000, RateE8: 1_000_000_00000000, PurchaseRateE8: 990_000_00000000, VATBase: 1_000_000}}}
	got := build(t, b, c)
	l := got.Body[0]
	if l["prdis"] != 100000000.0 || l["cfee"] != 100.0 || l["exr"] != 1000000.0 || l["cpr"] != 990000.0 || l["sovat"] != 1000000.0 || l["vam"] != 90000.0 || l["adis"] != 100000000.0 || l["tsstam"] != 100090000.0 {
		t.Fatalf("%v", l)
	}
	if _, has := l["am"]; has || got.Header["tbill"] != 100090000.0 {
		t.Fatalf("%v %v", l, got.Header)
	}
	c.Lines[0].FX.VATBase = 999_999
	refuse(t, b, c, "sovat below the margin")
	c.Lines[0].FX.VATBase = 100_000_001
	refuse(t, b, c, "sovat above the whole amount")
	c.Lines[0].FX.VATBase, c.Lines[0].FX.PurchaseRateE8 = 1_000_000, 1_000_000_00000000
	refuse(t, b, c, "purchase rate must be below the sale rate")
}

func TestOtherTaxesAndLateInvoice(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "A11216", DefaultStuffID: "2710000138624"}
	o := baseInv(1)
	o.Lines[0].OtherTaxSubject, o.Lines[0].OtherTaxBP = "levy", 150
	o.Lines[0].OtherLevySubject, o.Lines[0].OtherLevyBP = "fee", 100
	got := build(t, b, o)
	l, h := got.Body[0], got.Header
	// 1,000,000: odam 15,000 (1.5%), olam 10,000 (1%), vam 90,000, tsstam 1,115,000
	if l["odam"] != 15000.0 || l["olam"] != 10000.0 || l["odt"] != "levy" || l["odr"] != 1.5 || l["olr"] != 1.0 || l["tsstam"] != 1115000.0 || h["todam"] != 25000.0 || h["tbill"] != 1115000.0 {
		t.Fatalf("%v %v", l, h)
	}
	o.Lines[0].OtherTaxSubject = ""
	refuse(t, b, o, "a rate needs a subject")
	o.Lines[0].OtherTaxSubject = "levy"

	o.RegisteredAt = o.IssuedAt.Add(48 * time.Hour)
	if h := build(t, b, o).Header; h["insr"] != 1.0 || h["indati2m"] != float64(o.RegisteredAt.UnixMilli()) {
		t.Fatalf("%v", h)
	}
	o.RegisteredAt = o.IssuedAt.Add(-time.Hour)
	refuse(t, b, o, "registration before issue")
}
