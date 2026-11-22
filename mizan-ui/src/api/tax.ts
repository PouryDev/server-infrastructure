import { api, money } from './http'

export interface ITResult {
  accounting_profit: number
  add_back_total: number
  deduction_total: number
  depreciation_adjustment: number
  taxable_before_loss: number
  loss_used_total: number
  new_loss: number
  taxable_income: number
  effective_rate_bp: number
  tax: number
}

export interface ITAssessment extends ITResult {
  id: string
  fiscal_year: string
  from: string
  to: string
  credits: number
  payable: number
  paid: number
  outstanding: number
  due_on_jalali: string
  overdue: boolean
}

export interface ITRule {
  id?: string
  effective_from: string
  rate_bp: number
  cooperative_discount_bp?: number
  loss_carry_years?: number | null
  filing_months?: number
  source?: string
}

export interface ITAdjust {
  add_backs?: { label: string; amount: number }[]
  deductions?: { label: string; amount: number }[]
  depreciation_adj?: number
  credits?: number
}

export const incomeTax = {
  rules: () => api.get<{ items: ITRule[] } | ITRule[]>('/v1/incometax/rules'),
  addRule: (b: ITRule) => api.post<ITRule>('/v1/incometax/rules', b),
  preview: (fiscalYearId: number, adj: ITAdjust = {}) => api.post<ITResult & { fiscal_year: string }>(`/v1/incometax/preview/${fiscalYearId}`, adj),
  declare: (fiscalYearId: number, adj: ITAdjust, key: string) => api.post<ITAssessment>(`/v1/incometax/assessments/${fiscalYearId}`, adj, key),
  assessments: () => api.get<{ items: ITAssessment[] } | ITAssessment[]>('/v1/incometax/assessments'),
  pay: (id: string, b: { amount: number; account?: string; date?: string }, key: string) => api.post<ITAssessment>(`/v1/incometax/assessments/${id}/payments`, { ...b, amount: money(b.amount) }, key),
  losses: () => api.get<{ items: { assessment_id?: string; fiscal_year?: string; amount?: number; remaining?: number; expires?: string }[] } | { assessment_id?: string; fiscal_year?: string; amount?: number; remaining?: number }[]>('/v1/incometax/losses'),
}

export interface EInvoice {
  id: string
  source_type: string
  source_id: string
  status: string
  trace_id: string
  reference_number?: string
  errors: { code: string; message: string; type?: string }[]
  warnings: { code: string; message: string }[]
  attempts: number
  last_error?: string
  sent_at?: string
}

export interface EInvoiceOptions {
  pattern?: number
  subject?: number
  ref_taxid?: string
  settlement?: number
  cash_amount?: number
  contract_no?: string
}

export const einvoiceApi = {
  list: (q?: Record<string, string>) => api.get<{ items: EInvoice[]; meta: { total?: number } }>('/v1/einvoices', q),
  get: (id: string) => api.get<EInvoice>(`/v1/einvoices/${id}`),
  enqueue: (invoiceId: string, o: EInvoiceOptions = {}) => api.post<EInvoice>(`/v1/einvoice-requests/invoice/${invoiceId}`, o),
  requeue: (id: string) => api.post<EInvoice>(`/v1/einvoices/${id}/requeue`),
  send: () => api.post<{ sent: number }>('/v1/einvoices/send'),
  refresh: () => api.post<{ settled: number }>('/v1/einvoices/refresh'),
}
