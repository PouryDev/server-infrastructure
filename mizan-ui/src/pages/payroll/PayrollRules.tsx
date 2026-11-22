import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { payroll, type RuleVersion } from '@/api/payroll'
import { treasury } from '@/api/treasury'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday, showDate } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, KV, Message, PageBar, Select, useResource, useToast } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'

/** Payroll rules (versioned data), tax brackets, monthly pay run, tax/insurance remittance. */
export default function PayrollRules() {
  const toast = useToast()
  const rules = useResource(async () => itemsOf(await payroll.rules()), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const rems = useResource(async () => itemsOf(await payroll.remittances()), [])
  const [sel, setSel] = useState<RuleVersion>()
  const [dlg, setDlg] = useState(false)
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const [month, setMonth] = useState(jm)
  const [bank, setBank] = useState('')
  const bankId = bank || banks.data?.[0]?.id
  const opts = (banks.data ?? []).map((b) => ({ value: b.id, label: b.name }))
  const today = new Date().toISOString().slice(0, 10)
  const used = (r: RuleVersion, i: number, all: RuleVersion[]) => r.effective_from <= today && !all.some((o) => o.effective_from > r.effective_from && o.effective_from <= today) && i >= 0
  const cur = sel ?? rules.data?.find((r, i, a) => used(r, i, a))

  return (
    <>
      <PageBar title="قوانین حقوق، پرداخت و بیمه" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="نسخه‌های قانون" extra={<Button small onClick={() => setDlg(true)}>نسخهٔ جدید</Button>} bare>
          {rules.error != null ? <div className="cb"><ErrorMessage error={rules.error} onRetry={rules.reload} /></div> : (
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="نسخه‌های قانون"><thead><tr><th>از تاریخ</th><th className="n">حداقل مزد روزانه</th><th className="n">سهم کارمند</th><th>وضعیت</th><th /></tr></thead>
                <tbody>{(rules.data ?? []).map((r, i, all) => <tr key={r.id} tabIndex={0} className={r.id === cur?.id ? 'sel' : undefined} onClick={() => setSel(r)} style={{ cursor: 'pointer' }}><td className="n">{showDate(r.effective_from_jalali)}</td><td className="n">{fmt(r.floor_daily)}</td><td className="n">{toFa(r.employee_bp / 100)}٪</td><td>{r.effective_from > today ? <Badge tone="info">آینده</Badge> : used(r, i, all) ? <Badge tone="ok">در حال استفاده</Badge> : <Badge>قدیمی</Badge>}</td><td>{r.effective_from > today && <Button small variant="dng" onClick={async (e) => { e.stopPropagation(); try { await payroll.deleteRule(r.id); rules.reload() } catch (err) { toast.error(err) } }}>حذف</Button>}</td></tr>)}</tbody></table>
            </div>
          )}
          <div className="cb"><div className="sub">نسخهٔ استفاده‌شده در فیش حذف نمی‌شود؛ ماه‌های گذشته تغییر نمی‌کنند. اعداد شروع (۱۴۰۵) از منابع ثانویه‌اند و باید با بخشنامه‌های رسمی تطبیق داده شوند.</div></div>
        </Card>
        <Card title="پلکان مالیات ماهانه" bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="پلکان مالیات"><thead><tr><th>تا سقف</th><th className="n">نرخ</th></tr></thead>
            <tbody>{(cur?.brackets ?? []).map((b, i) => <tr key={i}><td className="n">{b.up_to ? fmt(b.up_to) : 'بیشتر'}</td><td className="n">{toFa(b.rate_bp / 100)}٪{i === 0 && b.rate_bp === 0 ? ' (معافیت)' : ''}</td></tr>)}</tbody></table></div>
        </Card>
        <Card title="پرداخت حقوق ماه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="ماه">{() => <Select label="ماه" width="130px" value={String(month)} options={MONTHS.map((n, i) => ({ value: String(i + 1), label: n }))} onChange={(v) => setMonth(Number(v))} />}</Field>
              <Field label="از حساب بانکی">{() => <Select label="حساب بانکی" width="220px" value={bankId ?? ''} options={opts} onChange={setBank} />}</Field>
            </div>
            <InlineForm submitLabel="پرداخت" fields={[]} onSubmit={(_, key) => payroll.run({ year: jy, month, bank_account_id: bankId }, key)} onDone={() => toast.push({ tone: 'ok', title: 'پرداخت حقوق انجام شد' })} />
            <div className="sub">فیش‌های صادرشدهٔ ماه پرداخت می‌شوند (از صفحهٔ «فیش‌های حقوقی» می‌توان فقط چند فیش را انتخاب کرد).</div>
          </div>
        </Card>
        <Card title="پرداخت مالیات و بیمه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <InlineForm submitLabel="ثبت پرداخت" rows={[['kind', 'amount', 'ref']]} fields={[{ name: 'kind', label: 'نوع', kind: 'select', options: [{ value: 'tax', label: 'مالیات حقوق' }, { value: 'insurance', label: 'بیمه' }], initial: 'tax', w: 'sm' }, { name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'ref', label: 'شمارهٔ پیگیری', kind: 'latin', w: 'md' }]}
              onSubmit={(v, key) => payroll.remit({ kind: v.kind as 'tax' | 'insurance', amount: val.amount(v.amount), period_year: jy, period_month: month, bank_account_id: bankId, reference: val.text(v.ref) || undefined }, key)} onDone={() => (rems.reload(), toast.push({ tone: 'ok', title: 'پرداخت ثبت شد' }))} />
            <KV rows={(rems.data ?? []).slice(0, 4).map((r) => [r.kind === 'tax' ? 'مالیات' : 'بیمه', <span key={r.id} className="n">{fmt(r.amount)}</span>])} />
            <Message tone="info" title="">فایل‌های تأمین اجتماعی از صفحهٔ «فیش‌های حقوقی» ساخته می‌شود.</Message>
          </div>
        </Card>
      </div>
      {dlg && <FormDialog title="نسخهٔ جدید قانون" submitLabel="ثبت نسخه" width={560} intro="قوانین به‌صورت داده نسخه‌دار ذخیره می‌شوند و تغییرناپذیرند. پلکان مالیات و جزئیات ساعات اضافه‌کار از نسخهٔ فعلی کپی می‌شود." fields={[{ name: 'name', label: 'نام', required: true }, { name: 'from', label: 'از تاریخ', kind: 'date', required: true, w: 'date' }, { name: 'emp', label: 'سهم کارمند (٪)', kind: 'number', required: true, w: 'sm', initial: String((cur?.employee_bp ?? 700) / 100) }, { name: 'er', label: 'سهم کارفرما (٪)', kind: 'number', required: true, w: 'sm', initial: String((cur?.employer_bp ?? 2000) / 100) }, { name: 'un', label: 'بیمه بیکاری (٪)', kind: 'number', required: true, w: 'sm', initial: String((cur?.unemployment_bp ?? 300) / 100) }, { name: 'ceil', label: 'سقف مزد روزانهٔ بیمه (ریال)', kind: 'amount', required: true, w: 'amt', initial: fmt(cur?.ceiling_daily) }, { name: 'floor', label: 'حداقل مزد روزانه (ریال)', kind: 'amount', required: true, w: 'amt', initial: fmt(cur?.floor_daily) }, { name: 'src', label: 'منبع', required: true }]}
        onSubmit={(v) => payroll.addRule({ name: val.text(v.name), effective_from: val.iso(v.from)!, employee_bp: Math.round(Number(val.int(v.emp)) * 100), employer_bp: Math.round(Number(val.int(v.er)) * 100), unemployment_bp: Math.round(Number(val.int(v.un)) * 100), ceiling_daily: val.amount(v.ceil), floor_daily: val.amount(v.floor), deduct_employee_insurance: cur?.deduct_employee_insurance ?? true, brackets: cur?.brackets ?? [], terms: cur?.terms ?? {}, source: val.text(v.src) })} onClose={() => setDlg(false)} onDone={() => (setDlg(false), rules.reload(), toast.push({ tone: 'ok', title: 'نسخه ثبت شد' }))} />}
    </>
  )
}
