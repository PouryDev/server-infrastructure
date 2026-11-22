import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { itemsOf } from '@/api/commerce'
import { assetApi, type IdleSpan } from '@/api/assets'
import { treasury } from '@/api/treasury'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday, showDate } from '@/shared/jalali'
import { Button, Card, Field, KV, Message, PageBar, Select, Seg, useResource, useToast } from '@/ui'
import { InlineForm, val } from '@/ui/FormDialog'

/** Acquisition, disposal (full / partial), usage for units-of-production assets, and idle periods. */
export default function AssetOps() {
  const toast = useToast()
  const [params, setParams] = useSearchParams()
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const cats = useResource(async () => itemsOf(await assetApi.categories()), [])
  const assets = useResource(async () => itemsOf(await assetApi.list({ pageSize: 100 })), [])
  const limit = useResource(() => assetApi.expenseLimit().catch(() => ({}) as Record<string, unknown>), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const asset = assets.data?.find((a) => a.id === params.get('asset')) ?? assets.data?.find((a) => a.status !== 'disposed')
  const idle = useResource(async () => (asset ? itemsOf(await assetApi.idleSpans(asset.id)) : []), [asset?.id])
  const [mode, setMode] = useState<'full' | 'part'>('full')
  const [proceeds, setProceeds] = useState('')
  const bank = banks.data?.[0]?.ledger_code ?? 'bank.main'
  const small = Number(limit.data?.small_limit ?? 0)
  const loss = asset ? val.amount(proceeds) - asset.book_value : undefined

  return (
    <>
      <PageBar title="تحصیل و واگذاری دارایی" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="تحصیل دارایی">
          <InlineForm submitLabel="ثبت تحصیل" rows={[['name', 'code'], ['cat', 'date'], ['cost', 'salvage']]} fields={[{ name: 'name', label: 'نام', required: true, w: 'md' }, { name: 'code', label: 'کد', required: true, kind: 'latin', w: 'code' }, { name: 'cat', label: 'طبقه', kind: 'select', required: true, options: (cats.data ?? []).filter((c) => c.active).map((c) => ({ value: c.id, label: `${c.name}${c.life_months ? ` · ${toFa(c.life_months / 12)} سال` : ''}` })) }, { name: 'date', label: 'تاریخ بهره‌برداری', kind: 'date', required: true, w: 'date', initial: showDate(jalaliToday()) }, { name: 'cost', label: 'بهای تمام‌شده', kind: 'amount', required: true, w: 'amt' }, { name: 'salvage', label: 'ارزش اسقاط', kind: 'amount', w: 'amt' }]}
            onSubmit={(v, key) => assetApi.acquire({ code: val.text(v.code), name: val.text(v.name), category_id: v.cat!, acquired_on: val.iso(v.date)!, in_service_on: val.iso(v.date), cost: val.amount(v.cost), salvage: val.amount(v.salvage) || undefined, paid_from: bank }, key)} onDone={() => (assets.reload(), toast.push({ tone: 'ok', title: 'دارایی ثبت شد' }))}>
            {small > 0 && <Message tone="warn" title="آستانهٔ دارایی">دارایی‌هایی با مبلغ کمتر از {fmt(small)} ریال باید «هزینهٔ دارایی کوچک» ثبت شوند (صفحهٔ «استهلاک و تنظیمات»).</Message>}
            <div className="sub">پرداخت از حساب بانکی «{banks.data?.[0]?.name ?? '—'}».</div>
          </InlineForm>
        </Card>
        <Card title={asset ? `واگذاری دارایی ${asset.code}` : 'واگذاری دارایی'}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label="دارایی">{() => <Select label="دارایی" width="280px" value={asset?.id ?? ''} options={(assets.data ?? []).filter((a) => a.status !== 'disposed').map((a) => ({ value: a.id, label: `${a.code} ${a.name}` }))} onChange={(v) => setParams({ asset: v })} />}</Field>
            <Seg<'full' | 'part'> label="نوع" value={mode} onChange={setMode} options={[{ value: 'full', label: 'کامل' }, { value: 'part', label: 'بخشی' }]} />
            {asset && (
              <InlineForm key={asset.id + mode} submitLabel="ثبت واگذاری" danger fields={[{ name: 'date', label: 'تاریخ', kind: 'date', required: true, w: 'date', initial: showDate(jalaliToday()) }, { name: 'price', label: 'قیمت فروش (ریال)', kind: 'amount', required: true, w: 'amt' }, ...(mode === 'part' ? [{ name: 'pct', label: 'درصد واگذاری', kind: 'number' as const, required: true, w: 'sm' as const, initial: '50' }] : [])]}
                onSubmit={(v, key) => { setProceeds(v.price ?? ''); return mode === 'full' ? assetApi.dispose(asset.id, { date: val.iso(v.date)!, proceeds: val.amount(v.price), proceeds_account: bank }, key) : assetApi.partialDispose(asset.id, { portion_bp: Math.round(Number(val.int(v.pct)) * 100), date: val.iso(v.date)!, proceeds: val.amount(v.price), proceeds_account: bank }, key) }} onDone={() => (assets.reload(), toast.push({ tone: 'ok', title: 'واگذاری ثبت شد' }))}>
                <KV rows={[['ارزش دفتری', <span key="b" className="n">{fmt(asset.book_value)}</span>], [<b key="l">{(loss ?? 0) < 0 ? 'زیان واگذاری' : 'سود واگذاری'}</b>, <b key="v" className={`n ${(loss ?? 0) < 0 ? 'neg' : ''}`}>{loss === undefined || Number.isNaN(loss) ? '—' : fmt(loss)}</b>]]} />
                <Message tone="warn" title="">استهلاک تا تاریخ واگذاری اول محاسبه می‌شود. واگذاری برگشت‌پذیر نیست.</Message>
                <div className="sub">ارزش دفتری و سود/زیان با فرض واگذاری کامل نمایش داده می‌شود؛ محاسبهٔ نهایی را سرور انجام می‌دهد. قیمت را در این کادر برای پیش‌نمایش تایپ کنید:
                  <input aria-label="قیمت برای پیش‌نمایش" className="inp w-amt n" style={{ marginInlineStart: 8 }} value={proceeds} onChange={(e) => setProceeds(e.target.value)} /></div>
              </InlineForm>
            )}
          </div>
        </Card>
        <Card title="ثبت استفاده (برای روش بر اساس استفاده)">
          {asset && <InlineForm key={asset.id} submitLabel="ثبت" rows={[['year', 'month', 'units']]} fields={[{ name: 'year', label: 'سال', kind: 'number', w: 'sm', initial: String(jy), required: true }, { name: 'month', label: 'ماه', kind: 'number', w: 'sm', initial: String(jm), required: true }, { name: 'units', label: 'مصرف (واحد)', kind: 'number', w: 'sm', required: true }]} onSubmit={(v) => assetApi.usage(asset.id, { jalali_year: val.int(v.year), jalali_month: val.int(v.month), units: val.int(v.units) })} onDone={() => toast.push({ tone: 'ok', title: 'استفاده ثبت شد' })} />}
        </Card>
        <Card title="دوره‌های بیکاری">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div className="tw"><table className="tbl" aria-label="دوره‌های بیکاری"><tbody>{(idle.data ?? []).map((s: IdleSpan) => <tr key={s.id}><td>{asset?.code}</td><td className="n">{`${MONTHS[s.from_jalali_month - 1]} ${toFa(s.from_jalali_year)} تا ${MONTHS[s.to_jalali_month - 1]} ${toFa(s.to_jalali_year)}`}</td><td><Button small variant="dng" onClick={async () => { try { await assetApi.removeIdle(asset!.id, s.id); idle.reload() } catch (e) { toast.error(e) } }}>حذف</Button></td></tr>)}</tbody></table>{idle.data?.length === 0 && <div className="empty">دورهٔ بیکاری‌ای نیست.</div>}</div>
            {asset && <InlineForm key={asset.id} submitLabel="افزودن" rows={[['fy', 'fm', 'ty', 'tm']]} fields={[{ name: 'fy', label: 'از سال', kind: 'number', w: 'sm', initial: String(jy) }, { name: 'fm', label: 'از ماه', kind: 'number', w: 'sm', initial: String(jm) }, { name: 'ty', label: 'تا سال', kind: 'number', w: 'sm', initial: String(jy) }, { name: 'tm', label: 'تا ماه', kind: 'number', w: 'sm', initial: String(jm) }]} onSubmit={(v) => assetApi.addIdle(asset.id, { from_jalali_year: val.int(v.fy), from_jalali_month: val.int(v.fm), to_jalali_year: val.int(v.ty), to_jalali_month: val.int(v.tm) })} onDone={() => idle.reload()} />}
            <div className="sub">در دورهٔ بیکاری، استهلاک محاسبه نمی‌شود.</div>
          </div>
        </Card>
      </div>
    </>
  )
}
