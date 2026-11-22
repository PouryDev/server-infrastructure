import { api, money } from './http'
import { ApiError } from './errors'
import { listQuery, type ListParams, type Page } from './page'

export interface BankAccount {
  id: string
  name: string
  bank_name?: string
  iban?: string
  account_no?: string
  currency: string
  ledger_code: string
  active: boolean
  balance?: number
}

export interface CashPosition {
  accounts: BankAccount[]
  banks: number
  cheques: { direction: 'receivable' | 'payable'; status: string; count: number; total: number }[]
  cheques_in: number
  cheques_out: number
  gateway_pending: number
  projected: number
}

export type ChequeStatus = 'in_hand' | 'issued' | 'deposited' | 'cleared' | 'bounced' | 'returned' | 'endorsed'

export interface Cheque {
  id: string
  direction: 'receivable' | 'payable'
  status: ChequeStatus
  sayad_id?: string
  number: string
  amount: number
  due_date: string
  due_date_jalali: string
  counterparty_ref: string
  drawer_bank?: string
  note?: string
  bank_account_id?: string
  reason?: string
  created_at: string
}

export interface StatementLine {
  id: string
  bank_account_id: string
  date: string
  date_jalali: string
  amount: number
  description?: string
  reference?: string
  balance?: number
  status: 'unmatched' | 'matched' | 'ignored'
  match_kind?: string
  match_id?: string
  matched_by?: string
  journal_id?: string
}

export const treasury = {
  position: () => api.get<CashPosition>('/v1/treasury/position'),
  cheques: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Cheque>>('/v1/cheques', listQuery(p), signal),
  lines: (p?: ListParams, signal?: AbortSignal) => api.get<Page<StatementLine>>('/v1/statement-lines', listQuery(p), signal),
  bankAccounts: () => api.get<{ items: BankAccount[] } | BankAccount[]>('/v1/bank-accounts'),
  openBank: (b: { name: string; bank_name?: string; iban?: string; account_no?: string; ledger_code?: string }) => api.post<BankAccount>('/v1/bank-accounts', b),
  importStatement: async (accountId: string, file: File) => {
    const res = await fetch(`/v1/bank-accounts/${accountId}/statements`, { method: 'POST', headers: { 'Content-Type': file.name.toLowerCase().endsWith('.json') ? 'application/json' : 'text/csv', 'X-Mizan-UI': '1' }, body: await file.text() })
    const json = (await res.json().catch(() => ({}))) as Record<string, unknown> & { error?: { code: string; message: string; fields?: { field: string; message: string }[] } }
    if (!res.ok) throw new ApiError(res.status, json.error?.code ?? 'http_' + res.status, json.error?.message ?? res.statusText, json.error?.fields)
    return json
  },
  autoMatch: (accountId: string) => api.post<Record<string, number>>(`/v1/bank-accounts/${accountId}/auto-match`),
  registerCheque: (b: { direction: 'receivable' | 'payable'; sayad_id?: string; number: string; amount: number; due_date: string; counterparty_ref: string; drawer_bank?: string; bank_account_id?: string; counter_account?: string }, key: string) =>
    api.post<Cheque>('/v1/cheques', { ...b, amount: money(b.amount) }, key),
  deposit: (id: string, bankAccountId: string, date?: string) => api.post<Cheque>(`/v1/cheques/${id}/deposit`, { bank_account_id: bankAccountId, date }),
  clear: (id: string, date?: string) => api.post<Cheque>(`/v1/cheques/${id}/clear`, { date }),
  bounce: (id: string, reason: string, fee?: number, date?: string) => api.post<Cheque>(`/v1/cheques/${id}/bounce`, { reason, fee: fee ? money(fee) : undefined, date }),
  returnCheque: (id: string, reason: string) => api.post<Cheque>(`/v1/cheques/${id}/return`, { reason }),
  endorse: (id: string, to: string, counterAccount: string, date?: string) => api.post<Cheque>(`/v1/cheques/${id}/endorse`, { to, counter_account: counterAccount, date }),
  takeBack: (id: string, reason: string) => api.post<Cheque>(`/v1/cheques/${id}/take-back`, { reason }),
  suggestions: (lineId: string) => api.get<{ kind: string; item_id: string; high_confidence: boolean; reason: string }[]>(`/v1/statement-lines/${lineId}/suggestions`),
  match: (lineId: string, b: { kind: string; item_id?: string; note?: string }) => api.post<unknown>(`/v1/statement-lines/${lineId}/match`, b),
  settlements: (p?: ListParams) => api.get<Page<Settlement>>('/v1/gateway-settlements', listQuery(p)),
  recordSettlement: (b: { gateway: string; ref: string; bank_account_id: string; gross: number; fee: number; settled_on?: string; note?: string }, key: string) =>
    api.post<Settlement>('/v1/gateway-settlements', { ...b, gross: money(b.gross), fee: money(b.fee) }, key),
}

export interface Settlement {
  id: string
  gateway: string
  ref: string
  bank_account_id: string
  gross: number
  fee: number
  net: number
  settled_on: string
  note?: string
  journal_id: string
}
