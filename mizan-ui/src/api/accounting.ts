import { api, money, type Query } from './http'
import { listQuery, type ListParams, type Page } from './page'

export type AccountType = 'asset' | 'liability' | 'equity' | 'revenue' | 'expense'
export type PeriodStatus = 'open' | 'closed'

export interface Period {
  id: number
  number: number
  from: string
  to: string
  status: PeriodStatus
}

export interface FiscalYear {
  id: number
  name: string
  from: string
  to: string
  status: PeriodStatus
  periods: Period[]
}

export interface JournalLine {
  no: number
  account: string
  side: 'debit' | 'credit'
  amount: number
  memo?: string
}

export interface Journal {
  id: string
  fiscal_year_id: number
  number: number
  kind: string
  date: string
  date_jalali: string
  description: string
  total: number
  ref_type?: string
  ref_id?: string
  reverses_id?: string
  created_by?: string
  created_at: string
  lines: JournalLine[]
}

export type JournalSummary = Omit<Journal, 'lines'>

export interface JournalDraftLine {
  account: string
  side: 'debit' | 'credit'
  amount: number
  memo?: string
}

export interface TrialBalanceRow {
  code: string
  name: string
  type: AccountType
  coding_code?: string
  debit: number
  credit: number
  balance: number
}

export interface TrialBalance {
  rows: TrialBalanceRow[]
  debit: number
  credit: number
  balanced: boolean
}

export interface CodingNode {
  code: string
  parent?: string
  level: 1 | 2 | 3 | 4
  title: string
  type_hint?: AccountType
}

export interface CodingBalance {
  code: string
  parent?: string
  level: number
  title: string
  debit: number
  credit: number
  balance: number
}

export interface StatementRow {
  line_id: number
  journal_id: string
  number: number
  fiscal_year: string
  date: string
  date_jalali: string
  description: string
  memo?: string
  ref_type?: string
  ref_id?: string
  debit: number
  credit: number
  balance: number
}

export interface Statement {
  account: string
  currency: string
  opening_balance: number
  rows: StatementRow[]
  next_after?: number
}

export const accounting = {
  fiscalYears: () => api.get<FiscalYear[]>('/v1/accounting/fiscal-years'),
  openFiscalYear: (jalaliYear: number) => api.post<{ id: number; name: string }>('/v1/accounting/fiscal-years', { jalali_year: jalaliYear }),
  setPeriod: (id: number, to: PeriodStatus) => api.post<void>(`/v1/accounting/periods/${id}/${to === 'closed' ? 'close' : 'open'}`),
  journals: (p?: ListParams, signal?: AbortSignal) => api.get<Page<JournalSummary>>('/v1/accounting/journals', listQuery(p), signal),
  journal: (id: string) => api.get<Journal>(`/v1/accounting/journals/${id}`),
  postJournal: (body: { date: string; description: string; lines: JournalDraftLine[] }, key: string) =>
    api.post<Journal>('/v1/accounting/journals', { ...body, lines: body.lines.map((l) => ({ ...l, amount: money(l.amount) })) }, key),
  reverseJournal: (id: string, body: { reason: string; date?: string }, key: string) => api.post<Journal>(`/v1/accounting/journals/${id}/reverse`, body, key),
  trialBalance: (q: { from?: string; to?: string; include_zero?: boolean } = {}, signal?: AbortSignal) => api.get<TrialBalance>('/v1/accounting/trial-balance', q as Query, signal),
  codingTrialBalance: async (q: { from?: string; to?: string } = {}, signal?: AbortSignal): Promise<{ nodes: CodingBalance[]; unclassified: TrialBalanceRow[] }> => {
    const r = await api.get<{ nodes: { Code: string; Parent: string; Level: number; Title: string; Debit: number; Credit: number; Balance: number }[] | null; unclassified: TrialBalanceRow[] | null }>(
      '/v1/accounting/trial-balance',
      { ...q, by: 'coding' } as Query,
      signal,
    )
    return {
      nodes: (r.nodes ?? []).map((n) => ({ code: n.Code, parent: n.Parent || undefined, level: n.Level, title: n.Title, debit: n.Debit, credit: n.Credit, balance: n.Balance })),
      unclassified: r.unclassified ?? [],
    }
  },
  coding: async (): Promise<CodingNode[]> => {
    // the Go struct has no json tags: keys come capitalised
    const r = await api.get<{ nodes: { Code: string; Parent: string; Level: 1 | 2 | 3 | 4; Title: string; TypeHint?: AccountType }[] | null }>('/v1/accounting/coding')
    return (r.nodes ?? []).map((n) => ({ code: n.Code, parent: n.Parent || undefined, level: n.Level, title: n.Title, type_hint: n.TypeHint || undefined }))
  },
  statement: (code: string, q: { from?: string; to?: string; after?: number; limit?: number } = {}, signal?: AbortSignal) =>
    api.get<Statement>(`/v1/accounting/accounts/${encodeURIComponent(code)}/statement`, q as Query, signal),
  account: (code: string) => api.get<{ code: string; name: string; type: AccountType; coding_code?: string; active: boolean; balance: number }>(`/v1/accounting/accounts/${encodeURIComponent(code)}`),
  createAccount: (body: { code: string; name: string; type: AccountType; coding_code?: string }) => api.post<{ code: string }>('/v1/accounting/accounts', body),
  mapAccount: (code: string, codingCode: string) => api.put<void>(`/v1/accounting/accounts/${encodeURIComponent(code)}/coding`, { coding_code: codingCode }),
  enableCoding: (full: boolean) => api.post<void>(`/v1/accounting/coding/${full ? 'iranian-full' : 'iranian'}`),
}
