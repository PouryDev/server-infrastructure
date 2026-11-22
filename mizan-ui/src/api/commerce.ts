import { api, money } from './http'
import { listQuery, type ListParams, type Page } from './page'

// ---- coupons ----
export interface Coupon {
  id: string
  code: string
  name: string
  kind: 'percent' | 'fixed'
  percent_bp?: number
  fixed_amount?: number
  min_amount: number
  valid_from?: string
  valid_until?: string
  max_redemptions: number | null
  max_per_customer: number | null
  active: boolean
  redeemed: number
}

export interface Redemption {
  id: string
  coupon_id: string
  customer_ref: string
  amount: number
  discount: number
  status: 'redeemed' | 'released'
  subject_type?: string
  subject_id?: string
  released_reason?: string
}

export const couponApi = {
  list: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Coupon>>('/v1/coupons', listQuery(p), signal),
  create: (b: { code: string; name: string; kind: 'percent' | 'fixed'; percent_bp?: number; fixed_amount?: number; min_amount?: number; valid_until?: string; max_redemptions?: number | null }) => api.post<Coupon>('/v1/coupons', b),
  update: (id: string, b: Record<string, unknown>) => api.put<Coupon>(`/v1/coupons/${id}`, b),
  redemptions: (id: string) => api.get<{ items: Redemption[] } | Redemption[]>(`/v1/coupons/${id}/redemptions`),
  preview: (code: string, customer: string, amount: number) => api.post<{ discount: number }>('/v1/coupon-actions/preview', { code, customer_ref: customer, amount: money(amount) }),
  redeem: (b: { code: string; customer_ref: string; amount: number; subject_type: string; subject_id: string }, key: string) => api.post<Redemption>('/v1/coupon-actions/redeem', { ...b, amount: money(b.amount) }, key),
  release: (id: string, reason: string) => api.post<unknown>(`/v1/coupon-redemptions/${id}/release`, { reason }),
}

// ---- subscriptions ----
export interface Plan {
  id: string
  code: string
  name: string
  price: number
  unit: 'day' | 'month' | 'year'
  count: number
  trial_days: number
  tax_bp: number | null
  active: boolean
}

export interface Subscription {
  id: string
  plan_id: string
  plan_name: string
  customer_ref: string
  status: 'trialing' | 'active' | 'paused' | 'canceled'
  price: number
  unit: string
  count: number
  coupon_code?: string
  start_date: string
  next_bill_date?: string
  next_bill_date_jalali?: string
  periods_billed: number
  cancel_at_period_end: boolean
}

export interface SubscriptionPeriod {
  no: number
  start_jalali: string
  end_jalali: string
  amount: number
  status: 'pending' | 'invoiced' | string
  invoice_id?: string
  attempts: number
  last_error?: string
}

export const subscriptionApi = {
  plans: () => api.get<Page<Plan> | Plan[]>('/v1/subscription-plans'),
  addPlan: (b: { code: string; name: string; price: number; unit: string; count: number; trial_days?: number }) => api.post<Plan>('/v1/subscription-plans', { ...b, price: money(b.price) }),
  updatePlan: (id: string, b: { name?: string; price?: number; active?: boolean }) => api.put<Plan>(`/v1/subscription-plans/${id}`, { ...b, price: b.price === undefined ? undefined : money(b.price) }),
  list: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Subscription>>('/v1/subscriptions', listQuery(p), signal),
  periods: (id: string) => api.get<{ items: SubscriptionPeriod[] } | SubscriptionPeriod[]>(`/v1/subscriptions/${id}/periods`),
  subscribe: (b: { plan_id: string; customer_ref: string; buyer?: { name: string }; start?: string; coupon_code?: string }, key: string) => api.post<Subscription>('/v1/subscriptions', b, key),
  bill: () => api.post<{ periods: number; invoiced: number; failed: number }>('/v1/subscriptions/bill'),
  pause: (id: string) => api.post<Subscription>(`/v1/subscriptions/${id}/pause`),
  resume: (id: string) => api.post<Subscription>(`/v1/subscriptions/${id}/resume`),
  cancel: (id: string, atPeriodEnd: boolean) => api.post<Subscription>(`/v1/subscriptions/${id}/cancel`, { at_period_end: atPeriodEnd }),
}

// ---- wallets ----
export interface Wallet {
  id: string
  owner_ref: string
  currency: string
  status: 'active' | 'frozen' | 'closed'
  balance?: number
}

