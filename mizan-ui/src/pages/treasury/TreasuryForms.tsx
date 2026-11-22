import { useState } from 'react'
import { treasury } from '@/api/treasury'
import { itemsOf } from '@/api/commerce'
import { fmt, toFa } from '@/shared/digits'
import { friendly } from '@/api/errors'
import { Card, Message, PageBar, Select, Seg, useResource, useToast } from '@/ui'
import { InlineForm, val } from '@/ui/FormDialog'

/** Forms of the treasury screen shown inline: bank account, statement import, cheque, gateway settlement. */
export default function TreasuryForms() {
  const toast = useToast()
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [dir, setDir] = useState<'receivable' | 'payable'>('receivable')
  const [bank, setBank] = useState('')
  const [file, setFile] = useState<File>()
  const [importMsg, setImportMsg] = useState<{ ok: boolean; text: string }>()
  const opts = (banks.data ?? []).map((b) => ({ value: b.id, label: b.name }))
  const chosen = bank || opts[0]?.value || ''

  return (
    <>
      <PageBar title="ثبت چک، حساب بانکی و درون‌ریزی" sub="فرم‌ها و دیالوگ‌هایی که از صفحهٔ خزانه‌داری باز می‌شوند" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="حساب بانکی جدید">
          <InlineForm submitLabel="ایجاد حساب" rows={[['name', 'ledger'], ['iban']]} fields={[{ name: 'name', label: 'نام حساب', required: true, w: 'md' }, { name: 'ledger', label: 'کد حساب دفتر کل', kind: 'latin', w: 'code', hint: 'اختیاری' }, { name: 'iban', label: 'شماره شبا', kind: 'latin', w: 'lg', placeholder: 'IR…' }]}
            onSubmit={(v) => treasury.openBank({ name: val.text(v.name), ...(val.text(v.ledger) ? { ledger_code: val.text(v.ledger) } : {}), ...(val.text(v.iban) ? { iban: val.text(v.iban).replaceAll(' ', '') } : {}) })} onDone={() => (toast.push({ tone: 'ok', title: 'حساب بانکی ایجاد شد' }), banks.reload())} />
        </Card>
        <Card title="درون‌ریزی صورت‌حساب بانکی">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="fld"><label>حساب</label><Select label="حساب" width="220px" value={chosen} options={opts} onChange={setBank} /></div>
            <label style={{ border: '1.5px dashed var(--line)', borderRadius: 6, padding: 22, textAlign: 'center', color: 'var(--mut)', cursor: 'pointer' }}>
              فایل CSV یا JSON را اینجا رها کنید یا <b style={{ color: 'var(--pri)' }}>انتخاب کنید</b>
              <input type="file" accept=".csv,.json" style={{ display: 'none' }} onChange={async (e) => { const f = e.target.files?.[0]; setFile(f); setImportMsg(undefined); if (f && chosen) { try { const r = await treasury.importStatement(chosen, f); setImportMsg({ ok: true, text: `${toFa(Number(r.imported ?? r.added ?? 0))} ردیف جدید افزوده شد. ${r.skipped != null || r.duplicates != null ? `${toFa(Number(r.skipped ?? r.duplicates))} ردیف تکراری نادیده گرفته شد.` : ''}` }) } catch (err) { setImportMsg({ ok: false, text: friendly(err).text + (err instanceof Error ? ` (${err.message})` : '') }) } } }} />
              <div className="hint" style={{ marginTop: 4 }}>ستون‌ها: تاریخ، شرح، مرجع، مانده، و مبلغ (یا بدهکار و بستانکار)</div>
              {file && <div className="sub n">{file.name}</div>}
            </label>
            {importMsg && <Message tone={importMsg.ok ? 'ok' : 'err'} title={importMsg.ok ? 'درون‌ریزی انجام شد' : 'درون‌ریزی انجام نشد'}>{importMsg.text}</Message>}
          </div>
        </Card>
        <Card title="ثبت چک">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Seg<'receivable' | 'payable'> label="جهت" value={dir} onChange={setDir} options={[{ value: 'receivable', label: 'دریافتی' }, { value: 'payable', label: 'پرداختی' }]} />
            <InlineForm key={dir} submitLabel="ثبت چک" rows={[['sayad', 'number', 'amount', 'due'], ['party', 'bank']]} fields={[{ name: 'sayad', label: 'شناسهٔ صیادی', kind: 'latin', w: 'md', hint: '۱۶ رقم؛ اختیاری و یکتا' }, { name: 'number', label: 'شمارهٔ چک', kind: 'latin', w: 'md', required: true }, { name: 'amount', label: 'مبلغ (ریال)', kind: 'amount', w: 'amt', required: true }, { name: 'due', label: 'سررسید', kind: 'date', w: 'date', required: true }, { name: 'party', label: 'طرف حساب', w: 'md', required: true }, { name: 'bank', label: 'بانک', w: 'md' }]}
              onSubmit={(v, key) => treasury.registerCheque({ direction: dir, ...(val.text(v.sayad) ? { sayad_id: val.text(v.sayad) } : {}), number: val.text(v.number), amount: val.amount(v.amount), due_date: val.date(v.due)!, counterparty_ref: val.text(v.party), ...(val.text(v.bank) ? { drawer_bank: val.text(v.bank) } : {}), ...(dir === 'payable' && chosen ? { bank_account_id: chosen } : {}) }, key)} onDone={() => toast.push({ tone: 'ok', title: 'چک ثبت شد' })}>
              {dir === 'payable' && <div className="sub">چک پرداختی از حساب «{opts.find((o) => o.value === chosen)?.label ?? '—'}» صادر می‌شود (حساب را در کارت درون‌ریزی تغییر دهید).</div>}
            </InlineForm>
          </div>
        </Card>
        <Card title="ثبت تسویهٔ درگاه">
          <InlineForm submitLabel="ثبت تسویه" rows={[['gateway', 'ref'], ['gross', 'fee']]} fields={[{ name: 'gateway', label: 'درگاه', kind: 'select', options: [{ value: 'zarinpal', label: 'زرین‌پال' }, { value: 'idpay', label: 'آیدی‌پی' }], initial: 'zarinpal', w: 'sm' }, { name: 'ref', label: 'مرجع تسویه', kind: 'latin', w: 'md', required: true }, { name: 'gross', label: 'مبلغ ناخالص', kind: 'amount', w: 'amt', required: true }, { name: 'fee', label: 'کارمزد', kind: 'amount', w: 'amt' }]}
            onSubmit={(v, key) => treasury.recordSettlement({ gateway: v.gateway!, ref: val.text(v.ref), bank_account_id: chosen, gross: val.amount(v.gross), fee: val.amount(v.fee) }, key)} onDone={() => toast.push({ tone: 'ok', title: 'تسویه ثبت شد' })}>
            <div className="sub">واریز به حساب «{opts.find((o) => o.value === chosen)?.label ?? '—'}» · خالص = ناخالص − کارمزد ({fmt(0)})</div>
          </InlineForm>
        </Card>
      </div>
    </>
  )
}
