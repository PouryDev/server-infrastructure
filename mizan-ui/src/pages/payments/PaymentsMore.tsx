import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { payments } from '@/api/payments'
import { paymentOps, type Dispute } from '@/api/sales'
import { friendly } from '@/api/errors'
import { paymentStatus } from '@/domain/status'
import { fmt, toFa } from '@/shared/digits'
import { showDateTime } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, Input, KV, Message, PageBar, Select, Seg, Skeleton, Textarea, useResource, useToast } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

/** Payment detail (refund request, dispute), unsettled payments with sweep, disputes with notes, gateway reconciliation run. */
export default function PaymentsMore() {
  const toast = useToast()
  const [params, setParams] = useSearchParams()
  const id = params.get('id') ?? ''
  const [idInput, setIdInput] = useState(id)
  const pay = useResource(async () => (id ? paymentOps.get(id) : undefined), [id])
  const unsettled = useResource(() => payments.unsettled(), [])
  const disputes = useResource(() => paymentOps.disputes(), [])
  const [dsel, setDsel] = useState<Dispute>()
  const [note, setNote] = useState('')
  const [method, setMethod] = useState<'gateway' | 'manual_card_to_card'>('gateway')
  const [refund, setRefund] = useState(false)
  const [newDispute, setNewDispute] = useState(false)
  const [sweepMsg, setSweepMsg] = useState<string>()
  const p = pay.data
  const left = p ? p.amount - p.refunded : 0

  // reconciliation run
  const [gateway, setGateway] = useState('zarinpal')
  const [file, setFile] = useState<File>()
  const [period, setPeriod] = useState({ from: '', to: '' })
  const [run, setRun] = useState<{ summary: Record<string, number>; items: { kind: string; gateway_ref: string; details: string }[] }>()
  const [runErr, setRunErr] = useState<unknown>()
  const doRun = async () => {
    if (!file) return
    setRunErr(undefined)
    try {
      const text = await file.text()
      const res = await fetch(`/v1/reconciliations?gateway=${gateway}&from=${val.iso(period.from) ?? ''}&to=${val.iso(period.to) ?? ''}`, { method: 'POST', headers: { 'Content-Type': file.name.endsWith('.json') ? 'application/json' : 'text/csv', 'X-Mizan-UI': '1' }, body: text })
      const json = await res.json()
      if (!res.ok) throw Object.assign(new Error(json?.error?.message), { status: res.status })
      setRun(json)
    } catch (e) {
      setRunErr(e)
    }
  }

  return (
    <>
      <PageBar title={p ? `پرداخت ${p.id.slice(0, 8)}` : 'جزئیات پرداخت و اختلاف‌ها'} sub={p ? `${p.purpose_type} ${p.purpose_id.slice(0, 8)} · ${p.gateway} · ${showDateTime(p.created_at)}` : undefined}>
        <Button disabled={!p || left <= 0} onClick={() => setRefund(true)}>درخواست بازگشت وجه</Button>
        <Button onClick={() => setNewDispute(true)}>ثبت اختلاف</Button>
      </PageBar>
      <div className="grid g3" style={{ alignItems: 'start' }}>
        <Card title={<>جزئیات{p && <Badge tone={paymentStatus(p.status).tone}>{paymentStatus(p.status).label}</Badge>}</>}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <form className="row" onSubmit={(e) => (e.preventDefault(), setParams(idInput ? { id: idInput.trim() } : {}))}>
              <Field label="شناسهٔ پرداخت">{(i) => <Input id={i} w="md" dir="ltr" num value={idInput} onChange={(e) => setIdInput(e.target.value)} />}</Field>
              <Button type="submit">نمایش</Button>
            </form>
            {pay.error != null ? <ErrorMessage error={pay.error} onRetry={pay.reload} /> : pay.loading && id ? <Skeleton rows={4} /> : p ? (
              <>
                <KV rows={[['مبلغ', <span className="n">{fmt(p.amount)} ریال</span>], ['درگاه', p.gateway], ['شناسهٔ مرجع', <span className="n">{p.gateway_txn_ref || '—'}</span>], ['تأیید درگاه', p.status === 'paid' ? 'انجام شد · مبلغ مطابق' : paymentStatus(p.status).label], ['بازگشت‌شده', <span className="n">{fmt(p.refunded)} از {fmt(p.amount)}</span>]]} />
                <Message tone="info" title="">وضعیت پرداخت از خود درگاه پرسیده می‌شود، نه از پیام بازگشتی کاربر.</Message>
              </>
            ) : <div className="sub">شناسهٔ یک پرداخت را وارد کنید (از فهرست «پرداخت‌ها»).</div>}
          </div>
        </Card>
        <Card title="درخواست بازگشت وجه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="fld"><label>روش</label><Seg<string> label="روش" value={method} onChange={(v) => setMethod(v as typeof method)} options={[{ value: 'gateway', label: 'از طریق درگاه' }, { value: 'manual_card_to_card', label: 'کارت‌به‌کارت دستی' }]} /></div>
            <div className="sub">{p ? `حداکثر ${fmt(left)} ریال قابل بازگشت است؛ مبلغ در انتظار هم از آن کم می‌شود.` : 'ابتدا یک پرداخت را باز کنید.'}</div>
            <div className="bar"><Button variant="pri" disabled={!p || left <= 0} onClick={() => setRefund(true)}>ثبت درخواست</Button></div>
          </div>
        </Card>
        <Card title="پرداخت‌های تسویه‌نشده" extra={<Button small onClick={async () => { try { const r = await paymentOps.sweep(); setSweepMsg(`اجرای قبلی: ${toFa(r.paid)} پرداخت پیدا و تسویه شد، ${toFa(r.expired)} پرداخت منقضی شد.`); unsettled.reload() } catch (e) { toast.error(e) } }}>بازیابی (sweep)</Button>} bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}>
            <table className="tbl" aria-label="تسویه‌نشده"><thead><tr><th>پرداخت</th><th className="n">مبلغ</th><th>زمان</th></tr></thead>
              <tbody>{(unsettled.data ?? []).map((u) => <tr key={u.id}><td className="n">{u.id.slice(0, 8)}</td><td className="n">{fmt(u.amount)}</td><td className="mut">{showDateTime(u.paid_at ?? u.created_at)}</td></tr>)}</tbody></table>
            {unsettled.data?.length === 0 && <div className="empty"><b>پرداخت تسویه‌نشده‌ای نیست</b></div>}
          </div>
          {sweepMsg && <div className="cb"><Message tone="ok" title="">{sweepMsg}</Message></div>}
        </Card>
        <Card title="اختلاف‌ها" extra={<Button small onClick={() => setNewDispute(true)}>اختلاف جدید</Button>} bare className="span2">
          <div className="tw" style={{ border: 0, borderRadius: 0 }}>
            <table className="tbl" aria-label="اختلاف‌ها"><thead><tr><th>شناسه</th><th>موضوع</th><th>پرداخت</th><th>وضعیت</th><th>یادداشت‌ها</th></tr></thead>
              <tbody>{(disputes.data ?? []).map((d) => <tr key={d.id} tabIndex={0} className={d.id === dsel?.id ? 'sel' : undefined} onClick={() => setDsel(d)} style={{ cursor: 'pointer' }}><td className="n">{d.id.slice(0, 8)}</td><td>{d.kind}</td><td className="n">{d.payment_id?.slice(0, 8) ?? '—'}</td><td><Badge tone={d.status === 'open' ? 'warn' : 'ok'}>{d.status === 'open' ? 'باز' : d.status === 'investigating' ? 'در بررسی' : 'بسته'}</Badge></td><td className="n mut">{toFa(d.notes.length)}</td></tr>)}</tbody></table>
            {disputes.data?.length === 0 && <div className="empty"><b>اختلافی ثبت نشده</b></div>}
          </div>
        </Card>
        <Card title={dsel ? `اختلاف ${dsel.id.slice(0, 8)}` : 'اختلاف'}>
          {dsel ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {dsel.notes.map((n, i) => <div key={i} className="sub">{n.text ?? n.note}</div>)}
              <div className="fld"><label htmlFor="dnote">یادداشت جدید</label><Textarea id="dnote" rows={3} value={note} onChange={(e) => setNote(e.target.value)} /></div>
              <div className="bar">
                <Button disabled={!note.trim()} onClick={async () => { try { await paymentOps.addNote(dsel.id, note.trim()); setNote(''); disputes.reload() } catch (e) { toast.error(e) } }}>افزودن یادداشت</Button>
                <Button variant="dng" disabled={dsel.status !== 'open' && dsel.status !== 'investigating'} onClick={async () => { try { await paymentOps.closeDispute(dsel.id, 'resolved', note.trim() || 'بسته شد'); disputes.reload(); setDsel(undefined) } catch (e) { toast.error(e) } }}>بستن اختلاف</Button>
              </div>
            </div>
          ) : <div className="empty">یک اختلاف را انتخاب کنید.</div>}
        </Card>
        <Card title="اجرای تطبیق (فایل درگاه)" className="span3">
          <div className="row">
            <Field label="درگاه">{() => <Select label="درگاه" width="160px" value={gateway} options={[{ value: 'zarinpal', label: 'زرین‌پال' }, { value: 'idpay', label: 'آی‌دی‌پی' }]} onChange={setGateway} />}</Field>
            <Field label="از تاریخ">{(i) => <Input id={i} w="date" num value={period.from} onChange={(e) => setPeriod({ ...period, from: e.target.value })} />}</Field>
            <Field label="تا تاریخ (بدون خودش)">{(i) => <Input id={i} w="date" num value={period.to} onChange={(e) => setPeriod({ ...period, to: e.target.value })} />}</Field>
            <Field label="فایل CSV یا JSON">{(i) => <input id={i} type="file" accept=".csv,.json" onChange={(e) => setFile(e.target.files?.[0])} />}</Field>
            <Button variant="pri" disabled={!file || !val.iso(period.from) || !val.iso(period.to)} onClick={() => void doRun()}>اجرا</Button>
            {run && Object.entries(run.summary).map(([k, v]) => <Badge key={k} tone={k.includes('match') && !k.includes('mis') ? 'ok' : 'warn'} square>{toFa(v)} {k}</Badge>)}
          </div>
          {runErr != null && <Message tone="err" title={friendly(runErr).title}>{friendly(runErr).text}</Message>}
        </Card>
      </div>
      {refund && p && (
        <FormDialog title="درخواست بازگشت وجه" submitLabel="ثبت درخواست" fields={[{ name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', required: true, w: 'amt', initial: fmt(left) }, { name: 'reason', label: 'دلیل', required: true }]} intro={`روش: ${method === 'gateway' ? 'از طریق درگاه' : 'کارت‌به‌کارت دستی'}`}
          onSubmit={(v, key) => paymentOps.refund(p.id, { amount: val.amount(v.amount), reason: val.text(v.reason), method }, key)} onClose={() => setRefund(false)} onDone={() => (setRefund(false), toast.push({ tone: 'ok', title: 'درخواست بازگشت وجه ثبت شد' }), pay.reload())} />
      )}
      {newDispute && (
        <FormDialog title="اختلاف جدید" submitLabel="ثبت اختلاف" fields={[{ name: 'kind', label: 'موضوع', required: true }, { name: 'note', label: 'یادداشت', kind: 'textarea' }]} onSubmit={(v) => paymentOps.openDispute({ ...(p ? { payment_id: p.id } : {}), kind: val.text(v.kind), note: val.text(v.note) })} onClose={() => setNewDispute(false)} onDone={() => (setNewDispute(false), disputes.reload())} />
      )}
    </>
  )
}
