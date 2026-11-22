import type { Approval } from '@/api/approvals'
import type { Refund } from '@/api/payments'
import type { Cheque } from '@/api/treasury'
import { toFa } from '@/shared/digits'
import type { Tone } from '@/ui'

export interface Task {
  id: string
  tone: Tone
  kind: string
  text: string
  amount: number | null
  age: string
  action: string
  to: string
}

/** "۵ ساعت" / "۲ روز" from an ISO time. */
export function ageText(iso: string, now: number): string {
  const ms = Math.max(0, now - new Date(iso).getTime())
  const h = Math.floor(ms / 36e5)
  if (h < 1) return 'کمتر از ۱ ساعت'
  if (h < 24) return `${toFa(h)} ساعت`
  return `${toFa(Math.floor(h / 24))} روز`
}

/** The to-do list of the dashboard, built only from what the API really reports. */
export function buildTasks(i: { approvals: Approval[]; bounced: Cheque[]; refunds: Refund[]; unmatchedLines: number; now: number }): Task[] {
  const out: Task[] = []
  for (const a of i.approvals) {
    out.push({ id: 'ap' + a.id, tone: 'warn', kind: 'تأیید', text: a.kind === 'year_close' ? `بستن سال مالی (${a.subject})` : `تقسیم سود (${a.subject})`, amount: null, age: ageText(a.created_at, i.now), action: 'بررسی', to: '/approvals' })
  }
  for (const c of i.bounced) {
    out.push({ id: 'ch' + c.id, tone: 'err', kind: 'چک برگشتی', text: `چک ${toFa(c.number)} – ${c.counterparty_ref}`, amount: c.amount, age: ageText(c.created_at, i.now), action: 'رسیدگی', to: '/treasury' })
  }
  for (const r of i.refunds) {
    out.push({ id: 'rf' + r.id, tone: 'info', kind: 'بازگشت وجه', text: r.status === 'pending' ? 'بازگشت وجه دستی – در صف' : `بازگشت وجه دستی – در حال انجام (${r.claimed_by ?? ''})`, amount: r.amount, age: ageText(r.created_at, i.now), action: r.status === 'pending' ? 'برداشتن' : 'ادامه', to: '/payments' })
  }
  if (i.unmatchedLines > 0) {
    out.push({ id: 'rc', tone: 'warn', kind: 'تطبیق بانکی', text: `${toFa(i.unmatchedLines)} ردیف صورت‌حساب بانک تطبیق نشده`, amount: null, age: '—', action: 'تطبیق', to: '/reconcile' })
  }
  return out
}
