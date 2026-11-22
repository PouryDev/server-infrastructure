import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { assetApi, type DepRun } from '@/api/assets'
import { treasury } from '@/api/treasury'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday, showDate } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, KV, Message, PageBar, Select, useResource, useToast } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'
import { newIdempotencyKey } from '@/shared/idempotency'

const METHOD: Record<string, string> = { straight_line: 'خط مستقیم', declining_balance: 'نزولی', units_of_production: 'بر اساس استفاده' }

/** Depreciation run (idempotent per month), categories (own or from the legal table), thresholds and small-asset expense. */
export default function AssetSetup() {
  const toast = useToast()
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const [month, setMonth] = useState(jm)
  const [run, setRun] = useState<DepRun>()
  const cats = useResource(async () => itemsOf(await assetApi.categories()), [])
  const thr = useResource(async () => itemsOf(await assetApi.thresholds()), [])
  const limit = useResource(() => assetApi.expenseLimit().catch(() => ({}) as Record<string, unknown>), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [dlg, setDlg] = useState<'cat' | 'legal' | null>(null)
  const legal = useResource(async () => (dlg === 'legal' ? itemsOf(await assetApi.legalTable()) : []), [dlg])
  const bank = banks.data?.[0]?.ledger_code ?? 'bank.main'

  return (
    <>
      <PageBar title="استهلاک و تنظیمات دارایی" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="اجرای استهلاک">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label="ماه">{() => <Select label="ماه" width="130px" value={String(month)} options={MONTHS.map((n, i) => ({ value: String(i + 1), label: n }))} onChange={(v) => setMonth(Number(v))} />}</Field>
            {run && (
              <div className="tw"><table className="tbl" aria-label="نتیجهٔ استهلاک"><thead><tr><th>دارایی</th><th className="n">استهلاک ماه</th></tr></thead><tbody>{run.items.map((i) => <tr key={i.asset_id}><td>{i.asset_code}</td><td className="n">{fmt(i.amount)}</td></tr>)}</tbody><tfoot><tr><td>جمع</td><td className="n">{fmt(run.total)}</td></tr></tfoot></table></div>
            )}
            <div className="bar">
              <Button variant="pri" onClick={async () => { try { setRun(await assetApi.run(jy, month, newIdempotencyKey('dep'))); toast.push({ tone: 'ok', title: 'استهلاک اجرا و سند ثبت شد' }) } catch (e) { toast.error(e) } }}>اجرا و ثبت سند</Button>
              <Button variant="dng" onClick={async () => { try { await assetApi.voidRun(jy, month, newIdempotencyKey('depvoid')); setRun(undefined); toast.push({ tone: 'ok', title: `اجرای ${MONTHS[month - 1]} ابطال شد` }) } catch (e) { toast.error(e) } }}>ابطال اجرای {MONTHS[month - 1]}</Button>
            </div>
            <Message tone="info" title="">اجرای دوباره برای همین ماه چیزی اضافه ثبت نمی‌کند. پیش‌نمایش پیش از اجرا در API وجود ندارد؛ جدول بالا نتیجهٔ اجراست.</Message>
          </div>
        </Card>
        <Card title="طبقه‌های دارایی" extra={<><Button small onClick={() => setDlg('legal')}>از جدول قانونی</Button><Button small onClick={() => setDlg('cat')}>طبقهٔ جدید</Button></>} bare>
          {cats.error != null ? <div className="cb"><ErrorMessage error={cats.error} /></div> : (
            <div className="tw" style={{ border: 0, borderRadius: 0, maxHeight: 360 }}><table className="tbl" aria-label="طبقه‌ها"><thead><tr><th>طبقه</th><th className="n">عمر مفید</th><th>روش</th><th>وضعیت</th></tr></thead>
              <tbody>{(cats.data ?? []).map((c) => <tr key={c.id}><td>{c.name}</td><td className="n">{c.life_months ? `${toFa(c.life_months / 12)} سال` : c.rate_bp ? `${toFa(c.rate_bp / 100)}٪` : '—'}</td><td>{METHOD[c.method] ?? c.method}</td><td><button className="btn link" onClick={async () => { try { await assetApi.setCategoryState(c.id, c.active ? 'deactivate' : 'activate'); cats.reload() } catch (e) { toast.error(e) } }}><Badge tone={c.active ? 'ok' : ''}>{c.active ? 'فعال' : 'غیرفعال'}</Badge></button></td></tr>)}</tbody></table></div>
          )}
        </Card>
        <Card title="آستانه‌ها و هزینهٔ دارایی کوچک">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <InlineForm submitLabel="نسخهٔ جدید" rows={[['limit', 'from', 'src']]} fields={[{ name: 'limit', label: 'آستانهٔ دارایی (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'from', label: 'از تاریخ', kind: 'date', w: 'date', required: true, initial: showDate(`${jy}/01/01`) }, { name: 'src', label: 'منبع', w: 'md', required: true }]} onSubmit={(v) => assetApi.addThreshold({ effective_from: val.iso(v.from)!, small_limit: val.amount(v.limit), verified: false, source: val.text(v.src) })} onDone={() => (thr.reload(), limit.reload())} />
            <KV rows={[['سقف هزینهٔ امسال', <span key="l" className="n">{limit.data?.limit != null ? fmt(Number(limit.data.limit)) : '—'}{limit.data?.used != null ? ` · مصرف ${fmt(Number(limit.data.used))}` : ''}</span>]]} />
            <div className="div" />
            <InlineForm submitLabel="ثبت هزینه" rows={[['name', 'cost']]} fields={[{ name: 'name', label: 'خرید کوچک (شرح)', w: 'md', required: true }, { name: 'cost', label: 'مبلغ (ریال)', kind: 'amount', w: 'amt', required: true }]} onSubmit={(v, key) => assetApi.expenseSmall({ name: val.text(v.name), date: val.iso(showDate(jalaliToday()))!, cost: val.amount(v.cost), paid_from: bank }, key)} onDone={() => (limit.reload(), toast.push({ tone: 'ok', title: 'هزینه ثبت شد' }))} />
          </div>
        </Card>
      </div>
      {dlg === 'cat' && <FormDialog title="طبقهٔ جدید" submitLabel="ثبت طبقه" fields={[{ name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }, { name: 'method', label: 'روش', kind: 'select', options: Object.entries(METHOD).map(([value, label]) => ({ value, label })), initial: 'straight_line' }, { name: 'life', label: 'عمر مفید (ماه)', kind: 'number', w: 'sm' }, { name: 'rate', label: 'نرخ (درصد)', kind: 'number', w: 'sm' }, { name: 'src', label: 'منبع', required: true }]} onSubmit={(v) => assetApi.addCategory({ code: val.text(v.code), name: val.text(v.name), method: v.method!, ...(v.life ? { life_months: val.int(v.life) } : {}), ...(v.rate ? { rate_bp: Math.round(Number(val.int(v.rate)) * 100) } : {}), source: val.text(v.src) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), cats.reload())} />}
      {dlg === 'legal' && <FormDialog title="طبقه از جدول قانونی" submitLabel="ساخت طبقه" fields={[{ name: 'row', label: 'ردیف جدول', kind: 'select', required: true, options: legal.data?.map((r) => ({ value: String((r as Record<string, unknown>).row_id), label: String((r as Record<string, unknown>).name ?? (r as Record<string, unknown>).row_id) })) ?? [] }, { name: 'code', label: 'کد', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }]} onSubmit={(v) => assetApi.fromLegal({ row_id: v.row!, code: val.text(v.code), name: val.text(v.name) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), cats.reload())} />}
    </>
  )
}
