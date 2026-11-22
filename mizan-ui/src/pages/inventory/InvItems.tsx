import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { itemsOf } from '@/api/commerce'
import { inventoryApi, type Movement } from '@/api/inventory'
import { fmt, toFa } from '@/shared/digits'
import { showDate } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, Field, PageBar, Select, useResource, useToast, type Column } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'

const KIND: Record<string, { l: string; t: 'ok' | 'warn' | 'info' | '' }> = { receipt: { l: 'رسید', t: 'ok' }, issue: { l: 'خروج', t: 'warn' }, transfer: { l: 'انتقال', t: 'info' }, count: { l: 'انبارگردانی', t: '' }, write_down: { l: 'کاهش ارزش', t: 'warn' } }

export default function InvItems() {
  const toast = useToast()
  const [params, setParams] = useSearchParams()
  const items = useResource(async () => itemsOf(await inventoryApi.items()), [])
  const wh = useResource(async () => itemsOf(await inventoryApi.warehouses()), [])
  const item = items.data?.find((i) => i.id === params.get('item')) ?? items.data?.[0]
  const mv = useResource(async () => (item ? itemsOf(await inventoryApi.movements(item.id) as never) as Movement[] : []), [item?.id])
  const stock = useResource(async () => (item ? (await inventoryApi.stock(item.id)).items : []), [item?.id])
  const [dlg, setDlg] = useState<'wh' | null>(null)
  const [filters, setFilters] = useState<Record<string, string>>({})
  const whName = (id?: string) => wh.data?.find((w) => w.id === id)?.name ?? '—'
  const cols: Column<Movement>[] = [
    { id: 'd', header: 'تاریخ', filter: 'd', cell: (m) => showDate(m.date_jalali) }, { id: 'k', header: 'نوع', filter: 'k', cell: (m) => <Badge tone={KIND[m.kind]?.t}>{KIND[m.kind]?.l ?? m.kind}</Badge> }, { id: 'w', header: 'انبار', filter: 'w', cell: (m) => whName(m.warehouse_id) },
    { id: 'q', header: 'تعداد', num: true, cell: (m) => fmt(m.quantity) }, { id: 'u', header: 'بهای واحد', num: true, cell: (m) => fmt(m.quantity ? Math.round(Math.abs(m.value / m.quantity)) : 0) }, { id: 'a', header: 'مانده', num: true, cell: (m) => fmt(m.quantity_after) },
  ]
  const rows = (mv.data ?? []).filter((m) => Object.entries(filters).every(([k, v]) => !v || String(k === 'd' ? m.date_jalali : k === 'k' ? (KIND[m.kind]?.l ?? m.kind) : whName(m.warehouse_id)).includes(v)))
  return (
    <>
      <PageBar title={item ? `${item.name} · ${item.code}` : 'کالا و حرکت‌ها'} sub={item ? `موجودی ${toFa(item.quantity)} ${item.unit} · ارزش ${fmt(item.value)} ریال` : undefined}>
        {items.data && items.data.length > 0 && <Select label="کالا" width="220px" value={item?.id ?? ''} options={items.data.map((i) => ({ value: i.id, label: `${i.code} ${i.name}` }))} onChange={(v) => setParams({ item: v })} />}
        <Button onClick={() => setDlg('wh')}>انبار جدید</Button>
      </PageBar>
      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title="حرکت‌های کالا" bare><div style={{ padding: 0 }}><DataTable caption="حرکت‌های کالا" columns={cols} rows={[...rows].reverse()} rowKey={(m) => String(m.id)} loading={mv.loading} error={mv.error} onRetry={mv.reload} empty={{ title: 'هنوز حرکتی ثبت نشده' }} filters={filters} onFilters={setFilters} /></div></Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Card title="کالای جدید">
            <InlineForm submitLabel="ایجاد کالا" rows={[['code', 'name'], ['unit', 'method']]} fields={[{ name: 'code', label: 'کد', kind: 'latin', w: 'code', required: true }, { name: 'name', label: 'نام', w: 'md', required: true }, { name: 'unit', label: 'واحد', w: 'sm', initial: 'عدد', required: true }, { name: 'method', label: 'روش ارزش‌گذاری', kind: 'select', options: [{ value: 'weighted_average', label: 'میانگین موزون' }, { value: 'fifo', label: 'اولین ورودی' }, { value: 'specific', label: 'شناسایی ویژه' }], initial: 'weighted_average' }]} onSubmit={(v) => inventoryApi.addItem({ code: val.text(v.code), name: val.text(v.name), unit: val.text(v.unit), method: v.method! })} onDone={(i) => (items.reload(), setParams({ item: i.id }), toast.push({ tone: 'ok', title: 'کالا ایجاد شد' }))} />
          </Card>
          <Card title="انبارها" bare><div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="انبارها"><thead><tr><th>انبار</th><th className="n">موجودی این کالا</th></tr></thead><tbody>{(wh.data ?? []).map((w) => <tr key={w.id}><td>{w.name}</td><td className="n">{fmt(stock.data?.find((s) => s.warehouse_id === w.id)?.quantity ?? 0)}</td></tr>)}</tbody></table></div></Card>
          <Field label="موجودی بر حسب انبار">{() => <span className="sub">از جدول بالا (هر انبار)</span>}</Field>
        </div>
      </div>
      {dlg === 'wh' && <FormDialog title="انبار جدید" submitLabel="ایجاد انبار" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }]} onSubmit={(v) => inventoryApi.addWarehouse({ code: val.text(v.code), name: val.text(v.name) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), wh.reload())} />}
    </>
  )
}
