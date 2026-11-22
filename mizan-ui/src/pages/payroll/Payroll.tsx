import { useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { itemsOf } from '@/api/commerce'
import { payroll, type Breakdown, type Employee, type Payslip } from '@/api/payroll'
import { treasury } from '@/api/treasury'
import { fmt, fmtOrDash, toFa } from '@/shared/digits'
import { MONTHS, jalaliToday } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, KV, PageBar, Select, Skeleton, SubTabs, useResource, useToast } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'

interface Row { key: string; emp: Employee; slip?: Payslip; calc?: Breakdown; err?: unknown }
const ST: Record<string, { l: string; t: 'ok' | 'warn' | 'info' | 'err' }> = { issued: { l: 'صادرشده', t: 'warn' }, paid: { l: 'پرداخت‌شده', t: 'ok' }, void: { l: 'ابطال‌شده', t: 'err' }, preview: { l: 'پیش‌نمایش', t: 'info' } }

/** Payslips of a month: issued ones and a calculated preview for employees who have none yet. */
export default function Payroll() {
  const toast = useToast()
  const nav = useNavigate()
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const [year, setYear] = useState(jy)
  const [month, setMonth] = useState(jm)
  const [sel, setSel] = useState<string>()
  const [dlg, setDlg] = useState<'void' | 'run' | 'ssa' | null>(null)
  const data = useResource(async (signal) => {
    const [slips, es] = await Promise.all([payroll.payslips({ pageSize: 100, filter: { year: String(year), month: String(month) } }, signal), itemsOf(await payroll.employees()).filter((e) => e.active)])
    const bySlip = new Map(slips.items.map((s) => [s.employee_id, s]))
    const rows: Row[] = await Promise.all(es.map(async (emp): Promise<Row> => {
      const slip = bySlip.get(emp.id)
      if (slip && slip.status !== 'void') return { key: slip.id, emp, slip: await payroll.payslip(slip.id).catch(() => slip) }
      try { return { key: 'p' + emp.id, emp, calc: await payroll.preview({ employee_id: emp.id, year, month }) } } catch (err) { return { key: 'p' + emp.id, emp, err } }
    }))
    return rows
  }, [year, month])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [bank, setBank] = useState('')
  const rows = useMemo(() => data.data ?? [], [data.data])
  const cur = rows.find((r) => r.key === sel)
  const b: Partial<Breakdown> | undefined = cur?.slip ?? cur?.calc
  const totals = useMemo(() => rows.reduce((t, r) => { const x = r.slip ?? r.calc; return x ? { g: t.g + (x.gross ?? 0), i: t.i + (x.employee_insurance ?? 0), tx: t.tx + (x.tax ?? 0), n: t.n + (x.net ?? 0) } : t }, { g: 0, i: 0, tx: 0, n: 0 }), [rows])
  const issue = async (r: Row) => { try { await payroll.issue({ employee_id: r.emp.id, year, month }); toast.push({ tone: 'ok', title: 'فیش صادر شد' }); data.reload() } catch (e) { toast.error(e) } }
  const ready = rows.filter((r) => r.slip?.status === 'issued')
  const net = ready.reduce((s, r) => s + (r.slip?.net ?? 0), 0)

  return (
    <>
      <PageBar title="حقوق و دستمزد">
        <Button onClick={() => setDlg('ssa')}>فایل‌های بیمه</Button>
        <Button disabled={ready.length === 0} onClick={() => setDlg('run')}>پرداخت دسته‌ای</Button>
        <Button variant="pri" disabled={rows.every((r) => !r.calc)} onClick={async () => { for (const r of rows.filter((x) => x.calc)) await issue(r) }}>صدور فیش {MONTHS[month - 1]}</Button>
      </PageBar>
      <SubTabs<string> value="slips" onChange={(v) => v === 'people' ? nav('/payroll/people') : v !== 'slips' && nav('/payroll/rules')} options={[{ value: 'people', label: 'کارکنان' }, { value: 'slips', label: 'فیش‌های حقوقی' }, { value: 'rules', label: 'قوانین سال' }, { value: 'loans', label: 'وام‌ها' }, { value: 'remit', label: 'پرداخت مالیات و بیمه' }]} />
      <div className="row">
        <Field label="سال">{() => <Select label="سال" width="100px" value={String(year)} options={[jy - 1, jy, jy + 1].map((y) => ({ value: String(y), label: toFa(y) }))} onChange={(v) => setYear(Number(v))} />}</Field>
        <Field label="ماه">{() => <Select label="ماه" width="130px" value={String(month)} options={MONTHS.map((n, i) => ({ value: String(i + 1), label: n }))} onChange={(v) => setMonth(Number(v))} />}</Field>
      </div>
      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div className="tw" style={{ flex: '999 1 560px', minWidth: 0 }}>
          {data.error != null ? <ErrorMessage error={data.error} onRetry={data.reload} /> : !data.data ? <Skeleton /> : (
            <table className="tbl" aria-label="فیش‌های حقوقی">
              <thead><tr><th>کارمند</th><th className="n">ناخالص</th><th className="n">بیمه کارمند</th><th className="n">مالیات</th><th className="n">خالص</th><th>وضعیت</th></tr></thead>
              <tbody>{rows.map((r) => { const x = r.slip ?? r.calc; const st = r.slip ? ST[r.slip.status] : r.calc ? ST.preview : undefined; return (
                <tr key={r.key} tabIndex={0} className={r.key === sel ? 'sel' : undefined} onClick={() => setSel(r.key)} onKeyDown={(e) => e.key === 'Enter' && setSel(r.key)} style={{ cursor: 'pointer' }}>
                  <td>{r.emp.name}</td><td className="n">{x ? fmt(x.gross) : '—'}</td><td className="n mut">{x ? fmtOrDash(x.employee_insurance) : '—'}</td><td className="n mut">{x ? fmt(x.tax) : '—'}</td><td className="n">{x ? fmt(x.net) : '—'}</td><td>{st ? <Badge tone={st.t}>{st.l}</Badge> : <Badge tone="err">بدون قرارداد</Badge>}</td>
                </tr>) })}</tbody>
              <tfoot><tr><td>جمع</td><td className="n">{fmt(totals.g)}</td><td className="n">{fmt(totals.i)}</td><td className="n">{fmt(totals.tx)}</td><td className="n">{fmt(totals.n)}</td><td /></tr></tfoot>
            </table>
          )}
          {data.data?.length === 0 && <div className="empty"><b>کارمندی ثبت نشده</b></div>}
        </div>
        <Card title={cur ? `${cur.slip ? 'فیش' : 'پیش‌نمایش فیش'}: ${cur.emp.name}` : 'فیش'} className="" >
          {cur && b ? (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              <KV rows={[['روزهای کار', `${toFa(b.days ?? 0)} روز`], ...(b.lines ?? []).map((l): [string, ReactNode] => [l.name, <span key={l.code} className="n">{fmt(l.amount)}</span>]), ['بیمه سهم کارمند', <span key="i" className="n">−{fmt(b.employee_insurance)}</span>], ['مالیات', <span key="t" className="n">−{fmt(b.tax)}</span>], [<b key="k">خالص پرداختی</b>, <b key="n" className="n">{fmt(b.net)}</b>]]} />
              <div className="sub">محاسبهٔ ماه‌های گذشته با نسخهٔ قانون همان ماه تغییر نمی‌کند.</div>
              <div className="bar">
                {cur.calc && <Button variant="pri" onClick={() => void issue(cur)}>صدور فیش</Button>}
                {cur.slip && cur.slip.status === 'issued' && <Button variant="dng" onClick={() => setDlg('void')}>ابطال</Button>}
              </div>
            </div>
          ) : cur?.err != null ? <ErrorMessage error={cur.err} /> : <div className="empty">یک ردیف را انتخاب کنید.</div>}
        </Card>
      </div>
      {dlg === 'void' && cur?.slip && <FormDialog title="ابطال فیش" danger submitLabel="ابطال فیش" fields={[{ name: 'r', label: 'دلیل', required: true }]} onSubmit={(v) => payroll.void(cur.slip!.id, val.text(v.r))} onClose={() => setDlg(null)} onDone={() => (setDlg(null), data.reload(), toast.push({ tone: 'ok', title: 'فیش ابطال شد' }))} />}
      {dlg === 'run' && <FormDialog title={`پرداخت حقوق ${MONTHS[month - 1]}`} submitLabel="پرداخت" intro={`${toFa(ready.length)} فیش آماده · جمع خالص ${fmt(net)} ریال`} fields={[{ name: 'bank', label: 'از حساب بانکی', kind: 'select', options: (banks.data ?? []).map((x) => ({ value: x.id, label: x.name })), initial: bank || banks.data?.[0]?.id }]} onSubmit={(v, key) => { setBank(v.bank ?? ''); return payroll.run({ year, month, bank_account_id: v.bank || undefined }, key) }} onClose={() => setDlg(null)} onDone={() => (setDlg(null), data.reload(), toast.push({ tone: 'ok', title: 'پرداخت انجام شد' }))} />}
      {dlg === 'ssa' && <FormDialog title="فایل‌های تأمین اجتماعی (لیست بیمه)" submitLabel="ساخت و دانلود" intro="فایل‌ها بر اساس ساختار نمونه ساخته می‌شوند؛ مشخصات رسمی لیست مالیاتی (WH/WP) موجود نیست." fields={[]} onSubmit={async () => { const r = await payroll.ssaFiles(year, month); for (const [name, b64] of Object.entries(r)) { if (!name.endsWith('.DBF')) continue; const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))])); a.download = name; a.click() } return r }} onClose={() => setDlg(null)} onDone={() => setDlg(null)} />}
    </>
  )
}
