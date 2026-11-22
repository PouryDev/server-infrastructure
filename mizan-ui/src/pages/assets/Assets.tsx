import { useState } from 'react'
import { Link } from 'react-router-dom'
import { itemsOf } from '@/api/commerce'
import { assetApi, type Asset } from '@/api/assets'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, ErrorMessage, Message, MasterDetail, PageBar, Skeleton, useResource, useToast, type Column } from '@/ui'
import { useListState } from '@/ui'

const ST: Record<string, { l: string; t: 'ok' | 'warn' | '' }> = { active: { l: 'فعال', t: 'ok' }, idle: { l: 'بیکار', t: 'warn' }, disposed: { l: 'واگذارشده', t: '' } }

export default function Assets() {
  const toast = useToast()
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const list = useListState({}, 50)
  const [sel, setSel] = useState<Asset>()
  const cats = useResource(async () => itemsOf(await assetApi.categories()), [])
  const assets = useResource(async (s) => itemsOf(await assetApi.list({ pageSize: 100, sort: 'code', filter: Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v).map(([k, v]) => [k === 'name' ? 'name:like' : k, v])) }, s)), [JSON.stringify(list.filters)])
  const sched = useResource(async () => (sel ? (await assetApi.schedule(sel.id)).items : []), [sel?.id])
  const catName = (id: string) => cats.data?.find((c) => c.id === id)?.name ?? '—'
  const cols: Column<Asset>[] = [
    { id: 'c', header: 'کد', num: true, filter: 'code', cell: (a) => a.code }, { id: 'n', header: 'دارایی', filter: 'name', cell: (a) => a.name }, { id: 'k', header: 'طبقه', mut: true, cell: (a) => catName(a.category_id) },
    { id: 'cost', header: 'بهای تمام‌شده', num: true, cell: (a) => fmt(a.cost) }, { id: 'bv', header: 'ارزش دفتری', num: true, cell: (a) => fmt(a.book_value) }, { id: 's', header: 'وضعیت', filter: 'status', cell: (a) => <Badge tone={ST[a.status]?.t}>{ST[a.status]?.l ?? a.status}</Badge> },
  ]
  return (
    <>
      <PageBar title="دارایی‌های ثابت">
        <Link className="btn" to="/assets/setup">طبقه‌بندی‌ها</Link>
        <Link className="btn" to="/assets/setup">اجرای استهلاک {MONTHS[jm - 1]}</Link>
        <Link className="btn pri" to="/assets/ops">تحصیل دارایی</Link>
      </PageBar>
      <Message tone="info" title="">اجرای دوباره استهلاک برای همین ماه، مبلغ را دو بار ثبت نمی‌کند. (سال {toFa(jy)})</Message>
      {cats.error != null && <ErrorMessage error={cats.error} onRetry={cats.reload} />}
      <MasterDetail
        listBasis={560}
        list={<DataTable caption="دارایی‌ها" columns={cols} rows={assets.data} rowKey={(a) => a.id} loading={assets.loading} error={assets.error} onRetry={assets.reload} empty={{ title: 'هنوز دارایی‌ای ثبت نشده', action: <Link className="btn pri" to="/assets/ops">تحصیل دارایی</Link> }} filters={list.filters} onFilters={list.setFilters} onRowClick={(a) => setSel(a)} rowClass={(a) => (a.id === sel?.id ? 'sel' : undefined)} />}
        detail={sel ? (
          <>
            <div className="ch">برنامهٔ استهلاک · {sel.name}</div>
            {sched.loading ? <Skeleton /> : (
              <div className="tw" style={{ border: 0, borderRadius: 0, maxHeight: 360 }}>
                <table className="tbl" aria-label="برنامهٔ استهلاک"><thead><tr><th>دوره</th><th className="n">استهلاک</th><th className="n">انباشته</th></tr></thead>
                  <tbody>{(sched.data ?? []).map((r) => <tr key={`${r.jalali_year}-${r.jalali_month}`} className={r.jalali_year === jy && r.jalali_month === jm ? 'sel' : undefined}><td>{MONTHS[r.jalali_month - 1]} {toFa(r.jalali_year)}</td><td className="n">{fmt(r.amount)}</td><td className="n">{fmt(r.accumulated)}</td></tr>)}</tbody></table>
              </div>
            )}
            <div className="cb bar"><Link className="btn" to={`/assets/ops?asset=${sel.id}`}>واگذاری</Link><Button onClick={() => toast.push({ tone: 'info', title: 'واگذاری بخشی را از صفحهٔ «تحصیل و واگذاری» انجام دهید.' })}>واگذاری بخشی</Button></div>
          </>
        ) : <Card><div className="empty">یک دارایی را انتخاب کنید.</div></Card>}
      />
    </>
  )
}
