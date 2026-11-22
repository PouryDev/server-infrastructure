// Package mizan is the financial module: double-entry accounting (ledger), payments through gateways, invoices,
// wallets, reconciliation, a transactional outbox and an audit log. This package only wires the contexts
// together; each of them lives in its own package and knows nothing about the others (see
// architecture_test.go). Use New once at start-up.
//
//	m, err := mizan.New(ctx, db, mizan.Config{...}, mizan.WithGateway(zarinpal))
//	api.Mount(router, m, api.Options{...})                       // HTTP API, package mizan/api
//	group.Go("outbox", m.OutboxRelay(publisher))                 // events to RabbitMQ
//	group.Go("payments", m.PaymentSweeper())                     // recovers lost callbacks, expires old payments
//	group.Go("einvoice", m.EInvoiceSender())                     // sends queued e-invoices, follows the authority's verdicts
package mizan

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/lifecycle"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation/pgappropriation"
	"github.com/PouryDev/server-infrastructure/mizan/approval"
	"github.com/PouryDev/server-infrastructure/mizan/approval/pgapproval"
	"github.com/PouryDev/server-infrastructure/mizan/audit"
	"github.com/PouryDev/server-infrastructure/mizan/coupon"
	"github.com/PouryDev/server-infrastructure/mizan/coupon/pgcoupon"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice/pgeinvoice"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/fixedasset/pgfixedasset"
	"github.com/PouryDev/server-infrastructure/mizan/idempotency"
	"github.com/PouryDev/server-infrastructure/mizan/incometax"
	"github.com/PouryDev/server-infrastructure/mizan/incometax/pgincometax"
	"github.com/PouryDev/server-infrastructure/mizan/inventory"
	"github.com/PouryDev/server-infrastructure/mizan/inventory/pginventory"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/invoice/pginvoice"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/ledger/pgledger"
	"github.com/PouryDev/server-infrastructure/mizan/migrations"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/outbox"
	"github.com/PouryDev/server-infrastructure/mizan/payment"
	"github.com/PouryDev/server-infrastructure/mizan/payment/pgpayment"
	"github.com/PouryDev/server-infrastructure/mizan/payout"
	"github.com/PouryDev/server-infrastructure/mizan/payout/pgpayout"
	"github.com/PouryDev/server-infrastructure/mizan/payroll"
	"github.com/PouryDev/server-infrastructure/mizan/payroll/pgpayroll"
	"github.com/PouryDev/server-infrastructure/mizan/reconcile"
	"github.com/PouryDev/server-infrastructure/mizan/reconcile/pgreconcile"
	"github.com/PouryDev/server-infrastructure/mizan/subscription"
	"github.com/PouryDev/server-infrastructure/mizan/subscription/pgsubscription"
	"github.com/PouryDev/server-infrastructure/mizan/treasury"
	"github.com/PouryDev/server-infrastructure/mizan/treasury/pgtreasury"
	"github.com/PouryDev/server-infrastructure/mizan/vat"
	"github.com/PouryDev/server-infrastructure/mizan/vat/pgvat"
	"github.com/PouryDev/server-infrastructure/mizan/wallet"
	"github.com/PouryDev/server-infrastructure/mizan/wallet/pgwallet"
)

// Config of the module.
type Config struct {
	// Currency of the books (default IRR, the legal accounting currency in Iran).
	Currency money.Currency
	// VATBasisPoints is applied to invoice items that do not set their own rate. Default 1000 (10%).
	// The rate is law and changes; keep it in configuration, never in code.
	VATBasisPoints int
	// IranianCoding loads the Iranian chart of accounts template and maps the system accounts to it. Leave it
	// off to run without a chart: everything works, reports just show accounts as unclassified.
	IranianCoding bool
	// IranianCodingFull enables the larger chart (ledger.IranianCodingFull, 362 codes from a published sample, see its
	// documentation) instead of the small template. Choose one; switching later remaps only the system accounts.
	IranianCodingFull bool
	// YearEndChunk is the most lines of one year-end document before it is split (default 3000).
	YearEndChunk int
	// RequireApproval switches the direct endpoints of year-end closing and profit appropriation off: the action then needs a request
	// (POST /approvals) and a DIFFERENT user's approval (the two-person rule). Off by default: the direct endpoints stay usable.
	RequireApproval bool
	// Payment configures gateways' callbacks and the allowed return hosts.
	Payment payment.Config
	Logger  *slog.Logger
}

// Option customises New.
type Option func(*options)

type options struct {
	gateways  []payment.Gateway
	clock     func() time.Time
	einvoice  einvoice.Provider
	einFiscal string
	einSeller einvoice.Party
}

