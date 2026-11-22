import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { auditApi } from '@/api/audit'
import { showDateTime } from '@/shared/jalali'
import { toFa } from '@/shared/digits'
import { Badge, Button, Card, ErrorMessage, Field, Input, PageBar, Select, Skeleton, useResource } from '@/ui'

const TYPES = [['invoice', 'فاکتور'], ['payment', 'پرداخت'], ['refund', 'بازگشت وجه'], ['wallet', 'کیف پول'], ['dispute', 'اختلاف'], ['reconciliation', 'تطبیق'], ['approval', 'تأیید'], ['fiscal_year', 'سال مالی'], ['fixed_asset', 'دارایی ثابت'], ['inventory_item', 'کالا'], ['production_order', 'سفارش تولید'], ['vat_return', 'اظهارنامه ارزش افزوده'], ['income_tax', 'برگ تشخیص'], ['einvoice', 'صورتحساب الکترونیکی']]

/** Audit trail of one record: who did what and when; records cannot be edited. */
export default function Audit() {
  const [params, setParams] = useSearchParams()
  const [type, setType] = useState(params.get('type') ?? 'invoice')
  const [id, setId] = useState(params.get('id') ?? '')
  const q = { type: params.get('type') ?? '', id: params.get('id') ?? '' }
  const res = useResource(async () => (q.type && q.id ? auditApi.trail(q.type, q.id) : undefined), [q.type, q.id])
  const rows = res.data
  return (
    <>
      <PageBar title="ردیابی رکورد" sub="هر تغییر با کاربر و زمان ثبت می‌شود و قابل ویرایش نیست" />
      <Card>
        <form className="row" onSubmit={(e) => (e.preventDefault(), setParams({ type, id: id.trim() }))}>
          <Field label="نوع رکورد">{() => <Select label="نوع رکورد" width="190px" value={type} options={TYPES.map(([value, label]) => ({ value: value!, label: label! }))} onChange={setType} />}</Field>
          <Field label="شناسه">{(i) => <Input id={i} w="lg" dir="ltr" num value={id} onChange={(e) => setId(e.target.value)} />}</Field>
          <Button variant="pri" type="submit" disabled={!id.trim()}>نمایش</Button>
        </form>
      </Card>
      {res.error != null ? <ErrorMessage error={res.error} onRetry={res.reload} /> : res.loading && q.id ? <Skeleton /> : rows ? (
        <Card title={`${TYPES.find(([v]) => v === q.type)?.[1] ?? q.type} ${q.id.slice(0, 8)} · ${toFa(rows.length)} رویداد`} bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}>
            <table className="tbl" aria-label="رویدادها"><thead><tr><th>زمان</th><th>کاربر</th><th>عملیات</th><th>جزئیات</th></tr></thead>
              <tbody>{rows.map((r) => <tr key={r.id}><td className="n">{showDateTime(r.at)}</td><td>{r.actor}</td><td><Badge tone="info">{r.action}</Badge></td><td className="mut">{typeof r.details === 'string' ? r.details : r.details ? JSON.stringify(r.details) : '—'}</td></tr>)}</tbody></table>
            {rows.length === 0 && <div className="empty"><b>رویدادی ثبت نشده</b><span>برای این شناسه رکوردی پیدا نشد.</span></div>}
          </div>
        </Card>
      ) : null}
    </>
  )
}
