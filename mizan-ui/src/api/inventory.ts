import { api, money } from './http'
import { listQuery, type ListParams, type Page } from './page'

export interface InvItem { id: string; code: string; name: string; unit: string; method: string; quantity: number; value: number; unit_cost: number; active: boolean; fefo?: boolean }
export interface Movement { id: number; item_id: string; kind: string; quantity: number; value: number; quantity_after: number; value_after: number; date: string; date_jalali: string; ref?: string; note?: string; warehouse_id?: string }
export interface Warehouse { id: string; code: string; name: string; active: boolean }
export interface ProdOrder { id: string; code: string; product_item_id: string; planned_quantity: number; status: string; material_cost: number; labor_cost: number; fixed_overhead: number; variable_overhead: number; output_quantity: number; abnormal_loss: number; finished_cost?: number }

export const inventoryApi = {
  items: (p?: ListParams) => api.get<Page<InvItem> | InvItem[]>('/v1/inventory/items', listQuery(p)),
  addItem: (b: { code: string; name: string; unit: string; method: string; fefo?: boolean }) => api.post<InvItem>('/v1/inventory/items', b),
  movements: (id: string) => api.get<Page<Movement> | Movement[] | { items: Movement[] }>(`/v1/inventory/items/${id}/movements`),
  stock: (id: string) => api.get<{ items: { warehouse_id: string; quantity: number }[] }>(`/v1/inventory/items/${id}/stock`),
  valuation: () => api.get<{ items: InvItem[]; total_value: number }>('/v1/inventory/valuation'),
  warehouses: () => api.get<Warehouse[] | { items: Warehouse[] }>('/v1/inventory/warehouses'),
  addWarehouse: (b: { code: string; name: string }) => api.post<Warehouse>('/v1/inventory/warehouses', b),
  expiring: () => api.get<Record<string, unknown>[] | { items: Record<string, unknown>[] }>('/v1/inventory/expiring-lots'),
  receive: (b: { item_id: string; quantity: number; total_cost: number; date?: string; counter_account: string; warehouse_id?: string; lot_no?: string; expires_on?: string; ref?: string }, key: string) => api.post<unknown>('/v1/inventory/receipts', { ...b, total_cost: money(b.total_cost), vat: money(0) }, key),
  issue: (b: { item_id: string; quantity: number; date?: string; expense_account: string; warehouse_id?: string; reason?: string; ref?: string }, key: string) => api.post<unknown>('/v1/inventory/issues', b, key),
  transfer: (b: { item_id: string; from_warehouse_id: string; to_warehouse_id: string; quantity: number; date?: string; note?: string }, key: string) => api.post<unknown>('/v1/inventory/transfers', b, key),
  count: (b: { item_id: string; counted_quantity: number; date?: string; reason?: string; warehouse_id?: string }, key: string) => api.post<unknown>('/v1/inventory/counts', b, key),
  writeDown: (b: { item_id: string; net_realizable_value: number; date?: string; reason: string }, key: string) => api.post<unknown>('/v1/inventory/write-downs', { ...b, net_realizable_value: money(b.net_realizable_value) }, key),
  reverseWriteDown: (b: { item_id: string; net_realizable_value: number; date?: string; reason: string }, key: string) => api.post<unknown>('/v1/inventory/write-down-reversals', { ...b, net_realizable_value: money(b.net_realizable_value) }, key),
  orders: () => api.get<Page<ProdOrder> | ProdOrder[] | { items: ProdOrder[] }>('/v1/production/orders'),
  openOrder: (b: { code: string; product_item_id: string; planned_quantity: number }) => api.post<ProdOrder>('/v1/production/orders', b),
  material: (id: string, b: { item_id: string; quantity: number; warehouse_id?: string; date?: string }) => api.post<unknown>(`/v1/production/orders/${id}/materials`, b),
  labor: (id: string, b: { amount: number; counter_account: string; date?: string }) => api.post<unknown>(`/v1/production/orders/${id}/labor`, { ...b, amount: money(b.amount) }),
  complete: (id: string, b: { output_quantity: number; machine_units?: number; abnormal_loss?: number; date?: string; warehouse_id?: string }) => api.post<unknown>(`/v1/production/orders/${id}/complete`, b),
  overhead: (b: { amount: number; counter_account: string; ref?: string; date?: string }, key: string) => api.post<unknown>('/v1/production/overhead', { ...b, amount: money(b.amount) }, key),
  settleOverhead: (date?: string) => api.post<unknown>('/v1/production/overhead-settlements', { date }),
  wip: () => api.get<Record<string, unknown>>('/v1/production/work-in-process'),
  rates: () => api.get<Record<string, unknown>[] | { items: Record<string, unknown>[] }>('/v1/production/overhead-rates'),
}
