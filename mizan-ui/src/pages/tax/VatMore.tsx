import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { treasury } from '@/api/treasury'
import { vat, type VatReturn } from '@/api/vat'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday, showDate, isoToJalali } from '@/shared/jalali'
import { Button, Card, Field, Input, Message, PageBar, Select, Seg, useResource, useToast } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'

/** VAT rules and operations: rates, exemptions, computation, apportionment, amendment with penalty, payment and refund. */
export default function VatMore() {
  const toast = useToast()
  const rates = useResource(async () => itemsOf(await vat.rates()), [])
  const exempt = useResource(async () => itemsOf(await vat.exemptions()), [])
  const returns = useResource(() => vat.returns(), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [dlg, setDlg] = useState<'rate' | 'exempt' | null>(null)
  const [base, setBase] = useState('')
  const [on, setOn] = useState(showDate(jalaliToday()))
  const [calc, setCalc] = useState<{ tax: number; total: number }>()
  const [ret, setRet] = useState('')
  const rs = returns.data?.items ?? []
  const chosen: VatReturn | undefined = rs.find((r) => r.id === ret) ?? rs[0]
  const pen = useResource(async () => (chosen ? vat.penalty(chosen.id) : undefined), [chosen?.id])
  const [mode, setMode] = useState<'pay' | 'refund'>('pay')
  const bankOpts = (banks.data ?? []).map((b) => ({ value: b.ledger_code, label: b.name }))
  const [bank, setBank] = useState('')
  const retLabel = (r: VatReturn) => `${MONTHS[(r.period - 1) % 12]} ${toFa(r.jalali_year)} · ${fmt(r.net)}`

  return (
    <>
      <PageBar title="قوانین و عملیات ارزش افزوده" sub="نرخ، معافیت، محاسبه، اصلاحیه، جریمه، تخصیص، پرداخت و استرداد" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="نرخ‌ها و قوانین" extra={<Button small onClick={() => setDlg('rate')}>نرخ جدید</Button>} bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="نرخ‌ها"><thead><tr><th>مورد</th><th className="n">نرخ</th><th>از تاریخ</th></tr></thead><tbody>{(rates.data ?? []).map((r, i) => <tr key={i}><td>{r.description || r.category}</td><td className="n">{toFa(r.rate_bp / 100)}٪</td><td className="n">{showDate(isoToJalali(r.effective_from))}</td></tr>)}</tbody></table></div>
          <div className="cb"><div className="sub">نسخه‌ها تغییرناپذیرند؛ تغییر نرخ یعنی نسخهٔ جدید با تاریخ شروع.</div></div>
        </Card>
        <Card title="محاسبهٔ مالیات (پیش‌نمایش)">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="مبلغ پایه (ریال)">{(id) => <Input id={id} w="amt" num value={base} onChange={(e) => setBase(e.target.value)} />}</Field>
              <Field label="تاریخ">{(id) => <Input id={id} w="date" num value={on} onChange={(e) => setOn(e.target.value)} />}</Field>
              <Button variant="pri" disabled={!(val.amount(base) > 0)} onClick={async () => { try { setCalc(await vat.compute(val.amount(base), undefined, val.iso(on))) } catch (e) { toast.error(e) } }}>محاسبه</Button>
            </div>
            <dl className="kv" style={{ margin: 0 }}><dt>مالیات</dt><dd className="n">{calc ? fmt(calc.tax) : '—'}</dd><dt>جمع</dt><dd className="n">{calc ? fmt(calc.total) : '—'}</dd></dl>
          </div>
        </Card>
        <Card title="معافیت‌ها" extra={<Button small onClick={() => setDlg('exempt')}>معافیت جدید</Button>} bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="معافیت‌ها"><thead><tr><th>مورد</th><th>از</th><th>تا</th></tr></thead><tbody>{(exempt.data ?? []).map((x, i) => <tr key={i}><td>{x.description}</td><td className="n">{showDate(isoToJalali(x.effective_from))}</td><td className="n">{x.effective_to ? showDate(isoToJalali(x.effective_to)) : '—'}</td></tr>)}</tbody></table></div>
        </Card>
        <Card title="تخصیص مالیات خرید (نسبت)">
          <InlineForm submitLabel="ثبت تخصیص" rows={[['year', 'period'], ['dt', 'de'], ['mach', 'common'], ['ts', 'es']]} fields={[{ name: 'year', label: 'سال', kind: 'number', w: 'sm', required: true, initial: jalaliToday().slice(0, 4) }, { name: 'period', label: 'ماه (دوره)', kind: 'number', w: 'sm', required: true, initial: String(Number(jalaliToday().split('/')[1])) }, { name: 'dt', label: 'خرید مستقیم مشمول', kind: 'amount', w: 'amt' }, { name: 'de', label: 'خرید مستقیم معاف', kind: 'amount', w: 'amt' }, { name: 'mach', label: 'ماشین‌آلات', kind: 'amount', w: 'amt' }, { name: 'common', label: 'مشترک', kind: 'amount', w: 'amt' }, { name: 'ts', label: 'فروش مشمول', kind: 'amount', w: 'amt' }, { name: 'es', label: 'فروش معاف', kind: 'amount', w: 'amt' }]}
            onSubmit={(v) => vat.apportion({ jalali_year: val.int(v.year), period: val.int(v.period), period_months: 1, direct_taxable: val.amount(v.dt), direct_exempt: val.amount(v.de), machinery: val.amount(v.mach), common: val.amount(v.common), taxable_sales: val.amount(v.ts), exempt_sales: val.amount(v.es) })} onDone={() => toast.push({ tone: 'ok', title: 'تخصیص ثبت شد' })}>
            <Message tone="info" title="">مالیات خرید مشترک بر پایهٔ نسبت فروش مشمول به کل تخصیص می‌یابد و بخش غیرقابل کسر به هزینه منتقل می‌شود.</Message>
          </InlineForm>
        </Card>
        <Card title="اصلاحیهٔ اظهارنامه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label="دورهٔ مرجع">{() => <Select label="دورهٔ مرجع" width="260px" value={chosen?.id ?? ''} options={rs.map((r) => ({ value: r.id, label: retLabel(r) }))} onChange={setRet} />}</Field>
            {pen.data && pen.data.penalty > 0 && <Message tone="warn" title={`جریمهٔ پیش‌بینی‌شده: ${fmt(pen.data.penalty)} ریال`}>بر پایهٔ {toFa(pen.data.months_late)} ماه دیرکرد. مبلغ نهایی را مرجع مالیاتی تعیین می‌کند.</Message>}
            <InlineForm submitLabel="ثبت اصلاحیه" fields={[{ name: 'reason', label: 'دلیل', required: true }]} onSubmit={(v, key) => { void v; return vat.amend({ jalali_year: chosen!.jalali_year, period: chosen!.period, period_months: chosen!.period_months }, key) }} onDone={() => (toast.push({ tone: 'ok', title: 'اصلاحیه ثبت شد' }), returns.reload())} />
          </div>
        </Card>
        <Card title="پرداخت و استرداد">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Seg<'pay' | 'refund'> label="نوع" value={mode} onChange={setMode} options={[{ value: 'pay', label: 'پرداخت مالیات' }, { value: 'refund', label: 'درخواست استرداد' }]} />
            <Field label="از حساب بانکی">{() => <Select label="حساب بانکی" width="220px" value={bank || bankOpts[0]?.value || ''} options={bankOpts} onChange={setBank} />}</Field>
            <InlineForm key={mode} submitLabel={mode === 'pay' ? 'ثبت پرداخت' : 'ثبت استرداد'} fields={[{ name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'date', label: 'تاریخ', kind: 'date', w: 'date', initial: showDate(jalaliToday()) }]}
              onSubmit={(v, key) => (mode === 'pay' ? vat.pay : vat.refund)({ amount: val.amount(v.amount), account: bank || bankOpts[0]?.value, date: val.iso(v.date) }, key)} onDone={() => toast.push({ tone: 'ok', title: 'ثبت شد' })} />
          </div>
        </Card>
      </div>
      {dlg === 'rate' && <FormDialog title="نرخ جدید" submitLabel="ثبت نرخ" fields={[{ name: 'cat', label: 'گروه', required: true, kind: 'latin', w: 'md', initial: 'general' }, { name: 'desc', label: 'شرح', required: true }, { name: 'rate', label: 'نرخ (درصد)', kind: 'number', required: true, w: 'sm' }, { name: 'from', label: 'از تاریخ', kind: 'date', required: true, w: 'date' }, { name: 'src', label: 'منبع', required: true }]} onSubmit={(v) => vat.addRate({ category: val.text(v.cat), description: val.text(v.desc), rate_bp: Math.round(Number(val.int(v.rate)) * 100), effective_from: val.iso(v.from)!, verified: false, source: val.text(v.src) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), rates.reload())} />}
      {dlg === 'exempt' && <FormDialog title="معافیت جدید" submitLabel="ثبت معافیت" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'desc', label: 'شرح', required: true }, { name: 'art', label: 'ماده قانون', required: true }, { name: 'from', label: 'از تاریخ', kind: 'date', required: true, w: 'date' }, { name: 'src', label: 'منبع', required: true }]} onSubmit={(v) => vat.addExemption({ code: val.text(v.code), kind: 'goods', description: val.text(v.desc), article: val.text(v.art), effective_from: val.iso(v.from)!, source: val.text(v.src) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), exempt.reload())} />}
    </>
  )
}
