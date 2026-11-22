package mizan

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/lifecycle"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
)

// SourceInvoice is the source type of e-invoices created from an invoice.
const SourceInvoice = "invoice"

// EnqueueInvoice queues an issued invoice for the tax authority. Idempotent: an invoice has at most one e-invoice.
func (m *Mizan) EnqueueInvoice(ctx context.Context, id uuid.UUID, actor string) (einvoice.Document, error) {
	return m.EnqueueInvoiceWith(ctx, id, actor, EInvoiceOptions{})
}

// EInvoiceOptions are the provider details of an e-invoice beyond a plain original sale (see einvoice.Invoice): the pattern, the subject (a correction, cancellation or return
// of an earlier e-invoice needs its RefTaxID), the settlement and the contract number. The zero value is a plain original sale.
type EInvoiceOptions struct {
	Pattern    int
	Subject    int
	RefTaxID   string
	Settlement int
	CashAmount int64
	ContractNo string
	// Details carries the remaining provider details (bill id, flight type, policy, exchange notice, payments, customs, buyer kind and passport, late registration time) and, line
	// by line (the same order as the invoice items), the extras of a line (gold wage, export or currency figures, other taxes, petroleum base). Only those fields are read.
	Details *einvoice.Invoice
}

// merge copies the provider details onto the neutral invoice.
func (o EInvoiceOptions) merge(n *einvoice.Invoice) {
	n.Pattern, n.Subject, n.RefTaxID, n.Settlement, n.CashAmount, n.ContractNo = o.Pattern, o.Subject, o.RefTaxID, o.Settlement, o.CashAmount, o.ContractNo
	d := o.Details
	if d == nil {
		return
	}
	n.BillID, n.FlightType, n.TravelAgencyCode, n.PolicyNo, n.EndorsementNo = d.BillID, d.FlightType, d.TravelAgencyCode, d.PolicyNo, d.EndorsementNo
	n.ExchangeNoticeNo, n.ExchangeNoticeDate, n.RegisteredAt, n.Payments, n.Customs = d.ExchangeNoticeNo, d.ExchangeNoticeDate, d.RegisteredAt, d.Payments, d.Customs
	n.Buyer.Kind, n.Buyer.PassportNo = d.Buyer.Kind, d.Buyer.PassportNo
	for i := range n.Lines {
		if i >= len(d.Lines) {
			break
		}
		x := d.Lines[i]
		n.Lines[i].WageFee, n.Lines[i].SellerProfit, n.Lines[i].Commission, n.Lines[i].Export, n.Lines[i].FX = x.WageFee, x.SellerProfit, x.Commission, x.Export, x.FX
		n.Lines[i].OtherTaxSubject, n.Lines[i].OtherTaxBP, n.Lines[i].OtherLevySubject, n.Lines[i].OtherLevyBP, n.Lines[i].PetroBaseE4 = x.OtherTaxSubject, x.OtherTaxBP, x.OtherLevySubject, x.OtherLevyBP, x.PetroBaseE4
	}
}

// EnqueueInvoiceWith is EnqueueInvoice with options. A correction, cancellation or return is a NEW e-invoice of the same invoice: its source id carries the subject and the
// reference, so it is idempotent per (invoice, subject, reference) and does not collide with the original.
func (m *Mizan) EnqueueInvoiceWith(ctx context.Context, id uuid.UUID, actor string, opt EInvoiceOptions) (einvoice.Document, error) {
	inv, err := m.Invoices.Get(ctx, id)
	if err != nil {
		return einvoice.Document{}, err
	}
	if inv.Status == invoice.Draft || inv.Status == invoice.Void || inv.Number == nil || inv.IssueDate == nil {
		return einvoice.Document{}, apperr.New(apperr.KindConflict, "invoice_not_issued", "only an issued invoice goes to the tax authority; this one is "+string(inv.Status))
	}
	n := neutralInvoice(inv, m.einSeller)
	opt.merge(&n)
	src := inv.ID.String()
	if opt.Subject > 1 {
		src += fmt.Sprintf(":s%d:%s", opt.Subject, opt.RefTaxID)
	}
	return m.EInvoice.Enqueue(ctx, einvoice.EnqueueInput{SourceType: SourceInvoice, SourceID: src, Invoice: n, Actor: actor})
}

// neutralInvoice maps an invoice to the vendor-neutral e-invoice content. No amount is recomputed: the invoice's own
// integer arithmetic is the single source of truth.
func neutralInvoice(inv invoice.Invoice, seller einvoice.Party) einvoice.Invoice {
	lines := make([]einvoice.Line, len(inv.Items))
	for i, it := range inv.Items {
		lines[i] = einvoice.Line{Description: it.Description, Quantity: it.Quantity, UnitPrice: it.UnitPrice, Discount: it.Discount, Net: it.Net, VATBP: it.TaxBP, VAT: it.Tax, Total: it.Total}
	}
	return einvoice.Invoice{
		Number: strconv.FormatInt(*inv.Number, 10), IssuedAt: *inv.IssueDate, Currency: inv.Currency, Seller: seller,
		Buyer: einvoice.Party{Name: inv.Buyer.Name, NationalID: inv.Buyer.NationalID, EconomicCode: inv.Buyer.EconomicCode, PostalCode: inv.Buyer.PostalCode,
			Phone: inv.Buyer.Phone, Address: inv.Buyer.Address},
		Lines: lines, Subtotal: inv.Subtotal.Amount(), Discount: inv.DiscountTotal.Amount(), VAT: inv.TaxTotal.Amount(), Total: inv.Total.Amount(), Memo: inv.Memo,
	}
}

// EInvoiceSender returns the runner that sends queued e-invoices and follows their verdicts (default: every minute).
func (m *Mizan) EInvoiceSender() lifecycle.Runner {
	return lifecycle.RunnerFunc(func(ctx context.Context) error { return m.EInvoice.Run(ctx, time.Minute) })
}

// RequeueEInvoice sends a rejected e-invoice again. For an invoice source the content is rebuilt from the invoice as it is now.
func (m *Mizan) RequeueEInvoice(ctx context.Context, id uuid.UUID, actor string) (einvoice.Document, error) {
	d, err := m.EInvoice.Get(ctx, id)
	if err != nil {
		return einvoice.Document{}, err
	}
	var corrected *einvoice.Invoice
	if d.SourceType == SourceInvoice {
		idPart, _, _ := strings.Cut(d.SourceID, ":")
		invID, err := uuid.Parse(idPart)
		if err != nil {
			return einvoice.Document{}, err
		}
		inv, err := m.Invoices.Get(ctx, invID)
		if err != nil {
			return einvoice.Document{}, err
		}
		if inv.Number != nil && inv.IssueDate != nil {
			n := neutralInvoice(inv, m.einSeller)
			o := d.Invoice // keep the provider details the document was queued with
			EInvoiceOptions{Pattern: o.Pattern, Subject: o.Subject, RefTaxID: o.RefTaxID, Settlement: o.Settlement, CashAmount: o.CashAmount, ContractNo: o.ContractNo, Details: &o}.merge(&n)
			corrected = &n
		}
	}
	return m.EInvoice.Requeue(ctx, id, corrected, actor)
}
