package moadian

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

// InvoiceBuilder is the PayloadBuilder: it turns the neutral invoice into the invoice JSON of the invoice-issuing instructions (RC_IITP.IS V7.9, Tir 1405).
//
// SOURCE: the official paper RC_IITP.IS V7.9 (Tir 1405, 116 pages), read as text from a copy hosted on maliyaar.ir (the official hosts intamedia.ir and tp.tax.gov.ir were
// unreachable); its field dictionary, arithmetic rules and per-pattern requirements are applied. Rules: lowercase keys; whole rials; every derived amount TRUNCATED; prdis = am x fee;
// adis = prdis - dis; vam = adis x vra / 100; tsstam = adis + vam + odam + olam; header totals are the sums of the lines; tbill = sum of tsstam.
// BUILT patterns: 1 sale, 3 gold/jewellery/platinum (tcpbs = consfee + bros + spro; adis = prdis + tcpbs - dis; vam = tcpbs x 10% + prdis x vra%), 4 contracting (crn), 7 export (customs and
// foreign-currency figures, VAT 0, tbill = torv + tvam + todam); subjects 1-4 (irtaxid required for 2-4, return not allowed for export); settlement 1, 2 and 3 (cap and insp, insp = tbill -
// todam - tvam - cap, both positive); the payments array. Everything else (currency sale 2, utility 5, airline 6, waybill 8, petroleum 9, commodity exchange 11, insurance 13, chain 14,
// late Art. 9 invoices insr/indati2m, other taxes and levies odam/olam, cop/vop shares) FAILS CLOSED or is omitted (optional in the paper). The paper has no numeric worked example for
// the invoice; the technical paper RC_TICS.IS_v1.3 has one (a pattern-1 credit sale, a test) and the other vectors in the tests are computed from the paper's formulas.
// A correction or return is the invoice WITHOUT the cancelled/returned items or with the changed quantities, positive amounts: the paper defines no negative amounts. The cancel (ins 3)
// takes its body from the reference on the server, but this builder still emits the lines it is given (supply a copy of the reference).
// The goods or service id (sstid, 13 digits registered at stuffid.tax.gov.ir) is not in the neutral invoice: it comes from Config (a function per line, or one default).
type InvoiceBuilder struct {
	// MemoryID is the fiscal-memory unique id the invoices are issued under (the same as Config.ClientID for a taxpayer sending its own invoices).
	MemoryID string
	// StuffID returns the goods or service id of a line; when nil DefaultStuffID is used. A 13-digit id is required.
	StuffID func(line einvoice.Line) string
	// DefaultStuffID is used for every line when StuffID is nil.
	DefaultStuffID string
	// UnitCode is the unit-of-measure code (mu) of the lines, from the list RC_UMGS.ST; empty leaves it out (optional). 1627 is "عدد" in the secondary sources read: NOT verified.
	UnitCode string
	// Credit sends the invoices as credit sales (setm 2) instead of cash (setm 1).
	Credit bool
	// Serial turns the invoice number into the memory's serial: default is the number parsed as a non-negative integer (a non-numeric number fails the build).
	Serial func(number string) (int64, error)
}

var _ PayloadBuilder = (*InvoiceBuilder)(nil)

var digitsRE = regexp.MustCompile(`^[0-9]+$`)

