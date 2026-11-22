import { useState } from 'react'
import { Link } from 'react-router-dom'
import { walletApi, type Wallet } from '@/api/commerce'
import { fmt } from '@/shared/digits'
import { Badge, Button, Field, Input, KV, MasterDetail, Message, PageBar, Skeleton, Stat, useResource, useToast } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { readJSON, writeJSON } from '@/shared/storage'

const STATUS: Record<string, { l: string; t: 'ok' | 'warn' | 'err' }> = { active: { l: 'فعال', t: 'ok' }, frozen: { l: 'مسدود', t: 'err' }, closed: { l: 'بسته', t: 'warn' } }
const RECENT = 'mizan.recentWalletOwners'

/**
 * mizan lists wallets only per owner (GET /wallets?owner_ref=), there is no "all wallets" list: the page searches by owner and keeps the
 * owners you looked at in this browser.
 */
export default function Wallets() {
  const toast = useToast()
  const [owner, setOwner] = useState('')
  const [recent, setRecent] = useState<string[]>(() => readJSON(RECENT, []))
  const [current, setCurrent] = useState<string>()
  const [sel, setSel] = useState<Wallet>()
  const [dlg, setDlg] = useState<'credit' | 'debit' | 'transfer' | 'open' | null>(null)
  const res = useResource(async () => (current ? walletApi.byOwner(current) : []), [current])
  const rows = res.data ?? []
  const w = rows.find((x) => x.id === sel?.id) ?? sel

  const search = (o: string) => {
    const v = o.trim()
    if (!v) return
    setCurrent(v)
    setSel(undefined)
    const next = [v, ...recent.filter((x) => x !== v)].slice(0, 10)
    setRecent(next)
    writeJSON(RECENT, next)
  }
  const done = (msg: string) => { setDlg(null); toast.push({ tone: 'ok', title: msg }); res.reload() }
  const setStatus = async (status: 'active' | 'frozen') => {
    if (!w) return
    try { await walletApi.setStatus(w.id, status); toast.push({ tone: 'ok', title: status === 'frozen' ? 'کیف پول مسدود شد' : 'کیف پول فعال شد' }); res.reload() } catch (e) { toast.error(e) }
  }

  return (
    <>
      <PageBar title="کیف پول‌ها">
        <Link className="btn" to="/wallets/ops">انتقال بین کیف پول‌ها</Link>
        <Button variant="pri" onClick={() => setDlg('open')}>کیف پول جدید</Button>
      </PageBar>
      <Message tone="info" title="جستجو بر اساس مالک">API فقط کیف پول‌های یک مالک را برمی‌گرداند؛ شناسهٔ مالک را وارد کنید.</Message>
      <form className="row" onSubmit={(e) => (e.preventDefault(), search(owner))}>
        <Field label="شناسهٔ مالک">{(id) => <Input id={id} w="md" dir="ltr" num value={owner} onChange={(e) => setOwner(e.target.value)} />}</Field>
        <Button variant="pri" type="submit">جستجو</Button>
        {recent.map((r) => <Button key={r} small onClick={() => (setOwner(r), search(r))}>{r}</Button>)}
      </form>
      <MasterDetail
        list={
          <div className="tw">
            <table className="tbl" aria-label="کیف پول‌ها">
              <thead><tr><th>مالک</th><th className="n">موجودی (ریال)</th><th>وضعیت</th></tr></thead>
              <tbody>{rows.map((r) => <tr key={r.id} tabIndex={0} className={r.id === w?.id ? 'sel' : undefined} onClick={() => setSel(r)} onKeyDown={(e) => e.key === 'Enter' && setSel(r)} style={{ cursor: 'pointer' }}><td>{r.owner_ref}</td><td className="n">{fmt(r.balance)}</td><td><Badge tone={STATUS[r.status]?.t}>{STATUS[r.status]?.l ?? r.status}</Badge></td></tr>)}</tbody>
            </table>
            {res.loading && current && <Skeleton rows={2} />}
            {!res.loading && rows.length === 0 && <div className="empty"><b>{current ? 'کیف پولی برای این مالک نیست' : 'مالک را جستجو کنید'}</b></div>}
          </div>
        }
        detail={w ? (
          <>
            <div className="ch">کیف پول {w.owner_ref}<Badge tone={STATUS[w.status]?.t}>{STATUS[w.status]?.l ?? w.status}</Badge></div>
            <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Stat label="موجودی" value={fmt(w.balance)} unit="ریال" />
              <KV rows={[['شناسه', <span className="n">{w.id}</span>]]} />
              <div className="bar">
                <Button variant="pri" onClick={() => setDlg('credit')}>افزایش موجودی</Button>
                <Button onClick={() => setDlg('debit')}>کاهش موجودی</Button>
                {w.status === 'active' ? <Button variant="dng" onClick={() => void setStatus('frozen')}>مسدود کردن</Button> : <Button onClick={() => void setStatus('active')}>فعال‌سازی</Button>}
              </div>
              <div className="sub">هر تغییر دستی با شناسهٔ یکتا ثبت می‌شود تا تکرار درخواست، دوبار اعمال نشود.</div>
            </div>
          </>
        ) : undefined}
      />
      {dlg === 'open' && <FormDialog title="کیف پول جدید" submitLabel="ایجاد کیف پول" fields={[{ name: 'owner', label: 'مالک (شناسه)', required: true, kind: 'latin', w: 'md' }]} onSubmit={(v) => walletApi.open(val.text(v.owner))} onClose={() => setDlg(null)} onDone={(x) => (setDlg(null), setOwner(x.owner_ref), search(x.owner_ref), toast.push({ tone: 'ok', title: 'کیف پول ایجاد شد' }))} />}
      {(dlg === 'credit' || dlg === 'debit') && w && <FormDialog title={dlg === 'credit' ? 'افزایش موجودی' : 'کاهش موجودی'} submitLabel={dlg === 'credit' ? 'افزایش' : 'کاهش'} fields={[{ name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'reason', label: 'دلیل', required: true }]} onSubmit={(v, k) => (dlg === 'credit' ? walletApi.credit : walletApi.debit)(w.id, val.amount(v.amount), val.text(v.reason), k)} onClose={() => setDlg(null)} onDone={() => done('انجام شد')} />}
    </>
  )
}
