import { useState, type FormEvent } from 'react'
import { useAuth, useProfile } from '@/auth/AuthContext'
import { friendly } from '@/api/errors'
import { Button, Dialog, Field, Input, Message } from '@/ui'

/**
 * Shown over the page when the session ends (design "نشست منقضی"): the page, and any document being entered, stays underneath
 * so nothing is lost; signing in again just continues.
 */
export function ReloginDialog() {
  const { login, logout } = useAuth()
  const { username } = useProfile()
  const [password, setPassword] = useState('')
  const [error, setError] = useState<unknown>()
  const [busy, setBusy] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      await login(username, password)
    } catch (err) {
      setError(err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog title="نشست شما تمام شد" dismissable={false} onClose={() => undefined}>
      <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <Message tone="warn" title="برای ادامه دوباره وارد شوید">سند در دست ثبت پاک نمی‌شود.</Message>
        {error != null && <Message tone="err" title={friendly(error).title}>{friendly(error).text}</Message>}
        <Field label="گذرواژه">{(id) => <Input id={id} data-autofocus w="xl" dir="ltr" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />}</Field>
        <div style={{ display: 'flex', gap: 8 }}>
          <Button variant="pri" type="submit" disabled={busy || !password}>ورود دوباره</Button>
          <Button onClick={() => void logout()}>خروج</Button>
        </div>
      </form>
    </Dialog>
  )
}
