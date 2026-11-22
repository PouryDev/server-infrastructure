import { api, money } from './http'

export interface VatFigures {
  jalali_year: number
  period: number
  period_months: number
  from: string
  to: string
  output_tax: number
  input_tax: number
  net: number
  opening_settlement_balance: number
  due_on?: string
  due_on_jalali?: string
}

export interface VatReturn extends VatFigures {
  id: string
  filed_on: string
  journal_id: string
}

export interface VatRule {
  id: string
  effective_from: string
  period_months: number
  filing_days?: number
  filing_month_end?: boolean
  late_penalty_bp: number
  source: string
}

export interface VatRate {
  id?: string
  category: string
  effective_from: string
  rate_bp: number
  levy_bp?: number | null
  description: string
  verified: boolean
  source: string
}

export interface VatExemption {
  id?: string
  code: string
  kind: string
  description: string
  article: string
  effective_from: string
  effective_to?: string
  source: string
}

export const vat = {
  position: () => api.get<{ balance: number; owed: number; credit: number }>('/v1/vat/position'),
  preview: (year: number, period: number, months?: number) => api.get<VatFigures>('/v1/vat/preview', { year, period, period_months: months }),
  returns: () => api.get<{ items: VatReturn[] }>('/v1/vat/returns'),
  file: (b: { jalali_year: number; period: number; period_months?: number; filed_on?: string }, key: string) => api.post<VatReturn>('/v1/vat/returns', b, key),
  amend: (b: { jalali_year: number; period: number; period_months?: number }, key: string) => api.post<VatReturn>('/v1/vat/returns/amend', b, key),
  penalty: (id: string, asOf?: string) => api.get<{ unpaid: number; months_late: number; penalty: number; penalty_bp_per_month: number; as_of: string }>(`/v1/vat/returns/${id}/penalty`, { as_of: asOf }),
  compute: (base: number, category?: string, date?: string) => api.get<{ category: string; rate_bp: number; tax: number; total: number }>('/v1/vat/compute', { base, category, date }),
  pay: (b: { amount: number; account?: string; date?: string }, key: string) => api.post<unknown>('/v1/vat/payments', { ...b, amount: money(b.amount) }, key),
  refund: (b: { amount: number; account?: string; date?: string }, key: string) => api.post<unknown>('/v1/vat/refunds', { ...b, amount: money(b.amount) }, key),
  rules: () => api.get<{ items: VatRule[] } | VatRule[]>('/v1/vat/rules'),
  addRule: (b: Omit<VatRule, 'id'>) => api.post<VatRule>('/v1/vat/rules', b),
  rates: () => api.get<{ items: VatRate[] } | VatRate[]>('/v1/vat/rates'),
  addRate: (b: Omit<VatRate, 'id'>) => api.post<VatRate>('/v1/vat/rates', b),
  exemptions: () => api.get<{ items: VatExemption[] } | VatExemption[]>('/v1/vat/exemptions'),
  addExemption: (b: Omit<VatExemption, 'id'>) => api.post<VatExemption>('/v1/vat/exemptions', b),
  apportionments: () => api.get<{ items: Record<string, unknown>[] } | Record<string, unknown>[]>('/v1/vat/apportionments'),
  apportion: (b: { jalali_year: number; period: number; period_months?: number; direct_taxable: number; direct_exempt: number; machinery: number; common: number; taxable_sales: number; exempt_sales: number }) => api.post<Record<string, unknown>>('/v1/vat/apportionments', b),
}
