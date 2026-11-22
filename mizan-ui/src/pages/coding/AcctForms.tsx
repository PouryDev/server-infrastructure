import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { accounting, type AccountType } from '@/api/accounting'
import { fieldErrors, friendly } from '@/api/errors'
import { useFiscal } from '@/app/Fiscal'
import { invalidateDirectory, useDirectory } from '@/domain/useDirectory'
import { toLatin, toFa } from '@/shared/digits'
import { MONTHS, showDate } from '@/shared/jalali'
import { actionKey } from '@/shared/idempotency'
import { Button, Card, Field, Input, Message, PageBar, Select, useToast } from '@/ui'
import { EnableCodingDialog } from './EnableCodingDialog'

const TYPES: { value: AccountType; label: string }[] = [
  { value: 'asset', label: 'دارایی (بدهکار)' },
  { value: 'liability', label: 'بدهی (بستانکار)' },
  { value: 'equity', label: 'حقوق صاحبان سرمایه (بستانکار)' },
  { value: 'revenue', label: 'درآمد (بستانکار)' },
  { value: 'expense', label: 'هزینه (بدهکار)' },
]

/** The dialogs reachable from the chart: new account, sample coding, opening a fiscal year, print/export. */
export default function AcctForms() {
  const toast = useToast()
  const navigate = useNavigate()
  const { dir, reload: reloadDir } = useDirectory()
  const { years, reload: reloadYears } = useFiscal()

  // new account
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [type, setType] = useState<AccountType>('asset')
  const [coding, setCoding] = useState('')
  const [err, setErr] = useState<unknown>()
  const [busy, setBusy] = useState(false)
  const clash = useMemo(() => dir.find((a) => a.code === toLatin(code).trim() || a.ledgerCode === toLatin(code).trim()), [dir, code])
  const fe = fieldErrors(err)

  const create = async () => {
    setBusy(true)
    setErr(undefined)
    try {
      const c = toLatin(code).trim()
      await accounting.createAccount({ code: c, name: name.trim(), type, ...(coding.trim() ? { coding_code: toLatin(coding).trim() } : {}) })
      invalidateDirectory()
      reloadDir()
      toast.push({ tone: 'ok', title: `حساب ${toFa(c)} ایجاد شد` })
      setCode('')
      setName('')
      setCoding('')
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }

  // sample coding
  const [enable, setEnable] = useState(false)

  // new fiscal year
  const next = Math.max(0, ...years.map((y) => Number(y.name) || 0)) + 1
  const [yearText, setYearText] = useState('')
  const [yearErr, setYearErr] = useState<unknown>()
  const yearNo = Number(toLatin(yearText || String(next)))
  const hasOpen = years.some((y) => y.status === 'open' && Number(y.name) < yearNo)
  const openKey = useMemo(() => actionKey('fiscal-year'), [])
  const openYear = async () => {
    setYearErr(undefined)
    try {
      await accounting.openFiscalYear(yearNo)
      openKey.done()
      reloadYears()
      toast.push({ tone: 'ok', title: `سال مالی ${toFa(yearNo)} گشوده شد` })
      navigate('/periods')
    } catch (e) {
      setYearErr(e)
    }
  }

  const [tpl, setTpl] = useState('doc')
  const [range, setRange] = useState('')

  return (
    <>
      <PageBar title="حساب جدید، کدینگ نمونه و سال مالی جدید" sub="دیالوگ‌هایی که از کدینگ و سال مالی باز می‌شوند" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="حساب جدید">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="کد" required error={clash ? `این کد برای «${clash.name}» استفاده شده است.` : fe.code}>{(id) => <Input id={id} w="code" num bad={!!clash} value={code} onChange={(e) => setCode(e.target.value)} />}</Field>
              <Field label="نام حساب" required grow error={fe.name}>{(id) => <Input id={id} w="xl" value={name} onChange={(e) => setName(e.target.value)} />}</Field>
            </div>
            <div className="row">
              <Field label="ماهیت">{() => <Select<AccountType> label="ماهیت" width="240px" value={type} options={TYPES} onChange={setType} />}</Field>
              <Field label="کد در کدینگ" hint="اختیاری؛ برای طبقه‌بندی در گزارش‌ها">{(id) => <Input id={id} w="code" num value={coding} onChange={(e) => setCoding(e.target.value)} />}</Field>
            </div>
            {err != null && <Message tone={friendly(err).tone} title={friendly(err).title}>{friendly(err).text}</Message>}
            <div className="bar">
              <Button variant="pri" disabled={busy || !code.trim() || !name.trim() || !!clash} onClick={() => void create()}>ایجاد حساب</Button>
              <Button onClick={() => (setCode(''), setName(''), setCoding(''), setErr(undefined))}>انصراف</Button>
            </div>
          </div>
        </Card>

        <Card title="فعال‌سازی کدینگ نمونه">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Message tone="warn" title="این یک نمونه است، نه استاندارد قانونی">
              کدینگ رسمی اجباری پیدا نشد. حساب‌های سیستمی به کدینگ نگاشت می‌شوند و پیشنهادی هستند؛ بازبینی کنید. همیشه یکی را انتخاب کنید.
            </Message>
            <div className="bar"><Button variant="pri" onClick={() => setEnable(true)}>انتخاب و فعال‌سازی…</Button></div>
          </div>
        </Card>

        <Card title="گشایش سال مالی جدید">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="سال">{(id) => <Input id={id} w="sm" num value={yearText || toFa(next)} onChange={(e) => setYearText(e.target.value)} />}</Field>
              <Field label="شروع">{(id) => <Input id={id} w="date" num ro value={showDate(`${yearNo}/01/01`)} />}</Field>
              <Field label="پایان">{(id) => <Input id={id} w="date" num ro value={showDate(`${yearNo}/12/29`)} />}</Field>
            </div>
            <div className="sub">۱۲ دورهٔ ماهانه ساخته می‌شود ({MONTHS[0]} تا {MONTHS[11]}) و شماره‌گذاری اسناد از ۱ شروع می‌شود.</div>
            {hasOpen && (
              <Message tone="err" title="سال قبل هنوز باز است">
                بدون بستن حساب‌ها هم می‌توان سال جدید را باز کرد، ولی سند افتتاحیه خودکار ساخته نمی‌شود.
              </Message>
            )}
            {yearErr != null && <Message tone={friendly(yearErr).tone} title={friendly(yearErr).title}>{friendly(yearErr).text}</Message>}
            <div className="bar"><Button variant="pri" disabled={!Number.isInteger(yearNo) || yearNo < 1300} onClick={() => void openYear()}>گشایش سال جدید</Button></div>
          </div>
        </Card>

        <Card title="چاپ و خروجی">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="قالب">{() => <Select label="قالب" width="220px" value={tpl} onChange={setTpl} options={[{ value: 'doc', label: 'سند حسابداری (A4)' }, { value: 'list', label: 'فهرست اسناد' }]} />}</Field>
              <Field label="بازهٔ شماره">{(id) => <Input id={id} w="md" num placeholder="۱ تا ۱۰" value={range} onChange={(e) => setRange(e.target.value)} />}</Field>
            </div>
            <div className="bar">
              <Button variant="pri" onClick={() => navigate('/journals')}>پیش‌نمایش چاپ</Button>
              <Button onClick={() => navigate('/journals')}>CSV</Button>
            </div>
            <div className="sub">چاپ و CSV از فهرست اسناد انجام می‌شود (دکمه‌های «چاپ» و «خروجی CSV»).</div>
          </div>
        </Card>
      </div>
      {enable && <EnableCodingDialog onClose={() => setEnable(false)} onDone={() => (setEnable(false), invalidateDirectory(), reloadDir(), toast.push({ tone: 'ok', title: 'کدینگ فعال شد' }))} />}
    </>
  )
}
