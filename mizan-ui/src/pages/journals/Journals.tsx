import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { accounting, type Journal, type JournalSummary } from '@/api/accounting'
import { useFiscal } from '@/app/Fiscal'
import { journalStatus, journalType, refKey } from '@/domain/journalView'
import { useDirectory } from '@/domain/useDirectory'
import { fmt, fmtOrDash, toFa } from '@/shared/digits'
import { isoToJalali, jalaliToIso, showDate, showDateTime } from '@/shared/jalali'
import type { ListParams } from '@/api/page'
import { Badge, Button, Card, DataTable, ErrorMessage, Field, Input, Kbd, PageBar, Select, Skeleton, useListState, useResource, useToast, type Column } from '@/ui'
import { ReverseDialog } from './ReverseDialog'
import { HistoryDialog } from './HistoryDialog'

type StatusFilter = 'all' | 'normal' | 'reversal'

export default function Journals() {
  const { year } = useFiscal()
  const list = useListState({}, 20)
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [fromNo, setFromNo] = useState('')
  const [toNo, setToNo] = useState('')
  const [status, setStatus] = useState<StatusFilter>('all')
  const [text, setText] = useState('')
  const [applied, setApplied] = useState({ from: '', to: '', fromNo: '', toNo: '', status: 'all' as StatusFilter, text: '' })
  const [params] = useSearchParams()
  const [active, setActive] = useState<string | undefined>(params.get('open') ?? undefined)
  const [reversing, setReversing] = useState<Journal | null>(null)
  const [history, setHistory] = useState<JournalSummary | null>(null)
  const toast = useToast()
  const { dir } = useDirectory()

  const filter = useMemo(() => {
    const f: ListParams['filter'] = {}
    if (year) f.fiscal_year_id = String(year.id)
    const gte = jalaliToIso(applied.from)
    const lte = jalaliToIso(applied.to)
    if (gte) f['date:gte'] = gte
    if (lte) f['date:lte'] = lte
    if (applied.fromNo) f['number:gte'] = applied.fromNo
    if (applied.toNo) f['number:lte'] = applied.toNo
    if (applied.status !== 'all') f.kind = applied.status
    if (applied.text) f['description:like'] = applied.text
    for (const [k, v] of Object.entries(list.filters)) {
      if (!v) continue
      f[k === 'description' ? 'description:like' : k] = k === 'date' ? (jalaliToIso(v) ?? v) : v
    }
    return f
  }, [year, applied, list.filters])

  const data = useResource(
    async (signal) => {
      const page = await accounting.journals({ page: list.page, pageSize: list.pageSize, sort: '-number', filter }, signal)
      const reversals = await accounting.journals({ pageSize: 200, filter: { kind: 'reversal', ...(year ? { fiscal_year_id: String(year.id) } : {}) } }, signal)
      return { page, reversed: new Set(reversals.items.map(refKey)) }
    },
    [list.page, JSON.stringify(filter)],
  )

  const detail = useResource(
    (signal) => (active ? accounting.journal(active) : Promise.resolve(undefined)).then((j) => (signal.aborted ? undefined : j)),
    [active],
  )
  const items = data.data?.page.items
  const reversed = data.data?.reversed ?? new Set<string>()

  const columns: Column<JournalSummary>[] = [
    { id: 'number', header: 'شمارهٔ سند', num: true, filter: 'number', cell: (j) => toFa(j.number) },
    { id: 'date', header: 'تاریخ', filter: 'date', cell: (j) => showDate(isoToJalali(j.date)) },
    { id: 'description', header: 'شرح', filter: 'description', cell: (j) => j.description },
    { id: 'total', header: 'جمع (ریال)', num: true, filter: 'total', cell: (j) => fmt(j.total) },
    { id: 'kind', header: 'نوع', mut: true, filter: 'kind', cell: (j) => journalType(j) },
    {
      id: 'status',
      header: 'وضعیت',
      cell: (j) => {
        const s = journalStatus(j, reversed)
        return <Badge tone={s.tone}>{s.label}</Badge>
      },
    },
  ]

  const apply = () => {
    setApplied({ from, to, fromNo, toNo, status, text })
    list.setPage(1)
  }
  const clear = () => {
    setFrom('')
    setTo('')
    setFromNo('')
    setToNo('')
    setStatus('all')
    setText('')
    setApplied({ from: '', to: '', fromNo: '', toNo: '', status: 'all', text: '' })
    list.setFilters({})
  }
  const pageTotal = (items ?? []).reduce((s, j) => s + j.total, 0)
  const d = detail.data

  const exportCsv = () => {
    const rows = [['number', 'date', 'description', 'total', 'type'], ...(items ?? []).map((j) => [j.number, isoToJalali(j.date), j.description, j.total, journalType(j)])]
    const csv = '﻿' + rows.map((r) => r.map((c) => `"${String(c).replaceAll('"', '""')}"`).join(',')).join('\n')
    const a = document.createElement('a')
    a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
    a.download = 'journals.csv'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  return (
    <>
      <PageBar title="اسناد حسابداری" sub={data.data ? `${fmt(data.data.page.meta.total ?? items?.length ?? 0)} سند در سال مالی ${toFa(year?.name ?? '')}` : undefined}>
        <Button onClick={() => window.print()}>چاپ</Button>
        <Button onClick={exportCsv}>خروجی CSV</Button>
        <Button disabled title="با Ctrl+M داخل جدول فعال می‌شود">
          انتخاب چندگانه <Kbd>Ctrl</Kbd>
          <Kbd>M</Kbd>
        </Button>
        <Link className="btn pri" to="/journals/new">
          سند جدید
        </Link>
      </PageBar>

      <Card>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault()
            apply()
          }}
        >
          <Field label="از تاریخ" error={from && !jalaliToIso(from) ? 'تاریخ نامعتبر' : undefined}>{(id) => <Input id={id} w="date" num placeholder="۱۴۰۵/۰۱/۰۱" value={from} onChange={(e) => setFrom(e.target.value)} />}</Field>
          <Field label="تا تاریخ" error={to && !jalaliToIso(to) ? 'تاریخ نامعتبر' : undefined}>{(id) => <Input id={id} w="date" num placeholder="۱۴۰۵/۱۲/۲۹" value={to} onChange={(e) => setTo(e.target.value)} />}</Field>
          <Field label="از شمارهٔ سند">{(id) => <Input id={id} w="sm" num placeholder="۱" value={fromNo} onChange={(e) => setFromNo(e.target.value)} />}</Field>
          <Field label="تا شمارهٔ سند">{(id) => <Input id={id} w="sm" num value={toNo} onChange={(e) => setToNo(e.target.value)} />}</Field>
          <Field label="وضعیت">
            {(id) => (
              <Select<StatusFilter> id={id} label="وضعیت" width="150px" value={status} onChange={setStatus} options={[{ value: 'all', label: 'همه' }, { value: 'normal', label: 'ثبت‌شده' }, { value: 'reversal', label: 'اصلاحی' }]} />
            )}
          </Field>
          <Field label="جستجو در شرح">{(id) => <Input id={id} w="md" placeholder="عبارت شرح" value={text} onChange={(e) => setText(e.target.value)} />}</Field>
          <Button variant="pri" type="submit">اعمال فیلتر</Button>
          <Button onClick={clear}>پاک‌کردن</Button>
        </form>
      </Card>

      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        <div style={{ flex: '999 1 620px', minWidth: 0 }}>
          <DataTable
            caption="اسناد حسابداری"
            columns={columns}
            rows={items}
            rowKey={(j) => j.id}
            loading={data.loading}
            error={data.error}
            onRetry={data.reload}
            empty={{ title: 'هنوز سندی ثبت نشده', text: 'با «سند جدید» اولین سند را ثبت کنید.', action: <Link className="btn pri" to="/journals/new">سند جدید</Link> }}
            filters={list.filters}
            onFilters={list.setFilters}
            meta={data.data?.page.meta}
            onPage={list.setPage}
            selectable
            onHistory={(j) => setHistory(j)}
            onRowClick={(j) => setActive(j.id)}
            rowClass={(j) => (j.id === active ? 'sel' : undefined)}
            footer={<span className="sub">جمع صفحه: <span className="n">{fmt(pageTotal)}</span> ریال</span>}
          />
        </div>

        {active && (
          <div className="card" style={{ flex: '1 1 380px', minWidth: 340 }}>
            {detail.loading && !d ? (
              <Skeleton />
            ) : detail.error != null ? (
              <div className="cb"><ErrorMessage error={detail.error} onRetry={detail.reload} /></div>
            ) : d ? (
              <>
                <div className="ch">
                  سند {toFa(d.number)}
                  <Badge tone={journalStatus(d, reversed).tone}>{journalStatus(d, reversed).label}</Badge>
                  <span className="sp" />
                  <Button small onClick={() => setHistory(d)}>
                    ردیابی <Kbd>Ctrl</Kbd>
                    <Kbd>H</Kbd>
                  </Button>
                </div>
                <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <dl className="kv" style={{ margin: 0 }}>
                    <dt>تاریخ</dt><dd>{showDate(d.date_jalali)}</dd>
                    <dt>شرح</dt><dd>{d.description}</dd>
                    <dt>ثبت‌کننده</dt><dd>{d.created_by ?? '—'} · {showDateTime(d.created_at).split(' ')[1]}</dd>
                  </dl>
                  <div className="tw">
                    <table className="tbl" aria-label="ردیف‌های سند">
                      <thead><tr><th>حساب</th><th className="n">بدهکار</th><th className="n">بستانکار</th></tr></thead>
                      <tbody>
                        {d.lines.map((l) => {
                          const a = dir.find((x) => x.ledgerCode === l.account || x.code === l.account)
                          return (
                            <tr key={l.no}>
                              <td>{a ? `${toFa(a.code)} ${a.name}` : l.account}</td>
                              <td className="n">{l.side === 'debit' ? fmt(l.amount) : fmtOrDash(0)}</td>
                              <td className="n">{l.side === 'credit' ? fmt(l.amount) : fmtOrDash(0)}</td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                  <div className="bar">
                    <Button variant="dng" disabled={d.kind === 'reversal' || reversed.has(refKey(d))} onClick={() => setReversing(d)}>
                      صدور سند برگشتی
                    </Button>
                    <Button onClick={() => window.print()}>چاپ سند</Button>
                  </div>
                </div>
              </>
            ) : null}
          </div>
        )}
      </div>

      {reversing && (
        <ReverseDialog
          journal={reversing}
          onClose={() => setReversing(null)}
          onDone={(j) => {
            setReversing(null)
            toast.push({ tone: 'ok', title: `سند برگشتی ${toFa(j.number)} صادر شد` })
            data.reload()
          }}
        />
      )}
      {history && <HistoryDialog journal={history} onClose={() => setHistory(null)} />}
    </>
  )
}

