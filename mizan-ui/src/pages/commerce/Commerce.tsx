import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { itemsOf, payoutApi, subscriptionApi, couponApi, type Seller } from '@/api/commerce'
import { fmt } from '@/shared/digits'
import { Badge, Button, Card, ErrorMessage, KV, PageBar, Skeleton, SubTabs, useResource, useToast } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

/** Sellers (marketplace payouts), with the settlement batch shortcut and counts of subscriptions and coupons. */
export default function Commerce() {
  const nav = useNavigate()
  const toast = useToast()
  const [dlg, setDlg] = useState(false)
  const [sel, setSel] = useState<string>()
  const sellers = useResource(async () => {
    const list = itemsOf(await payoutApi.sellers())
    const bal = await Promise.all(list.map((s) => payoutApi.balance(s.id).then((b) => b, () => ({}) as Record<string, unknown>)))
    return list.map((s, i) => ({ s, bal: bal[i] ?? {} }))
  }, [])
  const counts = useResource(async () => {
    const [subs, cps] = await Promise.all([subscriptionApi.list({ pageSize: 1, filter: { status: 'active' } }), couponApi.list({ pageSize: 1, filter: { active: 'true' } })])
    return { subs: subs.meta.total ?? 0, cps: cps.meta.total ?? 0 }
  }, [])
  const num = (b: Record<string, unknown>, ...keys: string[]) => keys.map((k) => b[k]).find((v) => typeof v === 'number') as number | undefined

  return (
    <>
      <PageBar title="فروشندگان، اشتراک و کوپن"><Button variant="pri" onClick={() => setDlg(true)}>فروشندهٔ جدید</Button></PageBar>
      <SubTabs<'sellers' | 'subs' | 'coupons'> value="sellers" onChange={(v) => v !== 'sellers' && nav(v === 'subs' ? '/subscriptions' : '/coupons')} options={[{ value: 'sellers', label: 'فروشندگان و تسویه' }, { value: 'subs', label: 'اشتراک‌ها' }, { value: 'coupons', label: 'کوپن‌ها' }]} />
      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title="فروشندگان" bare>
          {sellers.error != null ? <div className="cb"><ErrorMessage error={sellers.error} onRetry={sellers.reload} /></div> : !sellers.data ? <Skeleton /> : (
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="فروشندگان"><thead><tr><th>نام</th><th className="n">مانده قابل تسویه</th><th className="n">برگشتی در انتظار</th><th>وضعیت</th></tr></thead>
                <tbody>{sellers.data.map(({ s, bal }: { s: Seller; bal: Record<string, unknown> }) => <tr key={s.id} tabIndex={0} className={s.id === sel ? 'sel' : undefined} onClick={() => (setSel(s.id), nav(`/payout/sales?seller=${s.id}`))} onKeyDown={(e) => e.key === 'Enter' && nav(`/payout/sales?seller=${s.id}`)} style={{ cursor: 'pointer' }}><td>{s.name}</td><td className="n">{fmt(num(bal, 'payable', 'releasable', 'available', 'balance'))}</td><td className="n mut">{fmt(num(bal, 'clawback', 'clawbacks', 'outstanding_clawbacks') ?? 0)}</td><td><Badge tone={s.status === 'active' ? 'ok' : 'err'}>{s.status === 'active' ? 'فعال' : 'مسدود'}</Badge></td></tr>)}</tbody></table>
              {sellers.data.length === 0 && <div className="empty"><b>هنوز فروشنده‌ای ثبت نشده</b></div>}
            </div>
          )}
        </Card>
        <Card title="دستهٔ تسویه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div className="msg info"><div className="tx">فقط فروش‌های تحویل‌شده و آزادشده وارد دسته می‌شوند. فایل CSV برای پایا ساخته می‌شود.</div></div>
            <div className="bar"><Link className="btn pri" to="/payout/batches">ساخت و مدیریت دسته‌ها</Link></div>
            <div className="div" />
            <KV rows={[['اشتراک فعال', <span className="n">{counts.data ? fmt(counts.data.subs) : '…'}</span>], ['کوپن فعال', <span className="n">{counts.data ? fmt(counts.data.cps) : '…'}</span>]]} />
          </div>
        </Card>
      </div>
      {dlg && <FormDialog title="فروشندهٔ جدید" submitLabel="ثبت فروشنده" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }, { name: 'sheba', label: 'شبا', kind: 'latin', w: 'lg', placeholder: 'IR…' }]} onSubmit={(v) => payoutApi.addSeller({ code: val.text(v.code), name: val.text(v.name), sheba: val.text(v.sheba) || undefined })} onClose={() => setDlg(false)} onDone={() => (setDlg(false), sellers.reload(), toast.push({ tone: 'ok', title: 'فروشنده ثبت شد' }))} />}
    </>
  )
}
