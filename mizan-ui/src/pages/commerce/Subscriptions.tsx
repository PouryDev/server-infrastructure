import { useState } from 'react'
import { itemsOf, subscriptionApi, type Plan, type Subscription } from '@/api/commerce'
import { fmt, toFa } from '@/shared/digits'
import { showDate } from '@/shared/jalali'
import { Badge, Button, DataTable, ErrorMessage, MasterDetail, Message, PageBar, Skeleton, SubTabs, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

const ST: Record<string, { l: string; t: 'ok' | 'warn' | 'info' | '' }> = { active: { l: 'فعال', t: 'ok' }, trialing: { l: 'آزمایشی', t: 'info' }, paused: { l: 'متوقف', t: 'warn' }, canceled: { l: 'لغوشده', t: '' } }
const UNIT: Record<string, string> = { day: 'روز', month: 'ماه', year: 'سال' }

export default function Subscriptions() {
  const toast = useToast()
  const [tab, setTab] = useState<'subs' | 'plans'>('subs')
  const list = useListState({}, 20)
  const [sel, setSel] = useState<Subscription>()
  const [dlg, setDlg] = useState<'sub' | 'plan' | 'cancel' | null>(null)
  const [billMsg, setBillMsg] = useState<string>()
  const subs = useResource((s) => subscriptionApi.list({ page: list.page, pageSize: list.pageSize, filter: Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v).map(([k, v]) => [k === 'customer_ref' ? 'customer_ref:like' : k, v])) }, s), [list.page, JSON.stringify(list.filters)])
  const plans = useResource(async () => itemsOf(await subscriptionApi.plans()), [])
  const periods = useResource(async () => (sel ? itemsOf(await subscriptionApi.periods(sel.id)) : []), [sel?.id])

  const cols: Column<Subscription>[] = [
    { id: 'c', header: 'مشترک', filter: 'customer_ref', cell: (s) => s.customer_ref },
    { id: 'p', header: 'طرح', cell: (s) => s.plan_name },
    { id: 'a', header: 'مبلغ دوره', num: true, cell: (s) => fmt(s.price) },
    { id: 'n', header: 'دورهٔ بعد', num: true, cell: (s) => showDate(s.next_bill_date_jalali) },
    { id: 's', header: 'وضعیت', filter: 'status', cell: (s) => <Badge tone={ST[s.status]?.t}>{ST[s.status]?.l ?? s.status}</Badge> },
  ]
  const act = async (fn: () => Promise<Subscription>, msg: string) => { try { const s = await fn(); setSel(s); toast.push({ tone: 'ok', title: msg }); subs.reload() } catch (e) { toast.error(e) } }
  const planCols: Column<Plan>[] = [
    { id: 'c', header: 'کد', num: true, cell: (p) => p.code }, { id: 'n', header: 'نام', cell: (p) => p.name }, { id: 'p', header: 'مبلغ', num: true, cell: (p) => fmt(p.price) },
    { id: 'u', header: 'دوره', cell: (p) => `${toFa(p.count)} ${UNIT[p.unit] ?? p.unit}` }, { id: 'a', header: 'وضعیت', cell: (p) => <Badge tone={p.active ? 'ok' : ''}>{p.active ? 'فعال' : 'غیرفعال'}</Badge> },
  ]

  return (
    <>
      <PageBar title="اشتراک‌ها">
        <Button onClick={async () => { try { const r = await subscriptionApi.bill(); setBillMsg(`${toFa(r.invoiced)} فاکتور صادر شد، ${toFa(r.failed)} ناموفق (از ${toFa(r.periods)} دوره).`); subs.reload() } catch (e) { toast.error(e) } }}>صدور صورتحساب دوره</Button>
        <Button variant="pri" onClick={() => setDlg('sub')}>مشترک جدید</Button>
      </PageBar>
      {billMsg && <Message tone="ok" title="صدور صورتحساب دوره">{billMsg}</Message>}
      <SubTabs<'subs' | 'plans'> value={tab} onChange={setTab} options={[{ value: 'subs', label: 'مشترکان' }, { value: 'plans', label: 'طرح‌ها' }]} />
      {tab === 'subs' ? (
        <MasterDetail
          list={<DataTable caption="مشترکان" columns={cols} rows={subs.data?.items} rowKey={(s) => s.id} loading={subs.loading} error={subs.error} onRetry={subs.reload} empty={{ title: 'هنوز مشترکی نیست' }} filters={list.filters} onFilters={list.setFilters} meta={subs.data?.meta} onPage={list.setPage} onRowClick={(s) => setSel(s)} rowClass={(s) => (s.id === sel?.id ? 'sel' : undefined)} />}
          detail={sel ? (
            <>
              <div className="ch">اشتراک {sel.customer_ref}<Badge tone={ST[sel.status]?.t}>{ST[sel.status]?.l}</Badge></div>
              <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <div className="bar">
                  <Button disabled={sel.status !== 'active' && sel.status !== 'trialing'} onClick={() => void act(() => subscriptionApi.pause(sel.id), 'متوقف شد')}>توقف</Button>
                  <Button disabled={sel.status !== 'paused'} onClick={() => void act(() => subscriptionApi.resume(sel.id), 'از سر گرفته شد')}>ازسرگیری</Button>
                  <Button variant="dng" disabled={sel.status === 'canceled'} onClick={() => setDlg('cancel')}>لغو</Button>
                </div>
                {periods.error != null ? <ErrorMessage error={periods.error} /> : periods.loading ? <Skeleton rows={2} /> : <div className="tw"><table className="tbl" aria-label="دوره‌ها"><thead><tr><th>دوره</th><th className="n">مبلغ</th><th>وضعیت</th></tr></thead><tbody>{(periods.data ?? []).map((p) => <tr key={p.no}><td>{showDate(p.start_jalali)}</td><td className="n">{fmt(p.amount)}</td><td>{p.status === 'invoiced' ? <Badge tone="ok">صادرشده</Badge> : <Badge tone="warn">{p.last_error ? 'خطا' : 'در انتظار'}</Badge>}</td></tr>)}</tbody></table></div>}
                <Message tone="warn" title="">لغو، دوره‌های آینده را حذف می‌کند و فاکتورهای صادرشده را برنمی‌گرداند.</Message>
              </div>
            </>
          ) : undefined}
        />
      ) : (
        <>
          <div className="bar"><span className="grow" /><Button variant="pri" onClick={() => setDlg('plan')}>طرح جدید</Button></div>
          <DataTable caption="طرح‌ها" columns={planCols} rows={plans.data} rowKey={(p) => p.id} loading={plans.loading} error={plans.error} onRetry={plans.reload} empty={{ title: 'هنوز طرحی تعریف نشده' }} />
        </>
      )}
      {dlg === 'plan' && <FormDialog title="طرح جدید" submitLabel="ذخیرهٔ طرح" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }, { name: 'price', label: 'مبلغ (ریال)', required: true, kind: 'amount', w: 'amt' }, { name: 'unit', label: 'واحد دوره', kind: 'select', options: [{ value: 'month', label: 'ماهانه' }, { value: 'year', label: 'سالانه' }, { value: 'day', label: 'روزانه' }], initial: 'month' }, { name: 'count', label: 'تعداد واحد در هر دوره', kind: 'number', w: 'sm', initial: '1' }]} onSubmit={(v) => subscriptionApi.addPlan({ code: val.text(v.code), name: val.text(v.name), price: val.amount(v.price), unit: v.unit!, count: val.int(v.count) || 1 })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), plans.reload(), toast.push({ tone: 'ok', title: 'طرح ذخیره شد' }))} />}
      {dlg === 'sub' && <FormDialog title="مشترک جدید" submitLabel="ثبت مشترک" fields={[{ name: 'customer', label: 'مشتری (شناسه)', required: true }, { name: 'plan', label: 'طرح', kind: 'select', required: true, options: (plans.data ?? []).filter((p) => p.active).map((p) => ({ value: p.id, label: `${p.name} – ${fmt(p.price)}` })) }, { name: 'coupon', label: 'کد کوپن', kind: 'latin', w: 'md' }]} onSubmit={(v, key) => subscriptionApi.subscribe({ plan_id: v.plan!, customer_ref: val.text(v.customer), buyer: { name: val.text(v.customer) }, ...(val.text(v.coupon) ? { coupon_code: val.text(v.coupon) } : {}) }, key)} onClose={() => setDlg(null)} onDone={() => (setDlg(null), subs.reload(), toast.push({ tone: 'ok', title: 'مشترک ثبت شد' }))} />}
      {dlg === 'cancel' && sel && <FormDialog title="لغو اشتراک" danger submitLabel="لغو اشتراک" fields={[{ name: 'when', label: 'زمان لغو', kind: 'select', options: [{ value: 'end', label: 'پایان دورهٔ جاری' }, { value: 'now', label: 'همین حالا' }], initial: 'end' }]} onSubmit={(v) => subscriptionApi.cancel(sel.id, v.when !== 'now')} onClose={() => setDlg(null)} onDone={(s) => (setDlg(null), setSel(s), subs.reload(), toast.push({ tone: 'ok', title: 'لغو ثبت شد' }))} />}
    </>
  )
}
