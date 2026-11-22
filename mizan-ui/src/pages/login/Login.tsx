import { useState, type FormEvent } from 'react'
import { api } from '@/api/http'
import { friendly, ApiError } from '@/api/errors'
import { useAuth, type LoginOptions } from '@/auth/AuthContext'
import { Button, Field, Input, Message, Select, useResource } from '@/ui'
import { toFa } from '@/shared/digits'

/** Sign-in page: username, password and the company / fiscal year (a per-tab convenience, see app/Fiscal). */
export default function Login() {
  const { login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  const opts = useResource((s) => api.get<LoginOptions>('/auth/options', undefined, s), [])
  const [yearId, setYearId] = useState('')
  const options = (opts.data?.fiscal_years ?? []).map((y) => ({ value: String(y.id), label: `${opts.data?.company} · سال مالی ${toFa(y.name)}${y.status === 'closed' ? ' (بسته)' : ''}` }))
  const chosen = yearId || options[0]?.value || ''

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      if (chosen) {
        try {
          sessionStorage.setItem('mizan.fiscalYear', chosen)
        } catch {
          /* ignore */
        }
      }
      await login(username.trim(), password)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }

  const f = error ? friendly(error) : null
  const locked = error instanceof ApiError && error.code === 'account_locked'

  return (
    <div className="app" style={{ alignItems: 'center', justifyContent: 'center', gap: 24, padding: 24 }}>
      <form className="card" style={{ width: 400, maxWidth: '100%' }} onSubmit={submit}>
        <div className="ch" style={{ minHeight: 52, fontSize: 16 }}>
          <span className="brand" style={{ padding: 0, height: 'auto', color: 'var(--tx)' }}>
            <i />
            میزان
          </span>
          <span className="sp" />
          <span className="sub">سامانهٔ حسابداری</span>
        </div>
        <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          {f && (
            <Message tone="err" title="ورود انجام نشد">
              {locked || error instanceof ApiError && error.status !== 401 ? f.text : 'نام کاربری یا گذرواژه درست نیست. پس از ۵ تلاش ناموفق، حساب برای ۱۵ دقیقه قفل می‌شود.'}
            </Message>
          )}
          <Field label="نام کاربری">{(id) => <Input id={id} w="xl" dir="ltr" autoComplete="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} />}</Field>
          <Field label="گذرواژه">{(id) => <Input id={id} w="xl" dir="ltr" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />}</Field>
          <Field label="شرکت و سال مالی">
            {(id) => <Select id={id} label="شرکت و سال مالی" width="100%" options={options} value={chosen} onChange={setYearId} placeholder={opts.loading ? 'در حال بارگذاری…' : ''} />}
          </Field>
          <Button variant="pri" type="submit" disabled={busy || !username || !password} style={{ justifyContent: 'center', height: 38 }}>
            {busy ? 'در حال ورود…' : 'ورود'}
          </Button>
          <div className="sub" style={{ textAlign: 'center' }}>نشست پس از ۳۰ دقیقه بی‌فعالیتی بسته می‌شود.</div>
        </div>
      </form>
    </div>
  )
}
