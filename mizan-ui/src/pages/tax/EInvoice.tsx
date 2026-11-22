import { useState } from 'react'
import { invoices } from '@/api/sales'
import { einvoiceApi } from '@/api/tax'
import { fmt, toFa } from '@/shared/digits'
import { Badge, Button, Card, DataTable, Field, NotAvailable, PageBar, Select, Seg, SubTabs, useResource, useToast } from '@/ui'
import { InlineForm, val } from '@/ui/FormDialog'
import { Link } from 'react-router-dom'

type Tab = 'list' | 'conn' | 'tpl' | 'fix'
const PATTERNS = [{ value: '1', label: '۱ فروش' }, { value: '3', label: '۳ طلا و جواهر' }, { value: '4', label: '۴ پیمانکاری' }, { value: '7', label: '۷ صادرات' }]
const SUBJECTS = [{ value: '2', label: 'اصلاحی' }, { value: '3', label: 'ابطالی' }, { value: '4', label: 'برگشت از فروش' }]

/** Moadian: queue, connection/keys (no API in mizan: provider is set at start-up), invoice pattern per invoice, correction/cancel via subject + reference tax id. */
export default function EInvoice() {
  const toast = useToast()
  const [tab, setTab] = useState<Tab>('tpl')
  const [pattern, setPattern] = useState('1')
  const [invoiceId, setInvoiceId] = useState('')
  const issued = useResource(async () => (await invoices.list({ pageSize: 50, sort: '-created_at', filter: { status: 'issued' } })).items, [])
  const q = useResource(() => einvoiceApi.list({ page_size: '50', sort: '-created_at' }), [])
  const opts = (issued.data ?? []).map((i) => ({ value: i.id, label: `${i.number ? toFa(i.number) : 'پیش‌نویس'} · ${i.customer_ref} · ${fmt(i.total)}` }))
  const chosen = invoiceId || opts[0]?.value || ''

  return (
    <>
      <PageBar title="سامانهٔ مودیان" sub="ارسال صورتحساب الکترونیکی به سازمان امور مالیاتی">
        <Button onClick={async () => { try { const r = await einvoiceApi.send(); toast.push({ tone: 'ok', title: `${toFa(r.sent)} صورتحساب ارسال شد` }); q.reload() } catch (e) { toast.error(e) } }}>ارسال صف</Button>
        <Button onClick={async () => { try { await einvoiceApi.refresh(); toast.push({ tone: 'ok', title: 'وضعیت‌ها به‌روز شد' }); q.reload() } catch (e) { toast.error(e) } }}>به‌روزرسانی وضعیت‌ها</Button>
      </PageBar>
      <SubTabs<Tab> value={tab} onChange={setTab} options={[{ value: 'list', label: 'صورتحساب‌ها' }, { value: 'conn', label: 'اتصال و کلید' }, { value: 'tpl', label: 'الگوی فاکتور' }, { value: 'fix', label: 'اصلاح و ابطال' }]} />
      {tab === 'list' && <><DataTable caption="صورتحساب‌های الکترونیکی" columns={[{ id: 'i', header: 'فاکتور', num: true, cell: (e) => e.source_id.slice(0, 8) }, { id: 's', header: 'وضعیت', cell: (e) => <Badge tone={e.status === 'accepted' || e.status === 'confirmed' ? 'ok' : e.status === 'rejected' ? 'err' : 'info'}>{e.status}</Badge> }, { id: 't', header: 'شناسهٔ مالیاتی', num: true, mut: true, cell: (e) => e.reference_number ?? '—' }, { id: 'a', header: 'تلاش', num: true, cell: (e) => toFa(e.attempts) }]} rows={q.data?.items} rowKey={(e) => e.id} loading={q.loading} error={q.error} onRetry={q.reload} empty={{ title: 'هنوز صورتحسابی در صف نیست' }} /><Link className="btn" to="/einvoices">صف و وضعیت ارسال</Link></>}
      {tab === 'conn' && (
        <Card title="اتصال به سامانه" extra={<Badge tone="warn" square>در دسترس نیست</Badge>}>
          <NotAvailable title="تنظیمات اتصال و کلید از طریق API نیست">
            شناسهٔ حافظهٔ مالیاتی، شناسهٔ اقتصادی، محیط (sandbox/واقعی) و کلید خصوصی هنگام راه‌اندازی سرویس (mizan.WithEInvoice) تنظیم می‌شوند و API برای خواندن یا تغییرشان ندارد؛ کلید هیچ‌گاه از رابط نمایش داده نمی‌شود. ابزار تولید کلید و CSR خط‌فرمان است: mizan/cmd/moadian-keygen.
          </NotAvailable>
        </Card>
      )}
      {tab === 'tpl' && (
        <Card title="الگوی صورتحساب" extra={<span className="sub">برای هر فاکتور</span>}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label="فاکتور">{() => <Select label="فاکتور" width="320px" value={chosen} options={opts} onChange={setInvoiceId} placeholder="فاکتور صادرشده‌ای نیست" />}</Field>
            <Seg<string> label="الگو" value={pattern} onChange={setPattern} options={PATTERNS} />
            <InlineForm key={pattern + chosen} submitLabel="در صف قرار بده" fields={[{ name: 'settle', label: 'روش تسویه', kind: 'select', options: [{ value: '1', label: 'نقد' }, { value: '2', label: 'نسیه' }, { value: '3', label: 'نقد/نسیه' }], initial: '1', w: 'sm' }, { name: 'cash', label: 'مبلغ نقدی (ریال)', kind: 'amount', w: 'amt' }, { name: 'contract', label: 'شمارهٔ قرارداد', kind: 'latin', w: 'md' }]} onSubmit={(v) => einvoiceApi.enqueue(chosen, { pattern: Number(pattern), settlement: Number(v.settle), ...(val.amount(v.cash) ? { cash_amount: val.amount(v.cash) } : {}), ...(val.text(v.contract) ? { contract_no: val.text(v.contract) } : {}) })} onDone={() => (toast.push({ tone: 'ok', title: 'صورتحساب در صف قرار گرفت' }), q.reload())}>
              <div className="sub">الگو روی همین فاکتور اعمال می‌شود. فیلدهای ویژهٔ هر الگو (اجرت ساخت، سود فروشنده، حق‌العمل، نوع خریدار) در API فقط از طریق «details» پذیرفته می‌شوند و این فرم آن‌ها را ارسال نمی‌کند.</div>
            </InlineForm>
          </div>
        </Card>
      )}
      {tab === 'fix' && (
        <Card title="اصلاح یا ابطال صورتحساب">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <InlineForm submitLabel="ارسال" fields={[{ name: 'ref', label: 'شناسهٔ مالیاتی صورتحساب اصلی', kind: 'latin', w: 'lg', required: true }, { name: 'subject', label: 'نوع', kind: 'select', options: SUBJECTS, initial: '2', w: 'md' }]} onSubmit={(v) => einvoiceApi.enqueue(chosen, { subject: Number(v.subject), ref_taxid: val.text(v.ref) })} onDone={() => toast.push({ tone: 'ok', title: 'در صف قرار گرفت' })}>
              <Field label="فاکتور">{() => <Select label="فاکتور" width="320px" value={chosen} options={opts} onChange={setInvoiceId} />}</Field>
              <div className="msg warn"><div className="tx">صورتحساب اصلی تأییدشده است؛ اصلاح با ارجاع به شناسهٔ مالیاتی آن ارسال می‌شود و خود فاکتور دستکاری نمی‌شود.</div></div>
            </InlineForm>
          </div>
        </Card>
      )}
    </>
  )
}
