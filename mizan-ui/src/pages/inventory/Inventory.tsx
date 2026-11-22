import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { itemsOf } from '@/api/commerce'
import { inventoryApi, type InvItem } from '@/api/inventory'
import { fmt, toFa } from '@/shared/digits'
import { Badge, DataTable, ErrorMessage, PageBar, Skeleton, Stat, SubTabs, useResource, type Column } from '@/ui'
import { Card } from '@/ui'

type Tab = 'items' | 'wh' | 'val' | 'exp' | 'prod'

export default function Inventory() {
  const nav = useNavigate()
  const [tab, setTab] = useState<Tab>('items')
  const [filters, setFilters] = useState<Record<string, string>>({})
  const items = useResource(async () => itemsOf(await inventoryApi.items()), [])
  const wh = useResource(async () => itemsOf(await inventoryApi.warehouses()), [])
  const val = useResource(() => inventoryApi.valuation(), [tab === 'val'])
  const exp = useResource(async () => itemsOf(await inventoryApi.expiring()), [])
  const total = (items.data ?? []).reduce((s, i) => s + i.value, 0)
  const rows = (items.data ?? []).filter((i) => Object.entries(filters).every(([k, v]) => !v || String(k === 'code' ? i.code : i.name).includes(v)))
  const cols: Column<InvItem>[] = [
    { id: 'c', header: 'کد کالا', num: true, filter: 'code', cell: (i) => i.code }, { id: 'n', header: 'نام', filter: 'name', cell: (i) => <Link to={`/inventory/items?item=${i.id}`} style={{ color: 'var(--pri)' }}>{i.name}</Link> },
    { id: 'u', header: 'واحد', mut: true, cell: (i) => i.unit }, { id: 'q', header: 'موجودی', num: true, cell: (i) => fmt(i.quantity) }, { id: 'uc', header: 'بهای واحد', num: true, cell: (i) => fmt(i.unit_cost) }, { id: 'v', header: 'ارزش', num: true, cell: (i) => fmt(i.value) },
    { id: 's', header: 'وضعیت', cell: (i) => (i.quantity <= 0 ? <Badge tone="warn">ناموجود</Badge> : <Badge tone="ok">موجود</Badge>) },
  ]
  return (
    <>
      <PageBar title="انبار">
        <Link className="btn" to="/inventory/ops">انبارگردانی</Link>
        <Link className="btn" to="/inventory/ops">حواله</Link>
        <Link className="btn" to="/inventory/ops">رسید انبار</Link>
        <Link className="btn pri" to="/inventory/items">کالای جدید</Link>
      </PageBar>
      <SubTabs<Tab> value={tab} onChange={(t) => (t === 'prod' ? nav('/production') : setTab(t))} options={[{ value: 'items', label: 'کالاها و موجودی' }, { value: 'wh', label: 'انبارها' }, { value: 'val', label: 'ارزش‌گذاری' }, { value: 'exp', label: 'لات‌های رو به انقضا' }, { value: 'prod', label: 'سفارش تولید' }]} />
      <div className="grid g3"><Stat label="ارزش کل موجودی" value={items.data ? fmt(total) : '…'} /><Stat label="تعداد کالا" value={items.data ? fmt(items.data.length) : '…'} /><Stat label="لات‌های رو به انقضا" value={exp.data ? fmt(exp.data.length) : '…'} /></div>
      {tab === 'items' && <DataTable caption="کالاها" columns={cols} rows={rows} rowKey={(i) => i.id} loading={items.loading} error={items.error} onRetry={items.reload} empty={{ title: 'هنوز کالایی تعریف نشده', action: <Link className="btn pri" to="/inventory/items">کالای جدید</Link> }} filters={filters} onFilters={setFilters} />}
      {tab === 'wh' && <DataTable caption="انبارها" columns={[{ id: 'c', header: 'کد', num: true, cell: (w) => w.code }, { id: 'n', header: 'نام', cell: (w) => w.name }, { id: 'a', header: 'وضعیت', cell: (w) => <Badge tone={w.active ? 'ok' : ''}>{w.active ? 'فعال' : 'غیرفعال'}</Badge> }]} rows={wh.data} rowKey={(w) => w.id} loading={wh.loading} error={wh.error} onRetry={wh.reload} empty={{ title: 'هنوز انباری تعریف نشده', action: <Link className="btn pri" to="/inventory/items">انبار جدید</Link> }} />}
      {tab === 'val' && (val.error != null ? <ErrorMessage error={val.error} onRetry={val.reload} /> : !val.data ? <Skeleton /> : <Card title={`ارزش‌گذاری کل موجودی: ${fmt(val.data.total_value)} ریال`} bare><div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="ارزش‌گذاری"><thead><tr><th>کالا</th><th className="n">موجودی</th><th className="n">ارزش</th></tr></thead><tbody>{val.data.items.map((i) => <tr key={i.id}><td>{i.code} {i.name}</td><td className="n">{fmt(i.quantity)}</td><td className="n">{fmt(i.value)}</td></tr>)}</tbody></table></div></Card>)}
      {tab === 'exp' && <DataTable caption="لات‌های رو به انقضا" columns={[{ id: 'l', header: 'لات', cell: (l) => String(l.lot_no ?? l.id ?? '—') }, { id: 'i', header: 'کالا', cell: (l) => String(l.item_id ?? '').slice(0, 8) }, { id: 'q', header: 'مقدار', num: true, cell: (l) => fmt(Number(l.quantity ?? l.remaining ?? 0)) }, { id: 'e', header: 'انقضا', num: true, cell: (l) => toFa(String(l.expires_on ?? '—')) }]} rows={exp.data} rowKey={(l) => String(l.id ?? l.lot_no ?? Math.random())} loading={exp.loading} error={exp.error} onRetry={exp.reload} empty={{ title: 'لات رو به انقضایی نیست' }} />}
    </>
  )
}
