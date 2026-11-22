import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import { toFa } from '@/shared/digits'
import { BY_KEY, GROUPS } from './routes'

/**
 * The sidebar of the design: 8 collapsible groups, only the group of the active page open by default; clicking a group header toggles it.
 */
export function Nav({ activeKey }: { activeKey: string }) {
  const [open, setOpen] = useState<Record<number, boolean>>(() =>
    Object.fromEntries(GROUPS.map((g, i) => [i, g.keys.includes(activeKey)]).filter(([, o]) => o)),
  )
  return (
    <nav className="nav" aria-label="منوی اصلی">
      <div className="brand">
        <i />
        میزان
      </div>
      {GROUPS.map((g, gi) => {
        const o = !!open[gi]
        return (
          <div key={g.title}>
            <button
              className="ng"
              aria-expanded={o}
              onClick={() => setOpen({ ...open, [gi]: !o })}
              style={{ display: 'flex', width: '100%', alignItems: 'center', gap: 6, background: 'none', border: 0, cursor: 'pointer', textAlign: 'right', fontFamily: 'inherit', padding: '12px 16px 4px' }}
            >
              <span aria-hidden="true" style={{ width: 10 }}>{o ? '▾' : '◂'}</span>
              <span style={{ flex: 1 }}>{g.title}</span>
              <span style={{ color: '#7F8DA3' }}>{toFa(g.keys.length)}</span>
            </button>
            {o && (
              <div>
                {g.keys.map((k) => {
                  const r = BY_KEY.get(k)!
                  return (
                    <NavLink key={k} to={r.path} end className={k === activeKey ? 'on' : ''} aria-current={k === activeKey ? 'page' : undefined}>
                      {r.nav}
                    </NavLink>
                  )
                })}
              </div>
            )}
          </div>
        )
      })}
    </nav>
  )
}
