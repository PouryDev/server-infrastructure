import { useState } from 'react'
import { Link } from 'react-router-dom'
import { invoices, type InvoiceSummary } from '@/api/sales'
import { invoiceStatus } from '@/domain/status'
import { fmt, toFa } from '@/shared/digits'
import { isoToJalali, showDate } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, ErrorMessage, KV, MasterDetail, PageBar, Seg, Skeleton, Steps, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { useDirectory } from '@/domain/useDirectory'

type Filter = 'all' | 'draft' | 'issued' | 'paid' | 'void'

export default function Invoices() {
  const toast = useToast()
  const [status, setStatus] = useState<Filter>('all')
  const list = useListState({}, 20)
  const [active, setActive] = useState<string>()
  const [voiding, setVoiding] = useState(false)
  const [walletPay, setWalletPay] = useState(false)
  useDirectory()
  const filter = { ...(status !== 'all' ? { status } : {}), ...Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v).map(([k, v]) => [k === 'customer_ref' ? 'customer_ref:like' : k, v])) }
  const data = useResource((s) => invoices.list({ page: list.page, pageSize: list.pageSize, sort: '-created_at', filter }, s), [status, list.page, JSON.stringify(list.filters)])
  const detail = useResource(async () => (active ? invoices.get(active) : undefined), [active])
  const d = detail.data?.invoice

  const columns: Column<InvoiceSummary>[] = [
    { id: 'number', header: 'شماره', num: true, filter: 'number', cell: (i) => (i.number ? toFa(i.number) : '—') },
    { id: 'date', header: 'تاریخ', filter: 'issue_date', cell: (i) => showDate(i.issue_date ? isoToJalali(i.issue_date) : '') },
    { id: 'customer', header: 'مشتری', filter: 'customer_ref', cell: (i) => i.customer_ref },
    { id: 'total', header: 'مبلغ کل (ریال)', num: true, cell: (i) => fmt(i.total) },
    { id: 'paid', header: 'پرداخت‌شده', num: true, mut: true, cell: (i) => fmt(i.paid) },
    { id: 'status', header: 'وضعیت', filter: 'status', cell: (i) => <Badge tone={invoiceStatus(i.status).tone}>{invoiceStatus(i.status).label}</Badge> },
  ]
  const step = d ? ({ draft: 0, issued: 1, partially_paid: 1, paid: 2, void: 1 } as Record<string, number>)[d.status] ?? 0 : 0

  const issue = async () => {
    if (!d) return
    try {
      await invoices.issue(d.id)
      toast.push({ tone: 'ok', title: 'فاکتور صادر شد' })
      detail.reload()
      data.reload()
    } catch (e) {
      toast.error(e)
    }
  }

  return (
    <>
      <PageBar title="فاکتورها">
        <Link className="btn pri" to="/invoices/new">فاکتور جدید</Link>
      </PageBar>
      <Seg<Filter> label="وضعیت" value={status} onChange={(v) => (setStatus(v), list.setPage(1))} options={[{ value: 'all', label: 'همه' }, { value: 'draft', label: 'پیش‌نویس' }, { value: 'issued', label: 'صادرشده' }, { value: 'paid', label: 'پرداخت‌شده' }, { value: 'void', label: 'ابطال‌شده' }]} />
      <MasterDetail
        listBasis={560}
        list={<DataTable caption="فاکتورها" columns={columns} rows={data.data?.items} rowKey={(i) => i.id} loading={data.loading} error={data.error} onRetry={data.reload} empty={{ title: 'هنوز فاکتوری صادر نشده', action: <Link className="btn pri" to="/invoices/new">فاکتور جدید</Link> }} filters={list.filters} onFilters={list.setFilters} meta={data.data?.meta} onPage={list.setPage} onRowClick={(i) => setActive(i.id)} rowClass={(i) => (i.id === active ? 'sel' : undefined)} />}
        detail={
          active ? (
            detail.error != null ? <div className="cb"><ErrorMessage error={detail.error} onRetry={detail.reload} /></div> : !d ? <Skeleton /> : (
              <>
                <div className="ch">فاکتور {d.number ? toFa(d.number) : 'پیش‌نویس'}<Badge tone={invoiceStatus(d.status).tone}>{invoiceStatus(d.status).label}</Badge></div>
                <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <Steps steps={['پیش‌نویس', 'صادرشده', 'پرداخت‌شده']} current={step} />
                  <KV rows={[['مشتری', d.buyer.name || d.customer_ref], ['تاریخ', showDate(d.issue_date_jalali ?? '')], ['باقی‌مانده', <span className="n">{fmt(d.remaining)}</span>]]} />
                  <div className="tw">
                    <table className="tbl" aria-label="اقلام فاکتور">
                      <thead><tr><th>شرح</th><th className="n">تعداد</th><th className="n">مبلغ</th></tr></thead>
                      <tbody>{d.items.map((it) => <tr key={it.no}><td>{it.description}</td><td className="n">{fmt(it.quantity)}</td><td className="n">{fmt(it.net)}</td></tr>)}</tbody>
                      <tfoot>
                        <tr><td colSpan={2}>مالیات</td><td className="n">{fmt(d.tax_total)}</td></tr>
                        <tr><td colSpan={2}>جمع کل</td><td className="n">{fmt(d.total)}</td></tr>
                      </tfoot>
                    </table>
                  </div>
                  <div className="bar">
                    {d.status === 'draft' && <Button variant="pri" onClick={() => void issue()}>صدور فاکتور</Button>}
                    {(d.status === 'issued' || d.status === 'partially_paid') && <Button variant="pri" onClick={() => setWalletPay(true)}>دریافت وجه از کیف پول</Button>}
                    {d.status !== 'void' && d.status !== 'paid' && <Button variant="dng" onClick={() => setVoiding(true)}>ابطال فاکتور</Button>}
                  </div>
                  <div className="sub">ابطال سند برگشتی صادر می‌کند. (در API فعلی، ابطال فاکتور مرحلهٔ تأیید مدیر مالی ندارد.)</div>
                </div>
              </>
            )
          ) : (
            <Card><div className="empty">یک فاکتور را انتخاب کنید.</div></Card>
          )
        }
      />
      {voiding && d && (
        <FormDialog title={`ابطال فاکتور ${d.number ? toFa(d.number) : ''}`} submitLabel="ابطال فاکتور" danger intro="سند حسابداری فاکتور با سند برگشتی معکوس می‌شود." fields={[{ name: 'reason', label: 'دلیل', required: true }]} onSubmit={(v) => invoices.void(d.id, val.text(v.reason))} onClose={() => setVoiding(false)} onDone={() => (setVoiding(false), toast.push({ tone: 'ok', title: 'فاکتور ابطال شد' }), detail.reload(), data.reload())} />
      )}
      {walletPay && d && (
        <FormDialog title="دریافت وجه از کیف پول" submitLabel="دریافت وجه" fields={[{ name: 'wallet', label: 'شناسهٔ کیف پول مشتری', required: true, kind: 'latin', hint: 'کیف پول را از صفحهٔ «کیف پول‌ها» با شناسهٔ مالک پیدا کنید.' }]} onSubmit={(v, key) => invoices.payFromWallet(d.id, val.text(v.wallet), key)} onClose={() => setWalletPay(false)} onDone={() => (setWalletPay(false), toast.push({ tone: 'ok', title: 'وجه از کیف پول دریافت شد' }), detail.reload(), data.reload())} />
      )}
    </>
  )
}
