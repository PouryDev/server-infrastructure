import { toLatin, toFa } from './digits'

// The API takes and returns Jalali dates as 1405/08/18 (Persian digits are accepted); storage is Gregorian.
const parts = new Intl.DateTimeFormat('en-u-ca-persian-nu-latn', {
  timeZone: 'Asia/Tehran',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

/** Today's date (or `d`) as 1405/08/19 in Latin digits. */
export function jalaliOf(d: Date = new Date()): string {
  const p = Object.fromEntries(parts.formatToParts(d).map((x) => [x.type, x.value]))
  return `${p.year}/${p.month}/${p.day}`
}

export const jalaliToday = (): string => jalaliOf()

export const MONTHS = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند']

/** Normalises user input like ۱۴۰۵-۸-۹ to 1405/08/09, or null when it is not a plausible Jalali date. */
export function normalizeJalali(text: string): string | null {
  const m = /^(\d{4})[/-](\d{1,2})[/-](\d{1,2})$/.exec(toLatin(text).trim())
  if (!m) return null
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const maxDay = mo <= 6 ? 31 : mo <= 11 ? 30 : 30 // the leap-day check is the server's (Esfand 30)
  if (mo < 1 || mo > 12 || d < 1 || d > maxDay || y < 1200 || y > 1700) return null
  return `${y}/${String(mo).padStart(2, '0')}/${String(d).padStart(2, '0')}`
}

/** 1405/08/19 -> ۱۴۰۵/۰۸/۱۹ */
export const showDate = (j: string | null | undefined): string => (j ? toFa(j.replaceAll('-', '/')) : '—')

/** 2026-11-10T07:12:00Z -> ۱۴۰۵/۰۸/۱۹ ۱۰:۴۲ (Tehran time). */
export function showDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const time = new Intl.DateTimeFormat('en-GB', { timeZone: 'Asia/Tehran', hour: '2-digit', minute: '2-digit', hour12: false }).format(d)
  return `${toFa(jalaliOf(d))} ${toFa(time)}`
}

/** Jalali month number (1..12) of a 1405/08/19 string. */
export const monthOf = (j: string): number => Number(toLatin(j).split('/')[1] ?? 0)

// ---- Gregorian <-> Jalali, on top of the platform's Persian calendar (Intl), so no hand-written calendar table ----
const utcParts = new Intl.DateTimeFormat('en-u-ca-persian-nu-latn', { timeZone: 'UTC', year: 'numeric', month: '2-digit', day: '2-digit' })

/** 2026-11-10 (or an RFC 3339 timestamp's date part) -> 1405/08/19. */
export function isoToJalali(iso: string): string {
  const [y, m, d] = iso.slice(0, 10).split('-').map(Number) as [number, number, number]
  const p = Object.fromEntries(utcParts.formatToParts(new Date(Date.UTC(y, m - 1, d, 12))).map((x) => [x.type, x.value]))
  return `${p.year}/${p.month}/${p.day}`
}

/** 1405/08/19 -> 2026-11-10 (Gregorian ISO date); null when the input is not a Jalali date. */
export function jalaliToIso(j: string): string | null {
  const n = normalizeJalali(j)
  if (!n) return null
  const jy = Number(n.slice(0, 4))
  // the string form YYYY/MM/DD sorts like the dates themselves: binary-search the Gregorian day with that Jalali form
  let lo = Date.UTC(jy + 621, 2, 10, 12) / 86400000
  let hi = Date.UTC(jy + 622, 2, 30, 12) / 86400000
  while (lo <= hi) {
    const mid = Math.floor((lo + hi) / 2)
    const iso = new Date(mid * 86400000).toISOString().slice(0, 10)
    const got = isoToJalali(iso)
    if (got === n) return iso
    if (got < n) lo = mid + 1
    else hi = mid - 1
  }
  return null
}
