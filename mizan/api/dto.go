package api

import (
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

type paymentDTO struct {
	ID              uuid.UUID      `json:"id"`
	Status          payment.Status `json:"status"`
	PurposeType     string         `json:"purpose_type"`
	PurposeID       string         `json:"purpose_id"`
	Amount          money.Money    `json:"amount"`
	Refunded        money.Money    `json:"refunded"`
	Gateway         string         `json:"gateway"`
	PayURL          string         `json:"pay_url,omitempty"`
	GatewayTxnRef   string         `json:"gateway_txn_ref,omitempty"`
	CardPAN         string         `json:"card_pan,omitempty"`
	ExpiresAt       time.Time      `json:"expires_at"`
	PaidAt          *time.Time     `json:"paid_at,omitempty"`
	FailureCode     string         `json:"failure_code,omitempty"`
	SettlementError string         `json:"settlement_error,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

func toPayment(p payment.Payment) paymentDTO {
	d := paymentDTO{ID: p.ID, Status: p.Status, PurposeType: p.Purpose.Type, PurposeID: p.Purpose.ID, Amount: p.Amount, Refunded: p.Refunded,
		Gateway: p.Gateway, GatewayTxnRef: p.GatewayTxnRef, CardPAN: p.CardPAN, ExpiresAt: p.ExpiresAt, PaidAt: p.PaidAt, FailureCode: p.FailureCode,
		SettlementError: p.Metadata["settlement_error"], CreatedAt: p.CreatedAt}
	if p.Status == payment.Pending {
		d.PayURL = p.PayURL
	}
	return d
}

type refundDTO struct {
	ID                    uuid.UUID            `json:"id"`
	PaymentID             uuid.UUID            `json:"payment_id"`
	Amount                money.Money          `json:"amount"`
	Reason                string               `json:"reason"`
	Status                payment.RefundStatus `json:"status"`
	Method                payment.RefundMethod `json:"method"`
	GatewayRef            string               `json:"gateway_ref,omitempty"`
	Failure               string               `json:"failure,omitempty"`
	ClaimedBy             string               `json:"claimed_by,omitempty"`
	ClaimedAt             *time.Time           `json:"claimed_at,omitempty"`
	DestinationCardMasked string               `json:"destination_card_masked,omitempty"`
	TrackingNumber        string               `json:"tracking_number,omitempty"`
	PaidAt                *time.Time           `json:"paid_at,omitempty"`
	Note                  string               `json:"note,omitempty"`
	JournalID             *uuid.UUID           `json:"journal_id,omitempty"`
	CreatedAt             time.Time            `json:"created_at"`
}

func toRefund(r payment.Refund) refundDTO {
	return refundDTO{ID: r.ID, PaymentID: r.PaymentID, Amount: r.Amount, Reason: r.Reason, Status: r.Status, Method: r.Method, GatewayRef: r.GatewayRef,
		Failure: r.Failure, ClaimedBy: r.ClaimedBy, ClaimedAt: r.ClaimedAt, DestinationCardMasked: r.DestinationCardMasked, TrackingNumber: r.TrackingNumber,
		PaidAt: r.PaidAt, Note: r.Note, JournalID: r.JournalID, CreatedAt: r.CreatedAt}
}

type itemDTO struct {
	No          int    `json:"no"`
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
	Discount    int64  `json:"discount"`
	TaxBP       int    `json:"tax_bp"`
	Net         int64  `json:"net"`
	Tax         int64  `json:"tax"`
	Total       int64  `json:"total"`
}

type invoiceDTO struct {
	ID            uuid.UUID      `json:"id"`
	Number        *int64         `json:"number"`
	Status        invoice.Status `json:"status"`
	CustomerRef   string         `json:"customer_ref"`
	Buyer         invoice.Buyer  `json:"buyer"`
	Currency      money.Currency `json:"currency"`
	Items         []itemDTO      `json:"items"`
	Subtotal      money.Money    `json:"subtotal"`
	DiscountTotal money.Money    `json:"discount_total"`
	TaxTotal      money.Money    `json:"tax_total"`
	Total         money.Money    `json:"total"`
	Paid          money.Money    `json:"paid"`
	Remaining     money.Money    `json:"remaining"`
	IssueDate     *time.Time     `json:"issue_date"`
	IssueDateJ    string         `json:"issue_date_jalali,omitempty"`
	DueDate       *time.Time     `json:"due_date"`
	Memo          string         `json:"memo"`
	CreatedAt     time.Time      `json:"created_at"`
}

func toInvoice(i invoice.Invoice) invoiceDTO {
	d := invoiceDTO{ID: i.ID, Number: i.Number, Status: i.Status, CustomerRef: i.CustomerRef, Buyer: i.Buyer, Currency: i.Currency, Subtotal: i.Subtotal,
		DiscountTotal: i.DiscountTotal, TaxTotal: i.TaxTotal, Total: i.Total, Paid: i.Paid, Remaining: i.Remaining(), IssueDate: i.IssueDate,
		DueDate: i.DueDate, Memo: i.Memo, CreatedAt: i.CreatedAt, Items: make([]itemDTO, len(i.Items))}
	if i.IssueDate != nil {
		d.IssueDateJ = jdate(*i.IssueDate)
	}
	for n, it := range i.Items {
		d.Items[n] = itemDTO{No: it.No, Description: it.Description, Quantity: it.Quantity, UnitPrice: it.UnitPrice, Discount: it.Discount,
			TaxBP: it.TaxBP, Net: it.Net, Tax: it.Tax, Total: it.Total}
	}
	return d
}

type lineDTO struct {
	No      int         `json:"no"`
	Account string      `json:"account"`
	Side    string      `json:"side"`
	Amount  money.Money `json:"amount"`
	Memo    string      `json:"memo,omitempty"`
}

type journalDTO struct {
	ID          uuid.UUID   `json:"id"`
	FiscalYear  int64       `json:"fiscal_year_id"`
	Number      int64       `json:"number"`
	Kind        ledger.Kind `json:"kind"`
	Date        string      `json:"date"`
	DateJalali  string      `json:"date_jalali"`
	Description string      `json:"description"`
	Total       money.Money `json:"total"`
	RefType     string      `json:"ref_type,omitempty"`
	RefID       string      `json:"ref_id,omitempty"`
	ReversesID  *uuid.UUID  `json:"reverses_id,omitempty"`
	CreatedBy   string      `json:"created_by,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
	Lines       []lineDTO   `json:"lines"`
}

func toJournal(j ledger.Journal) journalDTO {
	d := journalDTO{ID: j.ID, FiscalYear: j.FiscalYearID, Number: j.Number, Kind: j.Kind, Date: j.Date.Format("2006-01-02"), DateJalali: jdate(j.Date),
		Description: j.Description, Total: j.Total, RefType: j.Ref.Type, RefID: j.Ref.ID, ReversesID: j.ReversesID, CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt}
	for _, l := range j.Lines {
		d.Lines = append(d.Lines, lineDTO{No: l.No, Account: l.AccountCode, Side: l.Side.String(), Amount: l.Amount, Memo: l.Memo})
	}
	return d
}

type walletDTO struct {
	ID       uuid.UUID      `json:"id"`
	OwnerRef string         `json:"owner_ref"`
	Currency money.Currency `json:"currency"`
	Status   wallet.Status  `json:"status"`
	Balance  *money.Money   `json:"balance,omitempty"`
}

func toWallet(w wallet.Wallet, bal *money.Money) walletDTO {
	return walletDTO{ID: w.ID, OwnerRef: w.OwnerRef, Currency: w.Currency, Status: w.Status, Balance: bal}
}
