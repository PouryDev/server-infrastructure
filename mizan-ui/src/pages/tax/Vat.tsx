import { useMemo, useState } from 'react'
import { vat, type VatReturn } from '@/api/vat'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, isoToJalali, jalaliToday, showDate } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, Input, Message, PageBar, Seg, Skeleton, Stat, SubTabs, useResource, useToast } from '@/ui'
import { InlineForm, val } from '@/ui/FormDialog'
import { Link, useNavigate } from 'react-router-dom'

const nowJ = () => jalaliToday().split('/').map(Number) as [number, number, number]

/** VAT: position, preview and filing of a monthly period, the returns list. */
export default function Vat() {
  const toast = useToast()
  const nav = useNavigate()
  const [jy, jm] = nowJ()
  const [year, setYear] = useState(String(jy))
  const [month, setMonth] = useState(jm)
  const y = Number(year)
  const preview = useResource(async () => (Number.isInteger(y) && y > 1300 ? vat.preview(y, month, 1) : undefined), [y, month])
  const returns = useResource(() => vat.returns(), [])
  const pos = useResource(() => vat.position(), [])
  const f = preview.data
  const filedFor = useMemo(() => new Set((returns.data?.items ?? []).map((r: VatReturn) => `${r.jalali_year}/${r.period}`)), [returns.data])
  const filed = filedFor.has(`${y}/${month}`)
  const days = f?.due_on ? Math.round((new Date(f.due_on).getTime() - Date.now()) / 864e5) : undefined

  return (
    <>
      <PageBar title="مالیات بر ارزش افزوده">
        <Link className="btn" to="/vat/rules">اصلاحیه</Link>
        <Button variant="pri" disabled={!f || filed} onClick={() => document.getElementById('file-return')?.scrollIntoView({ behavior: 'smooth' })}>ثبت اظهارنامه دورهٔ {MONTHS[month - 1]}</Button>
      </PageBar>
      <SubTabs<'status' | 'rules' | 'apportion'> value="status" onChange={(v) => v !== 'status' && nav('/vat/rules')} options={[{ value: 'status', label: 'وضعیت و اظهارنامه‌ها' }, { value: 'rules', label: 'نرخ‌ها و معافیت‌ها' }, { value: 'apportion', label: 'تخصیص (نسبت)' }]} />
      <div className="row">
        <Field label="سال">{(id) => <Input id={id} w="sm" num value={year} onChange={(e) => setYear(e.target.value)} />}</Field>
        <Field label="ماه (دورهٔ ماهانه)">{() => <Seg<string> label="ماه" value={String(month)} onChange={(v) => setMonth(Number(v))} options={MONTHS.map((n, i) => ({ value: String(i + 1), label: n }))} />}</Field>
      </div>
      {preview.error != null && <ErrorMessage error={preview.error} onRetry={preview.reload} />}
      <div className="grid g4">
        <Stat label={`مالیات فروش (${MONTHS[month - 1]})`} value={f ? fmt(f.output_tax) : '…'} />
        <Stat label="مالیات خرید قابل کسر" value={f ? fmt(f.input_tax) : '…'} />
        <Stat label="قابل پرداخت" value={f ? fmt(f.net) : '…'} note={pos.data ? `مانده حساب تسویه: ${fmt(pos.data.balance)}` : undefined} />
        <Stat label="مهلت" value={<span style={{ fontSize: 18 }}>{f?.due_on_jalali ? showDate(f.due_on_jalali) : '—'}</span>} note={days != null ? (days >= 0 ? `${toFa(days)} روز مانده` : `${toFa(-days)} روز گذشته`) : undefined} />
      </div>
      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title="اظهارنامه‌ها" bare>
          {returns.error != null ? <div className="cb"><ErrorMessage error={returns.error} onRetry={returns.reload} /></div> : !returns.data ? <Skeleton /> : (
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="اظهارنامه‌ها"><thead><tr><th>دوره</th><th className="n">قابل پرداخت</th><th>ثبت</th><th>وضعیت</th></tr></thead>
                <tbody>{returns.data.items.map((r) => <tr key={r.id}><td>{MONTHS[(r.period - 1) % 12]} {toFa(r.jalali_year)}</td><td className="n">{fmt(r.net)}</td><td>{showDate(isoToJalali(r.filed_on))}</td><td><Badge tone="ok">ثبت‌شده</Badge></td></tr>)}</tbody></table>
              {returns.data.items.length === 0 && <div className="empty"><b>هنوز اظهارنامه‌ای ثبت نشده</b></div>}
            </div>
          )}
        </Card>
        <Card title={`ثبت اظهارنامهٔ ${MONTHS[month - 1]}`}>
          <div id="file-return" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Message tone="warn" title="">پس از ثبت، فاکتورهای این دوره قفل می‌شوند. تغییر بعدی فقط با اصلاحیه ممکن است.</Message>
            {filed && <Message tone="info" title="این دوره قبلاً ثبت شده است">برای تغییر از «اصلاحیه» استفاده کنید.</Message>}
            <InlineForm submitLabel="ثبت اظهارنامه" fields={[{ name: 'on', label: 'تاریخ ثبت', kind: 'date', w: 'date', initial: showDate(jalaliToday()) }]} onSubmit={(v, key) => vat.file({ jalali_year: y, period: month, period_months: 1, filed_on: val.date(v.on) }, key)} onDone={() => (toast.push({ tone: 'ok', title: 'اظهارنامه ثبت شد' }), returns.reload(), pos.reload())} />
            <Link className="btn" to="/vat/rules">پرداخت مالیات و سایر عملیات</Link>
          </div>
        </Card>
      </div>
    </>
  )
}
