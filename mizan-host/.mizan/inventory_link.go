package mizan

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
)

// InvoiceStockLine is the stock that left for one line of a sales invoice.
type InvoiceStockLine struct {
	ItemID uuid.UUID
	Qty    int64
}

// IssueStockForInvoice issues the stock sold by an issued invoice (cost of goods sold at the item's carrying cost, referencing the invoice number) in one transaction. It is an
// OPTIONAL helper of the root package: the invoice and inventory contexts stay independent and the caller says which items and quantities left. Idempotent: every issue uses the
// key "invoice-stock:<invoice id>:<item id>", so a retry returns the same movements; the same item twice in one call is refused.
func (m *Mizan) IssueStockForInvoice(ctx context.Context, invoiceID uuid.UUID, lines []InvoiceStockLine, actor string) ([]inventory.Movement, error) {
	if len(lines) == 0 {
		return nil, apperr.InvalidField("lines", "at least one item")
	}
	inv, err := m.Invoices.Get(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if inv.Status == invoice.Draft || inv.Status == invoice.Void || inv.Number == nil {
		return nil, apperr.New(apperr.KindConflict, "invoice_not_issued", "stock is issued only for an issued invoice")
	}
	seen := map[uuid.UUID]bool{}
	for _, l := range lines {
		if seen[l.ItemID] {
			return nil, apperr.InvalidField("lines", "an item appears twice")
		}
		seen[l.ItemID] = true
	}
	var out []inventory.Movement
	err = m.DB.WithTx(ctx, func(ctx context.Context) error {
		for _, l := range lines {
			mv, err := m.Inventory.Issue(ctx, inventory.IssueInput{IdemKey: fmt.Sprintf("invoice-stock:%s:%s", invoiceID, l.ItemID), ItemID: l.ItemID, Qty: l.Qty,
				Date: *inv.IssueDate, Ref: fmt.Sprintf("invoice:%d", *inv.Number), Reason: "sale", Actor: actor})
			if err != nil {
				return err
			}
			out = append(out, mv)
		}
		return nil
	})
	return out, err
}
