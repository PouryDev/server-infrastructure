import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { payroll, type Contract, type Employee, type Loan } from '@/api/payroll'
import { treasury } from '@/api/treasury'
import { fmt, toFa } from '@/shared/digits'
import { jalaliToday, showDate } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, ErrorMessage, PageBar, Skeleton, useResource, useToast, type Column } from '@/ui'
import { FormDialog, InlineForm, val } from '@/ui/FormDialog'

const PROFILE = [['father_name', 'نام پدر'], ['id_number', 'شمارهٔ شناسنامه'], ['id_place', 'محل صدور'], ['birth_date', 'تاریخ تولد'], ['nationality', 'ملیت'], ['occupation', 'عنوان شغل'], ['job_code', 'کد شغل'], ['start_date', 'تاریخ شروع به کار']] as const

export default function PayrollPeople() {
  const toast = useToast()
  const emps = useResource(async () => itemsOf(await payroll.employees()), [])
  const [sel, setSel] = useState<Employee>()
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [dlg, setDlg] = useState<'emp' | 'contract' | 'loan' | 'end' | null>(null)
  const [endId, setEndId] = useState('')
  const contracts = useResource(async () => (sel ? itemsOf(await payroll.contracts(sel.id)) : []), [sel?.id])
  const loans = useResource(async () => (sel ? itemsOf(await payroll.loans(sel.id)) : []), [sel?.id])
  const profile = useResource(async () => (sel ? await payroll.profile(sel.id).catch(() => ({}) as Record<string, string>) : {}), [sel?.id])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [bank, setBank] = useState('')
  const [jy, jm] = jalaliToday().split('/').map(Number) as [number, number]
  const rows = (emps.data ?? []).filter((e) => Object.entries(filters).every(([k, v]) => !v || String(k === 'name' ? e.name : e.national_id ?? '').includes(v)))
  const cols: Column<Employee>[] = [{ id: 'name', header: 'کارمند', filter: 'name', cell: (e) => e.name }, { id: 'nid', header: 'کد ملی', num: true, filter: 'nid', cell: (e) => toFa(e.national_id ?? '—') }, { id: 'a', header: 'وضعیت', cell: (e) => <Badge tone={e.active ? 'ok' : ''}>{e.active ? 'فعال' : 'غیرفعال'}</Badge> }]
  const done = (m: string) => () => { setDlg(null); toast.push({ tone: 'ok', title: m }); contracts.reload(); loans.reload(); emps.reload() }

  return (
    <>
      <PageBar title="کارکنان و قراردادها"><Button variant="pri" onClick={() => setDlg('emp')}>کارمند جدید</Button></PageBar>
      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div style={{ flex: '999 1 400px', minWidth: 0 }}>
          <DataTable caption="کارکنان" columns={cols} rows={rows} rowKey={(e) => e.id} loading={emps.loading} error={emps.error} onRetry={emps.reload} empty={{ title: 'هنوز کارمندی ثبت نشده' }} filters={filters} onFilters={setFilters} onRowClick={(e) => setSel(e)} rowClass={(e) => (e.id === sel?.id ? 'sel' : undefined)} />
        </div>
        <div style={{ flex: '1 1 600px', minWidth: 340, display: 'flex', flexDirection: 'column', gap: 12 }}>
          {!sel ? <Card><div className="empty">یک کارمند را انتخاب کنید.</div></Card> : (
            <>
              <Card title={`پروفایل: ${sel.name}`}>
                {profile.loading ? <Skeleton rows={3} /> : (
                  <InlineForm key={sel.id} submitLabel="ذخیره" rows={[['father_name', 'id_number', 'id_place'], ['birth_date', 'nationality'], ['occupation', 'job_code', 'start_date']]} fields={PROFILE.map(([name, label]) => ({ name, label, w: 'md' as const, initial: profile.data?.[name] ?? '' }))} onSubmit={(v) => payroll.putProfile(sel.id, { ...(profile.data ?? {}), ...v })} onDone={() => (profile.reload(), toast.push({ tone: 'ok', title: 'پروفایل ذخیره شد' }))} />
                )}
                <div className="sub" style={{ marginTop: 8 }}>شبا، وضعیت تأهل و تعداد فرزند در پروفایل API وجود ندارد.</div>
              </Card>
              <Card title="قراردادها" extra={<Button small onClick={() => setDlg('contract')}>قرارداد جدید</Button>} bare>
                {contracts.error != null ? <div className="cb"><ErrorMessage error={contracts.error} /></div> : (
                  <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="قراردادها"><thead><tr><th>شروع</th><th className="n">حقوق پایه</th><th>مزایا</th><th>بیمه</th><th>پایان</th><th /></tr></thead>
                    <tbody>{(contracts.data ?? []).map((c: Contract) => <tr key={c.id}><td className="n">{showDate(c.start_jalali)}</td><td className="n">{fmt(c.base_salary)}</td><td className="mut">{c.allowances.map((a) => `${a.name}: ${fmt(a.amount)}`).join('، ') || '—'}</td><td>{c.insured ? <Badge tone="ok">مشمول</Badge> : <Badge>غیرمشمول</Badge>}</td><td className="n">{c.end_jalali ? showDate(c.end_jalali) : '—'}</td><td>{!c.end && <Button small variant="dng" onClick={() => (setEndId(c.id), setDlg('end'))}>پایان قرارداد</Button>}</td></tr>)}</tbody></table>
                    {contracts.data?.length === 0 && <div className="empty">قراردادی ثبت نشده.</div>}</div>
                )}
              </Card>
              <Card title="وام کارمند" extra={<Button small onClick={() => setDlg('loan')}>اعطای وام</Button>} bare>
                <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label="وام‌ها"><thead><tr><th>شروع</th><th className="n">مبلغ</th><th className="n">قسط</th><th className="n">مانده</th><th /></tr></thead>
                  <tbody>{(loans.data ?? []).map((l: Loan) => <tr key={l.id}><td className="n">{toFa(`${l.start_year}/${String(l.start_month).padStart(2, '0')}`)}</td><td className="n">{fmt(l.principal)}</td><td className="n">{fmt(l.installment)}</td><td className="n">{fmt(l.remaining)}</td><td>{l.status === 'active' && <Button small variant="dng" onClick={async () => { try { await payroll.writeOff(l.id, 'سوخت‌شده'); loans.reload() } catch (e) { toast.error(e) } }}>سوخت‌شده</Button>}</td></tr>)}</tbody></table>
                  {loans.data?.length === 0 && <div className="empty">وامی نیست.</div>}</div>
              </Card>
            </>
          )}
          <HolidaysAndWorkshop />
        </div>
      </div>
      {dlg === 'emp' && <FormDialog title="کارمند جدید" submitLabel="ثبت کارمند" fields={[{ name: 'code', label: 'کد پرسنلی', required: true, kind: 'latin', w: 'md' }, { name: 'name', label: 'نام', required: true }, { name: 'nid', label: 'کد ملی', kind: 'latin', w: 'md' }, { name: 'ins', label: 'شمارهٔ بیمه', kind: 'latin', w: 'md' }]} onSubmit={(v) => payroll.addEmployee({ code: val.text(v.code), name: val.text(v.name), national_id: val.text(v.nid) || undefined, insurance_no: val.text(v.ins) || undefined })} onClose={() => setDlg(null)} onDone={done('کارمند ثبت شد')} />}
      {dlg === 'contract' && sel && <FormDialog title="قرارداد جدید" submitLabel="ثبت قرارداد" fields={[{ name: 'start', label: 'شروع', kind: 'date', required: true, w: 'date', initial: showDate(jalaliToday()) }, { name: 'base', label: 'حقوق پایه (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'housing', label: 'حق مسکن (ریال)', kind: 'amount', w: 'amt' }, { name: 'food', label: 'حق خواروبار (ریال)', kind: 'amount', w: 'amt' }, { name: 'ins', label: 'مشمول بیمه', kind: 'select', options: [{ value: '1', label: 'بله' }, { value: '0', label: 'خیر' }], initial: '1', w: 'sm' }]} intro="مزایا به‌صورت پیش‌فرض مشمول مالیات و بیمه هستند." onSubmit={(v) => payroll.addContract(sel.id, { base_salary: val.amount(v.base), allowances: [...(val.amount(v.housing) ? [{ code: 'housing', name: 'حق مسکن', amount: val.amount(v.housing), taxable: true, insurable: true }] : []), ...(val.amount(v.food) ? [{ code: 'food', name: 'حق خواروبار', amount: val.amount(v.food), taxable: true, insurable: true }] : [])], insured: v.ins === '1', start: val.iso(v.start)! })} onClose={() => setDlg(null)} onDone={done('قرارداد ثبت شد')} />}
      {dlg === 'end' && <FormDialog title="پایان قرارداد" danger submitLabel="پایان قرارداد" fields={[{ name: 'end', label: 'تاریخ پایان', kind: 'date', required: true, w: 'date', initial: showDate(jalaliToday()) }]} onSubmit={(v) => payroll.endContract(endId, val.iso(v.end)!)} onClose={() => setDlg(null)} onDone={done('قرارداد پایان یافت')} />}
      {dlg === 'loan' && sel && <FormDialog title="اعطای وام" submitLabel="ثبت وام" fields={[{ name: 'p', label: 'مبلغ وام (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'i', label: 'قسط ماهانه (ریال)', kind: 'amount', required: true, w: 'amt' }, { name: 'bank', label: 'پرداخت از حساب', kind: 'select', options: (banks.data ?? []).map((b) => ({ value: b.id, label: b.name })), initial: bank || banks.data?.[0]?.id }]} onSubmit={(v) => (setBank(v.bank ?? ''), payroll.grantLoan(sel.id, { principal: val.amount(v.p), installment: val.amount(v.i), start_year: jy, start_month: jm, bank_account_id: v.bank || undefined }))} onClose={() => setDlg(null)} onDone={done('وام ثبت شد')} />}
    </>
  )
}

function HolidaysAndWorkshop() {
  const toast = useToast()
  const ws = useResource(async () => payroll.workshop().catch(() => ({}) as Record<string, unknown>), [])
  const w = ws.data ?? {}
  return (
    <Card title="تعطیلات و کارگاه">
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        <div className="fld"><label>تعطیلی جدید</label>
          <InlineForm submitLabel="افزودن" rows={[['date', 'name']]} fields={[{ name: 'date', label: 'تاریخ', kind: 'date', w: 'date', required: true }, { name: 'name', label: 'نام', w: 'md', required: true }]} onSubmit={(v) => { const [y, m, d] = (val.date(v.date) ?? '').split('/').map(Number) as [number, number, number]; return payroll.addHoliday({ jalali_year: y, jalali_month: m, jalali_day: d, name: val.text(v.name), verified: false, source: 'ثبت دستی' }) }} onDone={() => toast.push({ tone: 'ok', title: 'تعطیلی افزوده شد' })} />
        </div>
        <div className="fld"><label>کارگاه</label>
          <InlineForm key={JSON.stringify(w)} submitLabel="ذخیره" rows={[['name', 'code']]} fields={[{ name: 'name', label: 'نام کارگاه', w: 'md', initial: String(w.name ?? '') }, { name: 'code', label: 'کد کارگاه', kind: 'latin', w: 'md', initial: String(w.code ?? '') }]} onSubmit={(v) => payroll.putWorkshop({ ...w, ...v })} onDone={() => toast.push({ tone: 'ok', title: 'کارگاه ذخیره شد' })} />
        </div>
      </div>
    </Card>
  )
}
