package mizan

import (
	"context"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

// The use cases below span contexts, so they live here, in the composition layer, and not inside a context.

// StartInvoicePaymentInput starts a gateway payment for the remaining amount of an invoice.
type StartInvoicePaymentInput struct {
	InvoiceID uuid.UUID
	Gateway   string // default gateway when empty
	ReturnURL string
	IdemKey   string // required; the same key returns the same payment
	Actor     string
}

// StartInvoicePayment creates a payment for what is still owed on an issued invoice.
func (m *Mizan) StartInvoicePayment(ctx context.Context, in StartInvoicePaymentInput) (payment.Payment, error) {
	inv, err := m.Invoices.Get(ctx, in.InvoiceID)
	if err != nil {
		return payment.Payment{}, err
	}
	if inv.Status != invoice.Issued && inv.Status != invoice.PartiallyPaid {
		return payment.Payment{}, apperr.New(apperr.KindConflict, "invoice_not_payable", "the invoice is "+string(inv.Status))
	}
	return m.Payments.Initiate(ctx, payment.InitiateInput{
		IdemKey: "invoice:" + inv.ID.String() + ":" + in.IdemKey, Purpose: payment.Purpose{Type: PurposeInvoice, ID: inv.ID.String()},
		PayerRef: inv.CustomerRef, Amount: inv.Remaining(), Gateway: in.Gateway, ReturnURL: in.ReturnURL, Actor: in.Actor,
		Description: "Invoice " + inv.ID.String(),
	})
}

// TopUpInput starts a wallet top-up through a gateway.
type TopUpInput struct {
	WalletID  uuid.UUID
	Amount    money.Money
	Gateway   string
	ReturnURL string
	IdemKey   string
	Actor     string
}

// StartWalletTopUp creates a payment whose success credits the wallet.
func (m *Mizan) StartWalletTopUp(ctx context.Context, in TopUpInput) (payment.Payment, error) {
	w, err := m.Wallets.Get(ctx, in.WalletID)
	if err != nil {
		return payment.Payment{}, err
	}
	if w.Status == wallet.Closed {
		return payment.Payment{}, apperr.New(apperr.KindConflict, "wallet_closed", "the wallet is closed")
	}
	if in.Amount.Currency() != w.Currency {
		return payment.Payment{}, apperr.New(apperr.KindInvalid, "currency_mismatch", "the wallet is in "+string(w.Currency))
	}
	return m.Payments.Initiate(ctx, payment.InitiateInput{
		IdemKey: "topup:" + w.ID.String() + ":" + in.IdemKey, Purpose: payment.Purpose{Type: PurposeWalletTopUp, ID: w.ID.String()},
		PayerRef: w.OwnerRef, Amount: in.Amount, Gateway: in.Gateway, ReturnURL: in.ReturnURL, Actor: in.Actor, Description: "Wallet top-up",
	})
}

// PayInvoiceFromWallet pays the remaining amount of an invoice from a wallet in one transaction: the invoice
// is settled and the wallet debited by the same journal, so they cannot disagree. A wallet that cannot cover
// the amount fails with insufficient_funds and nothing changes. Idempotent per key.
func (m *Mizan) PayInvoiceFromWallet(ctx context.Context, invoiceID, walletID uuid.UUID, key, actor string) (invoice.Invoice, error) {
	if key == "" || len(key) > 150 {
		return invoice.Invoice{}, apperr.InvalidField("idempotency_key", "required, at most 150 characters")
	}
	var out invoice.Invoice
	err := m.DB.WithTx(ctx, func(ctx context.Context) error {
		w, err := m.Wallets.Get(ctx, walletID)
		if err != nil {
			return err
		}
		if w.Status != wallet.Active {
			return apperr.New(apperr.KindConflict, "wallet_"+string(w.Status), "the wallet is "+string(w.Status))
		}
		inv, err := m.Invoices.Get(ctx, invoiceID)
		if err != nil {
			return err
		}
		if inv.Status == invoice.Paid {
			out = inv // a retry of a payment that already went through
			return nil
		}
		if _, err := m.Invoices.ApplyPayment(ctx, invoice.PayInput{InvoiceID: invoiceID, Source: "wallet", SourceRef: walletID.String() + ":" + key,
			Amount: inv.Remaining(), DebitAccount: w.AccountCode, Actor: actor}); err != nil {
			return err
		}
		out, err = m.Invoices.Get(ctx, invoiceID)
		return err
	})
	return out, err
}
