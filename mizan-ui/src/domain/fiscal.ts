import type { FiscalYear, Period } from '@/api/accounting'
import { jalaliToday } from '@/shared/jalali'

const gregorianToday = () => new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tehran' }).format(new Date()) // 2026-11-10

/** The year that covers today, otherwise the latest open one, otherwise the newest. */
export function defaultYear(years: FiscalYear[]): FiscalYear | undefined {
  const t = gregorianToday()
  return years.find((y) => y.from <= t && t <= y.to) ?? years.find((y) => y.status === 'open') ?? years[0]
}

export function currentPeriod(year: FiscalYear | undefined): Period | undefined {
  const t = gregorianToday()
  return year?.periods.find((p) => p.from <= t && t <= p.to)
}

/** Whether the Jalali date (1405/08/19) falls in a period that is closed (client-side hint; the server decides). */
export function periodOf(year: FiscalYear | undefined, jalali: string): Period | undefined {
  const m = Number(jalali.split('/')[1])
  return year?.periods.find((p) => p.number === m)
}

export { jalaliToday }
