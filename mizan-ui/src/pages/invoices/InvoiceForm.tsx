import { useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { invoices } from '@/api/sales'
import { ErrorMessage, Field, Input, Message, PageBar, Button, Select, cx, useToast } from '@/ui'
import { fmt, groupDigits, parseAmount, toFa } from '@/shared/digits'
import { showDate, jalaliToday, jalaliToIso } from '@/shared/jalali'
import { newIdempotencyKey } from '@/shared/idempotency'
import { couponApi } from '@/api/commerce'

interface Line { d: string; q: string; p: string }
const VAT = [{ value: '1000', label: '۱۰٪ (استاندارد)' }, { value: '0', label: 'معاف' }]

/** New invoice: draft or issue right away. VAT, coupon discount and totals are shown live; the server recomputes them. */
export default function InvoiceForm() {
  const navigate = useNavigate()
  const toast = useToast()
  const [customer, setCustomer] = useState('')
  const [date, setDate] = useState(showDate(jalaliToday()))
  const [coupon, setCoupon] = useState('')
  const [tax, setTax] = useState('1000')
  const [lines, setLines] = useState<Line[]>([{ d: '', q: '1', p: '' }])
  const [error, setError] = useState<unknown>()
  const [issued, setIssued] = useState<{ number: number | null } | null>(null)
  const [busy, setBusy] = useState(false)
  const [disc, setDisc] = useState<{ amount: number; bad: boolean }>({ amount: 0, bad: false })
  const key = useRef<{ fp: string; key: string } | null>(null)

  const num = (v: string) => { const n = parseAmount(v); return Number.isNaN(n) ? 0 : n }
  const sum = lines.reduce((s, l) => s + num(l.q) * num(l.p), 0)
  const net = sum - disc.amount
  const vat = Math.round((net * Number(tax)) / 10000)
  const set = (i: number, patch: Partial<Line>) => setLines((ls) => ls.map((l, k) => (k === i ? { ...l, ...patch } : l)))
  const bad = useMemo(() => lines.some((l) => l.d.trim() && (num(l.q) <= 0 || num(l.p) < 0)), [lines])

  const checkCoupon = async () => {
    const c = coupon.trim()
    if (!c || sum <= 0) return setDisc({ amount: 0, bad: false })
    try {
      const r = await couponApi.preview(c, customer.trim() || 'anonymous', sum)
      setDisc({ amount: r.discount, bad: false })
    } catch {
      setDisc({ amount: 0, bad: true })
    }
  }

  const submit = async (issue: boolean) => {
    setBusy(true)
    setError(undefined)
    try {
      const items = lines.filter((l) => l.d.trim()).map((l, i) => ({ description: l.d.trim(), quantity: num(l.q), unit_price: num(l.p), tax_bp: Number(tax), ...(i === 0 && disc.amount ? { discount: disc.amount } : {}) }))
      const body = { customer_ref: customer.trim(), buyer: { name: customer.trim() }, items, ...(jalaliToIso(date) ? { due_date: jalaliToIso(date)! } : {}), issue }
      const fp = JSON.stringify(body)
      if (key.current?.fp !== fp) key.current = { fp, key: newIdempotencyKey('invoice') }
      const inv = await invoices.create(body, key.current.key)
      key.current = null
      if (disc.amount && coupon.trim()) {
        try {
          await couponApi.redeem({ code: coupon.trim(), customer_ref: customer.trim(), amount: sum, subject_type: 'invoice', subject_id: inv.id }, newIdempotencyKey('coupon'))
        } catch (e) {
          toast.error(e)
        }
      }
      if (issue) { setIssued({ number: inv.number }); setLines([{ d: '', q: '1', p: '' }]); setCustomer('') } else { toast.push({ tone: 'ok', title: 'پیش‌نویس ذخیره شد' }); navigate('/invoices') }
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }
  const ok = customer.trim() && sum > 0 && !bad && !busy

  return (
    <>
      <PageBar title="فاکتور جدید" sub="پیش‌نویس · پس از «صدور» شماره می‌گیرد و حسابداری ثبت می‌شود">
        <Button disabled={!ok} onClick={() => void submit(false)}>ذخیرهٔ پیش‌نویس</Button>
        <Button variant="pri" disabled={!ok} onClick={() => void submit(true)}>صدور فاکتور</Button>
      </PageBar>
      {issued && <Message tone="ok" title={`فاکتور ${issued.number ? toFa(issued.number) : ''} صادر شد`}>سند حسابداری ثبت شد. می‌توانید وجه را دریافت کنید یا برای مودیان در صف بگذارید.</Message>}
      {error != null && <ErrorMessage error={error} />}
      <div className="card"><div className="cb"><div className="row">
        <Field label="مشتری" required>{(id) => <Input id={id} w="md" value={customer} onChange={(e) => setCustomer(e.target.value)} />}</Field>
        <Field label="تاریخ سررسید">{(id) => <Input id={id} w="date" num value={date} onChange={(e) => setDate(e.target.value)} />}</Field>
        <Field label="کد کوپن">{(id) => <Input id={id} w="code" num placeholder="اختیاری" value={coupon} onChange={(e) => setCoupon(e.target.value.trim())} onBlur={() => void checkCoupon()} />}</Field>
        <Field label="نرخ مالیات">{() => <Select label="نرخ مالیات" width="130px" value={tax} options={VAT} onChange={setTax} />}</Field>
      </div></div></div>
      <div className="tw">
        <table className="tbl" aria-label="اقلام فاکتور">
          <thead><tr><th className="ck">#</th><th>شرح کالا / خدمت</th><th className="n" style={{ width: 90 }}>تعداد</th><th className="n" style={{ width: 160 }}>قیمت واحد (ریال)</th><th className="n" style={{ width: 160 }}>جمع (ریال)</th><th style={{ width: 40 }} /></tr></thead>
          <tbody>
            {lines.map((l, i) => (
              <tr key={i}>
                <td className="ck mut">{toFa(i + 1)}</td>
                <td className="ed"><input className="cell" aria-label="شرح" value={l.d} onChange={(e) => set(i, { d: e.target.value })} /></td>
                <td className="ed"><input className="cell n" aria-label="تعداد" value={l.q} onChange={(e) => set(i, { q: e.target.value })} /></td>
                <td className="ed"><input className={cx('cell n')} aria-label="قیمت واحد" value={l.p} onChange={(e) => set(i, { p: e.target.value })} onBlur={() => set(i, { p: groupDigits(l.p) })} /></td>
                <td className="n">{fmt(num(l.q) * num(l.p))}</td>
                <td className="ed"><Button small aria-label="حذف ردیف" onClick={() => lines.length > 1 && setLines(lines.filter((_, k) => k !== i))}>×</Button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="bar">
        <Button onClick={() => setLines([...lines, { d: '', q: '1', p: '' }])}>افزودن ردیف</Button>
        <span className="grow" />
        <dl className="kv" style={{ margin: 0, gridTemplateColumns: '140px 160px', textAlign: 'left' }}>
          <dt>جمع</dt><dd className="n">{fmt(sum)}</dd>
          <dt>تخفیف کوپن</dt><dd className="n neg">{disc.amount ? `−${fmt(disc.amount)}` : '—'}</dd>
          <dt>مالیات {toFa(Number(tax) / 100)}٪</dt><dd className="n">{fmt(vat)}</dd>
          <dt><b>مبلغ قابل پرداخت</b></dt><dd className="n"><b>{fmt(net + vat)}</b></dd>
        </dl>
      </div>
      {disc.bad && <Message tone="err" title="کوپن معتبر نیست">کد واردشده منقضی شده یا سقف استفاده‌اش پر شده است.</Message>}
      <div className="sub">تخفیف کوپن روی ردیف اول اعمال و استفادهٔ کوپن با ثبت فاکتور ثبت می‌شود.</div>
    </>
  )
}
