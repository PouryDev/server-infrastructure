import { useId, useMemo, useRef, useState } from 'react'
import { normSearch } from '@/shared/digits'
import { cx } from './controls'

export interface Option<V extends string = string> {
  value: V
  label: string
}

interface Props<V extends string> {
  options: Option<V>[]
  value: V | ''
  onChange(v: V): void
  label: string
  /** CSS width: "160px", "100%" */
  width?: string
  placeholder?: string
  disabled?: boolean
  id?: string
  className?: string
}

/**
 * Searchable select (no native <select>): type to filter, ArrowUp/Down to move, Enter to pick, Esc to close.
 * Digits are matched in Persian or Latin form.
 */
export function Select<V extends string = string>({ options, value, onChange, label, width = '200px', placeholder, disabled, id, className }: Props<V>) {
  const [open, setOpen] = useState(false)
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)

  const current = options.find((o) => o.value === value)
  const shown = useMemo(() => {
    const s = normSearch(q)
    return s ? options.filter((o) => normSearch(o.label).includes(s)) : options
  }, [options, q])

  const pick = (o: Option<V>) => {
    onChange(o.value)
    setOpen(false)
    setQ('')
    setHi(0)
  }

  return (
    <div className={cx('rel', className)} style={{ width, fontSize: 13 }}>
      <input
        ref={inputRef}
        id={id}
        className="inp"
        role="combobox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && shown[hi] ? `${listId}-${hi}` : undefined}
        aria-label={label}
        aria-autocomplete="list"
        autoComplete="off"
        disabled={disabled}
        style={{ cursor: 'pointer', paddingInlineEnd: 28 }}
        value={open ? q : (current?.label ?? '')}
        placeholder={open ? (current?.label ?? '') : (placeholder ?? '')}
        onFocus={() => {
          setOpen(true)
          setQ('')
          setHi(Math.max(0, options.findIndex((o) => o.value === value)))
        }}
        onBlur={() => {
          setOpen(false)
          setQ('')
        }}
        onChange={(e) => {
          setQ(e.target.value)
          setOpen(true)
          setHi(0)
        }}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            setOpen(true)
            setHi(Math.min(shown.length - 1, hi + 1))
          } else if (e.key === 'ArrowUp') {
            e.preventDefault()
            setHi(Math.max(0, hi - 1))
          } else if (e.key === 'Enter') {
            e.preventDefault()
            const o = shown[hi]
            if (open && o) pick(o)
          } else if (e.key === 'Escape') {
            e.preventDefault()
            e.stopPropagation()
            setOpen(false)
            setQ('')
          }
        }}
      />
      <span aria-hidden="true" style={{ position: 'absolute', insetInlineEnd: 10, top: 7, pointerEvents: 'none', color: 'var(--faint)', fontSize: 11 }}>
        ▾
      </span>
      {open && (
        <div
          id={listId}
          role="listbox"
          aria-label={label}
          style={{
            position: 'absolute', top: '100%', insetInlineStart: 0, marginTop: 2, zIndex: 30, minWidth: '100%', maxHeight: 240, overflow: 'auto',
            background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 6, boxShadow: '0 12px 30px #0004', padding: 4, color: 'var(--tx)',
          }}
        >
          {shown.map((o, j) => (
            <div
              key={o.value}
              id={`${listId}-${j}`}
              role="option"
              aria-selected={o.value === value}
              className={cx('tree-i', j === hi && 'on')}
              style={{ paddingInline: 8, cursor: 'pointer' }}
              onMouseDown={(e) => {
                e.preventDefault()
                pick(o)
              }}
            >
              <span style={{ width: 14, color: 'var(--pri)' }}>{o.value === value ? '✓' : ''}</span>
              <span>{o.label}</span>
            </div>
          ))}
          {shown.length === 0 && (
            <div className="empty" style={{ padding: 10 }}>
              موردی پیدا نشد
            </div>
          )}
        </div>
      )}
    </div>
  )
}
