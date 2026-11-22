import type { Cheque, ChequeStatus } from '@/api/treasury'
import type { Tone } from '@/ui'

export const chequeStatus: Record<ChequeStatus, { label: string; tone: Tone }> = {
  in_hand: { label: 'در دست', tone: 'info' },
  issued: { label: 'صادرشده', tone: 'info' },
  deposited: { label: 'واگذار به بانک', tone: 'warn' },
  cleared: { label: 'وصول‌شده', tone: 'ok' },
  bounced: { label: 'برگشتی', tone: 'err' },
  returned: { label: 'مرجوعی', tone: '' },
  endorsed: { label: 'ظهرنویسی‌شده', tone: '' },
}

export type ChequeAction = 'deposit' | 'clear' | 'bounce' | 'return' | 'endorse' | 'take-back'

/** Which operations the cheque's state machine allows (README "Treasury"): the server enforces the same rules. */
export function allowedActions(c: Pick<Cheque, 'direction' | 'status'>): ChequeAction[] {
  if (c.direction === 'receivable') {
    switch (c.status) {
      case 'in_hand': return ['deposit', 'return', 'endorse']
      case 'deposited': return ['clear', 'bounce']
      case 'bounced': return ['deposit', 'endorse', 'take-back']
      default: return []
    }
  }
  switch (c.status) {
    case 'issued': return ['clear', 'bounce', 'return']
    case 'bounced': return ['clear']
    default: return []
  }
}

export const ACTION_LABEL: Record<ChequeAction, string> = { deposit: 'واگذاری به بانک', clear: 'وصول', bounce: 'برگشت', return: 'مرجوعی', endorse: 'ظهرنویسی', 'take-back': 'بازپس‌گیری' }
