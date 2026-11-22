import { useMemo, useState, type ReactNode } from 'react'
import type { DirectoryEntry } from '@/domain/accounts'
import { toFa } from '@/shared/digits'

interface Props {
  dir: DirectoryEntry[]
  /** search results: a plain list, no nesting */
  flat?: boolean
  selected?: string
  onSelect(e: DirectoryEntry): void
  disabled?(e: DirectoryEntry): boolean
  badge(e: DirectoryEntry): ReactNode
  /** show codes with Persian digits */
  fa?: boolean
  maxHeight?: number
}

interface Shape {
  hasKids: boolean
  isLast: boolean
  /** cont[k]: the vertical guide of column k goes on below this row (an ancestor has later children) */
  cont: boolean[]
}

/** The shape of every row (children, last sibling, which ancestor guides continue), from the depth-first list. */
function shapes(dir: DirectoryEntry[]): Shape[] {
  const out: Shape[] = []
  const more: boolean[] = []
  for (let i = dir.length - 1; i >= 0; i--) {
    const d = dir[i]?.depth ?? 0
    const cont: boolean[] = []
    for (let k = 0; k < d; k++) cont.push(more[k + 1] ?? false)
    out.push({ hasKids: (dir[i + 1]?.depth ?? 0) > d, isLast: !(more[d] ?? false), cont })
    more[d] = true
    more.length = d + 1 // nothing deeper than this row has a later sibling beyond it
  }
  return out.reverse()
}

/** The chart of accounts as an expandable tree: chevrons, connecting guide lines and roomy rows. */
export function AccountTree({ dir, flat, selected, onSelect, disabled, badge, fa, maxHeight = 560 }: Props) {
  const shape = useMemo(() => shapes(dir), [dir])
  const [open, setOpen] = useState<Set<string>>(() => new Set(dir.filter((a) => a.depth === 0).map((a) => a.code)))

  const rows = useMemo(() => {
    const out: { e: DirectoryEntry; s: Shape }[] = []
    let skip = Infinity
    dir.forEach((e, i) => {
      const s = shape[i]
      if (!s || (!flat && e.depth > skip)) return
      skip = Infinity
      out.push({ e, s })
      if (s.hasKids && !open.has(e.code)) skip = e.depth
    })
    return out
  }, [dir, shape, open, flat])

  const toggle = (code: string) =>
    setOpen((s) => {
      const n = new Set(s)
      if (!n.delete(code)) n.add(code)
      return n
    })

  return (
    <div>
      {!flat && (
        <div className="tree-bar">
          <button type="button" className="btn sm" onClick={() => setOpen(new Set(dir.filter((_, i) => shape[i]?.hasKids).map((a) => a.code)))}>
            باز کردن همه
          </button>
          <button type="button" className="btn sm" onClick={() => setOpen(new Set())}>
            بستن همه
          </button>
        </div>
      )}
      <div role="tree" className="tree" style={{ maxHeight }}>
        {rows.map(({ e, s }) => {
          const dis = disabled?.(e) ?? false
          const isOpen = open.has(e.code)
          const d = flat ? 0 : e.depth
          return (
            <div
              key={e.code}
              role="treeitem"
              aria-level={d + 1}
              aria-selected={e.code === selected}
              aria-expanded={!flat && s.hasKids ? isOpen : undefined}
              aria-disabled={dis || undefined}
              tabIndex={dis ? -1 : 0}
              className={`tree-i${e.code === selected ? ' on' : ''}${dis ? ' dis' : ''}`}
              onClick={() => !dis && onSelect(e)}
              onKeyDown={(ev) => {
                if (ev.key === 'Enter' && !dis) onSelect(e)
                if (!flat && s.hasKids && ((ev.key === 'ArrowRight' && !document.dir.startsWith('r')) || ev.key === 'ArrowLeft' || ev.key === ' ')) {
                  ev.preventDefault()
                  toggle(e.code)
                }
              }}
            >
              {Array.from({ length: d }, (_, k) => (
                <span key={k} className={`tg${k === d - 1 ? ` e ${s.isLast ? 'half' : 'v'}` : s.cont[k] ? ' v' : ''}`} />
              ))}
              {!flat && s.hasKids ? (
                <button type="button" className="tgl" aria-label={isOpen ? 'بستن' : 'باز کردن'} onClick={(ev) => (ev.stopPropagation(), toggle(e.code))}>
                  {isOpen ? '▾' : '◂'}
                </button>
              ) : (
                <span className="tgl dot" aria-hidden="true" />
              )}
              <span className="cd">{fa ? toFa(e.code) : e.code}</span>
              <span className={s.hasKids && !flat ? 'tn grp' : 'tn'}>{e.name}</span>
              <span className="sp" />
              {badge(e)}
            </div>
          )
        })}
      </div>
    </div>
  )
}
