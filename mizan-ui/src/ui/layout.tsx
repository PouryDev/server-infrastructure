import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { cx } from './controls'

export function Card({ title, extra, children, bare, className }: { title?: ReactNode; extra?: ReactNode; children?: ReactNode; bare?: boolean; className?: string }) {
  return (
    <div className={cx('card', className)}>
      {title && (
        <div className="ch">
          {title}
          {extra && (
            <>
              <span className="sp" />
              {extra}
            </>
          )}
        </div>
      )}
      {bare ? children : <div className="cb">{children}</div>}
    </div>
  )
}

/** The page title row: h1 + subtitle on one side, actions on the other. */
export function PageBar({ title, sub, children }: { title: string; sub?: ReactNode; children?: ReactNode }) {
  return (
    <div className="bar">
      <div>
        <h1 className="t">{title}</h1>
        {sub && <div className="sub">{sub}</div>}
      </div>
      <span className="grow" />
      {children}
    </div>
  )
}

export function Stat({ label, value, unit, note }: { label: ReactNode; value: ReactNode; unit?: string; note?: ReactNode }) {
  return (
    <div className="card stat">
      <div className="l">{label}</div>
      <div className="v">
        {value}
        {unit && <small>{unit}</small>}
      </div>
      {note && <div className="d">{note}</div>}
    </div>
  )
}

export function KV({ rows }: { rows: [ReactNode, ReactNode][] }) {
  return (
    <dl className="kv" style={{ margin: 0 }}>
      {rows.map(([k, v], i) => (
        <KVRow key={i} k={k} v={v} />
      ))}
    </dl>
  )
}
function KVRow({ k, v }: { k: ReactNode; v: ReactNode }) {
  return (
    <>
      <dt>{k}</dt>
      <dd>{v}</dd>
    </>
  )
}

export function Steps({ steps, current }: { steps: string[]; current: number }) {
  return (
    <div className="steps" role="list">
      {steps.map((s, i) => (
        <span key={s} style={{ display: 'contents' }}>
          <span role="listitem" aria-current={i === current ? 'step' : undefined} className={cx('st', i < current && 'done', i === current && 'cur')}>
            {i < current ? '✓' : i + 1} {s}
          </span>
          {i < steps.length - 1 && <span className="ar" aria-hidden="true">‹</span>}
        </span>
      ))}
    </div>
  )
}

/** Segmented control made of buttons (filters like "همه / باز / بسته"). */
export function Seg<V extends string>({ value, options, onChange, label }: { value: V; options: { value: V; label: string }[]; onChange(v: V): void; label: string }) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => (
        <a key={o.value} role="button" tabIndex={0} aria-pressed={o.value === value} className={cx(o.value === value && 'on')} onClick={() => onChange(o.value)} onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onChange(o.value))}>
          {o.label}
        </a>
      ))}
    </div>
  )
}

export function SubTabs<V extends string>({ value, options, onChange }: { value: V; options: { value: V; label: string }[]; onChange(v: V): void }) {
  return (
    <div className="sub-tabs" role="tablist">
      {options.map((o) => (
        <a key={o.value} role="tab" tabIndex={0} aria-selected={o.value === value} className={cx(o.value === value && 'on')} onClick={() => onChange(o.value)} onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), onChange(o.value))}>
          {o.label}
        </a>
      ))}
    </div>
  )
}

export function LinkButton({ to, pri, children }: { to: string; pri?: boolean; children: ReactNode }) {
  return (
    <Link className={cx('btn', pri && 'pri')} to={to}>
      {children}
    </Link>
  )
}
