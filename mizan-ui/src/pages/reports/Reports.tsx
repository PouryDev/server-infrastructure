import { useMemo, useState } from 'react'
import { api } from '@/api/http'
import { useFiscal } from '@/app/Fiscal'
import { fmt, fmtOrDash, toFa } from '@/shared/digits'
import { isoToJalali, jalaliToIso, showDate } from '@/shared/jalali'
import { Button, Card, ErrorMessage, Field, Input, Message, PageBar, Skeleton, SubTabs, useResource } from '@/ui'

type Tab = 'bs' | 'is' | 'eq' | 'cf'
interface Line { code: string; title: string; amount: number; accounts?: { account: string; name: string; amount: number }[] }

/** Official financial statements (accounting standard 1 layout): balance sheet, income statement, equity changes, cash flow. */
export default function Reports() {
  const { year } = useFiscal()
  const today = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tehran' }).format(new Date())
  const [tab, setTab] = useState<Tab>('bs')
  const [dateText, setDateText] = useState(showDate(isoToJalali(today)))
  const [applied, setApplied] = useState(today)
  const to = applied
  const from = year?.from ?? today
  const bad = !!dateText && !jalaliToIso(dateText)
  const path = { bs: 'balance-sheet', is: 'income-statement', eq: 'changes-in-equity', cf: 'cash-flow' }[tab]
  const res = useResource((signal) => api.get<Record<string, unknown>>(`/v1/accounting/official/${path}`, tab === 'bs' ? { as_of: to } : { from, to }, signal), [tab, to, from])
  const d = res.data
  const unclassified = (d?.unclassified_accounts as unknown[] | undefined)?.length ?? 0

  return (
    <>
      <PageBar title="صورت‌های مالی رسمی">
        <Field label="تا تاریخ" error={bad ? 'تاریخ نامعتبر' : undefined}>
          {(id) => <Input id={id} w="date" num value={dateText} onChange={(e) => setDateText(e.target.value)} onBlur={() => { const iso = jalaliToIso(dateText); if (iso) setApplied(iso) }} onKeyDown={(e) => e.key === 'Enter' && jalaliToIso(dateText) && setApplied(jalaliToIso(dateText)!)} />}
        </Field>
        <Button onClick={() => window.print()}>چاپ</Button>
        <Button onClick={() => window.print()}>PDF</Button>
      </PageBar>
      <SubTabs<Tab> value={tab} onChange={setTab} options={[{ value: 'bs', label: 'ترازنامه' }, { value: 'is', label: 'سود و زیان' }, { value: 'eq', label: 'تغییرات حقوق صاحبان سرمایه' }, { value: 'cf', label: 'جریان وجوه نقد' }]} />
      {unclassified > 0 && (
        <Message tone="warn" title={`${toFa(unclassified)} حساب با کدینگ نامشخص`}>مانده این حساب‌ها در «طبقه‌بندی‌نشده» آمده است. در کدینگ حساب‌ها نگاشت کنید.</Message>
      )}
      {res.error != null ? <ErrorMessage error={res.error} onRetry={res.reload} /> : !d ? <div className="card"><Skeleton rows={6} /></div> : tab === 'bs' ? <BalanceSheet d={d} /> : tab === 'is' ? <IncomeStatement d={d} /> : tab === 'eq' ? <Equity d={d} /> : <CashFlow d={d} />}
    </>
  )
}

const Section = ({ title, lines, total, totalLabel }: { title: string; lines: Line[]; total?: number; totalLabel?: string }) => (
  <>
    <tr className="sub"><td>{title}</td><td className="n">{total == null ? '' : fmt(total)}</td></tr>
    {lines.map((l) => <tr key={l.code}><td style={{ paddingInlineStart: 24 }}>{l.title}</td><td className="n">{fmt(l.amount)}</td></tr>)}
    {totalLabel && null}
  </>
)

function Table({ title, children, foot }: { title: string; children: React.ReactNode; foot?: [string, number][] }) {
  return (
    <Card title={title} bare>
      <div className="tw" style={{ border: 0, borderRadius: 0 }}>
        <table className="tbl" aria-label={title}>
          <thead><tr><th>سرفصل</th><th className="n">مبلغ (ریال)</th></tr></thead>
          <tbody>{children}</tbody>
          {foot && <tfoot>{foot.map(([l, v]) => <tr key={l}><td>{l}</td><td className="n">{fmt(v)}</td></tr>)}</tfoot>}
        </table>
      </div>
    </Card>
  )
}

