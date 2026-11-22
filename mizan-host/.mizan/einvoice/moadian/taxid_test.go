package moadian

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

// The three worked examples of RC_DCPS.SN_V1.3: memory id DEF5GH, 1399/04/30 = 2020-07-20 = day 18463 = hex 0481F.
func TestTaxIDOfficialExamples(t *testing.T) {
	day := time.Date(2020, 7, 20, 10, 0, 0, 0, time.UTC)
	if DaysSinceEpoch(day) != 18463 {
		t.Fatalf("days %d", DaysSinceEpoch(day))
	}
	for _, c := range []struct {
		serial  int64
		control string
		digit   int
		taxid   string
	}{
		{12, "68697057172018463000000000012", 2, "DEF5GH0481F000000000C2"},
		{8173, "68697057172018463000000008173", 8, "DEF5GH0481F0000001FED8"},
		{2572613409, "68697057172018463002572613409", 1, "DEF5GH0481F009956F7211"},
	} {
		if d, err := VerhoeffDigit(c.control); err != nil || d != c.digit {
			t.Fatalf("control %s: %d %v", c.control, d, err)
		}
		got, err := TaxID("DEF5GH", day, c.serial)
		if err != nil || got != c.taxid {
			t.Fatalf("serial %d: %q %v (want %s)", c.serial, got, err, c.taxid)
		}
		if !ValidTaxID(got) {
			t.Fatalf("%s must validate", got)
		}
		if bad := got[:21] + string(rune('0'+(c.digit+1)%10)); ValidTaxID(bad) {
			t.Fatalf("%s must not validate", bad)
		}
	}
	for _, id := range []string{"", "DEF5G", "DEF5GV", "0EF5GH", "DEF5GHI"} {
		if _, err := TaxID(id, day, 1); err == nil {
			t.Fatalf("memory id %q must be refused", id)
		}
	}
	if _, err := TaxID("DEF5GH", day, -1); err == nil {
		t.Fatal("negative serial")
	}
}

func TestInvoiceBuilder(t *testing.T) {
	b := &InvoiceBuilder{MemoryID: "DEF5GH", DefaultStuffID: "1234567890123", UnitCode: "1627"}
	inv := einvoice.Invoice{Number: "12", IssuedAt: time.Date(2020, 7, 20, 10, 0, 0, 0, time.UTC),
		Seller: einvoice.Party{EconomicCode: "10101010101"}, Buyer: einvoice.Party{NationalID: "0012345678", PostalCode: "1234567890"},
		Lines: []einvoice.Line{{Description: "x", Quantity: 2, UnitPrice: 10_000, Discount: 500, VATBP: 900}}}
	raw, err := b.Build(inv, "trace")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Header map[string]any   `json:"header"`
		Body   []map[string]any `json:"body"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	// The worked example of the technical paper: am 2, fee 10000, dis 500 -> adis 19500, vam 1755 at 9%, tsstam 21255.
	h, l := got.Header, got.Body[0]
	if l["prdis"] != 20000.0 || l["adis"] != 19500.0 || l["vam"] != 1755.0 || l["tsstam"] != 21255.0 || l["vra"] != 9.0 {
		t.Fatalf("body: %v", l)
	}
	if h["tprdis"] != 20000.0 || h["tdis"] != 500.0 || h["tadis"] != 19500.0 || h["tvam"] != 1755.0 || h["tbill"] != 21255.0 || h["setm"] != 1.0 || h["todam"] != 0.0 {
		t.Fatalf("header: %v", h)
	}
	if h["taxid"] != "DEF5GH0481F000000000C2" || h["inno"] != "000000000C" || h["indatim"] != float64(inv.IssuedAt.UnixMilli()) {
		t.Fatalf("ids: %v", h)
	}
	// Truncation, not rounding: 99,999 x 10% = 9,999.9 -> 9,999.
	inv.Lines = []einvoice.Line{{Description: "y", Quantity: 1, UnitPrice: 99_999, VATBP: 1000}}
	raw, _ = b.Build(inv, "t")
	if !strings.Contains(string(raw), `"vam":9999,`) {
		t.Fatalf("truncation: %s", raw)
	}
	// Fail closed.
	for name, mut := range map[string]func(*einvoice.Invoice, *InvoiceBuilder){
		"no stuff id":       func(_ *einvoice.Invoice, b *InvoiceBuilder) { b.DefaultStuffID = "" },
		"bad number":        func(i *einvoice.Invoice, _ *InvoiceBuilder) { i.Number = "INV-1" },
		"bad seller code":   func(i *einvoice.Invoice, _ *InvoiceBuilder) { i.Seller.EconomicCode = "123" },
		"discount too high": func(i *einvoice.Invoice, _ *InvoiceBuilder) { i.Lines[0].Discount = 1_000_000 },
		"bad memory id":     func(_ *einvoice.Invoice, b *InvoiceBuilder) { b.MemoryID = "000000" },
	} {
		bb, ii := *b, inv
		ii.Lines = append([]einvoice.Line(nil), inv.Lines...)
		mut(&ii, &bb)
		if _, err := bb.Build(ii, "t"); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
}

func TestGenerateKeyAndCSR(t *testing.T) {
	keyPEM, csrPEM, err := GenerateKeyAndCSR(CSRSubject{CommonName: "Test Co", Organization: "Test", SerialNumber: "1234567890"}, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewKeySigner([]byte("not a cert"), keyPEM); err == nil {
		t.Fatal("garbage cert must fail") // the key itself is PKCS#8, readable by the signer once a certificate exists
	}
	blk, _ := pem.Decode(csrPEM)
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != "Test Co" || csr.SignatureAlgorithm != x509.SHA256WithRSA {
		t.Fatalf("csr: %+v %v", csr, err)
	}
	if _, _, err := GenerateKeyAndCSR(CSRSubject{CommonName: "x"}, 1024); err == nil {
		t.Fatal("a 1024-bit key must be refused")
	}
	if _, _, err := GenerateKeyAndCSR(CSRSubject{}, 2048); err == nil {
		t.Fatal("a missing common name must be refused")
	}
}
