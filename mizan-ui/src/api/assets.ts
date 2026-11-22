import { api, money } from './http'
import { listQuery, type ListParams, type Page } from './page'

export interface AssetCategory { id: string; code: string; name: string; method: string; life_months?: number; rate_bp?: number; salvage_bp: number; source: string; cost_account: string; accumulated_depreciation_account: string; active: boolean; idle_exempt: boolean }
export interface Asset { id: string; code: string; name: string; category_id: string; acquired_on: string; in_service_on: string; cost: number; salvage: number; method: string; life_months?: number; rate_bp?: number; accumulated_depreciation: number; book_value: number; status: string; disposed_on?: string }
export interface DepRun { jalali_year: number; jalali_month: number; journal_id: string; total: number; items: { asset_id: string; asset_code: string; amount: number }[] }
export interface IdleSpan { id: string; from_jalali_year: number; from_jalali_month: number; to_jalali_year: number; to_jalali_month: number; note?: string }

export const assetApi = {
  categories: () => api.get<AssetCategory[] | { items: AssetCategory[] }>('/v1/fixed-assets/categories'),
  addCategory: (b: { code: string; name: string; method: string; life_months?: number; rate_bp?: number; salvage_bp?: number; source: string }) => api.post<AssetCategory>('/v1/fixed-assets/categories', b),
  setCategoryState: (id: string, state: 'activate' | 'deactivate') => api.post<unknown>(`/v1/fixed-assets/categories/${id}/${state}`),
  legalTable: () => api.get<{ items: { row_id: string; name: string }[] } | { row_id: string; name: string }[]>('/v1/fixed-assets/legal-table'),
  fromLegal: (b: { row_id: string; code: string; name: string }) => api.post<AssetCategory>('/v1/fixed-assets/legal-table-categories', b),
  list: (p?: ListParams, signal?: AbortSignal) => api.get<Page<Asset> | Asset[]>('/v1/fixed-assets/assets', listQuery(p), signal),
  acquire: (b: { code: string; name: string; category_id: string; acquired_on: string; in_service_on?: string; cost: number; salvage?: number; paid_from: string }, key: string) =>
    api.post<Asset>('/v1/fixed-assets/assets', { ...b, cost: money(b.cost), vat: money(0), salvage: b.salvage === undefined ? undefined : money(b.salvage) }, key),
  schedule: (id: string) => api.get<{ items: { jalali_year: number; jalali_month: number; amount: number; accumulated: number; book_value: number }[] }>(`/v1/fixed-assets/assets/${id}/schedule`),
  history: (id: string) => api.get<{ items: { jalali_year: number; jalali_month: number; amount: number }[] }>(`/v1/fixed-assets/assets/${id}/history`),
  dispose: (id: string, b: { date: string; proceeds: number; proceeds_account: string }, key: string) => api.post<unknown>(`/v1/fixed-assets/assets/${id}/dispose`, { ...b, proceeds: money(b.proceeds) }, key),
  partialDispose: (id: string, b: { portion_bp: number; date: string; proceeds: number; proceeds_account: string }, key: string) => api.post<unknown>(`/v1/fixed-assets/assets/${id}/partial-disposal`, { ...b, proceeds: money(b.proceeds) }, key),
  usage: (id: string, b: { jalali_year: number; jalali_month: number; units: number }) => api.post<unknown>(`/v1/fixed-assets/assets/${id}/usage`, b),
  idleSpans: (id: string) => api.get<IdleSpan[] | { items: IdleSpan[] }>(`/v1/fixed-assets/assets/${id}/idle-spans`),
  addIdle: (id: string, b: { from_jalali_year: number; from_jalali_month: number; to_jalali_year: number; to_jalali_month: number; note?: string }) => api.post<IdleSpan>(`/v1/fixed-assets/assets/${id}/idle-spans`, b),
  removeIdle: (id: string, span: string) => api.del<void>(`/v1/fixed-assets/assets/${id}/idle-spans/${span}`),
  run: (year: number, month: number, key: string) => api.post<DepRun>('/v1/fixed-assets/depreciation-runs', { jalali_year: year, jalali_month: month }, key),
  voidRun: (year: number, month: number, key: string) => api.post<unknown>('/v1/fixed-assets/depreciation-runs/void', { jalali_year: year, jalali_month: month }, key),
  thresholds: () => api.get<{ items: Record<string, unknown>[] } | Record<string, unknown>[]>('/v1/fixed-assets/thresholds'),
  addThreshold: (b: { effective_from: string; small_limit: number; verified: boolean; source: string }) => api.post<unknown>('/v1/fixed-assets/thresholds', b),
  expenseLimit: () => api.get<Record<string, unknown>>('/v1/fixed-assets/expense-limit'),
  expenseSmall: (b: { name: string; date: string; cost: number; paid_from: string }, key: string) => api.post<unknown>('/v1/fixed-assets/small-assets', { ...b, cost: money(b.cost), vat: money(0) }, key),
}
