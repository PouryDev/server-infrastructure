import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { inventoryApi } from '@/api/inventory'
import { fmt } from '@/shared/digits'
import { jalaliToday, showDate } from '@/shared/jalali'
import { Card, Field, KV, PageBar, Select, Seg, useResource, useToast } from '@/ui'
import { InlineForm, val } from '@/ui/FormDialog'

/** Stock operations: receipt, issue / transfer, count, write-down and its reversal. */
export default function InvOps() {
  const toast = useToast()
  const items = useResource(async () => itemsOf(await inventoryApi.items()), [])
  const wh = useResource(async () => itemsOf(await inventoryApi.warehouses()), [])
  const valuation = useResource(() => inventoryApi.valuation(), [])
  const [mode, setMode] = useState<'issue' | 'transfer'>('issue')
  const [item, setItem] = useState('')
  const it = item || items.data?.[0]?.id || ''
  const whOpts = (wh.data ?? []).map((w) => ({ value: w.id, label: w.name }))
  const itemOpts = (items.data ?? []).map((i) => ({ value: i.id, label: `${i.code} ${i.name}` }))
  const today = showDate(jalaliToday())
  const done = (m: string) => () => { toast.push({ tone: 'ok', title: m }); items.reload(); valuation.reload() }
  const selItem = <Field label="کالا">{() => <Select label="کالا" width="260px" value={it} options={itemOpts} onChange={setItem} />}</Field>

  return (
    <>
      <PageBar title="عملیات انبار">
        {selItem}
      </PageBar>
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="رسید انبار">
          <InlineForm submitLabel="ثبت رسید" rows={[['wh', 'date'], ['qty', 'cost'], ['lot', 'exp']]} fields={[{ name: 'wh', label: 'انبار', kind: 'select', options: whOpts, initial: whOpts[0]?.value }, { name: 'date', label: 'تاریخ', kind: 'date', w: 'date', initial: today }, { name: 'qty', label: 'تعداد', kind: 'number', w: 'sm', required: true }, { name: 'cost', label: 'بهای کل (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'lot', label: 'شمارهٔ لات', kind: 'latin', w: 'md' }, { name: 'exp', label: 'انقضا', kind: 'date', w: 'date' }]}
            onSubmit={(v, key) => inventoryApi.receive({ item_id: it, quantity: val.int(v.qty), total_cost: val.amount(v.cost), date: val.iso(v.date), counter_account: 'payable.suppliers', warehouse_id: v.wh || undefined, lot_no: val.text(v.lot) || undefined, expires_on: val.iso(v.exp) }, key)} onDone={done('رسید ثبت شد')}>
            <div className="sub">بدهی به «حساب‌های پرداختنی تأمین‌کنندگان» ثبت می‌شود.</div>
          </InlineForm>
        </Card>
        <Card title="حواله (خروج) و انتقال">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Seg<'issue' | 'transfer'> label="نوع" value={mode} onChange={setMode} options={[{ value: 'issue', label: 'خروج' }, { value: 'transfer', label: 'انتقال بین انبارها' }]} />
            {mode === 'issue' ? (
              <InlineForm key="i" submitLabel="ثبت خروج" rows={[['wh', 'qty'], ['why']]} fields={[{ name: 'wh', label: 'انبار', kind: 'select', options: whOpts, initial: whOpts[0]?.value }, { name: 'qty', label: 'تعداد', kind: 'number', w: 'sm', required: true }, { name: 'why', label: 'دلیل', w: 'md' }]} onSubmit={(v, key) => inventoryApi.issue({ item_id: it, quantity: val.int(v.qty), expense_account: 'expense.cogs', warehouse_id: v.wh || undefined, reason: val.text(v.why) || undefined }, key)} onDone={done('خروج ثبت شد')} />
            ) : (
              <InlineForm key="t" submitLabel="ثبت انتقال" rows={[['from', 'to'], ['qty']]} fields={[{ name: 'from', label: 'از انبار', kind: 'select', options: whOpts, initial: whOpts[0]?.value, required: true }, { name: 'to', label: 'به انبار', kind: 'select', options: whOpts, initial: whOpts[1]?.value, required: true }, { name: 'qty', label: 'تعداد', kind: 'number', w: 'sm', required: true }]} onSubmit={(v, key) => inventoryApi.transfer({ item_id: it, from_warehouse_id: v.from!, to_warehouse_id: v.to!, quantity: val.int(v.qty) }, key)} onDone={done('انتقال ثبت شد')} />
            )}
          </div>
        </Card>
        <Card title="انبارگردانی">
          <InlineForm submitLabel="ثبت انبارگردانی" rows={[['wh', 'qty'], ['why']]} fields={[{ name: 'wh', label: 'انبار', kind: 'select', options: whOpts, initial: whOpts[0]?.value }, { name: 'qty', label: 'تعداد شمارش‌شده', kind: 'number', w: 'sm', required: true }, { name: 'why', label: 'دلیل', w: 'md' }]} onSubmit={(v, key) => inventoryApi.count({ item_id: it, counted_quantity: val.int(v.qty), warehouse_id: v.wh || undefined, reason: val.text(v.why) || undefined }, key)} onDone={done('انبارگردانی ثبت شد')}>
            <div className="msg warn"><div className="tx">با ثبت، کسری به هزینه و اضافی به درآمد منتقل می‌شود.</div></div>
          </InlineForm>
        </Card>
        <Card title="کاهش ارزش موجودی">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <InlineForm submitLabel="ثبت کاهش" rows={[['nrv', 'why']]} fields={[{ name: 'nrv', label: 'ارزش جدید واحد (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'why', label: 'دلیل', w: 'md', required: true }]} onSubmit={(v, key) => inventoryApi.writeDown({ item_id: it, net_realizable_value: val.amount(v.nrv), reason: val.text(v.why) }, key)} onDone={done('کاهش ارزش ثبت شد')} />
            <InlineForm submitLabel="برگشت کاهش" rows={[['nrv', 'why']]} fields={[{ name: 'nrv', label: 'ارزش جدید واحد (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'why', label: 'دلیل', w: 'md', required: true }]} onSubmit={(v, key) => inventoryApi.reverseWriteDown({ item_id: it, net_realizable_value: val.amount(v.nrv), reason: val.text(v.why) }, key)} onDone={done('برگشت کاهش ثبت شد')} />
            <div className="div" />
            <KV rows={[['ارزش‌گذاری کل موجودی', <b key="v" className="n">{valuation.data ? fmt(valuation.data.total_value) : '…'}</b>]]} />
          </div>
        </Card>
      </div>
    </>
  )
}
