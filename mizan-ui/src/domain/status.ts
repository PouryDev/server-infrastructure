import type { Tone } from '@/ui'

type S = { label: string; tone: Tone }
const make = (m: Record<string, S>, fallback: (k: string) => S) => (k: string): S => m[k] ?? fallback(k)

export const invoiceStatus = make(
  {
    draft: { label: 'پیش‌نویس', tone: 'info' },
    issued: { label: 'صادرشده', tone: 'warn' },
    partially_paid: { label: 'پرداخت جزئی', tone: 'warn' },
    paid: { label: 'پرداخت‌شده', tone: 'ok' },
    void: { label: 'ابطال‌شده', tone: 'err' },
  },
  (k) => ({ label: k, tone: '' }),
)

export const paymentStatus = make(
  {
    created: { label: 'ایجادشده', tone: '' },
    pending: { label: 'در انتظار', tone: 'warn' },
    paid: { label: 'پرداخت‌شده', tone: 'ok' },
    failed: { label: 'ناموفق', tone: 'err' },
    expired: { label: 'منقضی', tone: '' },
    partially_refunded: { label: 'بازگشت جزئی', tone: 'info' },
    refunded: { label: 'بازگشت‌شده', tone: 'info' },
  },
  (k) => ({ label: k, tone: '' }),
)

export const refundStatus = make(
  {
    pending: { label: 'در صف', tone: 'warn' },
    processing: { label: 'در حال انجام', tone: 'info' },
    done: { label: 'انجام‌شده', tone: 'ok' },
    failed: { label: 'ناموفق', tone: 'err' },
    cancelled: { label: 'لغوشده', tone: '' },
  },
  (k) => ({ label: k, tone: '' }),
)

export const refundMethod = (m: string): string => (m === 'gateway' ? 'درگاه' : m === 'manual_card_to_card' ? 'کارت‌به‌کارت' : m)
