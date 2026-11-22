import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { flushSync } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { accounting, type Journal } from '@/api/accounting'
import { ApiError } from '@/api/errors'
import { usePageHints } from '@/app/AppShell'
import { useFiscal } from '@/app/Fiscal'
import { findPostable, type DirectoryEntry } from '@/domain/accounts'
import { periodOf } from '@/domain/fiscal'
import { addRowAfter, copyFromAbove, deleteRow, duplicateRow, emptyRow, swapSides, toApiLines, totals, validate, type DraftRow } from '@/domain/journal'
import { useDirectory } from '@/domain/useDirectory'
import { fmt, groupDigits, toFa } from '@/shared/digits'
import { jalaliToday, showDate } from '@/shared/jalali'
import { newIdempotencyKey } from '@/shared/idempotency'
import { readJSON, remove, savedDescriptions, writeJSON } from '@/shared/storage'
import { Button, Dialog, ErrorMessage, Field, Input, Kbd, Message, PageBar, Select, cx } from '@/ui'
import { AccountCell } from './AccountCell'
import { AccountTreeDialog } from './AccountTreeDialog'

const DRAFT_KEY = 'mizan.journalDraft'
interface Draft {
  date: string
  description: string
  rows: DraftRow[]
}
const freshDraft = (): Draft => ({ date: showDate(jalaliToday()), description: '', rows: [emptyRow(), emptyRow()] })

const HINTS = [
  { keys: ['F3'], text: 'جابه‌جایی بدهکار/بستانکار' },
  { keys: ['F4'], text: 'کپی فیلد از ردیف بالا' },
  { keys: ['F8'], text: 'تکثیر ردیف' },
  { keys: ['Ctrl', 'Ins'], text: 'افزودن' },
  { keys: ['Ctrl', 'Del'], text: 'حذف ردیف' },
]

/**
 * The journal entry screen. Row shortcuts (F3, F4, F8, Ctrl+Insert, Ctrl+Delete, Alt+Shift+ArrowDown) are handled on the
 * focused row, not globally. Business rules live in domain/journal.ts; this component only wires them to the keyboard and the API.
 */
