// Persian/Latin digit helpers and rial formatting. Money is an integer number of rials (never a float).
const FA = '۰۱۲۳۴۵۶۷۸۹'
const AR = '٠١٢٣٤٥٦٧٨٩'

/** Converts Persian and Arabic-Indic digits to ASCII digits; other characters are kept. */
export function toLatin(s: string | number | null | undefined): string {
  return String(s ?? '').replace(/[۰-۹٠-٩]/g, (d) => {
    const i = FA.indexOf(d)
    return String(i >= 0 ? i : AR.indexOf(d))
  })
}

/** Converts ASCII digits to Persian digits. */
export function toFa(s: string | number | null | undefined): string {
  return String(s ?? '').replace(/\d/g, (d) => FA[Number(d)] ?? d)
}

/** 5800000 -> ۵٬۸۰۰٬۰۰۰ ; negative numbers get the true minus sign. */
export function fmt(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return '—'
  const abs = Math.abs(Math.trunc(n)).toString()
  const grouped = abs.replace(/\B(?=(\d{3})+(?!\d))/g, '٬')
  return (n < 0 ? '−' : '') + toFa(grouped)
}

/** Like fmt but shows an en dash for zero (statements, journals). */
export function fmtOrDash(n: number | null | undefined): string {
  return !n ? '—' : fmt(n)
}

/** Parses what a user typed in an amount field ("۵٬۸۰۰٬۰۰۰", "5,800,000", "5800000"). NaN if it is not a whole number of rials. */
export function parseAmount(text: string): number {
  const t = toLatin(text).replace(/[٬,\s،]/g, '')
  if (t === '') return 0
  if (!/^\d+$/.test(t)) return NaN
  const n = Number(t)
  return Number.isSafeInteger(n) ? n : NaN
}

/** Formats a field while typing: groups the digits, keeps it LTR-friendly. */
export function groupDigits(text: string): string {
  const n = parseAmount(text)
  return Number.isNaN(n) || text.trim() === '' ? text : n === 0 && !/\d/.test(toLatin(text)) ? '' : fmt(n)
}

/** Comparison helper: lower-cased, digits and Arabic letter variants normalised, for searching. */
export function normSearch(s: string): string {
  return toLatin(s)
    .replace(/ي/g, 'ی')
    .replace(/ك/g, 'ک')
    .trim()
    .toLowerCase()
}
