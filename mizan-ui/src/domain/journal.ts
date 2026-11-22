import type { JournalDraftLine } from '@/api/accounting'
import { findPostable, type DirectoryEntry } from './accounts'
import { parseAmount } from '@/shared/digits'
import { normalizeJalali } from '@/shared/jalali'

export interface DraftRow {
  code: string
  det: string
  desc: string
  dr: string
  cr: string
}

export const emptyRow = (): DraftRow => ({ code: '', det: '', desc: '', dr: '', cr: '' })

export interface Totals {
  debit: number
  credit: number
  diff: number
  balanced: boolean
}

/** Sums the typed amounts. Unparseable text counts as 0 here; validate() reports it. */
export function totals(rows: DraftRow[]): Totals {
  const n = (v: string) => {
    const x = parseAmount(v)
    return Number.isNaN(x) ? 0 : x
  }
  const debit = rows.reduce((s, r) => s + n(r.dr), 0)
  const credit = rows.reduce((s, r) => s + n(r.cr), 0)
  return { debit, credit, diff: Math.abs(debit - credit), balanced: debit === credit && debit > 0 }
}

export interface RowIssue {
  row: number
  field: 'code' | 'amount'
  message: string
}

/** A row without account and amounts is ignored (a blank trailing row). */
export const isBlank = (r: DraftRow): boolean => !r.code.trim() && !r.dr.trim() && !r.cr.trim()

export interface Validation {
  date: string | null
  dateError?: string
  issues: RowIssue[]
  /** rows that will be posted, with the account resolved */
  lines: { row: number; entry: DirectoryEntry; side: 'debit' | 'credit'; amount: number; memo: string }[]
}

/** The same rules the server enforces, so mistakes are shown next to the field instead of after a round trip. */
export function validate(date: string, rows: DraftRow[], dir: DirectoryEntry[]): Validation {
  const v: Validation = { date: normalizeJalali(date), issues: [], lines: [] }
  if (!v.date) v.dateError = 'تاریخ را به شکل ۱۴۰۵/۰۸/۱۹ وارد کنید.'
  rows.forEach((r, i) => {
    if (isBlank(r)) return
    const entry = findPostable(dir, r.code)
    if (!entry) v.issues.push({ row: i, field: 'code', message: r.code.trim() ? 'حسابی با این کد وجود ندارد یا قابل ثبت نیست.' : 'حساب را انتخاب کنید.' })
    const dr = parseAmount(r.dr)
    const cr = parseAmount(r.cr)
    if (Number.isNaN(dr) || Number.isNaN(cr)) v.issues.push({ row: i, field: 'amount', message: 'مبلغ باید عدد صحیح (ریال) باشد.' })
    else if ((dr > 0) === (cr > 0)) v.issues.push({ row: i, field: 'amount', message: 'هر ردیف فقط یکی از بدهکار یا بستانکار را دارد.' })
    if (entry && dr + cr > 0 && !Number.isNaN(dr + cr) && (dr > 0) !== (cr > 0)) {
      v.lines.push({ row: i, entry, side: dr > 0 ? 'debit' : 'credit', amount: dr > 0 ? dr : cr, memo: r.desc.trim() })
    }
  })
  return v
}

/** Lines ready for the API (ledger account codes). */
export function toApiLines(v: Validation): JournalDraftLine[] {
  return v.lines.map((l) => ({ account: l.entry.ledgerCode || l.entry.code, side: l.side, amount: l.amount, ...(l.memo ? { memo: l.memo } : {}) }))
}

// ---- row editing used by the keyboard shortcuts (pure, tested) ----

export function duplicateRow(rows: DraftRow[], i: number): DraftRow[] {
  const next = rows.slice()
  next.splice(i + 1, 0, { ...rows[i]! })
  return next
}

export function addRowAfter(rows: DraftRow[], i: number): DraftRow[] {
  const next = rows.slice()
  next.splice(i + 1, 0, emptyRow())
  return next
}

/** Removes a row; the last remaining row is cleared instead. */
export function deleteRow(rows: DraftRow[], i: number): DraftRow[] {
  if (rows.length <= 1) return [emptyRow()]
  return rows.filter((_, k) => k !== i)
}

export function swapSides(rows: DraftRow[], i: number): DraftRow[] {
  return rows.map((r, k) => (k === i ? { ...r, dr: r.cr, cr: r.dr } : r))
}

/** F4: the focused field is copied from the row above. Debit and credit stay exclusive. */
export function copyFromAbove(rows: DraftRow[], i: number, field: keyof DraftRow): DraftRow[] {
  if (i <= 0) return rows
  const v = rows[i - 1]![field]
  return rows.map((r, k) => {
    if (k !== i) return r
    const next = { ...r, [field]: v }
    if (field === 'dr' && v) next.cr = ''
    if (field === 'cr' && v) next.dr = ''
    return next
  })
}
