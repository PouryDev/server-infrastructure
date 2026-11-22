import { api } from './http'
import { listQuery, type ListParams, type Page } from './page'

export type PaymentStatus = 'created' | 'pending' | 'paid' | 'failed' | 'expired' | 'partially_refunded' | 'refunded'

export interface Payment {
  id: string
  status: PaymentStatus
  purpose_type: string
  purpose_id: string
  amount: number
  refunded: number
  gateway: string
  pay_url?: string
  gateway_txn_ref?: string
  card_pan?: string
  expires_at: string
  paid_at?: string
  failure_code?: string
  settlement_error?: string
  created_at: string
}

export type RefundStatus = 'pending' | 'processing' | 'done' | 'failed' | 'cancelled'

export interface Refund {
  id: string
  payment_id: string
  amount: number
  reason: string
  status: RefundStatus
  method: 'gateway' | 'manual_card_to_card'
  gateway_ref?: string
  failure?: string
  claimed_by?: string
  claimed_at?: string
  destination_card_masked?: string
  tracking_number?: string
  paid_at?: string
  note?: string
  journal_id?: string
  created_at: string
}

export const payments = {
  list: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Payment>>('/v1/payments', listQuery(p), signal),
  unsettled: () => api.get<Payment[]>('/v1/payments/unsettled'),
  refunds: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Refund>>('/v1/refunds', listQuery(p), signal),
}
