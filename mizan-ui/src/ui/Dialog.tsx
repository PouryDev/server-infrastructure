import { useEffect, useRef, type ReactNode } from 'react'
import { Button } from './controls'

interface Props {
  title: string
  onClose(): void
  children: ReactNode
  footer?: ReactNode
  width?: number
  /** false: only the buttons close it (confirmations that must not be dismissed by accident) */
  dismissable?: boolean
}

/** Modal dialog: scrim + .dlg, focus moves in, Tab stays inside, Esc closes, focus returns to the opener. */
export function Dialog({ title, onClose, children, footer, width, dismissable = true }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    const el = ref.current
    const focusables = () => Array.from(el?.querySelectorAll<HTMLElement>('button,input,[tabindex]:not([tabindex="-1"]),textarea,select,a[href]') ?? []).filter((x) => !x.hasAttribute('disabled'))
    ;(el?.querySelector<HTMLElement>('[data-autofocus]') ?? focusables()[1] ?? focusables()[0])?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && dismissable) {
        e.stopPropagation()
        onClose()
      } else if (e.key === 'Tab') {
        const f = focusables()
        if (f.length === 0) return
        const first = f[0]!
        const last = f[f.length - 1]!
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      opener?.focus?.()
    }
  }, [onClose, dismissable])

  return (
    <div className="scrim fixed" onMouseDown={(e) => e.target === e.currentTarget && dismissable && onClose()}>
      <div ref={ref} className="dlg" role="dialog" aria-modal="true" aria-label={title} style={width ? { width } : undefined}>
        <div className="dh">
          {title}
          <span className="sp" />
          {dismissable && (
            <Button small onClick={onClose}>
              بستن
            </Button>
          )}
        </div>
        <div className="db">{children}</div>
        {footer && <div className="df">{footer}</div>}
      </div>
    </div>
  )
}
