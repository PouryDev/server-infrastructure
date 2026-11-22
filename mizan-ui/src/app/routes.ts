import { lazy, type ComponentType, type LazyExoticComponent } from 'react'

export interface RouteDef {
  /** the design board's name */
  key: string
  path: string
  label: string
  /** label in the sidebar (the design's Nav component) */
  nav: string
  page: LazyExoticComponent<ComponentType>
}

export interface NavGroup {
  title: string
  keys: string[]
}

const page = (loader: () => Promise<{ default: ComponentType }>) => lazy(loader)

// The sidebar groups and labels are those of the design's Nav component, in the same order.
export const GROUPS: NavGroup[] = [
  { title: 'حسابداری', keys: ['Main', 'Journals', 'JournalForm', 'Ledger', 'Browse', 'Coding', 'AcctForms', 'Reports', 'Periods'] },
  { title: 'فروش و دریافت', keys: ['Invoices', 'InvoiceForm', 'Payments', 'PaymentsMore', 'Wallets', 'WalletOps', 'Coupons', 'Subscriptions'] },
  { title: 'فروشندگان و تسویه', keys: ['Commerce', 'PayoutSales', 'PayoutBatches'] },
  { title: 'خزانه', keys: ['Treasury', 'TreasuryForms', 'Reconcile'] },
  { title: 'مالیات', keys: ['Vat', 'VatMore', 'IncomeTax', 'Tax', 'EInvoice'] },
  { title: 'حقوق و دستمزد', keys: ['Payroll', 'PayrollPeople', 'PayrollRules'] },
  { title: 'دارایی و انبار', keys: ['Assets', 'AssetOps', 'AssetSetup', 'Inventory', 'InvItems', 'InvOps', 'Production'] },
  { title: 'سایر', keys: ['Approvals', 'Audit', 'Shortcuts'] },
]

const R = (key: string, path: string, nav: string, label: string, p: RouteDef['page']): RouteDef => ({ key, path, nav, label, page: p })