export const walletApi = {
  byOwner: (owner: string) => api.get<Wallet[]>('/v1/wallets', { owner_ref: owner }),
  get: (id: string) => api.get<Wallet>(`/v1/wallets/${id}`),
  open: (owner: string) => api.post<Wallet>('/v1/wallets', { owner_ref: owner, currency: 'IRR' }),
  statement: (id: string) => api.get<{ opening_balance: number; rows: { number: number; date_jalali: string; description: string; debit: number; credit: number; balance: number; journal_id: string }[] }>(`/v1/wallets/${id}/statement`),
  credit: (id: string, amount: number, description: string, key: string) => api.post<unknown>(`/v1/wallets/${id}/credit`, { amount: money(amount), description }, key),
  debit: (id: string, amount: number, description: string, key: string) => api.post<unknown>(`/v1/wallets/${id}/debit`, { amount: money(amount), description }, key),
  setStatus: (id: string, status: 'active' | 'frozen' | 'closed') => api.post<unknown>(`/v1/wallets/${id}/status`, { status }),
  topup: (id: string, amount: number, gateway: string, returnUrl: string, key: string) => api.post<{ pay_url?: string }>(`/v1/wallets/${id}/topups`, { amount: money(amount), gateway, return_url: returnUrl }, key),
  transfer: (from: string, to: string, amount: number, description: string, key: string) => api.post<unknown>('/v1/wallet-transfers', { from, to, amount: money(amount), description }, key),
}

// ---- payout (marketplace sellers) ----
export interface Seller {
  id: string
  code: string
  name: string
  sheba?: string
  status: 'active' | 'blocked'
  block_reason?: string
  account_code: string
}

export interface Sale {
  id: string
  seller_id: string
  reference?: string
  category?: string
  gross: number
  commission: number
  vat: number
  net: number
  rate_bp: number
  hold_days: number
  status: string
  phase: string
  sale_date: string
  delivered_at?: string
  release_at?: string
  batch_id?: string
}

export interface Terms {
  id: string
  name: string
  scope: string
  scope_key?: string
  effective_from: string
  effective_from_jalali: string
  rate_bp: number
  hold_days: number
  vat_bp: number | null
  source?: string
}

export interface Clawback {
  id: string
  sale_id: string
  seller_id: string
  amount: number
  recovered: number
  outstanding: number
  reason?: string
}

export interface BatchItem {
  seller_id: string
  sheba?: string
  amount: number
  sale_count: number
  rejected?: boolean
  reject_reason?: string
}

export interface Batch {
  id: string
  date: string
  date_jalali: string
  total: number
  sale_count: number
  pay_from: string
  items: BatchItem[]
}

export const payoutApi = {
  sellers: (p?: ListParams) => api.get<Page<Seller> | Seller[]>('/v1/payout/sellers', listQuery(p)),
  addSeller: (b: { code: string; name: string; sheba?: string }) => api.post<Seller>('/v1/payout/sellers', b),
  updateSeller: (id: string, b: { code: string; name: string; sheba?: string }) => api.put<Seller>(`/v1/payout/sellers/${id}`, b),
  block: (id: string, reason: string) => api.post<unknown>(`/v1/payout/sellers/${id}/block`, { reason }),
  unblock: (id: string) => api.post<unknown>(`/v1/payout/sellers/${id}/unblock`),
  balance: (id: string) => api.get<Record<string, unknown>>(`/v1/payout/sellers/${id}/balance`),
  sales: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Sale> | Sale[]>('/v1/payout/sales', listQuery(p), signal),
  recordSale: (b: { seller_id: string; gross: number; reference?: string; category?: string; date?: string }, key: string) => api.post<Sale>('/v1/payout/sales', { ...b, gross: money(b.gross) }, key),
  deliver: (id: string, date?: string) => api.post<Sale>(`/v1/payout/sales/${id}/deliver`, { date }),
  returnSale: (id: string, reason: string) => api.post<Sale>(`/v1/payout/sales/${id}/return`, { reason }),
  partialReturn: (id: string, gross: number, reason: string) => api.post<Sale>(`/v1/payout/sales/${id}/partial-return`, { gross: money(gross), reason }),
  returnPaid: (id: string, reason: string) => api.post<Sale>(`/v1/payout/sales/${id}/return-paid`, { reason }),
  clawbacks: (sellerId: string) => api.get<Clawback[] | { items: Clawback[] }>(`/v1/payout/sellers/${sellerId}/clawbacks`),
  terms: () => api.get<Terms[] | { items: Terms[] }>('/v1/payout/terms'),
  addTerms: (b: { name: string; scope: string; scope_key?: string; effective_from: string; rate_bp: number; hold_days: number; source?: string }) => api.post<Terms>('/v1/payout/terms', b),
  batches: () => api.get<Page<Batch> | Batch[]>('/v1/payout/batches'),
  batch: (id: string) => api.get<Batch>(`/v1/payout/batches/${id}`),
  createBatch: (b: { bank_account_id?: string; date?: string; seller_ids?: string[] }, key: string) => api.post<Batch>('/v1/payout/batches', b, key),
  rejectItem: (batchId: string, sellerId: string, reason: string) => api.post<unknown>(`/v1/payout/batches/${batchId}/reject-item`, { seller_id: sellerId, reason }),
}

/** Some list endpoints answer {items:[...]}, others a bare array. */
export const itemsOf = <T,>(r: { items: T[] } | T[] | undefined | null): T[] => (Array.isArray(r) ? r : (r?.items ?? []))