function BalanceSheet({ d }: { d: Record<string, unknown> }) {
  const L = (k: string) => (d[k] as Line[] | undefined) ?? []
  const n = (k: string) => (d[k] as number | undefined) ?? 0
  return (
    <div className="grid g2" style={{ alignItems: 'start' }}>
      <Table title="دارایی‌ها" foot={[['جمع دارایی‌ها', n('total_assets')]]}>
        <Section title="دارایی‌های جاری" lines={L('current_assets')} total={n('total_current_assets')} />
        <Section title="دارایی‌های غیرجاری" lines={L('non_current_assets')} total={n('total_non_current_assets')} />
      </Table>
      <Table title="بدهی‌ها و حقوق صاحبان سرمایه" foot={[['جمع بدهی‌ها و حقوق', n('total_liabilities_and_equity')]]}>
        <Section title="بدهی‌های جاری" lines={L('current_liabilities')} total={n('total_current_liabilities')} />
        <Section title="بدهی‌های غیرجاری" lines={L('non_current_liabilities')} total={n('total_non_current_liabilities')} />
        <Section title="حقوق صاحبان سرمایه" lines={L('equity')} total={n('total_equity')} />
        <tr><td style={{ paddingInlineStart: 24 }}>سود (زیان) جاری</td><td className="n">{fmt(n('current_result'))}</td></tr>
      </Table>
    </div>
  )
}

function IncomeStatement({ d }: { d: Record<string, unknown> }) {
  const line = (k: string) => d[k] as Line | undefined
  const n = (k: string) => (d[k] as number | undefined) ?? 0
  const row = (k: string, label: string) => { const l = line(k); return <tr key={k}><td>{label}</td><td className="n">{fmt(l?.amount ?? 0)}</td></tr> }
  const sub = (k: string, label: string) => <tr key={k} className="sub"><td>{label}</td><td className="n">{fmt(n(k))}</td></tr>
  return (
    <Table title="صورت سود و زیان" foot={[['سود (زیان) خالص', n('net_profit')]]}>
      {row('operating_revenue', 'درآمدهای عملیاتی')}
      {row('cost_of_sales', 'بهای تمام‌شده کالای فروش‌رفته')}
      {sub('gross_profit', 'سود ناخالص')}
      {row('operating_expenses', 'هزینه‌های عملیاتی')}
      {row('other_operating', 'سایر اقلام عملیاتی')}
      {sub('operating_profit', 'سود عملیاتی')}
      {row('finance_costs', 'هزینه‌های مالی')}
      {sub('profit_before_tax', 'سود قبل از مالیات')}
      {row('tax_expense', 'مالیات بر درآمد')}
    </Table>
  )
}

function Equity({ d }: { d: Record<string, unknown> }) {
  const comps = (d.components as { code: string; title: string; opening: number; profit: number; other_movements: number; closing: number }[] | undefined) ?? []
  return (
    <Card title="صورت تغییرات حقوق صاحبان سرمایه" bare>
      <div className="tw" style={{ border: 0, borderRadius: 0 }}>
        <table className="tbl" aria-label="تغییرات حقوق صاحبان سرمایه">
          <thead><tr><th>جزء</th><th className="n">ابتدای دوره</th><th className="n">سود (زیان)</th><th className="n">سایر تغییرات</th><th className="n">پایان دوره</th></tr></thead>
          <tbody>{comps.map((c) => <tr key={c.code}><td>{c.title}</td><td className="n">{fmt(c.opening)}</td><td className="n">{fmtOrDash(c.profit)}</td><td className="n">{fmtOrDash(c.other_movements)}</td><td className="n">{fmt(c.closing)}</td></tr>)}</tbody>
          <tfoot><tr><td>جمع</td><td className="n">{fmt(d.opening_total as number)}</td><td className="n">{fmt(d.net_profit as number)}</td><td /><td className="n">{fmt(d.closing_total as number)}</td></tr></tfoot>
        </table>
      </div>
    </Card>
  )
}

function CashFlow({ d }: { d: Record<string, unknown> }) {
  const op = (d.operating as Record<string, number> | undefined) ?? {}
  const labels: [string, string][] = [['net_profit', 'سود (زیان) خالص'], ['depreciation', 'استهلاک'], ['disposal_loss', 'زیان واگذاری دارایی'], ['receivables_change', 'تغییر دریافتنی‌ها'], ['inventories_change', 'تغییر موجودی‌ها'], ['prepayments_and_tax_assets_change', 'تغییر پیش‌پرداخت‌ها و دارایی مالیاتی'], ['payables_change', 'تغییر پرداختنی‌ها'], ['other', 'سایر']]
  const n = (k: string) => (d[k] as number | undefined) ?? 0
  const memo = useMemo(() => labels.filter(([k]) => op[k] !== undefined), [op]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <Table title="صورت جریان وجوه نقد (روش غیرمستقیم)" foot={[['خالص تغییر نقد', n('net_change_in_cash')], ['مانده نقد ابتدای دوره', n('opening_cash')], ['مانده نقد پایان دوره', n('closing_cash')]]}>
      <tr className="sub"><td>فعالیت‌های عملیاتی</td><td className="n">{fmt(op.total ?? 0)}</td></tr>
      {memo.map(([k, l]) => <tr key={k}><td style={{ paddingInlineStart: 24 }}>{l}</td><td className="n">{fmt(op[k] ?? 0)}</td></tr>)}
      <tr className="sub"><td>فعالیت‌های سرمایه‌گذاری</td><td className="n">{fmt(n('investing'))}</td></tr>
      <tr className="sub"><td>فعالیت‌های تأمین مالی</td><td className="n">{fmt(n('financing'))}</td></tr>
    </Table>
  )
}
