import { useState } from 'react'
import { Link } from 'react-router-dom'
import { payments, type Payment, type Refund } from '@/api/payments'
import { paymentOps } from '@/api/sales'
import { useProfile } from '@/auth/AuthContext'
import { paymentStatus, refundMethod, refundStatus } from '@/domain/status'
import { fmt, toFa } from '@/shared/digits'
import { showDateTime } from '@/shared/jalali'
import { Badge, Button, DataTable, ErrorMessage, MasterDetail, Message, PageBar, SubTabs, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

type Tab = 'all' | 'unsettled' | 'refunds'
const short = (id: string) => id.slice(0, 8)

/** Payments, payments paid but not settled, and the manual refund queue (claim → pay by card-to-card → record). */
export default function Payments() {
  const toast = useToast()
  const me = useProfile()
  const [tab, setTab] = useState<Tab>('refunds')
  const list = useListState({}, 20)
  const [active, setActive] = useState<Refund>()
  const [dialog, setDialog] = useState<'complete' | 'fail' | null>(null)
  const [sweepRes, setSweepRes] = useState<string>()

  const pays = useResource((s) => payments.list({ page: list.page, pageSize: list.pageSize, sort: '-created_at', filter: Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v)) }, s), [tab === 'all', list.page, JSON.stringify(list.filters)])
  const unsettled = useResource(() => payments.unsettled(), [tab === 'unsettled'])
  const refunds = useResource((s) => payments.refunds({ pageSize: 50, sort: 'created_at', filter: { method: 'manual_card_to_card', status: ['in', 'pending,processing'] } }, s), [tab === 'refunds'])
  const counts = useResource(async () => ({ u: (await payments.unsettled()).length, r: (await payments.refunds({ pageSize: 1, filter: { method: 'manual_card_to_card', status: ['in', 'pending,processing'] } })).meta.total ?? 0 }), [])

  const payCols: Column<Payment>[] = [
    { id: 'id', header: 'شناسه', num: true, cell: (p) => <Link to={`/payments/details?id=${p.id}`} style={{ color: 'var(--pri)' }}>{short(p.id)}</Link> },
    { id: 'purpose', header: 'بابت', filter: 'purpose_type', cell: (p) => `${p.purpose_type} ${p.purpose_id.slice(0, 8)}` },
    { id: 'amount', header: 'مبلغ (ریال)', num: true, cell: (p) => fmt(p.amount) },
    { id: 'gateway', header: 'درگاه', filter: 'gateway', cell: (p) => p.gateway },
    { id: 'status', header: 'وضعیت', filter: 'status', cell: (p) => <Badge tone={paymentStatus(p.status).tone}>{paymentStatus(p.status).label}</Badge> },
    { id: 'at', header: 'زمان', mut: true, cell: (p) => showDateTime(p.created_at) },
  ]
  const refundCols: Column<Refund>[] = [
    { id: 'id', header: 'شناسه', num: true, cell: (r) => short(r.id) },
    { id: 'pay', header: 'پرداخت', num: true, mut: true, cell: (r) => short(r.payment_id) },
    { id: 'amount', header: 'مبلغ (ریال)', num: true, cell: (r) => fmt(r.amount) },
    { id: 'method', header: 'روش', cell: (r) => refundMethod(r.method) },
    { id: 'status', header: 'وضعیت', cell: (r) => <Badge tone={refundStatus(r.status).tone}>{refundStatus(r.status).label}</Badge> },
    { id: 'who', header: 'مسئول', mut: true, cell: (r) => (r.claimed_by ? (r.claimed_by === me.username ? 'شما' : r.claimed_by) : '—') },
  ]

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      toast.push({ tone: 'ok', title: ok })
      refunds.reload()
      counts.reload()
      setActive(undefined)
    } catch (e) {
      toast.error(e)
    }
  }
  const a = active
  const mine = a?.claimed_by === me.username

  return (
    <>
      <PageBar title="پرداخت‌ها و بازگشت وجه">
        <Button onClick={async () => { try { const r = await paymentOps.sweep(); setSweepRes(`${toFa(r.paid)} پرداخت پیدا و تسویه شد، ${toFa(r.expired)} پرداخت منقضی شد.`); unsettled.reload() } catch (e) { toast.error(e) } }}>بازیابی پرداخت‌های گم‌شده</Button>
      </PageBar>
      {sweepRes && <Message tone="ok" title="اجرای بازیابی">{sweepRes}</Message>}
      <SubTabs<Tab> value={tab} onChange={(t) => (setTab(t), setActive(undefined))} options={[{ value: 'all', label: 'پرداخت‌ها' }, { value: 'unsettled', label: `تسویه‌نشده${counts.data?.u ? ` (${toFa(counts.data.u)})` : ''}` }, { value: 'refunds', label: `صف بازگشت وجه${counts.data?.r ? ` (${toFa(counts.data.r)})` : ''}` }]} />

      {tab === 'all' && <DataTable caption="پرداخت‌ها" columns={payCols} rows={pays.data?.items} rowKey={(p) => p.id} loading={pays.loading} error={pays.error} onRetry={pays.reload} empty={{ title: 'هنوز پرداختی ثبت نشده' }} filters={list.filters} onFilters={list.setFilters} meta={pays.data?.meta} onPage={list.setPage} />}
      {tab === 'unsettled' && <DataTable caption="پرداخت‌های تسویه‌نشده" columns={payCols} rows={unsettled.data} rowKey={(p) => p.id} loading={unsettled.loading} error={unsettled.error} onRetry={unsettled.reload} empty={{ title: 'پرداخت تسویه‌نشده‌ای نیست', text: 'همهٔ پرداخت‌های موفق تسویه شده‌اند.' }} />}
      {tab === 'refunds' && (
        <MasterDetail
          listBasis={520}
          list={<DataTable caption="صف بازگشت وجه" columns={refundCols} rows={refunds.data?.items} rowKey={(r) => r.id} loading={refunds.loading} error={refunds.error} onRetry={refunds.reload} empty={{ title: 'صف بازگشت وجه خالی است' }} onRowClick={(r) => setActive(r)} rowClass={(r) => (r.id === a?.id ? 'sel' : undefined)} />}
          detail={
            a ? (
              <>
                <div className="ch">بازگشت وجه {short(a.id)}<Badge tone={refundStatus(a.status).tone}>{refundStatus(a.status).label}</Badge></div>
                <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                  <Message tone="info" title="">وجه را با کارت‌به‌کارت از اپ بانک پرداخت کنید؛ سپس شمارهٔ پیگیری را اینجا ثبت کنید. شمارهٔ کامل کارت هرگز ذخیره نمی‌شود.</Message>
                  <dl className="kv" style={{ margin: 0 }}><dt>مبلغ</dt><dd className="n">{fmt(a.amount)} ریال</dd><dt>دلیل</dt><dd>{a.reason}</dd></dl>
                  <div className="bar">
                    {a.status === 'pending' && <Button variant="pri" onClick={() => void act(() => paymentOps.claim(a.id), 'بازگشت وجه به شما واگذار شد')}>برداشتن</Button>}
                    {a.status === 'processing' && mine && <Button variant="pri" onClick={() => setDialog('complete')}>ثبت انجام</Button>}
                    {a.status === 'processing' && <Button onClick={() => void act(() => paymentOps.release(a.id, !mine), 'رها شد')}>رها کردن</Button>}
                    {a.status === 'processing' && mine && <Button variant="dng" onClick={() => setDialog('fail')}>ناموفق</Button>}
                  </div>
                </div>
              </>
            ) : <div className="cb"><div className="empty">یک مورد از صف را انتخاب کنید.</div></div>
          }
        />
      )}
      {dialog === 'complete' && a && (
        <FormDialog title={`ثبت انجام بازگشت وجه ${short(a.id)}`} submitLabel="ثبت انجام" fields={[
          { name: 'tracking', label: 'شمارهٔ پیگیری بانک', required: true, kind: 'latin', w: 'md' },
          { name: 'paid', label: 'تاریخ پرداخت', kind: 'date', w: 'date', required: true },
          { name: 'card', label: 'کارت مقصد (پوشانده)', kind: 'latin', required: true, w: 'md', placeholder: '603799******1234', hint: 'حداکثر ۶ رقم اول و ۴ رقم آخر.' },
          { name: 'note', label: 'یادداشت' },
        ]} onSubmit={(v, key) => paymentOps.complete(a.id, { tracking_number: val.text(v.tracking), paid_at: val.date(v.paid)!, destination_card_masked: val.text(v.card), note: val.text(v.note) || undefined }, key)} onClose={() => setDialog(null)} onDone={() => (setDialog(null), toast.push({ tone: 'ok', title: 'بازگشت وجه ثبت شد' }), refunds.reload(), counts.reload(), setActive(undefined))} />
      )}
      {dialog === 'fail' && a && (
        <FormDialog title="ناموفق بودن بازگشت وجه" danger submitLabel="ثبت ناموفق" fields={[{ name: 'reason', label: 'دلیل', required: true }]} onSubmit={(v, key) => paymentOps.fail(a.id, val.text(v.reason), key)} onClose={() => setDialog(null)} onDone={() => (setDialog(null), toast.push({ tone: 'ok', title: 'ثبت شد' }), refunds.reload(), counts.reload(), setActive(undefined))} />
      )}
      {counts.error != null && <ErrorMessage error={counts.error} />}
    </>
  )
}