export const ROUTES: RouteDef[] = [
  R('Main', '/', 'داشبورد', 'داشبورد', page(() => import('@/pages/dashboard/Dashboard'))),
  R('Journals', '/journals', 'اسناد حسابداری', 'اسناد حسابداری', page(() => import('@/pages/journals/Journals'))),
  R('JournalForm', '/journals/new', 'سند جدید', 'سند جدید', page(() => import('@/pages/journals/JournalForm'))),
  R('Ledger', '/ledger', 'گردش حساب و تراز', 'گردش حساب و تراز', page(() => import('@/pages/ledger/Ledger'))),
  R('Browse', '/accounts', 'مرور حساب‌ها', 'مرور حساب‌ها', page(() => import('@/pages/coding/AccountBrowser'))),
  R('Coding', '/coding', 'کدینگ حساب‌ها', 'کدینگ حساب‌ها', page(() => import('@/pages/coding/Coding'))),
  R('AcctForms', '/coding/new', 'حساب جدید و کدینگ نمونه', 'حساب جدید', page(() => import('@/pages/coding/AcctForms'))),
  R('Reports', '/reports', 'صورت‌های مالی', 'صورت‌های مالی', page(() => import('@/pages/reports/Reports'))),
  R('Periods', '/periods', 'سال مالی و دوره‌ها', 'سال مالی و دوره‌ها', page(() => import('@/pages/periods/Periods'))),
  R('Invoices', '/invoices', 'فاکتورها', 'فاکتورها', page(() => import('@/pages/invoices/Invoices'))),
  R('InvoiceForm', '/invoices/new', 'فاکتور جدید', 'فاکتور جدید', page(() => import('@/pages/invoices/InvoiceForm'))),
  R('Payments', '/payments', 'پرداخت‌ها و بازگشت وجه', 'پرداخت‌ها', page(() => import('@/pages/payments/Payments'))),
  R('PaymentsMore', '/payments/details', 'جزئیات پرداخت و اختلاف‌ها', 'جزئیات پرداخت', page(() => import('@/pages/payments/PaymentsMore'))),
  R('Wallets', '/wallets', 'کیف پول‌ها', 'کیف پول‌ها', page(() => import('@/pages/wallets/Wallets'))),
  R('WalletOps', '/wallets/ops', 'عملیات کیف پول', 'عملیات کیف پول', page(() => import('@/pages/wallets/WalletOps'))),
  R('Coupons', '/coupons', 'کوپن‌ها', 'کوپن‌ها', page(() => import('@/pages/commerce/Coupons'))),
  R('Subscriptions', '/subscriptions', 'اشتراک‌ها', 'اشتراک‌ها', page(() => import('@/pages/commerce/Subscriptions'))),
  R('Commerce', '/sellers', 'فروشندگان', 'فروشندگان', page(() => import('@/pages/commerce/Commerce'))),
  R('PayoutSales', '/payout/sales', 'شرایط و فروش‌ها', 'شرایط و فروش‌ها', page(() => import('@/pages/commerce/PayoutSales'))),
  R('PayoutBatches', '/payout/batches', 'دسته‌های پرداخت', 'دسته‌های پرداخت', page(() => import('@/pages/commerce/PayoutBatches'))),
  R('Treasury', '/treasury', 'خزانه‌داری و چک', 'خزانه‌داری', page(() => import('@/pages/treasury/Treasury'))),
  R('TreasuryForms', '/treasury/new', 'ثبت چک و حساب بانکی', 'ثبت چک', page(() => import('@/pages/treasury/TreasuryForms'))),
  R('Reconcile', '/reconcile', 'تطبیق بانکی', 'تطبیق بانکی', page(() => import('@/pages/treasury/Reconcile'))),
  R('Vat', '/vat', 'ارزش افزوده', 'ارزش افزوده', page(() => import('@/pages/tax/Vat'))),
  R('VatMore', '/vat/rules', 'قوانین و عملیات ارزش افزوده', 'عملیات ارزش افزوده', page(() => import('@/pages/tax/VatMore'))),
  R('IncomeTax', '/income-tax', 'مالیات بر درآمد', 'مالیات بر درآمد', page(() => import('@/pages/tax/IncomeTax'))),
  R('Tax', '/einvoices', 'صف صورتحساب الکترونیکی', 'صورتحساب الکترونیکی', page(() => import('@/pages/tax/Tax'))),
  R('EInvoice', '/moadian', 'سامانه مودیان', 'سامانه مودیان', page(() => import('@/pages/tax/EInvoice'))),
  R('Payroll', '/payroll', 'فیش‌های حقوقی', 'فیش‌های حقوقی', page(() => import('@/pages/payroll/Payroll'))),
  R('PayrollPeople', '/payroll/people', 'کارکنان و قراردادها', 'کارکنان', page(() => import('@/pages/payroll/PayrollPeople'))),
  R('PayrollRules', '/payroll/rules', 'قوانین و پرداخت‌ها', 'قوانین حقوق', page(() => import('@/pages/payroll/PayrollRules'))),
  R('Assets', '/assets', 'دارایی‌های ثابت', 'دارایی‌های ثابت', page(() => import('@/pages/assets/Assets'))),
  R('AssetOps', '/assets/ops', 'تحصیل و واگذاری', 'تحصیل و واگذاری', page(() => import('@/pages/assets/AssetOps'))),
  R('AssetSetup', '/assets/setup', 'استهلاک و تنظیمات', 'استهلاک', page(() => import('@/pages/assets/AssetSetup'))),
  R('Inventory', '/inventory', 'انبار', 'انبار', page(() => import('@/pages/inventory/Inventory'))),
  R('InvItems', '/inventory/items', 'کالا و حرکت‌ها', 'کالا و حرکت‌ها', page(() => import('@/pages/inventory/InvItems'))),
  R('InvOps', '/inventory/ops', 'عملیات انبار', 'عملیات انبار', page(() => import('@/pages/inventory/InvOps'))),
  R('Production', '/production', 'تولید', 'تولید', page(() => import('@/pages/inventory/Production'))),
  R('Approvals', '/approvals', 'تأییدیه‌ها', 'تأییدیه‌ها', page(() => import('@/pages/misc/Approvals'))),
  R('Audit', '/audit', 'ردیابی رکورد', 'ردیابی رکورد', page(() => import('@/pages/misc/Audit'))),
  R('Shortcuts', '/shortcuts', 'کلیدهای میانبر', 'کلیدهای میانبر', page(() => import('@/pages/misc/Shortcuts'))),
]

export const HIDDEN: RouteDef[] = [R('SystemStates', '/states', 'حالت‌های سیستمی', 'حالت‌های سیستمی', page(() => import('@/pages/misc/SystemStates')))]

export const BY_KEY = new Map(ROUTES.map((r) => [r.key, r]))

export function routeFor(pathname: string): RouteDef | undefined {
  return [...ROUTES, ...HIDDEN].find((r) => r.path === pathname) ?? ROUTES.filter((r) => r.path !== '/' && pathname.startsWith(r.path + '/')).sort((a, b) => b.path.length - a.path.length)[0]
}