export default function JournalForm() {
  usePageHints(HINTS)
  const navigate = useNavigate()
  const { dir, loading: dirLoading, error: dirError, reload: reloadDir } = useDirectory()
  const { year, reload: reloadYears } = useFiscal()
  const [draft, setDraft] = useState<Draft>(() => readJSON<Draft>(DRAFT_KEY, freshDraft(), 'session'))
  const [sel, setSel] = useState(0)
  const [browse, setBrowse] = useState<number | null>(null)
  const [saved, setSaved] = useState<Journal | null>(null)
  const [error, setError] = useState<unknown>()
  const [busy, setBusy] = useState(false)
  const [descPick, setDescPick] = useState<{ row: number; list: string[]; hi: number } | null>(null)
  const dateRef = useRef<HTMLInputElement>(null)
  const keyRef = useRef<{ fp: string; key: string } | null>(null)

  useEffect(() => writeJSON(DRAFT_KEY, draft, 'session'), [draft])
  const { date, description, rows } = draft
  const setRows = (f: (r: DraftRow[]) => DraftRow[]) => setDraft((d) => ({ ...d, rows: f(d.rows) }))
  const edit = (i: number, patch: Partial<DraftRow>) => {
    setSaved(null)
    setRows((rs) => rs.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  }

  const t = useMemo(() => totals(rows), [rows])
  const v = useMemo(() => validate(date, rows, dir), [date, rows, dir])
  const closed = v.date ? periodOf(year, v.date)?.status === 'closed' : false
  const canSave = t.balanced && v.issues.length === 0 && !v.dateError && v.lines.length >= 2 && !busy && !dirLoading
  const issueFor = (i: number, f: 'code' | 'amount') => v.issues.find((x) => x.row === i && x.field === f)

  const focusNow = (i: number, f: string) => document.querySelector<HTMLInputElement>(`tr[data-row="${i}"] [data-f="${f}"]`)?.focus()

  const onRowKey = (i: number) => (e: KeyboardEvent<HTMLTableRowElement>) => {
    const f = (e.target as HTMLElement).dataset?.f as keyof DraftRow | undefined
    if (e.key === 'F3') {
      e.preventDefault()
      setRows((r) => swapSides(r, i))
    } else if (e.key === 'F8') {
      e.preventDefault()
      flushSync(() => {
        setRows((r) => duplicateRow(r, i))
        setSel(i + 1)
      })
      if (f) focusNow(i + 1, f)
    } else if (e.key === 'F4') {
      if (i > 0 && f) {
        e.preventDefault()
        setRows((r) => copyFromAbove(r, i, f))
      }
    } else if (e.key === 'Insert' && e.ctrlKey) {
      e.preventDefault()
      flushSync(() => {
        setRows((r) => addRowAfter(r, i))
        setSel(i + 1)
      })
      focusNow(i + 1, 'code')
    } else if (e.key === 'Delete' && e.ctrlKey) {
      e.preventDefault()
      setRows((r) => deleteRow(r, i))
      setSel(Math.max(0, i - 1))
    } else if (e.key === 'ArrowDown' && e.altKey && e.shiftKey && f === 'desc') {
      e.preventDefault()
      const list = savedDescriptions.list()
      if (list.length) setDescPick({ row: i, list, hi: 0 })
    }
  }

  const onDescPickKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!descPick) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setDescPick({ ...descPick, hi: Math.min(descPick.list.length - 1, descPick.hi + 1) })
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setDescPick({ ...descPick, hi: Math.max(0, descPick.hi - 1) })
    } else if (e.key === 'Enter') {
      e.preventDefault()
      edit(descPick.row, { desc: descPick.list[descPick.hi] ?? '' })
      setDescPick(null)
    } else if (e.key === 'Delete') {
      e.preventDefault()
      const list = savedDescriptions.remove(descPick.list[descPick.hi]!)
      setDescPick(list.length ? { ...descPick, list, hi: Math.min(descPick.hi, list.length - 1) } : null)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      setDescPick(null)
    }
  }

  const pick = (i: number, a: DirectoryEntry) => {
    // commit first, then move focus in the same tick: fast typing must land in the description, not append to the code
    flushSync(() => {
      edit(i, { code: a.code })
      setBrowse(null)
    })
    focusNow(i, 'desc')
  }

  const save = async () => {
    if (!canSave || !v.date) return
    setBusy(true)
    setError(undefined)
    try {
      // subsidiary accounts of the chart that have no ledger account yet are created first (idempotent)
      const toCreate = new Map(v.lines.filter((l) => !l.entry.ledgerCode).map((l) => [l.entry.code, l.entry]))
      for (const a of toCreate.values()) {
        if (!a.type) throw new ApiError(400, 'invalid_argument', `account ${a.code} has no type`)
        await accounting.createAccount({ code: a.code, name: a.name, type: a.type, coding_code: a.code })
      }
      const body = { date: v.date, description: description.trim() || rows.find((r) => r.desc.trim())?.desc.trim() || 'سند دستی', lines: toApiLines(v) }
      // the same content keeps the same key on retry; edited content gets a new one
      const fp = JSON.stringify(body)
      if (keyRef.current?.fp !== fp) keyRef.current = { fp, key: newIdempotencyKey('journal') }
      const j = await accounting.postJournal(body, keyRef.current.key)
      keyRef.current = null
      for (const r of rows) savedDescriptions.add(r.desc)
      savedDescriptions.add(description)
      setSaved(j)
      setDraft(freshDraft())
      remove(DRAFT_KEY, 'session')
      setSel(0)
      reloadDir()
      reloadYears()
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }

  const periodClosedError = error instanceof ApiError && error.code === 'period_closed'

  return (
    <>
      <PageBar title="سند حسابداری" sub="پیش‌نویس · شمارهٔ سند پس از ثبت داده می‌شود">
        <Button onClick={() => navigate('/journals')}>انصراف</Button>
        <Button variant="pri" onClick={() => void save()} disabled={!canSave}>
          {busy ? 'در حال ثبت…' : 'ثبت سند'}
        </Button>
      </PageBar>

      {saved && (
        <Message tone="ok" title={`سند ${toFa(saved.number)} ثبت شد`}>
          شمارهٔ سند در سال مالی {toFa(year?.name ?? '')} بدون فاصله داده شد. سند قابل ویرایش نیست؛ اصلاح با سند برگشتی انجام می‌شود.
        </Message>
      )}
      {!t.balanced && t.diff > 0 && (
        <Message tone="err" title="سند تراز نیست">
          اختلاف بدهکار و بستانکار {fmt(t.diff)} ریال است. تا تراز نشود، ثبت انجام نمی‌شود.
        </Message>
      )}
      {closed && !periodClosedError && (
        <Message tone="warn" title="دورهٔ این تاریخ بسته است">
          ثبت در دورهٔ بسته ممکن نیست؛ تاریخ را تغییر دهید.
        </Message>
      )}
      {error != null && !periodClosedError && <ErrorMessage error={error} />}
      {dirError != null && <ErrorMessage error={dirError} onRetry={reloadDir} />}

      <div className="card">
        <div className="cb">
          <div className="row">
            <Field label="شمارهٔ سند">{(id) => <Input id={id} w="sm" num ro value="—" />}</Field>
            <Field label="تاریخ" required error={v.dateError && date ? v.dateError : undefined}>
              {(id) => <Input id={id} ref={dateRef} w="date" num bad={!!v.dateError} value={date} onChange={(e) => setDraft({ ...draft, date: e.target.value })} onBlur={() => v.date && setDraft((d) => ({ ...d, date: showDate(v.date) }))} />}
            </Field>
            <Field label="نوع سند" hint="API فقط سند عمومی ثبت می‌کند">
              {(id) => <Select id={id} label="نوع سند" width="160px" disabled options={[{ value: 'general', label: 'عمومی' }]} value="general" onChange={() => undefined} />}
            </Field>
            <Field label="شمارهٔ مرجع" hint="API شمارهٔ مرجع نمی‌پذیرد">
              {(id) => <Input id={id} w="code" num disabled placeholder="اختیاری" />}
            </Field>
            <Field label="شرح سند" grow>
              {(id) => <Input id={id} w="xl" value={description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />}
            </Field>
          </div>
        </div>
      </div>

      <div className="tw" style={{ overflow: 'visible' }}>
        <table className="tbl" aria-label="ردیف‌های سند">
          <thead>
            <tr>
              <th className="ck">#</th>
              <th style={{ width: 150 }}>کد حساب (معین)</th>
              <th style={{ width: 260 }}>نام حساب</th>
              <th style={{ width: 190 }}>تفصیلی</th>
              <th>شرح ردیف</th>
              <th className="n" style={{ width: 160 }}>بدهکار (ریال)</th>
              <th className="n" style={{ width: 160 }}>بستانکار (ریال)</th>
              <th style={{ width: 40 }} />
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => {
              const found = findPostable(dir, r.code)
              return (
                <tr key={i} data-row={i} className={cx(i === sel && 'sel')} onKeyDown={onRowKey(i)} onFocus={() => sel !== i && setSel(i)}>
                  <td className="ck mut">{toFa(i + 1)}</td>
                  <td className="ed" style={{ position: 'relative' }}>
                    <AccountCell dir={dir} value={r.code} invalid={!!issueFor(i, 'code') && !!r.code.trim()} onChange={(code) => edit(i, { code })} onPick={(a) => pick(i, a)} onBrowse={() => setBrowse(i)} />
                  </td>
                  <td className="mut" style={{ lineHeight: 1.3 }}>
                    {found ? found.name : r.code ? 'حساب یافت نشد' : '—'}
                    <div style={{ fontSize: 11, color: 'var(--faint)' }}>{found?.path}</div>
                  </td>
                  <td className="ed">
                    <input className="cell" data-f="det" disabled title="پیشنهادی: API فعلی mizan تفصیلی ندارد" placeholder="شخص / مرکز هزینه" aria-label="تفصیلی (در دسترس نیست)" value={r.det} readOnly />
                  </td>
                  <td className="ed" style={{ position: 'relative' }}>
                    <input className="cell" data-f="desc" aria-label="شرح ردیف" value={r.desc} onChange={(e) => edit(i, { desc: e.target.value })} />
                    {descPick?.row === i && (
                      <div role="listbox" aria-label="شرح‌های ذخیره‌شده" tabIndex={-1} ref={(el) => el?.focus()} onKeyDown={onDescPickKey} onBlur={() => setDescPick(null)} style={{ position: 'absolute', top: '100%', insetInlineStart: 4, zIndex: 7, width: 360, background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 6, boxShadow: '0 12px 30px #0004', padding: 4, outline: 'none' }}>
                        {descPick.list.map((d, j) => (
                          <div key={d} role="option" aria-selected={j === descPick.hi} className={cx('tree-i', j === descPick.hi && 'on')} onMouseDown={(e) => (e.preventDefault(), edit(i, { desc: d }), setDescPick(null))}>
                            {d}
                          </div>
                        ))}
                        <div className="sub" style={{ paddingTop: 6 }}>
                          <Kbd>Delete</Kbd> حذف شرح انتخاب‌شده از فهرست
                        </div>
                      </div>
                    )}
                  </td>
                  <td className="ed">
                    <input className={cx('cell n', issueFor(i, 'amount') && 'bad')} data-f="dr" aria-label="بدهکار" inputMode="numeric" value={r.dr} onChange={(e) => edit(i, { dr: e.target.value, cr: e.target.value ? '' : r.cr })} onBlur={() => edit(i, { dr: groupDigits(r.dr) })} />
                  </td>
                  <td className="ed">
                    <input className={cx('cell n', issueFor(i, 'amount') && 'bad')} data-f="cr" aria-label="بستانکار" inputMode="numeric" value={r.cr} onChange={(e) => edit(i, { cr: e.target.value, dr: e.target.value ? '' : r.dr })} onBlur={() => edit(i, { cr: groupDigits(r.cr) })} />
                  </td>
                  <td className="ed">
                    <Button small aria-label="حذف ردیف" onClick={() => (setRows((x) => deleteRow(x, i)), setSel(Math.max(0, i - 1)))}>
                      ×
                    </Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
          <tfoot>
            <tr>
              <td colSpan={5}>
                جمع <span className="sub" style={{ fontWeight: 400, marginInlineStart: 10 }}>{toFa(rows.length)} ردیف</span>
              </td>
              <td className="n">{fmt(t.debit)}</td>
              <td className="n">{fmt(t.credit)}</td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>

      <div className="bar">
        <Button onClick={() => (setRows((r) => addRowAfter(r, sel)), setSel(sel + 1))}>
          افزودن ردیف <Kbd>Ctrl</Kbd>
          <Kbd>Ins</Kbd>
        </Button>
        <Button onClick={() => (setRows((r) => duplicateRow(r, sel)), setSel(sel + 1))}>
          تکثیر ردیف <Kbd>F8</Kbd>
        </Button>
        <Button onClick={() => setRows((r) => swapSides(r, sel))}>
          جابه‌جایی بدهکار/بستانکار <Kbd>F3</Kbd>
        </Button>
        <Button onClick={() => (setRows((r) => deleteRow(r, sel)), setSel(Math.max(0, sel - 1)))}>
          حذف ردیف <Kbd>Ctrl</Kbd>
          <Kbd>Del</Kbd>
        </Button>
        <span className="grow" />
        <span className={cx('badge', t.balanced ? 'ok' : 'err')} role="status">
          {t.balanced ? 'تراز است' : `اختلاف ${fmt(t.diff)} ریال`}
        </span>
      </div>
      <div className="sub">ستون «تفصیلی» پیشنهادی است؛ API فعلی mizan فقط حساب‌های کل و معین را دارد.</div>

      {browse !== null && <AccountTreeDialog dir={dir} onClose={() => setBrowse(null)} onPick={(a) => pick(browse, a)} />}
      {periodClosedError && (
        <Dialog
          title="ثبت در دورهٔ بسته ممکن نیست"
          onClose={() => setError(undefined)}
          footer={
            <>
              <Button variant="pri" onClick={() => (setError(undefined), dateRef.current?.focus())}>
                تغییر تاریخ
              </Button>
              <Button onClick={() => setError(undefined)}>بستن</Button>
            </>
          }
        >
          <div>
            دورهٔ <b>{v.date ? toFa(v.date.split('/')[1]!) : ''}</b> بسته است. تاریخ سند را به دورهٔ باز تغییر دهید یا از مدیر مالی بخواهید دوره را باز کند.
          </div>
        </Dialog>
      )}
    </>
  )
}
