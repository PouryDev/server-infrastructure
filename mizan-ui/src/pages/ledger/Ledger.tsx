import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { accounting, type Statement } from '@/api/accounting'
import { useDirectory } from '@/domain/useDirectory'
import { fmt, fmtOrDash, toFa } from '@/shared/digits'
import { jalaliToIso, showDate } from '@/shared/jalali'
import { Badge, Button, Card, DataTable, Field, Input, LinkButton, PageBar, Stat, SubTabs, useResource, type Column } from '@/ui'
import { AccountCell } from '../journals/AccountCell'
import { TrialBalanceCard } from './TrialBalanceCard'

type Tab = 'statement' | 'trial'

/** Account statement (گردش حساب) and trial balance (تراز) of the design, both against the real ledger. */
export default function Ledger() {
  const [params, setParams] = useSearchParams()
  const { dir } = useDirectory()
  const [tab, setTab] = useState<Tab>('statement')
  const [code, setCode] = useState(params.get('account') ?? '')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [applied, setApplied] = useState({ code: params.get('account') ?? '', from: '', to: '' })
  const [filters, setFilters] = useState<Record<string, string>>({})

  const entry = useMemo(() => dir.find((a) => a.code === applied.code.trim()), [dir, applied.code])
  const ledgerCode = entry?.ledgerCode
  const gte = jalaliToIso(applied.from) ?? undefined
  const lte = jalaliToIso(applied.to) ?? undefined

  const st = useResource<{ statement: Statement; type: string } | null>(
    async (signal) => {
      if (!ledgerCode) return null
      const all: Statement['rows'] = []
      let after: number | undefined
      let first: Statement | undefined
      for (let i = 0; i < 10; i++) {
        const s = await accounting.statement(ledgerCode, { from: gte, to: lte, after, limit: 200 }, signal)
        first ??= s
        all.push(...s.rows)
        if (!s.next_after) break
        after = s.next_after
      }
      const acc = await accounting.account(ledgerCode)
      return { statement: { ...first!, rows: all }, type: acc.type }
    },
    [ledgerCode, gte, lte],
  )

  useEffect(() => {
    if (applied.code) setParams({ account: applied.code }, { replace: true })
  }, [applied.code, setParams])

  const s = st.data?.statement
  const credNatural = st.data ? st.data.type === 'liability' || st.data.type === 'equity' || st.data.type === 'revenue' : false
  const sumDr = (s?.rows ?? []).reduce((x, r) => x + r.debit, 0)
  const sumCr = (s?.rows ?? []).reduce((x, r) => x + r.credit, 0)
  const closing = s ? (s.rows.length ? s.rows[s.rows.length - 1]!.balance : s.opening_balance) : 0
  const side = (b: number) => (b === 0 ? '' : (b > 0) !== credNatural ? 'بد' : 'بس')

  const columns: Column<Statement['rows'][number]>[] = [
    { id: 'd', header: 'تاریخ', filter: 'date_jalali', cell: (r) => showDate(r.date_jalali) },
    { id: 'no', header: 'سند', num: true, filter: 'number', cell: (r) => <Link to={`/journals?open=${r.journal_id}`} style={{ color: 'var(--pri)' }}>{toFa(r.number)}</Link> },
    { id: 't', header: 'شرح', filter: 'description', cell: (r) => r.description },
    { id: 'dr', header: 'بدهکار', num: true, cell: (r) => fmtOrDash(r.debit) },
    { id: 'cr', header: 'بستانکار', num: true, cell: (r) => fmtOrDash(r.credit) },
    { id: 'bal', header: 'مانده', num: true, cell: (r) => fmt(r.balance) },
    { id: 'side', header: 'م/ب', mut: true, cell: (r) => side(r.balance) },
  ]
  // the filter row filters the loaded rows (the statement endpoint has no filter syntax)
  const rows = (s?.rows ?? []).filter((r) => Object.entries(filters).every(([k, v]) => !v || String(k === 'number' ? r.number : k === 'date_jalali' ? r.date_jalali : r.description).includes(v)))

  const exportCsv = () => {
    const csv = '﻿' + [['date', 'document', 'description', 'debit', 'credit', 'balance'], ...rows.map((r) => [r.date_jalali, r.number, r.description, r.debit, r.credit, r.balance])].map((r) => r.map((c) => `"${String(c).replaceAll('"', '""')}"`).join(',')).join('\n')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
    a.download = `statement-${applied.code}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <>
      <PageBar title="گردش حساب و تراز">
        <Button onClick={() => window.print()}>چاپ</Button>
        <Button onClick={exportCsv} disabled={tab !== 'statement' || rows.length === 0}>خروجی CSV</Button>
      </PageBar>
      <SubTabs<Tab> value={tab} onChange={setTab} options={[{ value: 'statement', label: 'گردش حساب' }, { value: 'trial', label: 'تراز آزمایشی' }]} />

      <Card>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault()
            setApplied({ code, from, to })
          }}
        >
          {tab === 'statement' && (
            <>
              <Field label="کد حساب">
                {() => (
                  <div style={{ width: 200 }}>
                    <AccountCell dir={dir} value={code} field="acct" onChange={setCode} onPick={(a) => (setCode(a.code), setApplied({ code: a.code, from, to }))} />
                  </div>
                )}
              </Field>
              <Field label="نام حساب">{(id) => <Input id={id} w="md" ro value={entry?.name ?? ''} />}</Field>
            </>
          )}
          <Field label="از تاریخ">{(id) => <Input id={id} w="date" num placeholder="۱۴۰۵/۰۸/۰۱" value={from} onChange={(e) => setFrom(e.target.value)} />}</Field>
          <Field label="تا تاریخ">{(id) => <Input id={id} w="date" num placeholder="۱۴۰۵/۰۸/۳۰" value={to} onChange={(e) => setTo(e.target.value)} />}</Field>
          <Button variant="pri" type="submit">نمایش</Button>
        </form>
      </Card>

      {tab === 'statement' && (
        <>
          {applied.code && !entry && !st.loading && <div className="msg err"><div><b>حساب پیدا نشد</b><span className="tx">حسابی با این کد وجود ندارد.</span></div></div>}
          {entry && !ledgerCode && <div className="msg info"><div><b>برای این حساب هنوز سندی ثبت نشده</b><span className="tx">حساب هنگام اولین سند ساخته می‌شود.</span></div></div>}
          <div className="grid g4">
            <Stat label="مانده ابتدای دوره" value={s ? fmt(s.opening_balance) : '—'} />
            <Stat label="گردش بدهکار" value={s ? fmt(sumDr) : '—'} />
            <Stat label="گردش بستانکار" value={s ? fmt(sumCr) : '—'} />
            <Stat label={<>مانده پایان {side(closing) && <Badge square>{side(closing) === 'بد' ? 'بدهکار' : 'بستانکار'}</Badge>}</>} value={s ? fmt(closing) : '—'} />
          </div>
          <DataTable
            caption="گردش حساب"
            columns={columns}
            rows={ledgerCode ? rows : []}
            rowKey={(r) => String(r.line_id)}
            loading={st.loading && !!ledgerCode}
            error={st.error}
            onRetry={st.reload}
            empty={{ title: applied.code ? 'گردشی در این بازه نیست' : 'یک حساب انتخاب کنید', text: applied.code ? undefined : 'کد یا نام حساب را وارد و «نمایش» را بزنید.' }}
            filters={filters}
            onFilters={setFilters}
          />
        </>
      )}
      <TrialBalanceCard from={gte} to={lte} title={tab === 'trial' ? 'تراز آزمایشی' : 'تراز آزمایشی (پیش‌نمایش)'} />
      {tab === 'trial' && <LinkButton to="/reports">صورت‌های مالی رسمی</LinkButton>}
    </>
  )
}
