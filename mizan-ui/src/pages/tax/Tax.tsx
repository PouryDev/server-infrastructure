import { useState } from 'react'
import { Link } from 'react-router-dom'
import { einvoiceApi, incomeTax } from '@/api/tax'
import { useFiscal } from '@/app/Fiscal'
import { fmt, toFa } from '@/shared/digits'
import { Badge, Button, Card, ErrorMessage, KV, Message, PageBar, Skeleton, SubTabs, useResource, useToast } from '@/ui'

const ST: Record<string, { l: string; t: 'ok' | 'warn' | 'err' | 'info' | '' }> = { queued: { l: 'در صف', t: 'info' }, sent: { l: 'ارسال‌شده', t: 'warn' }, accepted: { l: 'تأییدشده', t: 'ok' }, confirmed: { l: 'تأییدشده', t: 'ok' }, rejected: { l: 'رد شد', t: 'err' }, failed: { l: 'ناموفق', t: 'err' } }

/** Income-tax preview next to the e-invoice (Moadian) queue: send the queue, refresh statuses, requeue a rejected one. */
export default function Tax() {
  const toast = useToast()
  const { year } = useFiscal()
  const [tab, setTab] = useState<'it' | 'ei'>('it')
  const prev = useResource(async () => (year ? incomeTax.preview(year.id) : undefined), [year?.id])
  const q = useResource(() => einvoiceApi.list({ page_size: '50', sort: '-created_at' }), [])
  const p = prev.data
  const run = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast.push({ tone: 'ok', title: msg }); q.reload() } catch (e) { toast.error(e) } }
  return (
    <>
      <PageBar title="مالیات بر درآمد و صورتحساب الکترونیکی" />
      <SubTabs<'it' | 'ei'> value={tab} onChange={setTab} options={[{ value: 'it', label: 'مالیات بر درآمد' }, { value: 'ei', label: 'صورتحساب الکترونیکی (مودیان)' }]} />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        {tab === 'it' && (
          <Card title={`پیش‌نمایش مالیات سال ${toFa(year?.name ?? '')}`}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {prev.error != null ? <ErrorMessage error={prev.error} onRetry={prev.reload} /> : !p ? <Skeleton rows={4} /> : (
                <KV rows={[['سود حسابداری', <span className="n">{fmt(p.accounting_profit)}</span>], ['تعدیلات', <span className="n">{fmt(p.add_back_total - p.deduction_total + p.depreciation_adjustment)}</span>], ['زیان انتقالی', <span className="n">{fmt(-p.loss_used_total)}</span>], [<b key="k">مالیات محاسبه‌شده</b>, <b key="v" className="n">{fmt(p.tax)}</b>]]} />
              )}
              <Message tone="warn" title="">نرخ‌ها و معافیت‌ها از جدول قوانین می‌آیند. پیش از قطعی‌کردن، با مشاور مالیاتی بررسی کنید.</Message>
              <div className="bar"><Button onClick={prev.reload}>پیش‌نمایش</Button><Link className="btn pri" to="/income-tax">قطعی‌کردن برگ تشخیص</Link></div>
            </div>
          </Card>
        )}
        <Card title="صف صورتحساب الکترونیکی" extra={<><Button small onClick={() => void run(async () => { const r = await einvoiceApi.send(); toast.push({ tone: 'info', title: `${toFa(r.sent)} صورتحساب ارسال شد` }) }, 'صف ارسال شد')}>ارسال صف</Button><Button small onClick={() => void run(async () => { await einvoiceApi.refresh() }, 'وضعیت‌ها به‌روز شد')}>به‌روزرسانی وضعیت</Button></>} bare className={tab === 'ei' ? 'span2' : undefined}>
          {q.error != null ? <div className="cb"><ErrorMessage error={q.error} onRetry={q.reload} /></div> : !q.data ? <Skeleton /> : (
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="صف صورتحساب الکترونیکی"><thead><tr><th>فاکتور</th><th>وضعیت ارسال</th><th>شناسهٔ مالیاتی</th><th /></tr></thead>
                <tbody>{q.data.items.map((e) => <tr key={e.id}><td className="n">{e.source_id.slice(0, 8)}</td><td><Badge tone={ST[e.status]?.t}>{ST[e.status]?.l ?? e.status}</Badge>{e.errors?.[0] && <span className="sub"> {e.errors[0].message}</span>}</td><td className="n mut">{e.reference_number ?? '—'}</td><td>{(e.status === 'rejected' || e.status === 'failed') && <Button small onClick={() => void run(() => einvoiceApi.requeue(e.id), 'دوباره در صف قرار گرفت')}>صف مجدد</Button>}</td></tr>)}</tbody></table>
              {q.data.items.length === 0 && <div className="empty"><b>صف خالی است</b><span>از صفحهٔ «سامانهٔ مودیان» فاکتور را برای ارسال در صف بگذارید.</span></div>}
            </div>
          )}
        </Card>
      </div>
    </>
  )
}
