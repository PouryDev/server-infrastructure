import { ApiError, type ErrorBody } from './errors'

/** Where a request goes wrong with 401 the app is told, so it can show the "session expired" state without losing the page. */
type Listener = () => void
const expiredListeners = new Set<Listener>()
export const onSessionExpired = (l: Listener): (() => void) => {
  expiredListeners.add(l)
  return () => expiredListeners.delete(l)
}

export type Query = Record<string, string | number | boolean | undefined | null | (string | number)[]>

export interface RequestOptions {
  query?: Query
  body?: unknown
  /** Required by money-moving POSTs; build it with actionKey() so a retry reuses it. */
  idempotencyKey?: string
  signal?: AbortSignal
}

export function toSearch(q?: Query): string {
  if (!q) return ''
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === '') continue
    sp.set(k, Array.isArray(v) ? v.join(',') : String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

/**
 * mizan writes some amounts as plain int64 rials and others (money.Money) as {"amount":5800000,"currency":"IRR"}. The UI works with
 * whole rials only, so every Money object in a response becomes its integer amount here; requests build them with money().
 */
function reviveMoney(_key: string, v: unknown): unknown {
  if (v && typeof v === 'object' && !Array.isArray(v)) {
    const o = v as Record<string, unknown>
    const keys = Object.keys(o)
    if (keys.length === 2 && typeof o.amount === 'number' && typeof o.currency === 'string') return o.amount
  }
  return v
}

/** The wire form of an amount for request bodies (rials). */
export const money = (amount: number): { amount: number; currency: 'IRR' } => ({ amount, currency: 'IRR' })

let refreshing: Promise<boolean> | null = null
function refreshSession(): Promise<boolean> {
  refreshing ??= fetch('/auth/refresh', { method: 'POST', headers: { 'X-Mizan-UI': '1' } })
    .then((r) => r.ok)
    .catch(() => false)
    .finally(() => {
      refreshing = null
    })
  return refreshing
}

async function once(method: string, path: string, o: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (method !== 'GET') headers['X-Mizan-UI'] = '1'
  if (o.body !== undefined) headers['Content-Type'] = 'application/json'
  if (o.idempotencyKey) headers['Idempotency-Key'] = o.idempotencyKey
  try {
    return await fetch(path + toSearch(o.query), {
      method,
      headers,
      body: o.body === undefined ? undefined : JSON.stringify(o.body),
      signal: o.signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', 'network error')
  }
}

export async function request<T>(method: string, path: string, o: RequestOptions = {}): Promise<T> {
  let res = await once(method, path, o)
  if (res.status === 401 && !path.startsWith('/auth/')) {
    if (await refreshSession()) res = await once(method, path, o)
    if (res.status === 401) {
      expiredListeners.forEach((l) => l())
    }
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let json: unknown = undefined
  if (text) {
    try {
      json = JSON.parse(text, reviveMoney)
    } catch {
      /* not JSON: handled below */
    }
  }
  if (!res.ok) {
    const e = (json as ErrorBody | undefined)?.error
    throw new ApiError(res.status, e?.code ?? 'http_' + res.status, e?.message ?? res.statusText, e?.fields, e?.request_id)
  }
  return json as T
}

export const api = {
  get: <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>('GET', path, { query, signal }),
  post: <T>(path: string, body?: unknown, idempotencyKey?: string) => request<T>('POST', path, { body: body ?? {}, idempotencyKey }),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, { body: body ?? {} }),
  del: <T>(path: string) => request<T>('DELETE', path),
}

/** Raw fetch for CSV/file downloads and uploads (same credentials and CSRF header). */
export async function download(path: string, query?: Query): Promise<Blob> {
  const res = await once('GET', path, { query })
  if (!res.ok) throw new ApiError(res.status, 'http_' + res.status, res.statusText)
  return res.blob()
}