// WithEInvoice connects the tax authority's e-invoice provider (for Moadian: einvoice/moadian). fiscalID is the taxpayer's
// fiscal-memory id invoices are sent under and seller is the seller identity printed on them. Without a provider invoices can
// still be queued; they are sent once one is configured.
func WithEInvoice(p einvoice.Provider, fiscalID string, seller einvoice.Party) Option {
	return func(o *options) { o.einvoice, o.einFiscal, o.einSeller = p, fiscalID, seller }
}

// WithGateway registers a payment gateway adapter.
func WithGateway(g payment.Gateway) Option {
	return func(o *options) { o.gateways = append(o.gateways, g) }
}

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(o *options) { o.clock = f } }

// Mizan holds the services of every context.
type Mizan struct {
	DB          *postgres.DB
	Ledger      *ledger.Service
	Payments    *payment.Service
	Invoices    *invoice.Service
	Wallets     *wallet.Service
	Reconcile   *reconcile.Service
	Treasury    *treasury.Service
	Payroll     *payroll.Service
	Payout      *payout.Service
	EInvoice    *einvoice.Service
	Coupons     *coupon.Service
	Subscribe   *subscription.Service
	FixedAssets *fixedasset.Service
	Inventory   *inventory.Service
	Appropriate *appropriation.Service
	VAT         *vat.Service
	IncomeTax   *incometax.Service
	Approvals   *approval.Service
	Audit       *audit.Log
	Idempotency *idempotency.Store
	Events      *outbox.Writer

	cfg       Config
	log       *slog.Logger
	einSeller einvoice.Party
}

// Migrate applies mizan's schema (own goose table). Run it before New, as a one-shot step.
func Migrate(ctx context.Context, databaseURL string) ([]string, error) {
	return migrations.Up(ctx, databaseURL)
}

