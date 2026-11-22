import { api, money } from './http'
import type { Payment, Refund } from './payments'
import { listQuery, type ListParams, type Page } from './page'

// ---- invoices ----
export type InvoiceStatus = 'draft' | 'issued' | 'paid' | 'void' | 'partially_paid' | string

export interface InvoiceSummary {
  id: string
  number: number | null
  status: InvoiceStatus
  customer_ref: string
  total: number
  paid: number
  issue_date: string | null
  due_date: string | null
  created_at: string
}

export interface InvoiceItem {
  no: number
  description: string
  quantity: number
  unit_price: number
  discount: number
  tax_bp: number
  net: number
  tax: number
  total: number
}

export interface Invoice {
  id: string
  number: number | null
  status: InvoiceStatus
  customer_ref: string
  buyer: { name?: string; national_id?: string; economic_code?: string; postal_code?: string; phone?: string; address?: string }
  items: InvoiceItem[]
  subtotal: number
  discount_total: number
  tax_total: number
  total: number
  paid: number
  remaining: number
  issue_date: string | null
  issue_date_jalali?: string
  due_date: string | null
  memo: string
  created_at: string
}

export interface NewInvoice {
  customer_ref: string
  buyer?: Invoice['buyer']
  items: { description: string; quantity: number; unit_price: number; discount?: number; tax_bp?: number }[]
  due_date?: string
  memo?: string
  issue: boolean
}

export const invoices = {
  list: (p?: ListParams, signal?: AbortSignal) => api.get<Page<InvoiceSummary>>('/v1/invoices', listQuery(p), signal),
  get: (id: string) => api.get<{ invoice: Invoice; payments: { source: string; amount: number; journal_id?: string; at: string }[] }>(`/v1/invoices/${id}`),
  create: (body: NewInvoice, key: string) => api.post<Invoice>('/v1/invoices', body, key),
  issue: (id: string) => api.post<Invoice>(`/v1/invoices/${id}/issue`),
  void: (id: string, reason: string) => api.post<Invoice>(`/v1/invoices/${id}/void`, { reason }),
  payFromWallet: (id: string, walletId: string, key: string) => api.post<unknown>(`/v1/invoices/${id}/pay-from-wallet`, { wallet_id: walletId }, key),
  payByGateway: (id: string, gateway: string, returnUrl: string, key: string) => api.post<{ pay_url?: string }>(`/v1/invoices/${id}/payments`, { gateway, return_url: returnUrl }, key),
}

// ---- payments, refunds, disputes, reconciliation ----
export interface Dispute {
  id: string
  payment_id?: string
  recon_item?: number
  kind: string
  status: 'open' | 'closed' | string
  opened_by: string
  notes: { by?: string; at?: string; text?: string; note?: string }[]
  resolution?: string
  created_at: string
  updated_at: string
}

export const paymentOps = {
  get: (id: string) => api.get<Payment & { refunds: Refund[]; history: { from: string; to: string; reason: string; at: string }[] }>(`/v1/payments/${id}`),
  sweep: () => api.post<{ checked: number; paid: number; failed: number; expired: number; unknown: number }>('/v1/payments/sweep'),
  refund: (id: string, body: { amount?: number; reason: string; method?: string }, key: string) =>
    api.post<unknown>(`/v1/payments/${id}/refunds`, { ...body, amount: body.amount === undefined ? undefined : money(body.amount) }, key),
  claim: (id: string) => api.post<unknown>(`/v1/refunds/${id}/claim`),
  release: (id: string, force = false) => api.post<unknown>(`/v1/refunds/${id}/release`, { force }),
  complete: (id: string, body: { tracking_number: string; paid_at: string; destination_card_masked: string; note?: string }, key: string) => api.post<unknown>(`/v1/refunds/${id}/complete`, body, key),
  fail: (id: string, reason: string, key: string) => api.post<unknown>(`/v1/refunds/${id}/fail`, { reason }, key),
  cancel: (id: string, reason: string, key: string) => api.post<unknown>(`/v1/refunds/${id}/cancel`, { reason }, key),
  disputes: (status?: string) => api.get<Dispute[]>('/v1/disputes', status ? { status } : undefined),
  openDispute: (body: { payment_id?: string; kind: string; note?: string }) => api.post<Dispute>('/v1/disputes', body),
  addNote: (id: string, note: string) => api.post<Dispute>(`/v1/disputes/${id}/notes`, { text: note }),
  closeDispute: (id: string, status: 'resolved' | 'rejected', resolution: string) => api.post<Dispute>(`/v1/disputes/${id}/close`, { status, resolution }),
}
