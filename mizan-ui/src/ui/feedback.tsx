import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'
import { friendly } from '@/api/errors'
import { Button, cx } from './controls'

export type MsgTone = 'err' | 'warn' | 'ok' | 'info'

/** Inline message: bold title + text (the design's .msg). */
export function Message({ tone, title, children, action, role }: { tone: MsgTone; title: string; children?: ReactNode; action?: ReactNode; role?: 'alert' | 'status' }) {
  return (
    <div className={cx('msg', tone)} role={role ?? (tone === 'err' ? 'alert' : 'status')}>
      <div>
        <b>{title}</b>
        {children && <span className="tx">{children}</span>}
        {action && <div style={{ marginTop: 8 }}>{action}</div>}
      </div>
    </div>
  )
}

/** A thrown API error rendered as its friendly Persian message, the machine code only under "details". */
export function ErrorMessage({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const f = friendly(error)
  return (
    <Message tone={f.tone} title={f.title} action={onRetry && <Button small onClick={onRetry}>تلاش مجدد</Button>}>
      {f.text}
      {f.details && (
        <details style={{ marginTop: 4 }}>
          <summary className="sub" style={{ cursor: 'pointer' }}>جزئیات</summary>
          <span className="sub n" dir="ltr">{f.details}</span>
        </details>
      )}
    </Message>
  )
}

export function Empty({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <b>{title}</b>
      {children && <span>{children}</span>}
      {action}
    </div>
  )
}

export function Skeleton({ rows = 4 }: { rows?: number }) {
  const widths = ['60%', '100%', '80%', '100%', '70%']
  return (
    <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }} role="status" aria-label="در حال بارگذاری">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skel" style={{ width: widths[i % widths.length] }} />
      ))}
    </div>
  )
}

interface Toast {
  id: number
  tone: MsgTone
  title: string
  text?: string
}

interface ToastApi {
  push(t: Omit<Toast, 'id'>): void
  error(e: unknown): void
}

const ToastCtx = createContext<ToastApi | null>(null)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const push = useCallback((t: Omit<Toast, 'id'>) => {
    const id = Date.now() + Math.random()
    setItems((x) => [...x, { ...t, id }])
    setTimeout(() => setItems((x) => x.filter((i) => i.id !== id)), t.tone === 'err' ? 9000 : 5000)
  }, [])
  const api = useMemo<ToastApi>(
    () => ({
      push,
      error: (e) => {
        const f = friendly(e)
        push({ tone: f.tone, title: f.title, text: f.text })
      },
    }),
    [push],
  )
  return (
    <ToastCtx.Provider value={api}>
      {children}
      <div className="toasts" aria-live="polite">
        {items.map((t) => (
          <Message key={t.id} tone={t.tone} title={t.title}>
            {t.text}
          </Message>
        ))}
      </div>
    </ToastCtx.Provider>
  )
}

export function useToast(): ToastApi {
  const v = useContext(ToastCtx)
  if (!v) throw new Error('useToast outside ToastProvider')
  return v
}

/** The "not available" state for design screens whose backing API does not exist (never fake data). */
export function NotAvailable({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="msg warn unavailable" role="note">
      <div>
        <b>{title}</b>
        <span className="tx">{children ?? 'برای این بخش هنوز API در mizan وجود ندارد؛ تا افزودن آن، این بخش غیرفعال است.'}</span>
      </div>
    </div>
  )
}
