import { useRef, useState, type ReactNode } from 'react'
import { fieldErrors, friendly } from '@/api/errors'
import { newIdempotencyKey } from '@/shared/idempotency'
import { Button, Field, Input, Message, Textarea, type Tone } from './index'
import { Dialog } from './Dialog'
import { Select } from './Select'
import { parseAmount, toLatin } from '@/shared/digits'
import { jalaliToIso, normalizeJalali } from '@/shared/jalali'

export interface FieldSpec {
  name: string
  label: string
  kind?: 'text' | 'amount' | 'number' | 'date' | 'textarea' | 'select' | 'latin'
  required?: boolean
  options?: { value: string; label: string }[]
  hint?: string
  placeholder?: string
  w?: 'xs' | 'sm' | 'date' | 'code' | 'amt' | 'md' | 'lg' | 'xl'
  initial?: string
}

export type Values = Record<string, string>

/** Typed readers for the string values of a FormDialog. */
export const val = {
  amount: (v: string | undefined): number => parseAmount(v ?? ''),
  int: (v: string | undefined): number => Number(toLatin(v ?? '').trim()),
  date: (v: string | undefined): string | undefined => (v?.trim() ? (normalizeJalali(v) ?? undefined) : undefined),
  iso: (v: string | undefined): string | undefined => (v?.trim() ? (jalaliToIso(v) ?? undefined) : undefined),
  text: (v: string | undefined): string => (v ?? '').trim(),
}

interface Props<R> {
  title: string
  fields: FieldSpec[]
  intro?: ReactNode
  introTone?: Tone | 'warn' | 'info'
  submitLabel: string
  danger?: boolean
  /** Receives the typed values and a fresh-per-attempt idempotency key (the same key is reused when the request is retried). */
  onSubmit(values: Values, key: string): Promise<R>
  onDone(result: R): void
  onClose(): void
  width?: number
}

interface FormModel<R> {
  values: Values
  setValue(name: string, v: string): void
  problem(f: FieldSpec): string | undefined
  server: Record<string, string>
  touched: boolean
  busy: boolean
  failure: ReturnType<typeof friendly> | null
  submit(): Promise<void>
  reset(): void
  _r?: R
}

