import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { itemsOf, payoutApi, type Sale, type Seller } from '@/api/commerce'
import { fmt, toFa } from '@/shared/digits'
import { isoToJalali, showDate } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, KV, MasterDetail, PageBar, Select, Skeleton, Steps, useResource, useToast } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

const phase = (s: Sale) => (s.status === 'returned' ? { l: 'برگشتی', t: 'err' as const, step: 0 } : s.status === 'paid' ? { l: 'آزادشده', t: 'ok' as const, step: 2 } : s.delivered_at ? { l: 'تحویل‌شده', t: 'warn' as const, step: 1 } : { l: 'در انتظار تحویل', t: 'info' as const, step: 0 })

/** One seller: sales (record, deliver, return), settlement terms and pending clawbacks. */
export default function PayoutSales() {
  const toast = useToast()
  const [params, setParams] = useSearchParams()
  const sellerId = params.get('seller') ?? ''
  const [sale, setSale] = useState<Sale>()
  const [dlg, setDlg] = useState<'sale' | 'partial' | 'return' | 'returnPaid' | 'terms' | 'block' | 'edit' | null>(null)
  const sellers = useResource(async () => itemsOf(await payoutApi.sellers()), [])
  const seller: Seller | undefined = sellers.data?.find((s) => s.id === sellerId) ?? sellers.data?.[0]
  const sales = useResource(async (signal) => (seller ? itemsOf(await payoutApi.sales({ pageSize: 50, sort: '-sale_date', filter: { seller_id: seller.id } }, signal)) : []), [seller?.id])
  const terms = useResource(async () => itemsOf(await payoutApi.terms()), [])
  const claw = useResource(async () => (seller ? itemsOf(await payoutApi.clawbacks(seller.id)) : []), [seller?.id])
  const bal = useResource(async () => (seller ? payoutApi.balance(seller.id) : undefined), [seller?.id])
  const reload = () => { sales.reload(); claw.reload(); bal.reload(); sellers.reload() }
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast.push({ tone: 'ok', title: msg }); reload(); setSale(undefined) } catch (e) { toast.error(e) } }
  const payable = (['payable', 'releasable', 'available', 'balance'].map((k) => bal.data?.[k]).find((v) => typeof v === 'number') as number | undefined)
  const done = (m: string) => () => { setDlg(null); toast.push({ tone: 'ok', title: m }); reload(); setSale(undefined) }

  return (
    <>
      <PageBar title={seller?.name ?? 'شرایط و فروش‌های فروشنده'} sub={payable !== undefined ? `مانده قابل تسویه ${fmt(payable)} ریال` : undefined}>
        {sellers.data && sellers.data.length > 0 && <Select label="فروشنده" width="200px" value={seller?.id ?? ''} options={sellers.data.map((s) => ({ value: s.id, label: s.name }))} onChange={(v) => (setParams({ seller: v }), setSale(undefined))} />}
        <Button disabled={!seller} onClick={() => setDlg('edit')}>ویرایش فروشنده</Button>
        {seller?.status === 'blocked' ? <Button disabled={!seller} onClick={() => void act(() => payoutApi.unblock(seller.id), 'فروشنده فعال شد')}>رفع مسدودی</Button> : <Button variant="dng" disabled={!seller} onClick={() => setDlg('block')}>مسدود کردن</Button>}
        <Button variant="pri" disabled={!seller} onClick={() => setDlg('sale')}>ثبت فروش</Button>
      </PageBar>
      {sellers.error != null && <ErrorMessage error={sellers.error} onRetry={sellers.reload} />}
      <MasterDetail
        listBasis={560}
        list={
          <Card title="فروش‌ها" bare>
            {sales.error != null ? <div className="cb"><ErrorMessage error={sales.error} onRetry={sales.reload} /></div> : sales.loading ? <Skeleton /> : (
              <div className="tw" style={{ border: 0, borderRadius: 0 }}>
                <table className="tbl" aria-label="فروش‌ها"><thead><tr><th>شماره</th><th>تاریخ</th><th className="n">مبلغ</th><th className="n">کمیسیون</th><th>وضعیت</th></tr></thead>
                  <tbody>{(sales.data ?? []).map((s) => <tr key={s.id} tabIndex={0} className={s.id === sale?.id ? 'sel' : undefined} onClick={() => setSale(s)} onKeyDown={(e) => e.key === 'Enter' && setSale(s)} style={{ cursor: 'pointer' }}><td className="n">{s.reference || s.id.slice(0, 8)}</td><td>{showDate(isoToJalali(s.sale_date))}</td><td className="n">{fmt(s.gross)}</td><td className="n">{fmt(s.commission)}</td><td><Badge tone={phase(s).t}>{phase(s).l}</Badge></td></tr>)}</tbody></table>
                {sales.data?.length === 0 && <div className="empty"><b>هنوز فروشی ثبت نشده</b></div>}
              </div>
            )}
          </Card>
        }
        detail={undefined}
      />
      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title={sale ? `فروش ${sale.reference || sale.id.slice(0, 8)}` : 'فروش'}>
          {sale ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <Steps steps={['ثبت', 'تحویل', 'آزادسازی']} current={phase(sale).step} />
              <KV rows={[['مبلغ', <span className="n">{fmt(sale.gross)}</span>], ['کمیسیون', <span className="n">{fmt(sale.commission)}</span>], ['خالص فروشنده', <span className="n">{fmt(sale.net)}</span>]]} />
              <div className="bar">
                <Button variant="pri" disabled={!!sale.delivered_at || sale.status === 'returned'} onClick={() => void act(() => payoutApi.deliver(sale.id), 'تحویل تأیید شد')}>تأیید تحویل</Button>
                <Button disabled={sale.status === 'returned' || sale.status === 'paid'} onClick={() => setDlg('return')}>برگشت کامل</Button>
                <Button disabled={sale.status === 'returned' || sale.status === 'paid'} onClick={() => setDlg('partial')}>برگشت جزئی</Button>
                <Button disabled={sale.status !== 'paid'} onClick={() => setDlg('returnPaid')}>برگشت پرداخت‌شده</Button>
              </div>
              <div className="msg warn"><div><b>برگشت پس از پرداخت</b><span className="tx">اگر فروش قبلاً به فروشنده پرداخت شده باشد، «برگشت پرداخت‌شده» مبلغ را از تسویهٔ بعدی کسر می‌کند.</span></div></div>
            </div>
          ) : <div className="empty">یک فروش را انتخاب کنید.</div>}
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Card title="شرایط تسویه" extra={<Button small onClick={() => setDlg('terms')}>نسخهٔ جدید</Button>} bare>
            <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="شرایط تسویه"><thead><tr><th>از</th><th className="n">کمیسیون</th><th className="n">نگهداری (روز)</th></tr></thead><tbody>{(terms.data ?? []).map((t) => <tr key={t.id}><td className="n">{showDate(t.effective_from_jalali)}</td><td className="n">{toFa(t.rate_bp / 100)}٪</td><td className="n">{toFa(t.hold_days)}</td></tr>)}</tbody></table></div>
          </Card>
          <Card title="کسرهای معلق">
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {(claw.data ?? []).filter((c) => c.outstanding > 0).map((c) => <div key={c.id} className="row" style={{ alignItems: 'center' }}><span>کسر {c.sale_id.slice(0, 8)}</span><span className="n">{fmt(c.outstanding)}</span><Button small onClick={() => void act(() => api_recover(c.id, c.outstanding), 'بازیابی شد')}>بازیابی</Button></div>)}
              {claw.data?.filter((c) => c.outstanding > 0).length === 0 && <div className="sub">کسر معلقی نیست.</div>}
            </div>
          </Card>
        </div>
      </div>
      {dlg === 'sale' && seller && <FormDialog title="ثبت فروش" submitLabel="ثبت فروش" fields={[{ name: 'ref', label: 'شمارهٔ مرجع', kind: 'latin', w: 'md' }, { name: 'gross', label: 'مبلغ فروش (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'cat', label: 'دسته' }]} onSubmit={(v, k) => payoutApi.recordSale({ seller_id: seller.id, gross: val.amount(v.gross), reference: val.text(v.ref) || undefined, category: val.text(v.cat) || undefined }, k)} onClose={() => setDlg(null)} onDone={done('فروش ثبت شد')} />}
      {dlg === 'return' && sale && <FormDialog title="برگشت کامل" danger submitLabel="برگشت" fields={[{ name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => payoutApi.returnSale(sale.id, val.text(v.r))} onClose={() => setDlg(null)} onDone={done('برگشت ثبت شد')} />}
      {dlg === 'partial' && sale && <FormDialog title="برگشت جزئی" submitLabel="ثبت" fields={[{ name: 'g', label: 'مبلغ برگشتی (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => payoutApi.partialReturn(sale.id, val.amount(v.g), val.text(v.r))} onClose={() => setDlg(null)} onDone={done('برگشت جزئی ثبت شد')} />}
      {dlg === 'returnPaid' && sale && <FormDialog title="برگشت پرداخت‌شده" danger submitLabel="ثبت" intro="مبلغ از تسویهٔ بعدی فروشنده کسر می‌شود." fields={[{ name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => payoutApi.returnPaid(sale.id, val.text(v.r))} onClose={() => setDlg(null)} onDone={done('ثبت شد')} />}
      {dlg === 'block' && seller && <FormDialog title="مسدود کردن فروشنده" danger submitLabel="مسدود کردن" fields={[{ name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => payoutApi.block(seller.id, val.text(v.r))} onClose={() => setDlg(null)} onDone={done('مسدود شد')} />}
      {dlg === 'edit' && seller && <FormDialog title="ویرایش فروشنده" submitLabel="ذخیره" fields={[{ name: 'code', label: 'کد', kind: 'latin', w: 'md', initial: seller.code, required: true }, { name: 'name', label: 'نام', initial: seller.name, required: true }, { name: 'sheba', label: 'شبا', kind: 'latin', w: 'lg', initial: seller.sheba }]} onSubmit={(v) => payoutApi.updateSeller(seller.id, { code: val.text(v.code), name: val.text(v.name), sheba: val.text(v.sheba) || undefined })} onClose={() => setDlg(null)} onDone={done('ذخیره شد')} />}
      {dlg === 'terms' && <FormDialog title="نسخهٔ جدید شرایط تسویه" submitLabel="ثبت" intro="شرایط به صورت نسخه‌ای با تاریخ اعمال ذخیره می‌شود؛ نسخهٔ قبلی تغییر نمی‌کند." fields={[{ name: 'name', label: 'نام', required: true }, { name: 'from', label: 'تاریخ اعمال', kind: 'date', required: true, w: 'date' }, { name: 'rate', label: 'کمیسیون (درصد)', kind: 'number', required: true, w: 'sm' }, { name: 'hold', label: 'نگهداری (روز)', kind: 'number', required: true, w: 'sm' }]} onSubmit={(v) => payoutApi.addTerms({ name: val.text(v.name), scope: 'default', effective_from: val.iso(v.from)!, rate_bp: Math.round(Number(val.int(v.rate)) * 100), hold_days: val.int(v.hold) })} onClose={() => setDlg(null)} onDone={() => (setDlg(null), terms.reload(), toast.push({ tone: 'ok', title: 'شرایط ثبت شد' }))} />}
    </>
  )
}

const api_recover = (id: string, amount: number) => import('@/api/http').then(({ api, money }) => api.post(`/v1/payout/clawbacks/${id}/recover`, { amount: money(amount), offset: false }))
