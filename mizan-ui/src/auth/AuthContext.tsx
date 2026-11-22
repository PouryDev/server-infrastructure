import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, onSessionExpired } from '@/api/http'
import { ApiError } from '@/api/errors'

export interface Profile {
  username: string
  name: string
  title: string
  company: string
  roles: string[]
}

export interface LoginOptions {
  company: string
  fiscal_years: { id: number; name: string; status: string }[]
}

type State = { status: 'loading' } | { status: 'out' } | { status: 'in'; profile: Profile; expired: boolean }

interface AuthValue {
  state: State
  login(username: string, password: string): Promise<void>
  logout(): Promise<void>
}

const Ctx = createContext<AuthValue | null>(null)

/** Session state. Tokens are httpOnly cookies set by the same-origin gateway: this code never sees or stores them. */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<State>({ status: 'loading' })

  useEffect(() => {
    let alive = true
    api
      .get<Profile>('/auth/me')
      .catch(async (e) => {
        if (e instanceof ApiError && e.status === 401) {
          // the access cookie may just have expired: one silent refresh
          try {
            await api.post('/auth/refresh')
            return await api.get<Profile>('/auth/me')
          } catch {
            return null
          }
        }
        return null
      })
      .then((p) => alive && setState(p ? { status: 'in', profile: p, expired: false } : { status: 'out' }))
    return () => {
      alive = false
    }
  }, [])

  useEffect(() => onSessionExpired(() => setState((s) => (s.status === 'in' ? { ...s, expired: true } : s))), [])

  const login = useCallback(async (username: string, password: string) => {
    const p = await api.post<Profile>('/auth/login', { username, password })
    setState({ status: 'in', profile: p, expired: false })
  }, [])

  const logout = useCallback(async () => {
    await api.post('/auth/logout').catch(() => undefined)
    setState({ status: 'out' })
  }, [])

  const value = useMemo(() => ({ state, login, logout }), [state, login, logout])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth outside AuthProvider')
  return v
}

export function useProfile(): Profile {
  const { state } = useAuth()
  if (state.status !== 'in') throw new Error('not signed in')
  return state.profile
}
