import { useId, useState } from 'react'
import { searchAccounts, type DirectoryEntry } from '@/domain/accounts'
import { cx } from '@/ui'

interface Props {
  value: string
  onChange(code: string): void
  onPick(entry: DirectoryEntry): void
  onBrowse?(): void
  dir: DirectoryEntry[]
  invalid?: boolean
  label?: string
  /** forwarded so rows can find their fields for F4 */
  field?: string
  width?: number | string
}

/**
 * Account-code combobox of the journal grid: type a code or a name, the list filters, ArrowUp/Down + Enter pick, Esc closes.
 * Only postable accounts can be chosen; groups and general accounts are shown dimmed. "…" opens the tree.
 */
export function AccountCell({ value, onChange, onPick, onBrowse, dir, invalid, label = 'کد حساب', field = 'code' }: Props) {
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const listId = useId()
  const list = open ? searchAccounts(dir, value) : []

  return (
    <div style={{ position: 'relative' }}>
      <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
        <input
          className={cx('cell n', invalid && 'bad')}
          data-f={field}
          role="combobox"
          aria-expanded={open}
          aria-controls={open ? listId : undefined}
          aria-activedescendant={open && list[hi] ? `${listId}-${hi}` : undefined}
          aria-label={label}
          aria-invalid={invalid || undefined}
          autoComplete="off"
          placeholder="کد یا نام"
          value={value}
          onChange={(e) => {
            onChange(e.target.value)
            setOpen(true)
            setHi(0)
          }}
          onFocus={() => {
            setOpen(true)
            setHi(0)
          }}
          onBlur={() => setOpen(false)}
          onKeyDown={(e) => {
            // the list is recomputed from what is in the box right now, so a fast typist's Enter never acts on a stale list
            const cur = searchAccounts(dir, e.currentTarget.value)
            if (cur.length === 0) return
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setOpen(true)
              setHi(Math.min(cur.length - 1, hi + 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setHi(Math.max(0, hi - 1))
            } else if (e.key === 'Enter' && open) {
              e.preventDefault()
              const a = cur[Math.min(hi, cur.length - 1)]
              if (a?.postable) {
                onPick(a)
                setOpen(false)
              }
            } else if (e.key === 'Escape') {
              e.preventDefault()
              e.stopPropagation()
              setOpen(false)
            }
          }}
        />
        {onBrowse && (
          <button type="button" className="btn sm" style={{ padding: '0 7px' }} aria-label="مرور درخت حساب‌ها" onClick={onBrowse}>
            …
          </button>
        )}
      </div>
      {open && (
        <div
          id={listId}
          role="listbox"
          style={{ position: 'absolute', top: '100%', insetInlineStart: 4, zIndex: 6, width: 440, background: 'var(--panel)', border: '1px solid var(--line)', borderRadius: 6, boxShadow: '0 12px 30px #0004', padding: 4 }}
        >
          {list.map((a, j) => (
            <div
              key={a.code}
              id={`${listId}-${j}`}
              role="option"
              aria-selected={j === hi}
              aria-disabled={!a.postable}
              className={cx('tree-i', j === hi && 'on')}
              style={{ paddingInline: 8, cursor: a.postable ? 'pointer' : 'default', opacity: a.postable ? 1 : 0.55 }}
              onMouseDown={(e) => {
                e.preventDefault()
                if (a.postable) {
                  onPick(a)
                  setOpen(false)
                }
              }}
            >
              <span className="cd">{a.code}</span>
              <span>{a.name}</span>
              <span className="sp" />
              <span className={cx('badge', a.postable && 'ok')}>{a.levelName || 'معین'}</span>
            </div>
          ))}
          {list.length === 0 && (
            <div className="empty" style={{ padding: 12 }}>
              حسابی با این کد یا نام پیدا نشد
            </div>
          )}
          <div className="sub" style={{ padding: '6px 8px', borderTop: '1px solid var(--line2)' }}>
            بالا و پایین برای حرکت · Enter برای انتخاب · Esc برای بستن · فقط حساب معین قابل ثبت است
          </div>
        </div>
      )}
    </div>
  )
}
