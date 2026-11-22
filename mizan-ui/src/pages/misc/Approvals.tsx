import { useState } from 'react'
import { Link } from 'react-router-dom'
import { approvals, type Approval } from '@/api/approvals'
import { api } from '@/api/http'
import { useProfile } from '@/auth/AuthContext'
import { useFiscal } from '@/app/Fiscal'
import { ageText } from '@/domain/tasks'
import { toFa } from '@/shared/digits'
import { showDateTime } from '@/shared/jalali'
import { Badge, Button, DataTable, MasterDetail, Message, PageBar, SubTabs, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

const KIND: Record<string, string> = { year_close: 'بستن سال مالی', appropriation: 'تقسیم سود' }
const ST: Record<string, { l: string; t: 'warn' | 'ok' | 'err' }> = { pending: { l: 'در انتظار', t: 'warn' }, approved: { l: 'تأییدشده', t: 'ok' }, rejected: { l: 'ردشده', t: 'err' } }

/** Two-person approvals: the requester and the approver must differ (enforced by the API). */
export default function Approvals() {
  const toast = useToast()
  const me = useProfile()
  const { years } = useFiscal()
  const list = useResource(async () => (await approvals.list()).items, [])
  const [sel, setSel] = useState<Approval>()
  const [dlg, setDlg] = useState<'new' | 'reject' | null>(null)
  const pending = (list.data ?? []).filter((a) => a.status === 'pending').length
  const cols: Column<Approval>[] = [
    { id: 'k', header: 'درخواست', cell: (a) => `${KIND[a.kind] ?? a.kind} (${a.subject})` }, { id: 'w', header: 'درخواست‌کننده', mut: true, cell: (a) => a.requested_by }, { id: 'g', header: 'سن', mut: true, cell: (a) => ageText(a.created_at, Date.now()) },
    { id: 's', header: 'وضعیت', cell: (a) => <Badge tone={ST[a.status]?.t}>{ST[a.status]?.l ?? a.status}</Badge> },
  ]
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast.push({ tone: 'ok', title: msg }); list.reload(); setSel(undefined) } catch (e) { toast.error(e) } }
  const self = sel?.requested_by === me.username

  return (
    <>
      <PageBar title="تأییدیه‌ها و ردیابی"><Button variant="pri" onClick={() => setDlg('new')}>درخواست تأیید جدید</Button></PageBar>
      <SubTabs<string> value="req" onChange={() => undefined} options={[{ value: 'req', label: `درخواست‌های تأیید${pending ? ` (${toFa(pending)})` : ''}` }]} />
      <div className="row"><Link className="btn" to="/audit">ردیابی رکورد</Link><Link className="btn" to="/payments/details">اختلاف‌ها</Link></div>
      <Message tone="info" title="">API فعلی فقط دو نوع درخواست تأیید می‌شناسد: بستن سال مالی و تقسیم سود. تأیید ابطال فاکتور یا اصلاح پرداخت در mizan وجود ندارد.</Message>
      <MasterDetail
        list={<DataTable caption="درخواست‌های تأیید" columns={cols} rows={list.data} rowKey={(a) => a.id} loading={list.loading} error={list.error} onRetry={list.reload} empty={{ title: 'درخواستی نیست' }} onRowClick={(a) => setSel(a)} rowClass={(a) => (a.id === sel?.id ? 'sel' : undefined)} />}
        detail={sel ? (
          <>
            <div className="ch">{KIND[sel.kind] ?? sel.kind} · {sel.subject}</div>
            <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Message tone="warn" title="">درخواست‌کننده و تأییدکننده نمی‌توانند یک نفر باشند.</Message>
              <dl className="kv" style={{ margin: 0 }}><dt>درخواست‌کننده</dt><dd>{sel.requested_by}</dd><dt>زمان</dt><dd>{showDateTime(sel.created_at)}</dd>{sel.decided_by && <><dt>تصمیم‌گیرنده</dt><dd>{sel.decided_by}</dd></>}{sel.reason && <><dt>دلیل</dt><dd>{sel.reason}</dd></>}</dl>
              {sel.status === 'pending' && (
                <div className="bar">
                  <Button variant="pri" disabled={self} title={self ? 'درخواست‌کننده نمی‌تواند خودش تأیید کند' : undefined} onClick={() => void act(() => api.post(`/v1/approvals/${sel.id}/approve`), 'تأیید و اجرا شد')}>تأیید</Button>
                  <Button variant="dng" onClick={() => setDlg('reject')}>رد</Button>
                </div>
              )}
              <div className="div" />
              <div className="sub">تاریخچهٔ رکورد (Ctrl+H): <Link to={`/audit?type=approval&id=${sel.id}`} style={{ color: 'var(--pri)' }}>مشاهدهٔ ردیابی</Link></div>
            </div>
          </>
        ) : undefined}
      />
      {dlg === 'new' && <FormDialog title="درخواست تأیید جدید" submitLabel="ارسال برای تأیید" fields={[{ name: 'kind', label: 'نوع عملیات', kind: 'select', required: true, options: [{ value: 'year_close', label: 'بستن سال مالی' }, { value: 'appropriation', label: 'تقسیم سود' }], initial: 'year_close' }, { name: 'fy', label: 'سال مالی', kind: 'select', required: true, options: years.map((y) => ({ value: String(y.id), label: y.name })), initial: String(years[0]?.id ?? '') }]} onSubmit={(v, key) => api.post('/v1/approvals', { kind: v.kind, payload: { fiscal_year_id: Number(v.fy) } }, key)} onClose={() => setDlg(null)} onDone={() => (setDlg(null), list.reload(), toast.push({ tone: 'ok', title: 'درخواست ارسال شد' }))} />}
      {dlg === 'reject' && sel && <FormDialog title="رد درخواست" danger submitLabel="رد" fields={[{ name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => api.post(`/v1/approvals/${sel.id}/reject`, { reason: val.text(v.r) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), list.reload(), setSel(undefined))} />}
    </>
  )
}
