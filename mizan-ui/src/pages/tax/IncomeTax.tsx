import { useState } from 'react'
import { incomeTax, type ITAssessment } from '@/api/tax'
import { itemsOf } from '@/api/commerce'
import { treasury } from '@/api/treasury'
import { useFiscal } from '@/app/Fiscal'
import { fmt, toFa } from '@/shared/digits'
import { jalaliToday, showDate, isoToJalali } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, ErrorMessage, Message, PageBar, SubTabs, useResource, useToast, type Column } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'
import { KV } from '@/ui'

type Tab = 'assess' | 'rules' | 'losses'

/** Income tax: assessments (preview, declare, pay), rule versions and carried-forward losses. */
export default function IncomeTax() {
  const toast = useToast()
  const { year } = useFiscal()
  const [tab, setTab] = useState<Tab>('assess')
  const as = useResource(async () => itemsOf(await incomeTax.assessments()), [])
  const rules = useResource(async () => itemsOf(await incomeTax.rules()), [])
  const losses = useResource(async () => itemsOf(await incomeTax.losses()), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [sel, setSel] = useState<ITAssessment>()
  const [prev, setPrev] = useState<Awaited<ReturnType<typeof incomeTax.preview>>>()
  const [dlg, setDlg] = useState<'declare' | null>(null)
  const bankOpt = (banks.data ?? [])[0]?.ledger_code
  const cols: Column<ITAssessment>[] = [
    { id: 'y', header: 'سال', cell: (a) => toFa(a.fiscal_year) }, { id: 'p', header: 'سود مشمول', num: true, cell: (a) => fmt(a.taxable_income) }, { id: 't', header: 'مالیات', num: true, cell: (a) => fmt(a.tax) },
    { id: 'pd', header: 'پرداخت‌شده', num: true, cell: (a) => fmt(a.paid) }, { id: 's', header: 'وضعیت', cell: (a) => (a.outstanding === 0 ? <Badge tone="ok">تسویه‌شده</Badge> : <Badge tone={a.overdue ? 'err' : 'warn'}>{a.overdue ? 'دیرکرد' : 'اعلام‌شده'}</Badge>) },
  ]
  return (
    <>
      <PageBar title="مالیات بر درآمد">
        <Button disabled={!year} onClick={async () => { try { setPrev(await incomeTax.preview(year!.id)); setTab('assess') } catch (e) { toast.error(e) } }}>پیش‌نمایش سال {toFa(year?.name ?? '')}</Button>
        <Button variant="pri" disabled={!year} onClick={() => setDlg('declare')}>قطعی‌کردن برگ تشخیص</Button>
      </PageBar>
      <SubTabs<Tab> value={tab} onChange={setTab} options={[{ value: 'assess', label: 'برگ‌های تشخیص' }, { value: 'rules', label: 'قوانین و نرخ‌ها' }, { value: 'losses', label: 'زیان‌های انتقالی' }]} />
      {prev && <Card title={`پیش‌نمایش مالیات سال ${toFa(prev.fiscal_year)}`}><KV rows={[['سود حسابداری', <span className="n">{fmt(prev.accounting_profit)}</span>], ['تعدیلات', <span className="n">{fmt(prev.add_back_total - prev.deduction_total + prev.depreciation_adjustment)}</span>], ['زیان انتقالی', <span className="n">{fmt(-prev.loss_used_total)}</span>], [<b key="k">مالیات محاسبه‌شده</b>, <b key="v" className="n">{fmt(prev.tax)}</b>]]} /></Card>}
      {tab === 'assess' && (
        <div className="grid g21" style={{ alignItems: 'start' }}>
          <DataTable caption="برگ‌های تشخیص" columns={cols} rows={as.data} rowKey={(a) => a.id} loading={as.loading} error={as.error} onRetry={as.reload} empty={{ title: 'هنوز برگ تشخیصی صادر نشده' }} onRowClick={(a) => setSel(a)} rowClass={(a) => (a.id === sel?.id ? 'sel' : undefined)} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Card title={sel ? `پرداخت مالیات ${toFa(sel.fiscal_year)}` : 'پرداخت مالیات'}>
              {sel ? (
                <InlineForm submitLabel="ثبت پرداخت" fields={[{ name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', w: 'amt', required: true, initial: fmt(sel.outstanding) }, { name: 'date', label: 'تاریخ', kind: 'date', w: 'date', initial: showDate(jalaliToday()) }]} onSubmit={(v, key) => incomeTax.pay(sel.id, { amount: val.amount(v.amount), account: bankOpt, date: val.iso(v.date) }, key)} onDone={(a) => (setSel(a), as.reload(), toast.push({ tone: 'ok', title: 'پرداخت ثبت شد' }))}>
                  <div className="sub">مانده: {fmt(sel.outstanding)} ریال · سررسید {showDate(sel.due_on_jalali)}</div>
                </InlineForm>
              ) : <div className="sub">یک برگ تشخیص را انتخاب کنید.</div>}
            </Card>
            <Message tone="warn" title="">پیش از قطعی‌کردن، تعدیلات و زیان‌های انتقالی را با مشاور مالیاتی بررسی کنید.</Message>
          </div>
        </div>
      )}
      {tab === 'rules' && (
        <div className="grid g21" style={{ alignItems: 'start' }}>
          <DataTable caption="قوانین" columns={[{ id: 'f', header: 'از تاریخ', cell: (r) => showDate(isoToJalali(r.effective_from)) }, { id: 'r', header: 'نرخ', num: true, cell: (r) => `${toFa(r.rate_bp / 100)}٪` }, { id: 's', header: 'منبع', mut: true, cell: (r) => r.source ?? '' }]} rows={rules.data} rowKey={(r) => r.id ?? r.effective_from} loading={rules.loading} error={rules.error} onRetry={rules.reload} empty={{ title: 'قانونی ثبت نشده' }} />
          <Card title="قانون جدید"><InlineForm submitLabel="افزودن نسخه" fields={[{ name: 'rate', label: 'نرخ (٪)', kind: 'number', w: 'sm', required: true }, { name: 'from', label: 'از تاریخ', kind: 'date', w: 'date', required: true }, { name: 'src', label: 'منبع', required: true }]} onSubmit={(v) => incomeTax.addRule({ effective_from: val.iso(v.from)!, rate_bp: Math.round(Number(val.int(v.rate)) * 100), source: val.text(v.src), filing_months: 4 })} onDone={() => (rules.reload(), toast.push({ tone: 'ok', title: 'نسخه افزوده شد' }))} /></Card>
        </div>
      )}
      {tab === 'losses' && (losses.error != null ? <ErrorMessage error={losses.error} /> : <DataTable caption="زیان‌های انتقالی" columns={[{ id: 'y', header: 'سال', cell: (l) => toFa(l.fiscal_year ?? '') }, { id: 'a', header: 'مبلغ زیان', num: true, cell: (l) => fmt(l.amount) }, { id: 'r', header: 'باقی‌مانده', num: true, cell: (l) => fmt(l.remaining) }]} rows={losses.data} rowKey={(l) => l.assessment_id ?? String(l.fiscal_year)} loading={losses.loading} empty={{ title: 'زیان انتقالی‌ای نیست' }} />)}
      {dlg === 'declare' && year && <FormDialog title={`قطعی‌کردن برگ تشخیص ${toFa(year.name)}`} submitLabel="قطعی‌کردن" danger intro="پس از قطعی‌کردن، سند مالیات ثبت می‌شود. پیش از آن تعدیلات را با مشاور مالیاتی بررسی کنید." fields={[{ name: 'ok', label: 'برای تأیید «قطعی» را بنویسید', required: true }]} onSubmit={(v, key) => { if (val.text(v.ok) !== 'قطعی') throw new Error('confirm'); return incomeTax.declare(year.id, {}, key) }} onClose={() => setDlg(null)} onDone={() => (setDlg(null), as.reload(), toast.push({ tone: 'ok', title: 'برگ تشخیص قطعی شد' }))} />}
      {as.error != null && tab !== 'assess' && <ErrorMessage error={as.error} />}
    </>
  )
}
