import { useState } from 'react'
import { couponApi, itemsOf, type Coupon } from '@/api/commerce'
import { fmt, toFa } from '@/shared/digits'
import { isoToJalali, showDate } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, ErrorMessage, MasterDetail, Message, PageBar, Skeleton, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { Field, Input } from '@/ui'

const status = (c: Coupon) => (!c.active ? { l: 'غیرفعال', t: 'warn' as const } : c.max_redemptions != null && c.redeemed >= c.max_redemptions ? { l: 'پایان‌یافته', t: '' as const } : { l: 'فعال', t: 'ok' as const })
const valueOf = (c: Coupon) => (c.kind === 'percent' ? `${toFa((c.percent_bp ?? 0) / 100)}٪` : fmt(c.fixed_amount))

export default function Coupons() {
  const toast = useToast()
  const list = useListState({}, 20)
  const [sel, setSel] = useState<Coupon>()
  const [dlg, setDlg] = useState<'new' | 'edit' | null>(null)
  const [test, setTest] = useState('')
  const [disc, setDisc] = useState<number>()
  const data = useResource((s) => couponApi.list({ page: list.page, pageSize: list.pageSize, filter: Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v).map(([k, v]) => [k === 'code' ? 'code:like' : k, v])) }, s), [list.page, JSON.stringify(list.filters)])
  const reds = useResource(async () => (sel ? itemsOf(await couponApi.redemptions(sel.id)) : []), [sel?.id])

  const cols: Column<Coupon>[] = [
    { id: 'code', header: 'کد', num: true, filter: 'code', cell: (c) => c.code },
    { id: 'kind', header: 'نوع', cell: (c) => (c.kind === 'percent' ? 'درصدی' : 'مبلغ ثابت') },
    { id: 'value', header: 'مقدار', num: true, cell: valueOf },
    { id: 'used', header: 'استفاده', num: true, cell: (c) => (c.max_redemptions != null ? `${toFa(c.redeemed)} از ${toFa(c.max_redemptions)}` : toFa(c.redeemed)) },
    { id: 'until', header: 'اعتبار تا', num: true, cell: (c) => (c.valid_until ? showDate(isoToJalali(c.valid_until)) : '—') },
    { id: 'st', header: 'وضعیت', cell: (c) => <Badge tone={status(c).t}>{status(c).l}</Badge> },
  ]
  const preview = async () => { if (!sel) return; try { setDisc((await couponApi.preview(sel.code, 'preview', val.amount(test))).discount) } catch (e) { setDisc(undefined); toast.error(e) } }
  const formFields = (c?: Coupon) => [
    { name: 'code', label: 'کد', required: true, kind: 'latin' as const, w: 'md' as const, initial: c?.code },
    { name: 'name', label: 'نام', initial: c?.name },
    { name: 'kind', label: 'نوع', kind: 'select' as const, options: [{ value: 'percent', label: 'درصدی' }, { value: 'fixed', label: 'مبلغ ثابت' }], initial: c?.kind ?? 'percent' },
    { name: 'value', label: 'مقدار (درصد یا ریال)', required: true, kind: 'number' as const, w: 'amt' as const, initial: c ? String(c.kind === 'percent' ? (c.percent_bp ?? 0) / 100 : c.fixed_amount) : '' },
    { name: 'max', label: 'سقف استفاده', kind: 'number' as const, w: 'sm' as const, initial: c?.max_redemptions != null ? String(c.max_redemptions) : '' },
    { name: 'until', label: 'اعتبار تا', kind: 'date' as const, w: 'date' as const, initial: c?.valid_until ? showDate(isoToJalali(c.valid_until)) : '' },
  ]
  return (
    <>
      <PageBar title="کوپن‌ها"><Button variant="pri" onClick={() => setDlg('new')}>کوپن جدید</Button></PageBar>
      <MasterDetail
        list={<DataTable caption="کوپن‌ها" columns={cols} rows={data.data?.items} rowKey={(c) => c.id} loading={data.loading} error={data.error} onRetry={data.reload} empty={{ title: 'هنوز کوپنی تعریف نشده' }} filters={list.filters} onFilters={list.setFilters} meta={data.data?.meta} onPage={list.setPage} onRowClick={(c) => setSel(c)} rowClass={(c) => (c.id === sel?.id ? 'sel' : undefined)} />}
        detail={sel ? (
          <>
            <div className="ch">کوپن {sel.code}<span className="sp" /><Button small onClick={() => setDlg('edit')}>ویرایش</Button></div>
            <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Field label="آزمون اعمال روی مبلغ">{(id) => <div className="row"><Input id={id} w="amt" num value={test} onChange={(e) => setTest(e.target.value)} /><Button onClick={() => void preview()}>پیش‌نمایش</Button></div>}</Field>
              <dl className="kv" style={{ margin: 0 }}><dt>تخفیف</dt><dd className="n">{disc === undefined ? '—' : fmt(disc)}</dd></dl>
              <div className="div" />
              <div className="sub">آخرین استفاده‌ها</div>
              {reds.error != null ? <ErrorMessage error={reds.error} /> : reds.loading ? <Skeleton rows={2} /> : (
                <div className="tw"><table className="tbl" aria-label="استفاده‌ها"><tbody>{(reds.data ?? []).map((r) => <tr key={r.id}><td className="n">{r.subject_type} {r.subject_id?.slice(0, 8)}</td><td className="n">{fmt(r.discount)}</td><td>{r.status === 'redeemed' ? <Button small onClick={async () => { try { await couponApi.release(r.id, 'آزادسازی دستی'); reds.reload(); data.reload() } catch (e) { toast.error(e) } }}>آزادسازی</Button> : <Badge>آزادشده</Badge>}</td></tr>)}</tbody></table>{reds.data?.length === 0 && <div className="empty">هنوز استفاده‌ای نیست.</div>}</div>
              )}
              <Message tone="warn" title="">آزادسازی استفاده را به سقف برمی‌گرداند و تخفیف را از فاکتور برنمی‌دارد.</Message>
            </div>
          </>
        ) : <Card><div className="empty">یک کوپن را انتخاب کنید.</div></Card>}
      />
      {dlg === 'new' && <FormDialog title="کوپن جدید" submitLabel="ایجاد" fields={formFields()} onSubmit={(v) => couponApi.create({ code: val.text(v.code), name: val.text(v.name) || val.text(v.code), kind: v.kind as 'percent' | 'fixed', ...(v.kind === 'percent' ? { percent_bp: Math.round(Number(val.int(v.value)) * 100) } : { fixed_amount: val.amount(v.value) }), ...(v.max ? { max_redemptions: val.int(v.max) } : {}), ...(val.iso(v.until) ? { valid_until: val.iso(v.until) } : {}) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), data.reload(), toast.push({ tone: 'ok', title: 'کوپن ایجاد شد' }))} />}
      {dlg === 'edit' && sel && <FormDialog title={`ویرایش کوپن ${sel.code}`} submitLabel="ذخیره" intro="کد و نوع کوپن بعد از ساخت تغییر نمی‌کند." fields={formFields(sel).filter((f) => ['name', 'max', 'until'].includes(f.name))} onSubmit={(v) => couponApi.update(sel.id, { name: val.text(v.name), ...(v.max ? { max_redemptions: val.int(v.max) } : { clear_max_redemptions: true }), ...(val.iso(v.until) ? { valid_until: val.iso(v.until) } : { clear_valid_until: true }) })} onClose={() => setDlg(null)} onDone={(c) => (setDlg(null), setSel(c), data.reload(), toast.push({ tone: 'ok', title: 'ذخیره شد' }))} />}
    </>
  )
}