// New wires the module. It is idempotent: it creates the system accounts and the current fiscal year when
// they do not exist.
func New(ctx context.Context, db *postgres.DB, cfg Config, opts ...Option) (*Mizan, error) {
	var o options
	for _, f := range opts {
		f(&o)
	}
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	if cfg.VATBasisPoints == 0 {
		cfg.VATBasisPoints = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Payment.DefaultGateway == "" && len(o.gateways) > 0 {
		cfg.Payment.DefaultGateway = o.gateways[0].Name()
	}

	ledgerStore, err := pgledger.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	events, auditLog := outbox.NewWriter(db), audit.New(db)
	lopts := []ledger.Option{ledger.WithYearEnd(AccProfitLossSummary, AccRetainedEarnings), ledger.WithYearEndTransit(AccClosingBalance, AccOpeningBalance),
		ledger.WithYearEndChunk(cfg.YearEndChunk), ledger.WithTrail(auditLog, events)}
	if o.clock != nil {
		lopts = append(lopts, ledger.WithClock(o.clock))
	}
	led := ledger.NewService(ledgerStore, db, lopts...)
	books := ledgerBooks{led}

	wallets := wallet.NewService(pgwallet.New(db), db, books, events, auditLog, wallet.Accounts{Adjustment: AccAdjustments, Coding: codingWalletLiability})

	invRepo, err := pginvoice.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	invoices := invoice.NewService(invRepo, db, books, events, auditLog, invoice.Config{
		DefaultTaxBP: cfg.VATBasisPoints,
		Accounts:     invoice.Accounts{Receivable: AccReceivable, Revenue: AccRevenue, VATPayable: AccVATPayable},
	})

	payRepo, err := pgpayment.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	popts := []payment.Option{
		payment.WithSettlement(PurposeInvoice, invoiceSettlement{invoices}),
		payment.WithSettlement(PurposeWalletTopUp, walletSettlement{wallets}),
		payment.WithLogger(cfg.Logger),
	}
	for _, g := range o.gateways {
		popts = append(popts, payment.WithGateway(g))
	}
	if o.clock != nil {
		popts = append(popts, payment.WithClock(o.clock))
	}
	payments := payment.NewService(payRepo, db, events, auditLog, cfg.Payment, popts...)

	trRepo, err := pgtreasury.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	var topts []treasury.Option
	if o.clock != nil {
		topts = append(topts, treasury.WithClock(o.clock))
	}
	treasurySvc := treasury.NewService(trRepo, db, books, events, auditLog, treasury.Config{Currency: cfg.Currency, Accounts: treasury.Accounts{
		ChequesInHand: AccChequesInHand, ChequesInCollection: AccChequesInCollection, ChequesPayable: AccChequesPayable,
		Receivable: AccReceivable, Payable: AccPayable, BankFees: AccBankFees, GatewayClearing: AccGatewayClearing, GatewayFees: AccGatewayFees, VATInput: AccVATInput,
		BankCoding: codingBankAccounts}}, topts...)

	prRepo, err := pgpayroll.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	var propts []payroll.Option
	if o.clock != nil {
		propts = append(propts, payroll.WithClock(o.clock))
	}
	payrollSvc := payroll.NewService(prRepo, db, books, events, auditLog, payroll.Config{Currency: cfg.Currency, Accounts: payroll.Accounts{
		SalaryExpense: AccSalaryExpense, EmployerInsuranceExpense: AccEmployerInsuranceExpense, SalariesPayable: AccSalariesPayable,
		TaxPayable: AccPayrollTaxPayable, InsurancePayable: AccInsurancePayable, EmployeeLoans: AccEmployeeLoans, LoanWriteOffExpense: AccLoanWriteOff, Bank: AccBank}}, propts...)

	poRepo, err := pgpayout.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	var poopts []payout.Option
	if o.clock != nil {
		poopts = append(poopts, payout.WithClock(o.clock))
	}
	payoutSvc := payout.NewService(poRepo, db, books, events, auditLog, payout.Config{Currency: cfg.Currency, DefaultVATBP: cfg.VATBasisPoints, Accounts: payout.Accounts{
		Receivable: AccReceivable, Commission: AccCommissionRevenue, VATPayable: AccVATPayable, Bank: AccBank, SellerRecoverable: AccSellerRecoverable, SellerCoding: codingSellerPayable}}, poopts...)

	einRepo, err := pgeinvoice.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	einopts := []einvoice.Option{einvoice.WithLogger(cfg.Logger)}
	if o.clock != nil {
		einopts = append(einopts, einvoice.WithClock(o.clock))
	}
	einSvc := einvoice.NewService(einRepo, db, o.einvoice, events, auditLog, einvoice.Config{FiscalID: o.einFiscal}, einopts...)

	cpRepo, err := pgcoupon.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	subRepo, err := pgsubscription.New(db)
	if err != nil {
		return nil, fmt.Errorf("mizan: %w", err)
	}
	var cpopts []coupon.Option
	var subopts []subscription.Option
	if o.clock != nil {
		cpopts, subopts = append(cpopts, coupon.WithClock(o.clock)), append(subopts, subscription.WithClock(o.clock))
	}
	couponSvc := coupon.NewService(cpRepo, db, events, auditLog, cpopts...)
	subSvc := subscription.NewService(subRepo, db, events, auditLog, subopts...)

	var faopts []fixedasset.Option
	if o.clock != nil {
		faopts = append(faopts, fixedasset.WithClock(o.clock))
	}
	faSvc := fixedasset.NewService(pgfixedasset.New(db), db, books, events, auditLog, fixedasset.Config{Currency: cfg.Currency, Accounts: fixedasset.Accounts{
		DepreciationExpense: AccDepreciationExpense, DisposalGainLoss: AccAssetDisposal, Bank: AccBank, VATInput: AccVATInput, SmallAssetExpense: AccSmallAssetExpense}}, faopts...)

	var invopts []inventory.Option
	if o.clock != nil {
		invopts = append(invopts, inventory.WithClock(o.clock))
	}
	invSvc := inventory.NewService(pginventory.New(db), db, books, events, auditLog, inventory.Config{Currency: cfg.Currency, Accounts: inventory.Accounts{
		Inventory: AccInventory, COGS: AccCOGS, Adjustment: AccInventoryAdjustment, Payable: AccPayable, VATInput: AccVATInput, WIP: AccWIP, Overhead: AccProductionOverhead, ProductionVariance: AccProductionVariance}}, invopts...)

	var apopts []appropriation.Option
	if o.clock != nil {
		apopts = append(apopts, appropriation.WithClock(o.clock))
	}
	apSvc := appropriation.NewService(pgappropriation.New(db), db, books, events, auditLog, appropriation.Config{Currency: cfg.Currency, Accounts: appropriation.Accounts{
		Retained: AccRetainedEarnings, LegalReserve: AccLegalReserve, OptionalReserve: AccOptionalReserve, DividendsPayable: AccDividendsPayable, Bank: AccBank}}, apopts...)

	var itopts []incometax.Option
	if o.clock != nil {
		itopts = append(itopts, incometax.WithClock(o.clock))
	}
	itSvc := incometax.NewService(pgincometax.New(db), db, books, events, auditLog, incometax.Config{Currency: cfg.Currency, Accounts: incometax.Accounts{
		Expense: AccIncomeTaxExpense, Payable: AccIncomeTaxPayable, Prepaid: AccIncomeTaxPrepaid, Bank: AccBank}}, itopts...)

	var vopts []vat.Option
	if o.clock != nil {
		vopts = append(vopts, vat.WithClock(o.clock))
	}
	vatSvc := vat.NewService(pgvat.New(db), db, books, ledgerBalances{led}, events, auditLog, vat.Config{Currency: cfg.Currency, Accounts: vat.Accounts{
		Output: AccVATPayable, Input: AccVATInput, Settlement: AccVATSettlement, Bank: AccBank, Expense: AccVATNonDeductible}}, vopts...)

	m := &Mizan{
		DB: db, Ledger: led, Payments: payments, Invoices: invoices, Wallets: wallets, Audit: auditLog, Idempotency: idempotency.New(db), Events: events,
		Reconcile: reconcile.NewService(pgreconcile.New(db), db, events, auditLog), Treasury: treasurySvc, Payroll: payrollSvc, Payout: payoutSvc, EInvoice: einSvc, Coupons: couponSvc, Subscribe: subSvc, FixedAssets: faSvc, Inventory: invSvc, Appropriate: apSvc, VAT: vatSvc, IncomeTax: itSvc, einSeller: o.einSeller, cfg: cfg, log: cfg.Logger,
	}
	var apvopts []approval.Option
	if o.clock != nil {
		apvopts = append(apvopts, approval.WithClock(o.clock))
	}
	m.Approvals = approval.NewService(pgapproval.New(db), db, events, auditLog, apvopts...)
	m.registerApprovals()
	if err := m.bootstrap(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Mizan) bootstrap(ctx context.Context) error {
	sys := systemAccounts(m.cfg.Currency)
	specs := make([]ledger.AccountSpec, len(sys))
	for i, a := range sys {
		specs[i] = a.spec
	}
	if err := m.Ledger.Ensure(ctx, specs...); err != nil {
		return fmt.Errorf("mizan: system accounts: %w", err)
	}
	if err := m.Ledger.EnsureCurrentFiscalYear(ctx); err != nil {
		return fmt.Errorf("mizan: fiscal year: %w", err)
	}
	// The system bank account is a bank account of treasury too: settlements and cheques can name it.
	if _, err := m.Treasury.OpenBankAccount(ctx, treasury.OpenAccountInput{Name: "Main bank account", LedgerCode: AccBank, Currency: m.cfg.Currency, Actor: "system"}); err != nil {
		return fmt.Errorf("mizan: main bank account: %w", err)
	}
	switch {
	case m.cfg.IranianCodingFull:
		return m.EnableIranianCodingFull(ctx)
	case m.cfg.IranianCoding:
		return m.EnableIranianCoding(ctx)
	}
	return nil
}

// EnableIranianCoding imports the Iranian chart of accounts template and maps the system accounts and all
// wallet accounts to it. Safe to call repeatedly; accounts you mapped yourself are overwritten only for the
// system accounts.
func (m *Mizan) EnableIranianCoding(ctx context.Context) error {
	return m.enableCoding(ctx, ledger.IranianCoding(), func(a sysAccount) string { return a.coding }, codingWalletLiability, codingSellerPayable)
}

// EnableIranianCodingFull does the same with the larger chart ledger.IranianCodingFull. Read that function's
// documentation: the chart is a published sample, not a legal standard, and mapping system accounts to it (for example
// VAT payable to the general account 2112, which has no VAT line) is a suggestion for the accountant to review.
func (m *Mizan) EnableIranianCodingFull(ctx context.Context) error {
	return m.enableCoding(ctx, ledger.IranianCodingFull(), func(a sysAccount) string { return a.full }, fullWalletLiability, fullSellerPayable)
}

func (m *Mizan) enableCoding(ctx context.Context, chart []ledger.CodingNode, pick func(sysAccount) string, wallet, seller string) error {
	if err := m.Ledger.ImportCoding(ctx, chart); err != nil {
		return err
	}
	for _, a := range systemAccounts(m.cfg.Currency) {
		code := pick(a)
		if code == "" {
			continue
		}
		if err := m.Ledger.MapAccount(ctx, a.spec.Code, code); err != nil {
			return err
		}
	}
	if err := m.Ledger.MapOwnerKind(ctx, "wallet", wallet); err != nil {
		return err
	}
	return m.Ledger.MapOwnerKind(ctx, "seller", seller)
}

// OutboxRelay returns the runner that publishes events through pub.
func (m *Mizan) OutboxRelay(pub outbox.Publisher, opts ...outbox.RelayOption) lifecycle.Runner {
	r := outbox.NewRelay(m.DB, pub, append([]outbox.RelayOption{outbox.WithLogger(m.log)}, opts...)...)
	return lifecycle.RunnerFunc(r.Run)
}

// PaymentSweeper returns the runner that verifies open payments (default: every minute, payments older than
// two minutes) so a lost callback never leaves a paid payment unrecorded.
func (m *Mizan) PaymentSweeper() lifecycle.Runner {
	return lifecycle.RunnerFunc(func(ctx context.Context) error { return m.Payments.Run(ctx, time.Minute, 2*time.Minute) })
}
