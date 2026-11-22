import { useState } from 'react'
import { itemsOf, payoutApi, type Batch } from '@/api/commerce'
import { download } from '@/api/http'
import { fmt, toFa } from '@/shared/digits'
import { showDate } from '@/shared/jalali'
import { Badge, Button, DataTable, ErrorMessage, MasterDetail, Message, PageBar, Skeleton, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { treasury } from '@/api/treasury'

/** Payout batches to sellers: create, review rows (reject with a reason), download the bank CSV. */
export default function PayoutBatches() {
  const toast = useToast()
  const list = useResource(async () => itemsOf(await payoutApi.batches()), [])
  const [sel, setSel] = useState<string>()
  const detail = useResource(async () => (sel ? payoutApi.batch(sel) : undefined), [sel])
  const sellers = useResource(async () => itemsOf(await payoutApi.sellers()), [])
  const banks = useResource(async () => { const r = await treasury.bankAccounts(); return Array.isArray(r) ? r : r.items }, [])
  const [dlg, setDlg] = useState<'new' | string | null>(null)
  const b = detail.data
  const name = (id: string) => sellers.data?.find((s) => s.id === id)?.name ?? id.slice(0, 8)
  const cols: Column<Batch>[] = [
    { id: 'id', header: 'دسته', num: true, cell: (x) => x.id.slice(0, 8) }, { id: 'd', header: 'تاریخ', cell: (x) => showDate(x.date_jalali) },
    { id: 'n', header: 'ردیف', num: true, cell: (x) => toFa(x.items?.length ?? x.sale_count) }, { id: 't', header: 'جمع (ریال)', num: true, cell: (x) => fmt(x.total) },
  ]
  const csv = async () => { if (!b) return; try { const blob = await download(`/v1/payout/batches/${b.id}/csv`); const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = `payout-${b.id.slice(0, 8)}.csv`; a.click(); URL.revokeObjectURL(a.href) } catch (e) { toast.error(e) } }

  return (
    <>
      <PageBar title="دسته‌های پرداخت به فروشندگان"><Button variant="pri" onClick={() => setDlg('new')}>ساخت دستهٔ جدید</Button></PageBar>
      <MasterDetail
        listBasis={480}
        detailBasis={440}
        list={<DataTable caption="دسته‌ها" columns={cols} rows={list.data} rowKey={(x) => x.id} loading={list.loading} error={list.error} onRetry={list.reload} empty={{ title: 'هنوز دسته‌ای ساخته نشده' }} onRowClick={(x) => setSel(x.id)} rowClass={(x) => (x.id === sel ? 'sel' : undefined)} />}
        detail={b ? (
          <>
            <div className="ch">دسته {b.id.slice(0, 8)}<span className="sp" /><Button small variant="pri" onClick={() => void csv()}>دانلود CSV برای بانک</Button></div>
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="ردیف‌های دسته"><thead><tr><th>فروشنده</th><th className="n">پرداختنی</th><th /></tr></thead>
                <tbody>{b.items.map((it) => <tr key={it.seller_id}><td>{name(it.seller_id)}</td><td className="n">{it.rejected ? <Badge tone="err">ردشده</Badge> : fmt(it.amount)}</td><td><Button small disabled={it.rejected} onClick={() => setDlg(it.seller_id)}>رد ردیف</Button></td></tr>)}</tbody></table>
            </div>
            <div className="cb"><Message tone="info" title="">فقط فروش‌های تحویل‌شده و آزادشده، و مانده‌های بدون مسدودی وارد دسته می‌شوند.</Message></div>
          </>
        ) : detail.error != null ? <div className="cb"><ErrorMessage error={detail.error} /></div> : sel ? <Skeleton /> : undefined}
      />
      {dlg === 'new' && <FormDialog title="ساخت دستهٔ پرداخت" submitLabel="ساخت دسته" fields={[{ name: 'bank', label: 'پرداخت از حساب بانکی', kind: 'select', options: (banks.data ?? []).map((x) => ({ value: x.id, label: x.name })) }, { name: 'date', label: 'تاریخ', kind: 'date', w: 'date' }]} onSubmit={(v, k) => payoutApi.createBatch({ ...(v.bank ? { bank_account_id: v.bank } : {}), ...(val.iso(v.date) ? { date: val.iso(v.date) } : {}) }, k)} onClose={() => setDlg(null)} onDone={(x) => (setDlg(null), list.reload(), setSel(x.id), toast.push({ tone: 'ok', title: 'دسته ساخته شد' }))} />}
      {dlg && dlg !== 'new' && b && <FormDialog title="رد ردیف" danger submitLabel="رد ردیف" fields={[{ name: 'r', label: 'دلیل رد ردیف', required: true, initial: '' }]} onSubmit={(v) => payoutApi.rejectItem(b.id, dlg, val.text(v.r))} onClose={() => setDlg(null)} onDone={() => (setDlg(null), detail.reload(), toast.push({ tone: 'ok', title: 'ردیف رد شد' }))} />}
    </>
  )
}
