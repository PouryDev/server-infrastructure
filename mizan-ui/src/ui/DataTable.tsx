import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import type { PageMeta } from '@/api/page'
import { fmt } from '@/shared/digits'
import { Button, Input, cx } from './controls'
import { Empty, ErrorMessage, Skeleton } from './feedback'

export interface Column<R> {
  id: string
  header: ReactNode
  cell(row: R, index: number): ReactNode
  /** numeric column: LTR, aligned to the end */
  num?: boolean
  /** API filter field; the column gets an input in the filter row (Ctrl+F) */
  filter?: string
  mut?: boolean
  width?: number | string
}

interface Props<R> {
  columns: Column<R>[]
  rows: R[] | undefined
  rowKey(row: R): string
  loading?: boolean
  error?: unknown
  onRetry?(): void
  /** shown when the list is empty and no filter is set */
  empty?: { title: string; text?: string; action?: ReactNode }
  filters?: Record<string, string>
  onFilters?(f: Record<string, string>): void
  meta?: PageMeta
  onPage?(page: number): void
  footer?: ReactNode
  /** Ctrl+M turns on the check column; the parent receives the selected rows */
  selectable?: boolean
  onSelection?(rows: R[]): void
  /** Ctrl+H on a focused row */
  onHistory?(row: R): void
  onRowClick?(row: R): void
  rowClass?(row: R): string | undefined
  caption: string
}

/**
 * Dense list with the design's behaviours: skeleton / error / empty states, a column-filter row (Ctrl+F), multi-select (Ctrl+M),
 * "who created it" (Ctrl+H) and pagination. Shortcuts are handled on the focused element inside the table, never globally.
 */
export function DataTable<R>({ columns, rows, rowKey, loading, error, onRetry, empty, filters = {}, onFilters, meta, onPage, footer, selectable, onSelection, onHistory, onRowClick, rowClass, caption }: Props<R>) {
  const [showFilter, setShowFilter] = useState(false)
  const [multi, setMulti] = useState(false)
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [active, setActive] = useState<string>()
  const hasFilters = columns.some((c) => c.filter)
  const filtering = Object.values(filters).some(Boolean)

  const onSelRef = useRef(onSelection)
  onSelRef.current = onSelection
  useEffect(() => {
    onSelRef.current?.((rows ?? []).filter((r) => sel.has(rowKey(r))))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sel, rows])

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.ctrlKey && !e.shiftKey && !e.altKey && (e.key === 'f' || e.key === 'F') && hasFilters) {
      e.preventDefault()
      setShowFilter((v) => !v)
    } else if (e.ctrlKey && !e.altKey && (e.key === 'm' || e.key === 'M') && selectable) {
      e.preventDefault()
      setMulti((v) => !v)
      setSel(new Set())
    } else if (e.ctrlKey && !e.altKey && (e.key === 'h' || e.key === 'H') && onHistory) {
      const row = (rows ?? []).find((r) => rowKey(r) === active)
      if (row) {
        e.preventDefault()
        onHistory(row)
      }
    }
  }

  const toggle = (k: string, on: boolean) =>
    setSel((s) => {
      const n = new Set(s)
      if (on) n.add(k)
      else n.delete(k)
      return n
    })

  if (error) return <ErrorMessage error={error} onRetry={onRetry} />
  if (loading && !rows) {
    return (
      <div className="tw">
        <Skeleton rows={5} />
      </div>
    )
  }

  const empty0 = (rows ?? []).length === 0
  return (
    <div onKeyDown={onKeyDown}>
      <div className="tw" aria-busy={loading || undefined}>
        <table className="tbl" aria-label={caption}>
          <thead>
            <tr>
              {multi && <th className="ck" scope="col" />}
              {columns.map((c) => (
                <th key={c.id} scope="col" className={cx(c.num && 'n')} style={c.width ? { width: c.width } : undefined}>
                  {c.header}
                </th>
              ))}
            </tr>
            {showFilter && hasFilters && (
              <tr className="flt">
                {multi && <th className="ck" />}
                {columns.map((c) => (
                  <th key={c.id}>
                    {c.filter && (
                      <Input
                        aria-label={`فیلتر ${typeof c.header === 'string' ? c.header : c.id}`}
                        defaultValue={filters[c.filter] ?? ''}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') onFilters?.({ ...filters, [c.filter!]: (e.target as HTMLInputElement).value })
                        }}
                        onBlur={(e) => e.target.value !== (filters[c.filter!] ?? '') && onFilters?.({ ...filters, [c.filter!]: e.target.value })}
                      />
                    )}
                  </th>
                ))}
              </tr>
            )}
          </thead>
          <tbody>
            {(rows ?? []).map((r, i) => {
              const k = rowKey(r)
              return (
                <tr
                  key={k}
                  tabIndex={0}
                  className={cx(sel.has(k) && 'sel', rowClass?.(r))}
                  onFocus={() => setActive(k)}
                  onClick={onRowClick ? () => onRowClick(r) : undefined}
                  onKeyDown={(e) => e.key === 'Enter' && e.target === e.currentTarget && onRowClick?.(r)}
                  style={onRowClick ? { cursor: 'pointer' } : undefined}
                >
                  {multi && (
                    <td className="ck">
                      <input type="checkbox" aria-label="انتخاب ردیف" checked={sel.has(k)} onChange={(e) => toggle(k, e.target.checked)} onClick={(e) => e.stopPropagation()} />
                    </td>
                  )}
                  {columns.map((c) => (
                    <td key={c.id} className={cx(c.num && 'n', c.mut && 'mut')}>
                      {c.cell(r, i)}
                    </td>
                  ))}
                </tr>
              )
            })}
          </tbody>
        </table>
        {empty0 && !loading && (
          <Empty
            title={filtering ? 'نتیجه‌ای پیدا نشد' : (empty?.title ?? 'موردی ثبت نشده')}
            action={filtering ? <Button onClick={() => onFilters?.({})}>پاک‌کردن فیلترها</Button> : empty?.action}
          >
            {filtering ? 'فیلترها را تغییر دهید یا پاک کنید.' : empty?.text}
          </Empty>
        )}
      </div>
      {(meta || footer) && (
        <div className="bar" style={{ marginTop: 8 }}>
          {meta && (
            <>
              <Button small disabled={meta.page <= 1} onClick={() => onPage?.(meta.page - 1)}>
                قبلی
              </Button>
              <span className="sub">
                صفحهٔ {fmt(meta.page)}
                {meta.total_pages ? ` از ${fmt(meta.total_pages)}` : ''}
                {meta.total != null ? ` · ${fmt(meta.total)} مورد` : ''}
              </span>
              <Button small disabled={!meta.has_next} onClick={() => onPage?.(meta.page + 1)}>
                بعدی
              </Button>
            </>
          )}
          <span className="grow" />
          {footer}
          {multi && <span className="sub">{fmt(sel.size)} ردیف انتخاب شده · Ctrl+M برای خروج از انتخاب چندگانه</span>}
        </div>
      )}
    </div>
  )
}
