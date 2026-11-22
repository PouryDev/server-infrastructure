import type { JournalSummary } from '@/api/accounting'

export type JournalStatus = { label: string; tone: 'ok' | 'warn' | 'err' | '' }

/** Type column: manual documents are "عمومی", documents made by invoices/payments/... are "خودکار". */
export function journalType(j: Pick<JournalSummary, 'kind' | 'ref_type'>): string {
  switch (j.kind) {
    case 'reversal':
      return 'اصلاحی'
    case 'opening':
      return 'افتتاحیه'
    case 'closing':
      return 'اختتامیه'
    default:
      return j.ref_type === 'manual' ? 'عمومی' : 'خودکار'
  }
}

/** A journal is "reversed" when a reversal shares its reference (reversals copy the original's ref). */
export function journalStatus(j: Pick<JournalSummary, 'kind' | 'ref_type' | 'ref_id'>, reversedRefs: Set<string>): JournalStatus {
  if (j.kind === 'reversal') return { label: 'سند اصلاحی', tone: 'warn' }
  if (reversedRefs.has(`${j.ref_type}:${j.ref_id}`)) return { label: 'برگشت‌خورده', tone: 'err' }
  return { label: 'ثبت‌شده', tone: 'ok' }
}

export const refKey = (j: Pick<JournalSummary, 'ref_type' | 'ref_id'>): string => `${j.ref_type}:${j.ref_id}`
