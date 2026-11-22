import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { inventoryApi, type ProdOrder } from '@/api/inventory'
import { fmt, toFa } from '@/shared/digits'
import { Badge, Button, Card, DataTable, MasterDetail, Message, PageBar, Stat, Steps, useResource, useToast, type Column } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'
import { jalaliToday, showDate } from '@/shared/jalali'

const ST: Record<string, { l: string; t: 'ok' | 'info' | 'warn' | ''; step: number }> = { open: { l: 'باز', t: 'info', step: 0 }, in_progress: { l: 'در حال تولید', t: 'info', step: 1 }, completed: { l: 'تکمیل', t: 'ok', step: 2 } }

export default function Production() {
  const toast = useToast()
  const orders = useResource(async () => itemsOf(await inventoryApi.orders() as never) as ProdOrder[], [])
  const items = useResource(async () => itemsOf(await inventoryApi.items()), [])
  const wip = useResource(() => inventoryApi.wip().catch(() => ({}) as Record<string, unknown>), [])
  const [sel, setSel] = useState<ProdOrder>()
  const [dlg, setDlg] = useState<'new' | 'complete' | null>(null)
  const name = (id: string) => items.data?.find((i) => i.id === id)?.name ?? id.slice(0, 8)
  const itemOpts = (items.data ?? []).map((i) => ({ value: i.id, label: `${i.code} ${i.name}` }))
  const cost = (o: ProdOrder) => o.material_cost + o.labor_cost + o.fixed_overhead + o.variable_overhead
  const cols: Column<ProdOrder>[] = [{ id: 'c', header: 'سفارش', num: true, cell: (o) => o.code }, { id: 'p', header: 'محصول', cell: (o) => name(o.product_item_id) }, { id: 'q', header: 'تعداد', num: true, cell: (o) => toFa(o.planned_quantity) }, { id: 'k', header: 'هزینه تا الان', num: true, cell: (o) => fmt(cost(o)) }, { id: 's', header: 'وضعیت', cell: (o) => <Badge tone={ST[o.status]?.t}>{ST[o.status]?.l ?? o.status}</Badge> }]
  const after = (m: string) => () => { toast.push({ tone: 'ok', title: m }); orders.reload(); wip.reload() }
  const openCount = (orders.data ?? []).filter((o) => o.status !== 'completed').length
  const w = wip.data ?? {}

  return (
    <>
      <PageBar title="تولید"><Button variant="pri" onClick={() => setDlg('new')}>سفارش تولید جدید</Button></PageBar>
      <div className="grid g3"><Stat label="کار در جریان ساخت" value={fmt(Number(w.total ?? w.value ?? (orders.data ?? []).filter((o) => o.status !== 'completed').reduce((s, o) => s + cost(o), 0)))} /><Stat label="سربار تسهیم‌نشده" value={w.unabsorbed != null ? fmt(Number(w.unabsorbed)) : '—'} /><Stat label="سفارش باز" value={fmt(openCount)} /></div>
      <MasterDetail listBasis={440} detailBasis={440}
        list={<DataTable caption="سفارش‌های تولید" columns={cols} rows={orders.data} rowKey={(o) => o.id} loading={orders.loading} error={orders.error} onRetry={orders.reload} empty={{ title: 'هنوز سفارش تولیدی نیست' }} onRowClick={(o) => setSel(o)} rowClass={(o) => (o.id === sel?.id ? 'sel' : undefined)} />}
        detail={sel ? (
          <>
            <div className="ch">سفارش {sel.code}</div>
            <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Steps steps={['باز', 'مواد و دستمزد', 'تکمیل']} current={ST[sel.status]?.step ?? 0} />
              {sel.status !== 'completed' && (
                <>
                  <InlineForm submitLabel="ثبت مصرف" rows={[['item', 'qty']]} fields={[{ name: 'item', label: 'مصرف مواد', kind: 'select', options: itemOpts, required: true }, { name: 'qty', label: 'مقدار', kind: 'number', w: 'sm', required: true }]} onSubmit={(v) => inventoryApi.material(sel.id, { item_id: v.item!, quantity: val.int(v.qty) })} onDone={after('مصرف ثبت شد')} />
                  <InlineForm submitLabel="ثبت دستمزد" fields={[{ name: 'amt', label: 'دستمزد مستقیم (ریال)', kind: 'amount', w: 'amt', required: true }]} onSubmit={(v) => inventoryApi.labor(sel.id, { amount: val.amount(v.amt), counter_account: 'payroll.salaries_payable' })} onDone={after('دستمزد ثبت شد')} />
                  <InlineForm submitLabel="ثبت" fields={[{ name: 'amt', label: 'سربار واقعی (ریال)', kind: 'amount', w: 'amt', required: true }]} onSubmit={(v, key) => inventoryApi.overhead({ amount: val.amount(v.amt), counter_account: 'payable.suppliers', ref: sel.code }, key)} onDone={after('سربار ثبت شد')}><Button onClick={async () => { try { await inventoryApi.settleOverhead(); after('سربار تسهیم شد')() } catch (e) { toast.error(e) } }}>تسهیم سربار</Button></InlineForm>
                  <Message tone="info" title="">سربار با نرخ از پیش‌تعیین‌شده جذب می‌شود؛ تفاوت آن با سربار واقعی در پایان دوره تسهیم می‌شود.</Message>
                  <div className="bar"><Button variant="pri" onClick={() => setDlg('complete')}>تکمیل سفارش</Button></div>
                </>
              )}
            </div>
          </>
        ) : undefined} />
      {dlg === 'new' && <FormDialog title="سفارش تولید جدید" submitLabel="ثبت سفارش" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'item', label: 'محصول', kind: 'select', options: itemOpts, required: true }, { name: 'qty', label: 'تعداد برنامه‌ریزی‌شده', kind: 'number', required: true, w: 'sm' }]} onSubmit={(v) => inventoryApi.openOrder({ code: val.text(v.code), product_item_id: v.item!, planned_quantity: val.int(v.qty) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), after('سفارش ثبت شد')())} />}
      {dlg === 'complete' && sel && <FormDialog title={`تکمیل سفارش ${sel.code}`} submitLabel="تکمیل" fields={[{ name: 'out', label: 'تعداد تولیدشده', kind: 'number', required: true, w: 'sm', initial: String(sel.planned_quantity) }, { name: 'loss', label: 'ضایعات غیرعادی', kind: 'number', w: 'sm' }, { name: 'date', label: 'تاریخ', kind: 'date', w: 'date', initial: showDate(jalaliToday()) }]} onSubmit={(v) => inventoryApi.complete(sel.id, { output_quantity: val.int(v.out), abnormal_loss: val.int(v.loss) || undefined, date: val.iso(v.date) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), setSel(undefined), after('سفارش تکمیل شد')())} />}
      <Card title="نرخ سربار" bare><div className="cb sub">نرخ‌های سربار به‌صورت نسخه‌دار از طریق API (POST /production/overhead-rates) ثبت می‌شوند؛ رابط افزودن نرخ هنوز ساخته نشده است.</div></Card>
    </>
  )
}
