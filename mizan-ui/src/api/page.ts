import type { Query } from './http'

export interface PageMeta {
  page: number
  page_size: number
  has_next: boolean
  total?: number
  total_pages?: number
}

export interface Page<T> {
  items: T[]
  meta: PageMeta
}

export type FilterOp = 'eq' | 'in' | 'gte' | 'lte' | 'like' | 'between'

export interface ListParams {
  page?: number
  pageSize?: number
  sort?: string // "-created_at,name"
  /** "field" -> value (equality), "field:gte" -> value (operator in the key, so one field can have a range), or field -> [op, value] */
  filter?: Record<string, string | [FilterOp, string] | undefined>
}

/** Builds gokit's list syntax: filter[field][op]=v, sort=, page=, page_size=. */
export function listQuery(p: ListParams = {}, extra: Query = {}): Query {
  const q: Query = { ...extra }
  if (p.page) q.page = p.page
  if (p.pageSize) q.page_size = p.pageSize
  if (p.sort) q.sort = p.sort
  for (const [field, v] of Object.entries(p.filter ?? {})) {
    if (v === undefined || v === '') continue
    const [name, op] = field.split(':') as [string, string | undefined]
    if (Array.isArray(v)) q[`filter[${name}][${v[0]}]`] = v[1]
    else q[op ? `filter[${name}][${op}]` : `filter[${name}]`] = v
  }
  return q
}
