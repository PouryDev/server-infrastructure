package mizan

import (
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Codes of the system accounts. They are plain ledger accounts: rename the display names, map them to your
// own chart of accounts, or post to additional ones; the codes are what the contexts use.
const (
	AccGatewayClearing = "bank.gateway_clearing" // money collected by gateways, not yet settled to the bank
	AccBank            = "bank.main"
	AccReceivable      = "receivable.customers"
	AccVATPayable      = "vat.output"
	AccRevenue         = "revenue.sales"
	AccGatewayFees     = "expense.gateway_fees"
	AccAdjustments     = "expense.adjustments"

	AccChequesInHand       = "cheques.receivable_in_hand"       // received cheques in our safe (اسناد دریافتنی نزد صندوق)
	AccChequesInCollection = "cheques.receivable_in_collection" // received cheques deposited at the bank (در جریان وصول)
	AccChequesPayable      = "cheques.payable"                  // cheques we issued, not yet paid (اسناد پرداختنی)
	AccPayable             = "payable.suppliers"                // supplier payables
	AccBankFees            = "expense.bank_fees"                // bank charges

	AccSalaryExpense            = "payroll.salary_expense"             // gross pay (حقوق و دستمزد)
	AccEmployerInsuranceExpense = "payroll.employer_insurance_expense" // employer's social-security and unemployment shares
	AccSalariesPayable          = "payroll.salaries_payable"           // net pay owed to employees (حقوق پرداختنی)
	AccPayrollTaxPayable        = "payroll.tax_payable"                // income tax withheld, owed to the tax office
	AccInsurancePayable         = "payroll.insurance_payable"          // social security owed to the insurer
	AccEmployeeLoans            = "payroll.employee_loans"             // loans and advances owed by employees

	AccCommissionRevenue = "revenue.marketplace_commission" // commission kept on marketplace sales (کارمزد فروش)

	AccVATInput         = "vat.input"                 // مالیات بر ارزش افزوده خرید (قابل کسر)
	AccVATSettlement    = "vat.settlement"            // تسویه مالیات بر ارزش افزوده با سازمان امور مالیاتی: اعتبار = بدهی، بدهکار = اعتبار انتقالی
	AccVATNonDeductible = "expense.vat_nondeductible" // مالیات خرید غیرقابل کسر (ماده ۸ قانون ارزش افزوده، تبصره ۵: هزینه قابل قبول)

	AccIncomeTaxExpense = "expense.income_tax" // هزینه مالیات بر درآمد
	AccIncomeTaxPayable = "payable.income_tax" // مالیات بر درآمد پرداختنی
	AccIncomeTaxPrepaid = "prepaid.income_tax" // پیش‌پرداخت مالیات (علی‌الحساب و مالیات‌های کسرشده)

	AccCapital          = "equity.capital"          // سرمایه
	AccLegalReserve     = "equity.legal_reserve"    // اندوخته قانونی
	AccOptionalReserve  = "equity.optional_reserve" // سایر اندوخته‌ها
	AccDividendsPayable = "payable.dividends"       // سود سهام پرداختنی

	AccInventory           = "inventory.goods"                // موجودی مواد و کالا
	AccCOGS                = "expense.cogs"                   // بهای تمام‌شده کالای فروش‌رفته
	AccInventoryAdjustment = "expense.inventory_adjustment"   // کسری/اضافی انبارگردانی و کاهش ارزش موجودی
	AccSellerRecoverable   = "receivable.sellers"             // آنچه فروشندگان پس از پرداخت و مرجوعی به پلتفرم بدهکارند (clawback)
	AccLoanWriteOff        = "expense.employee_loan_writeoff" // وام کارکنان بخشوده‌شده
	AccWIP                 = "inventory.wip"                  // کالای در جریان ساخت
	AccProductionOverhead  = "production.overhead"            // سربار تولید (clearing: Dr actual, Cr applied)
	AccProductionVariance  = "expense.production_variance"    // هزینه ظرفیت بلااستفاده / مغایرت سربار تولید
	AccDepreciationExpense = "expense.depreciation"           // هزینه استهلاک
	AccAssetDisposal       = "expense.asset_disposal"         // سود و زیان فروش دارایی: debit = loss, credit = gain
	AccSmallAssetExpense   = "expense.small_assets"           // دارایی‌های کم‌ارزش هزینه‌شده هنگام خرید (ماده ۱۲)

	AccRetainedEarnings  = "equity.retained_earnings"   // سود (زیان) انباشته: where the year's result goes at year-end closing
	AccProfitLossSummary = "equity.profit_loss_summary" // حساب خلاصه سود و زیان: temporary, always zero outside the closing documents
	AccClosingBalance    = "equity.closing_balance"     // تراز اختتامیه: balances the parts of a split closing document, nets to zero
	AccOpeningBalance    = "equity.opening_balance"     // تراز افتتاحیه: the same for a split opening document
)

// Coding codes (see ledger.IranianCoding) the system accounts are mapped to when the Iranian chart is enabled.
const (
	codingWalletLiability = "30201"
	codingBankAccounts    = "10102"
	codingSellerPayable   = "30101" // trade payables: what the marketplace owes its sellers
)

// The same three in ledger.IranianCodingFull.
const (
	fullWalletLiability = "211409" // other advances received (سایر پیش‌دریافت‌ها)
	fullBankAccounts    = "111005"
	fullSellerPayable   = "211001"
)

type sysAccount struct {
	spec   ledger.AccountSpec
	coding string // code in ledger.IranianCoding
	full   string // code in ledger.IranianCodingFull
}

func systemAccounts(cur money.Currency) []sysAccount {
	a := func(code, name string, t ledger.AccountType, coding, full string) sysAccount {
		return sysAccount{spec: ledger.AccountSpec{Code: code, Name: name, Type: t, Currency: cur}, coding: coding, full: full}
	}
	return []sysAccount{
		a(AccGatewayClearing, "Payment gateways (clearing)", ledger.Asset, "10103", "111007"),
		a(AccBank, "Bank", ledger.Asset, "10102", "111005"),
		a(AccReceivable, "Customer receivables", ledger.Asset, "10201", "111301"),
		a(AccVATPayable, "VAT payable (output)", ledger.Liability, "30301", "2112"),
		a(AccRevenue, "Sales revenue", ledger.Revenue, "60101", "4110"),
		a(AccGatewayFees, "Payment gateway fees", ledger.Expense, "70202", "621104"),
		a(AccAdjustments, "Manual adjustments", ledger.Expense, "70203", "621509"),
		a(AccChequesInHand, "Cheques received (in hand)", ledger.Asset, "10202", "111203"),
		a(AccChequesInCollection, "Cheques received (in collection)", ledger.Asset, "10202", "111201"),
		a(AccChequesPayable, "Cheques issued (payable)", ledger.Liability, "30102", "211101"),
		a(AccPayable, "Supplier payables", ledger.Liability, "30101", "211001"),
		a(AccBankFees, "Bank charges", ledger.Expense, "70202", "621104"),
		a(AccSalaryExpense, "Salaries and wages", ledger.Expense, "70201", "611001"),
		a(AccEmployerInsuranceExpense, "Employer social-security expense", ledger.Expense, "70201", "611011"),
		a(AccSalariesPayable, "Salaries payable", ledger.Liability, "30401", "211208"),
		a(AccPayrollTaxPayable, "Payroll tax payable", ledger.Liability, "30302", "211202"),
		a(AccInsurancePayable, "Social-security payable", ledger.Liability, "30303", "211204"),
		a(AccEmployeeLoans, "Employee loans and advances", ledger.Asset, "10203", "1114"),
		a(AccCommissionRevenue, "Marketplace commission revenue", ledger.Revenue, "60101", "4110"),
		a(AccVATInput, "VAT paid on purchases (input)", ledger.Asset, "10501", ""),
		a(AccVATSettlement, "VAT settlement with the tax authority", ledger.Liability, "30301", "2112"),
		a(AccVATNonDeductible, "VAT on purchases that may not be deducted", ledger.Expense, "70203", "621509"),
		// The template chart has no income-tax codes and none is invented: they are unmapped there (the full chart has the payable and the prepayment).
		a(AccIncomeTaxExpense, "Income tax expense", ledger.Expense, "", ""),
		a(AccIncomeTaxPayable, "Income tax payable", ledger.Liability, "", "211201"),
		a(AccIncomeTaxPrepaid, "Prepaid income tax", ledger.Asset, "", "111909"),
		a(AccCapital, "Share capital", ledger.Equity, "50101", "311001"),
		a(AccLegalReserve, "Legal reserve", ledger.Equity, "50201", "311101"),
		a(AccOptionalReserve, "Other reserves", ledger.Equity, "", "3112"),
		a(AccDividendsPayable, "Dividends payable", ledger.Liability, "", "211602"),
		a(AccInventory, "Inventory (goods and materials)", ledger.Asset, "10301", "1116"),
		a(AccSellerRecoverable, "Sellers recoverable (clawbacks after payout)", ledger.Asset, "", ""),
		a(AccLoanWriteOff, "Employee loans written off", ledger.Expense, "70203", "621509"),
		a(AccWIP, "Work in process (production orders)", ledger.Asset, "", ""),
		a(AccProductionOverhead, "Production overhead pool (actual less applied)", ledger.Expense, "", ""),
		a(AccProductionVariance, "Production overhead variance and cost of unused capacity", ledger.Expense, "70203", "621509"),
		a(AccCOGS, "Cost of goods sold", ledger.Expense, "70101", "5110"),
		a(AccInventoryAdjustment, "Inventory count differences and write-downs", ledger.Expense, "70203", "621509"),
		a(AccDepreciationExpense, "Depreciation expense", ledger.Expense, "70203", "6113"),
		a(AccAssetDisposal, "Gain or loss on disposal of assets", ledger.Expense, "70203", "621502"),
		a(AccSmallAssetExpense, "Small assets expensed at purchase", ledger.Expense, "70203", "621509"),
		a(AccRetainedEarnings, "Retained earnings (accumulated profit or loss)", ledger.Equity, "50202", "311301"),
		// The template chart has no code for the summary account and none is invented: it is unmapped until the
		// accountant maps it to the code their chart uses.
		a(AccProfitLossSummary, "Profit and loss summary", ledger.Equity, "", "311401"),
		a(AccClosingBalance, "Closing balance sheet (تراز اختتامیه)", ledger.Equity, "", ""),
		a(AccOpeningBalance, "Opening balance sheet (تراز افتتاحیه)", ledger.Equity, "", ""),
	}
}
