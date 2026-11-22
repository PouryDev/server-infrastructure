package mizan

import (
	"context"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
)

// Purpose types of payments created by mizan itself.
const (
	PurposeInvoice     = "invoice"
	PurposeWalletTopUp = "wallet_topup"
)

// invoiceSettlement gives payments with purpose "invoice" their meaning: the invoice is paid and the cash is booked.
type invoiceSettlement struct {
	invoices *invoice.Service
}

var _ payment.Settlement = invoiceSettlement{}

func parseID(p payment.Payment) (uuid.UUID, error) {
	id, err := uuid.Parse(p.Purpose.ID)
	if err != nil {
		return uuid.Nil, apperr.Invalid("the payment's purpose id is not a valid id")
	}
	return id, nil
}

func (s invoiceSettlement) OnPaid(ctx context.Context, p payment.Payment) (*uuid.UUID, error) {
	id, err := parseID(p)
	if err != nil {
		return nil, err
	}
	jid, err := s.invoices.ApplyPayment(ctx, invoice.PayInput{InvoiceID: id, Source: "gateway", SourceRef: p.ID.String(),
		Amount: p.Amount, DebitAccount: AccGatewayClearing, Actor: "system"})
	if err != nil {
		return nil, err
	}
	return &jid, nil
}

func (s invoiceSettlement) CanRefund(ctx context.Context, p payment.Payment, total int64) error {
	id, err := parseID(p)
	if err != nil {
		return err
	}
	inv, err := s.invoices.Get(ctx, id)
	if err != nil {
		return err
	}
	if inv.Paid.Amount() < total {
		return apperr.New(apperr.KindConflict, "refund_exceeds_paid", "the invoice has less paid than the refund")
	}
	return nil
}

// refundCounter is the account a refund pays out of: the gateway's clearing account when the gateway returned the
// money, the company's own bank account when an agent paid it card-to-card.
func refundCounter(r payment.Refund) string {
	if r.Method == payment.RefundManualCardToCard {
		return AccBank
	}
	return AccGatewayClearing
}

func (s invoiceSettlement) OnRefunded(ctx context.Context, p payment.Payment, r payment.Refund) (*uuid.UUID, error) {
	id, err := parseID(p)
	if err != nil {
		return nil, err
	}
	jid, err := s.invoices.ApplyRefund(ctx, invoice.RefundInput{InvoiceID: id, SourceRef: r.ID.String(), Amount: r.Amount, CreditAccount: refundCounter(r), Actor: "system"})
	if err != nil {
		return nil, err
	}
	return &jid, nil
}

// walletSettlement credits a wallet when its top-up payment succeeds.
type walletSettlement struct {
	wallets *wallet.Service
}

var _ payment.Settlement = walletSettlement{}

func (s walletSettlement) OnPaid(ctx context.Context, p payment.Payment) (*uuid.UUID, error) {
	id, err := parseID(p)
	if err != nil {
		return nil, err
	}
	jid, err := s.wallets.Credit(ctx, wallet.Movement{WalletID: id, Amount: p.Amount, Key: "topup:" + p.ID.String(), Counter: AccGatewayClearing,
		Description: "Wallet top-up", RefType: "payment", RefID: p.ID.String(), Actor: "system"})
	if err != nil {
		return nil, err
	}
	return &jid, nil
}

func (s walletSettlement) CanRefund(ctx context.Context, p payment.Payment, total int64) error {
	id, err := parseID(p)
	if err != nil {
		return err
	}
	bal, err := s.wallets.Balance(ctx, id)
	if err != nil {
		return err
	}
	if bal.Amount() < total {
		return apperr.New(apperr.KindConflict, "insufficient_funds", "the wallet no longer holds the amount to refund")
	}
	return nil
}

func (s walletSettlement) OnRefunded(ctx context.Context, p payment.Payment, r payment.Refund) (*uuid.UUID, error) {
	id, err := parseID(p)
	if err != nil {
		return nil, err
	}
	jid, err := s.wallets.Debit(ctx, wallet.Movement{WalletID: id, Amount: r.Amount, Key: "topup-refund:" + r.ID.String(), Counter: refundCounter(r),
		Description: "Wallet top-up refund", RefType: "payment", RefID: p.ID.String(), Actor: "system"})
	if err != nil {
		return nil, err
	}
	return &jid, nil
}