type header struct {
	TaxID    string      `json:"taxid"`
	Indatim  int64       `json:"indatim"`
	Inty     int         `json:"inty"`
	Inno     string      `json:"inno"`
	Irtaxid  string      `json:"irtaxid,omitempty"`
	Inp      int         `json:"inp"`
	Ins      int         `json:"ins"`
	Tins     string      `json:"tins"`
	Tob      *int        `json:"tob,omitempty"`
	Bid      string      `json:"bid,omitempty"`
	Tinb     string      `json:"tinb,omitempty"`
	Bpc      string      `json:"bpc,omitempty"`
	Scln     string      `json:"scln,omitempty"`
	Scc      string      `json:"scc,omitempty"`
	Cdcn     string      `json:"cdcn,omitempty"`
	Cdcd     *int64      `json:"cdcd,omitempty"`
	Crn      string      `json:"crn,omitempty"`
	Billid   string      `json:"billid,omitempty"`
	Ft       int         `json:"ft,omitempty"`
	Bpn      string      `json:"bpn,omitempty"`
	Tinc     string      `json:"tinc,omitempty"`
	Asn      string      `json:"asn,omitempty"`
	Asd      *int64      `json:"asd,omitempty"`
	In       string      `json:"in,omitempty"`
	An       string      `json:"an,omitempty"`
	Insr     int         `json:"insr,omitempty"`
	Indati2m *int64      `json:"indati2m,omitempty"`
	Tprdis   *int64      `json:"tprdis,omitempty"`
	Tdis     *int64      `json:"tdis,omitempty"`
	Tadis    *int64      `json:"tadis,omitempty"`
	Tvam     int64       `json:"tvam"`
	Todam    int64       `json:"todam"`
	Tbill    int64       `json:"tbill"`
	Torv     *int64      `json:"torv,omitempty"`
	Tocv     json.Number `json:"tocv,omitempty"`
	Setm     int         `json:"setm"`
	Cap      *int64      `json:"cap,omitempty"`
	Insp     *int64      `json:"insp,omitempty"`
}

type bodyItem struct {
	Sstid   string      `json:"sstid"`
	Sstt    string      `json:"sstt,omitempty"`
	Am      *int64      `json:"am,omitempty"`
	Mu      string      `json:"mu,omitempty"`
	Fee     *int64      `json:"fee,omitempty"`
	Cfee    json.Number `json:"cfee,omitempty"`
	Cut     string      `json:"cut,omitempty"`
	Cpr     json.Number `json:"cpr,omitempty"`
	Sovat   *int64      `json:"sovat,omitempty"`
	Vba     json.Number `json:"vba,omitempty"`
	Exr     json.Number `json:"exr,omitempty"`
	Ssrv    *int64      `json:"ssrv,omitempty"`
	Sscv    json.Number `json:"sscv,omitempty"`
	Prdis   *int64      `json:"prdis,omitempty"`
	Dis     *int64      `json:"dis,omitempty"`
	Adis    *int64      `json:"adis,omitempty"`
	Vra     float64     `json:"vra"`
	Vam     int64       `json:"vam"`
	Odt     string      `json:"odt,omitempty"`
	Odr     *float64    `json:"odr,omitempty"`
	Odam    int64       `json:"odam"`
	Olt     string      `json:"olt,omitempty"`
	Olr     *float64    `json:"olr,omitempty"`
	Olam    int64       `json:"olam"`
	Consfee *int64      `json:"consfee,omitempty"`
	Spro    *int64      `json:"spro,omitempty"`
	Bros    *int64      `json:"bros,omitempty"`
	Tcpbs   *int64      `json:"tcpbs,omitempty"`
	Tsstam  int64       `json:"tsstam"`
}

type paymentJSON struct {
	Iinn string `json:"iinn,omitempty"`
	Acn  string `json:"acn,omitempty"`
	Trmn string `json:"trmn,omitempty"`
	Pmt  int    `json:"pmt"`
	Trn  string `json:"trn,omitempty"`
	Pcn  string `json:"pcn,omitempty"`
	Pid  string `json:"pid,omitempty"`
	Pdt  int64  `json:"pdt"`
	Pv   int64  `json:"pv"`
}

type invoiceJSON struct {
	Header   header        `json:"header"`
	Body     []bodyItem    `json:"body"`
	Payments []paymentJSON `json:"payments,omitempty"`
}

func i64(v int64) *int64 { return &v }

// scaled formats an integer holding value x 10^-scale as a plain decimal JSON number without float arithmetic.
func scaled(v int64, scale int) json.Number {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatInt(v, 10)
	for len(s) <= scale {
		s = "0" + s
	}
	whole, frac := s[:len(s)-scale], strings.TrimRight(s[len(s)-scale:], "0")
	if frac != "" {
		whole += "." + frac
	}
	if neg {
		whole = "-" + whole
	}
	return json.Number(whole)
}