/** Values, validation, idempotency key and submit state of a declarative form (shared by FormDialog and InlineForm). */
export function useFormModel<R>(fields: FieldSpec[], onSubmit: (values: Values, key: string) => Promise<R>, onDone: (r: R) => void): FormModel<R> {
  const initial = () => Object.fromEntries(fields.map((f) => [f.name, f.initial ?? '']))
  const [values, setValues] = useState<Values>(initial)
  const [error, setError] = useState<unknown>()
  const [busy, setBusy] = useState(false)
  const [touched, setTouched] = useState(false)
  const key = useRef<string | null>(null)

  const problem = (f: FieldSpec): string | undefined => {
    const v = (values[f.name] ?? '').trim()
    if (!v) return f.required ? 'این فیلد الزامی است.' : undefined
    if (f.kind === 'amount' && Number.isNaN(parseAmount(v))) return 'مبلغ باید عدد صحیح (ریال) باشد.'
    if (f.kind === 'number' && !/^\d+(\.\d+)?$/.test(toLatin(v))) return 'عدد معتبر وارد کنید.'
    if (f.kind === 'date' && !normalizeJalali(v)) return 'تاریخ را به شکل ۱۴۰۵/۰۸/۱۹ وارد کنید.'
    return undefined
  }
  const submit = async () => {
    setTouched(true)
    if (fields.some((f) => problem(f))) return
    setBusy(true)
    setError(undefined)
    key.current ??= newIdempotencyKey()
    try {
      const r = await onSubmit(values, key.current)
      key.current = null
      onDone(r)
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }
  return {
    values,
    setValue: (name, v) => setValues((x) => ({ ...x, [name]: v })),
    problem,
    server: fieldErrors(error),
    touched,
    busy,
    failure: error ? friendly(error) : null,
    submit,
    reset: () => (setValues(initial()), setTouched(false), setError(undefined)),
  }
}

/** The fields of a form model (labels, inputs, errors). */
export function FormFields<R>({ fields, m }: { fields: FieldSpec[]; m: FormModel<R> }) {
  return (
    <>
      {fields.map((fd, i) => (
        <Field key={fd.name} label={fd.label} required={fd.required} hint={fd.hint} error={(m.touched ? m.problem(fd) : undefined) ?? m.server[fd.name]}>
          {(id) =>
            fd.kind === 'select' ? (
              <Select id={id} label={fd.label} width={fd.w ? undefined : '100%'} options={fd.options ?? []} value={m.values[fd.name] ?? ''} onChange={(v) => m.setValue(fd.name, v)} />
            ) : fd.kind === 'textarea' ? (
              <Textarea id={id} rows={3} w="xl" value={m.values[fd.name]} onChange={(e) => m.setValue(fd.name, e.target.value)} />
            ) : (
              <Input id={id} data-autofocus={i === 0 || undefined} w={fd.w ?? 'xl'} num={fd.kind === 'amount' || fd.kind === 'number' || fd.kind === 'date' || fd.kind === 'latin'} dir={fd.kind === 'latin' ? 'ltr' : undefined} placeholder={fd.placeholder} value={m.values[fd.name]} onChange={(e) => m.setValue(fd.name, e.target.value)} />
            )
          }
        </Field>
      ))}
    </>
  )
}

/**
 * Declarative form in a dialog for the many small "operation" screens: required-field checks, amount/date validation, an idempotency
 * key per action, server field errors next to the field, friendly message for everything else.
 */
export function FormDialog<R>({ title, fields, intro, introTone = 'info', submitLabel, danger, onSubmit, onDone, onClose, width }: Props<R>) {
  const m = useFormModel(fields, onSubmit, onDone)
  return (
    <Dialog
      title={title}
      width={width}
      onClose={onClose}
      footer={
        <>
          <Button variant={danger ? 'dng' : 'pri'} disabled={m.busy} onClick={() => void m.submit()}>
            {m.busy ? 'در حال انجام…' : submitLabel}
          </Button>
          <Button onClick={onClose}>انصراف</Button>
        </>
      }
    >
      <form onSubmit={(e) => (e.preventDefault(), void m.submit())} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {intro && <Message tone={introTone === 'warn' ? 'warn' : 'info'} title="">{intro}</Message>}
        {m.failure && <Message tone={m.failure.tone} title={m.failure.title}>{m.failure.text}</Message>}
        <FormFields fields={fields} m={m} />
        <button type="submit" hidden />
      </form>
    </Dialog>
  )
}

/** The same form inline in a card (design screens that show the form on the page): fields, the server's message, a submit button. */
export function InlineForm<R>({ fields, submitLabel, onSubmit, onDone, danger, children, rows }: { fields: FieldSpec[]; submitLabel: string; onSubmit(values: Values, key: string): Promise<R>; onDone(r: R): void; danger?: boolean; children?: ReactNode; rows?: string[][] }) {
  const m = useFormModel(fields, onSubmit, (r) => (onDone(r), m.reset()))
  const byName = Object.fromEntries(fields.map((f) => [f.name, f]))
  return (
    <form onSubmit={(e) => (e.preventDefault(), void m.submit())} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
      {rows ? rows.map((r, i) => <div className="row" key={i}><FormFields fields={r.map((n) => byName[n]!)} m={m} /></div>) : <FormFields fields={fields} m={m} />}
      {children}
      {m.failure && <Message tone={m.failure.tone} title={m.failure.title}>{m.failure.text}</Message>}
      <div className="bar"><Button variant={danger ? 'dng' : 'pri'} type="submit" disabled={m.busy}>{m.busy ? 'در حال انجام…' : submitLabel}</Button></div>
    </form>
  )
}
