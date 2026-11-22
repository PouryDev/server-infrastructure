import { forwardRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes, useId } from 'react'

export const cx = (...c: (string | false | null | undefined)[]): string => c.filter(Boolean).join(' ')

/** Keyboard-key cap used in hints and on buttons. */
export function Kbd({ children }: { children: ReactNode }) {
  return <span className="k">{children}</span>
}

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'pri' | 'dng' | 'plain'; small?: boolean }

export const Button = forwardRef<HTMLButtonElement, BtnProps>(function Button({ variant = 'plain', small, className, type = 'button', ...p }, ref) {
  return <button ref={ref} type={type} className={cx('btn', variant === 'pri' && 'pri', variant === 'dng' && 'dng', small && 'sm', className)} {...p} />
})

type Width = 'xs' | 'sm' | 'date' | 'code' | 'amt' | 'md' | 'lg' | 'xl'

type InputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> & { w?: Width; num?: boolean; bad?: boolean; ro?: boolean }

/** Text input sized to its content (w) as in the design; num = LTR tabular digits. */
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ w, num, bad, ro, className, readOnly, ...p }, ref) {
  return <input ref={ref} className={cx('inp', w && `w-${w}`, num && 'n', bad && 'bad', ro && 'ro', className)} readOnly={readOnly ?? ro} aria-invalid={bad || undefined} {...p} />
})

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement> & { w?: Width }>(function Textarea({ w, className, ...p }, ref) {
  return <textarea ref={ref} className={cx('inp', w && `w-${w}`, className)} {...p} />
})

interface FieldProps {
  label: string
  required?: boolean
  hint?: string
  error?: string
  grow?: boolean
  children: (id: string, describedBy: string | undefined) => ReactNode
}

/** Label above the control, hint and error below. The control receives its id so the label is a real <label>. */
export function Field({ label, required, hint, error, grow, children }: FieldProps) {
  const id = useId()
  const msgId = error || hint ? `${id}-m` : undefined
  return (
    <div className="fld" style={grow ? { flex: 1, minWidth: 260 } : undefined}>
      <label htmlFor={id}>
        {label} {required && <em>*</em>}
      </label>
      {children(id, msgId)}
      {error ? (
        <span id={msgId} className="errm" role="alert">
          {error}
        </span>
      ) : hint ? (
        <span id={msgId} className="hint">
          {hint}
        </span>
      ) : null}
    </div>
  )
}

export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'pri' | ''

export function Badge({ tone = '', square, children }: { tone?: Tone; square?: boolean; children: ReactNode }) {
  return <span className={cx('badge', tone, square && 'sq')}>{children}</span>
}

export function Checkbox({ checked, onChange, label }: { checked: boolean; onChange(v: boolean): void; label: string }) {
  return (
    <button type="button" role="checkbox" aria-checked={checked} aria-label={label} className="btn link" style={{ padding: 0 }} onClick={() => onChange(!checked)}>
      <span className={cx('cb2', checked && 'on')} />
    </button>
  )
}