// Build returns the invoice JSON. It fails closed: a missing id, a non-numeric number, an unsupported pattern or an amount that breaks the arithmetic rules is an error, never a guess.
func (b *InvoiceBuilder) Build(inv einvoice.Invoice, _ string) ([]byte, error) {
	serialOf := b.Serial
	if serialOf == nil {
		serialOf = func(n string) (int64, error) {
			if !digitsRE.MatchString(n) {
				return 0, fmt.Errorf("the invoice number %q is not numeric: set InvoiceBuilder.Serial", n)
			}
			return strconv.ParseInt(n, 10, 64)
		}
	}
	serial, err := serialOf(inv.Number)
	if err != nil {
		return nil, wrap("serial", err)
	}
	taxid, err := TaxID(b.MemoryID, inv.IssuedAt, serial)
	if err != nil {
		return nil, err
	}
	pattern, subject := inv.Pattern, inv.Subject
	if pattern == 0 {
		pattern = 1
	}
	if subject == 0 {
		subject = 1
	}
	switch pattern {
	case 1, 2, 3, 4, 5, 6, 7, 9, 11, 13, 14:
	default:
		return nil, fmt.Errorf("moadian: invoice pattern %d is not built (supported: 1 sale, 2 currency sale, 3 gold, 4 contracting, 5 utility bill, 6 airline, 7 export, 9 petroleum, 11 commodity exchange, 13 insurance, 14 chain; 8 waybill needs the transport code tables the paper lists separately)", pattern)
	}
	if subject < 1 || subject > 4 || ((pattern == 7 || pattern == 11) && subject == 4) {
		return nil, errors.New("moadian: the subject must be 1 original, 2 correction, 3 cancel or 4 return (a return is not allowed for an export or a commodity-exchange invoice)")
	}
	irtaxid := strings.TrimSpace(inv.RefTaxID)
	if subject == 1 && irtaxid != "" {
		return nil, errors.New("moadian: an original invoice has no reference tax id")
	}
	if subject != 1 {
		if !ValidTaxID(irtaxid) {
			return nil, errors.New("moadian: a correction, cancellation or return needs the valid 22-character tax id of the invoice it refers to (irtaxid)")
		}
	}
	tins := strings.TrimSpace(inv.Seller.EconomicCode)
	if !digitsRE.MatchString(tins) || (len(tins) != 11 && len(tins) != 14) {
		return nil, errors.New("moadian: the seller's economic code (tins) must be 11 or 14 digits")
	}
	if len(inv.Lines) == 0 {
		return nil, errors.New("moadian: an invoice needs at least one line")
	}
	h := header{TaxID: taxid, Indatim: inv.IssuedAt.UnixMilli(), Inty: 2, Inno: taxid[11:21], Irtaxid: irtaxid, Inp: pattern, Ins: subject, Tins: tins, Setm: 1}
	if pattern != 1 && pattern != 3 && pattern != 9 && pattern != 13 {
		h.Inty = 1 // type 2 invoices allow only patterns 1, 3, 9, 13: the others are type 1
	}
	if b.Credit {
		h.Setm = 2
	}
	if inv.Settlement != 0 {
		if inv.Settlement < 1 || inv.Settlement > 3 {
			return nil, errors.New("moadian: the settlement must be 1 cash, 2 credit or 3 mixed")
		}
		h.Setm = inv.Settlement
	}
	// Buyer: a buyer with an economic code is type 1 (with the buyer's identity), otherwise type 2 (secondary reading of the type rules: not verified against the server).
	if tinb := strings.TrimSpace(inv.Buyer.EconomicCode); tinb != "" {
		if !digitsRE.MatchString(tinb) || (len(tinb) != 11 && len(tinb) != 14) {
			return nil, errors.New("moadian: the buyer's economic code (tinb) must be 11 or 14 digits")
		}
		h.Inty, h.Tinb = 1, tinb
	}
	kind := inv.Buyer.Kind
	if bid := strings.TrimSpace(inv.Buyer.NationalID); bid != "" {
		if !digitsRE.MatchString(bid) {
			return nil, errors.New("moadian: the buyer's national id must be digits")
		}
		if kind == 0 {
			kind = 1 // natural person
			if len(bid) == 11 {
				kind = 2 // legal entity
			}
		}
		h.Bid = bid
		if h.Inty == 2 && len(bid) == 10 && inv.Buyer.PostalCode != "" {
			h.Inty = 1
		}
	}
	if kind != 0 {
		if kind < 1 || kind > 4 {
			return nil, errors.New("moadian: the buyer kind must be 1 natural, 2 legal, 3 civil partnership or 4 non-Iranian")
		}
		h.Tob = &kind
	}
	if pc := strings.TrimSpace(inv.Buyer.PostalCode); pc != "" {
		if !digitsRE.MatchString(pc) || len(pc) != 10 {
			return nil, errors.New("moadian: the buyer's postal code must be 10 digits")
		}
		h.Bpc = pc
	}
	if h.Inty == 1 && h.Tob == nil && pattern != 7 {
		return nil, errors.New("moadian: a type 1 invoice needs the buyer's person type (tob): set Buyer.Kind or give the buyer's national id")
	}
	switch pattern {
	case 4:
		crn := strings.TrimSpace(inv.ContractNo)
		if !digitsRE.MatchString(crn) || len(crn) > 12 {
			return nil, errors.New("moadian: a contracting invoice needs the contract number (crn): at most 12 digits, as registered in the taxpayer portal")
		}
		h.Crn = crn
	case 5:
		bill := strings.TrimSpace(inv.BillID)
		if !digitsRE.MatchString(bill) || len(bill) > 19 {
			return nil, errors.New("moadian: a utility-bill invoice needs the bill id (billid): at most 19 digits")
		}
		h.Billid = bill
	case 6:
		if inv.FlightType != 1 && inv.FlightType != 2 {
			return nil, errors.New("moadian: an airline invoice needs the flight type (ft): 1 domestic, 2 international")
		}
		h.Ft = inv.FlightType
		if pp := strings.TrimSpace(inv.Buyer.PassportNo); pp != "" {
			if len(pp) > 9 {
				return nil, errors.New("moadian: the passport number (bpn) is at most 9 characters")
			}
			h.Bpn = pp
		}
		if h.Bid == "" && h.Bpn == "" {
			return nil, errors.New("moadian: an airline invoice needs the passenger's national id or passport number")
		}
		if tinc := strings.TrimSpace(inv.TravelAgencyCode); tinc != "" {
			if !digitsRE.MatchString(tinc) || (len(tinc) != 11 && len(tinc) != 14) {
				return nil, errors.New("moadian: the travel agency's economic code (tinc) must be 11 or 14 digits")
			}
			h.Tinc = tinc
		}
	case 2:
		if pp := strings.TrimSpace(inv.Buyer.PassportNo); pp != "" {
			if len(pp) > 9 {
				return nil, errors.New("moadian: the passport number (bpn) is at most 9 characters")
			}
			h.Bpn = pp
		}
		if h.Bid == "" && h.Tinb == "" && h.Bpn == "" {
			return nil, errors.New("moadian: a currency sale needs the buyer's national id, economic code or passport number")
		}
	case 11:
		if strings.TrimSpace(inv.ExchangeNoticeNo) == "" || inv.ExchangeNoticeDate.IsZero() || b.UnitCode == "" || len(inv.Lines) != 1 {
			return nil, errors.New("moadian: a commodity-exchange invoice needs one line, the unit code, and the sale notice number and date (asn, asd)")
		}
		d := inv.ExchangeNoticeDate.Unix() / 86400
		h.Asn, h.Asd = strings.TrimSpace(inv.ExchangeNoticeNo), &d
		if inv.IssuedAt.Before(inv.ExchangeNoticeDate) {
			return nil, errors.New("moadian: a commodity-exchange invoice must not be dated before its sale notice")
		}
	case 13:
		pol := strings.TrimSpace(inv.PolicyNo)
		if !digitsRE.MatchString(pol) || len(pol) < 9 || len(pol) > 12 {
			return nil, errors.New("moadian: an insurance invoice needs the policy id (in): 9 to 12 digits")
		}
		h.In = pol
		if an := strings.TrimSpace(inv.EndorsementNo); an != "" {
			if !digitsRE.MatchString(an) || len(an) < 9 || len(an) > 12 {
				return nil, errors.New("moadian: the endorsement id (an) is 9 to 12 digits")
			}
			h.An = an
		}
	case 14:
		if h.Tinb == "" {
			return nil, errors.New("moadian: a chain-sale invoice needs the buyer's economic code (tinb)")
		}
	case 7:
		if c := inv.Customs; c != nil {
			if c.CottageNo != "" {
				if len(c.CottageNo) > 14 {
					return nil, errors.New("moadian: the customs cottage number is at most 14 characters")
				}
				h.Cdcn = c.CottageNo
			}
			if !c.CottageDate.IsZero() {
				d := c.CottageDate.Unix() / 86400
				h.Cdcd = &d
			}
			if c.OfficeCode != "" {
				if !digitsRE.MatchString(c.OfficeCode) || len(c.OfficeCode) != 5 {
					return nil, errors.New("moadian: the customs office code (scc) is 5 digits")
				}
				h.Scc = c.OfficeCode
			}
			if c.LicenseNo != "" {
				if !digitsRE.MatchString(c.LicenseNo) || len(c.LicenseNo) > 14 {
					return nil, errors.New("moadian: the customs licence number (scln) is at most 14 digits")
				}
				h.Scln = c.LicenseNo
			}
		}
	}

	body := make([]bodyItem, 0, len(inv.Lines))
	var tprdis, tdis, tadis, tvam, tbill, torv, tocv, todam int64
	for i, l := range inv.Lines {
		sstid := b.DefaultStuffID
		if b.StuffID != nil {
			sstid = b.StuffID(l)
		}
		if len(sstid) != 13 || !digitsRE.MatchString(sstid) {
			return nil, fmt.Errorf("moadian: line %d has no 13-digit goods/service id (sstid): register it at stuffid.tax.gov.ir and configure it", i+1)
		}
		if l.Quantity <= 0 {
			return nil, fmt.Errorf("moadian: line %d needs a positive quantity", i+1)
		}
		qty := l.Quantity
		it := bodyItem{Sstid: sstid, Sstt: l.Description, Am: &qty, Mu: b.UnitCode, Vra: float64(l.VATBP) / 100}
		if (pattern == 7 || pattern == 11 || pattern == 14) && l.VATBP != 0 {
			return nil, fmt.Errorf("moadian: line %d: the VAT rate of this pattern is 0", i+1)
		}
		if pattern == 6 && l.Quantity != 1 {
			return nil, fmt.Errorf("moadian: airline line %d: the quantity must be 1", i+1)
		}
		odBase := func(base int64) (odam, olam int64, err error) {
			if (l.OtherTaxBP < 0 || l.OtherLevyBP < 0) || (l.OtherTaxBP > 0 && strings.TrimSpace(l.OtherTaxSubject) == "") || (l.OtherLevyBP > 0 && strings.TrimSpace(l.OtherLevySubject) == "") {
				return 0, 0, fmt.Errorf("moadian: line %d: another tax or levy needs a subject and a non-negative rate", i+1)
			}
			if l.OtherTaxBP > 0 {
				it.Odt, odam = l.OtherTaxSubject, base*int64(l.OtherTaxBP)/10000
				r := float64(l.OtherTaxBP) / 100
				it.Odr = &r
			}
			if l.OtherLevyBP > 0 {
				it.Olt, olam = l.OtherLevySubject, base*int64(l.OtherLevyBP)/10000
				r := float64(l.OtherLevyBP) / 100
				it.Olr = &r
			}
			it.Odam, it.Olam = odam, olam
			return odam, olam, nil
		}
		if pattern == 7 {
			e := l.Export
			if e == nil || len(e.Currency) != 3 || e.ForeignValueE4 <= 0 || e.RialValue <= 0 || e.RateE8 <= 0 {
				return nil, fmt.Errorf("moadian: export line %d needs the customs currency (ISO 4217), foreign value, rial value and rate", i+1)
			}
			if l.VATBP != 0 {
				return nil, fmt.Errorf("moadian: export line %d: the VAT rate of an export is 0", i+1)
			}
			it.Cut, it.Exr, it.Ssrv, it.Sscv = e.Currency, scaled(e.RateE8, 8), i64(e.RialValue), scaled(e.ForeignValueE4, 4)
			od, ol, err := odBase(e.RialValue)
			if err != nil {
				return nil, err
			}
			it.Tsstam = e.RialValue + od + ol
			torv += e.RialValue
			tocv += e.ForeignValueE4
			todam += od + ol
			tbill += it.Tsstam
			body = append(body, it)
			continue
		}
		if pattern == 11 {
			if l.UnitPrice <= 0 {
				return nil, fmt.Errorf("moadian: line %d needs a positive unit price", i+1)
			}
			adis := l.Quantity * l.UnitPrice // no prdis, dis or fee on a commodity-exchange invoice
			it.Adis, it.Tsstam = i64(adis), adis
			body = append(body, it)
			tadis += adis
			tbill += adis
			continue
		}
		var prdis, adis, vam, odBaseAmount int64
		if pattern == 2 {
			f := l.FX
			if f == nil || len(f.Currency) != 3 || f.AmountE4 <= 0 || f.RateE8 <= 0 || f.PurchaseRateE8 <= 0 || f.PurchaseRateE8 >= f.RateE8 {
				return nil, fmt.Errorf("moadian: currency line %d needs the currency (ISO 4217), the amount, the sale rate and a purchase rate below it", i+1)
			}
			prdis = mulDiv(f.AmountE4, f.RateE8, 1_000_000_000_000) // cfee x exr, truncated
			margin := mulDiv(f.AmountE4, f.RateE8-f.PurchaseRateE8, 1_000_000_000_000)
			if f.VATBase < margin || f.VATBase > prdis {
				return nil, fmt.Errorf("moadian: currency line %d: the VAT base (sovat) must lie between cfee x (exr - cpr) and cfee x exr", i+1)
			}
			if l.Discount < 0 || l.Discount > prdis {
				return nil, fmt.Errorf("moadian: line %d: the discount must be between 0 and the line amount", i+1)
			}
			it.Am, it.Mu = nil, ""
			it.Cfee, it.Cut, it.Exr, it.Cpr, it.Sovat = scaled(f.AmountE4, 4), f.Currency, scaled(f.RateE8, 8), scaled(f.PurchaseRateE8, 8), i64(f.VATBase)
			adis = prdis - l.Discount
			vam = f.VATBase * int64(l.VATBP) / 10000
			odBaseAmount = adis
		} else {
			if l.UnitPrice <= 0 {
				return nil, fmt.Errorf("moadian: line %d needs a positive unit price", i+1)
			}
			prdis = l.Quantity * l.UnitPrice
			if l.Discount < 0 || l.Discount > prdis {
				return nil, fmt.Errorf("moadian: line %d: the discount must be between 0 and the line amount", i+1)
			}
			it.Fee = i64(l.UnitPrice)
			switch pattern {
			case 3:
				tcpbs := l.WageFee + l.SellerProfit + l.Commission
				if l.WageFee < 0 || l.SellerProfit < 0 || l.Commission < 0 || tcpbs <= 0 || l.WageFee >= prdis {
					return nil, fmt.Errorf("moadian: gold line %d needs the wage, profit and commission (not negative, the wage below the line amount, the sum positive)", i+1)
				}
				adis = prdis + tcpbs - l.Discount
				vam = (tcpbs*1000 + prdis*int64(l.VATBP)) / 10000 // tcpbs x 10% + prdis x vra%, truncated once
				it.Consfee, it.Spro, it.Bros, it.Tcpbs = i64(l.WageFee), i64(l.SellerProfit), i64(l.Commission), i64(tcpbs)
				odBaseAmount = tcpbs // the paper: odam = tcpbs x odr / 100
			case 9:
				adis = prdis - l.Discount
				if l.PetroBaseE4 > 0 {
					vam = mulDiv(l.Quantity, l.PetroBaseE4, 10000) // am x vba
					it.Vba = scaled(l.PetroBaseE4, 4)
				} else {
					vam = adis * int64(l.VATBP) / 10000
				}
				odBaseAmount = adis
			default:
				adis = prdis - l.Discount
				vam = adis * int64(l.VATBP) / 10000 // truncated: adis x vra/100 with vra = bp/100
				odBaseAmount = adis
			}
		}
		it.Prdis, it.Dis = i64(prdis), i64(l.Discount)
		od, ol, err := odBase(odBaseAmount)
		if err != nil {
			return nil, err
		}
		it.Adis, it.Vam, it.Tsstam = i64(adis), vam, adis+vam+od+ol
		body = append(body, it)
		tprdis += prdis
		tdis += l.Discount
		tadis += adis
		tvam += vam
		todam += od + ol
		tbill += it.Tsstam
	}
	h.Tvam, h.Todam, h.Tbill = tvam, todam, tbill
	switch pattern {
	case 7:
		h.Torv = i64(torv)
		h.Tocv = scaled(tocv, 4)
		h.Tbill = torv + tvam + todam
	case 11:
		h.Tadis = i64(tadis)
	default:
		h.Tprdis, h.Tdis, h.Tadis = i64(tprdis), i64(tdis), i64(tadis)
	}
	if !inv.RegisteredAt.IsZero() { // a late invoice (Art. 9): registration time not before the issue time
		if inv.RegisteredAt.Before(inv.IssuedAt) {
			return nil, errors.New("moadian: the registration time of a late invoice must not be before its issue time")
		}
		ms := inv.RegisteredAt.UnixMilli()
		h.Insr, h.Indati2m = 1, &ms
	}
	if h.Setm == 3 {
		if inv.CashAmount <= 0 {
			return nil, errors.New("moadian: a mixed settlement needs the cash amount (cap)")
		}
		insp := h.Tbill - h.Todam - h.Tvam - inv.CashAmount
		if insp <= 0 || inv.CashAmount >= h.Tbill {
			return nil, errors.New("moadian: cap and insp must both be positive with cap + insp = tbill - tvam - todam")
		}
		h.Cap, h.Insp = i64(inv.CashAmount), i64(insp)
	}
	out := invoiceJSON{Header: h, Body: body}
	for i, p := range inv.Payments {
		if p.Method < 1 || p.Method > 8 || p.Amount < 0 || p.Amount > h.Tbill || (h.Setm == 2 && p.Amount != 0) || p.PaidAt.IsZero() {
			return nil, fmt.Errorf("moadian: payment %d: a method 1-8, a date, and an amount between 0 and tbill (0 for a credit sale) are required", i+1)
		}
		for _, f := range []struct{ v, name string }{{p.CardNo, "card"}, {p.SwitchNo, "switch"}, {p.MerchantNo, "merchant"}, {p.TerminalNo, "terminal"}, {p.TraceNo, "trace"}, {p.PayerID, "payer id"}} {
			if f.v != "" && !digitsRE.MatchString(f.v) {
				return nil, fmt.Errorf("moadian: payment %d: the %s number must be digits", i+1, f.name)
			}
		}
		out.Payments = append(out.Payments, paymentJSON{Iinn: p.SwitchNo, Acn: p.MerchantNo, Trmn: p.TerminalNo, Pmt: p.Method, Trn: p.TraceNo, Pcn: p.CardNo, Pid: p.PayerID, Pdt: p.PaidAt.UnixMilli(), Pv: p.Amount})
	}
	return json.Marshal(out)
}

// mulDiv is floor(a*b/c) without overflowing int64 intermediates.
func mulDiv(a, b, c int64) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return n.Div(n, big.NewInt(c)).Int64()
}
