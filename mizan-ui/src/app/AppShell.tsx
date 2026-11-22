import { Suspense, createContext, useContext, useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useAuth, useProfile } from '@/auth/AuthContext'
import { toFa } from '@/shared/digits'
import { MONTHS } from '@/shared/jalali'
import { Badge, Button, Kbd, Select, Skeleton } from '@/ui'
import { useCurrentPeriod, useFiscal } from './Fiscal'
import { Nav } from './Nav'
import { routeFor } from './routes'
import { ReloginDialog } from '@/pages/login/ReloginDialog'

export interface Hint {
  keys: string[]
  text: string
}

const DEFAULT_HINTS: Hint[] = [
  { keys: ['Ctrl', 'F'], text: 'ردیف فیلتر' },
  { keys: ['Ctrl', 'H'], text: 'ثبت‌کنندهٔ رکورد' },
]

const StatusCtx = createContext<(h: Hint[] | null) => void>(() => undefined)

/** A page can replace the status-bar shortcut hints (the journal form lists its own). */
export function usePageHints(hints: Hint[]): void {
  const set = useContext(StatusCtx)
  useEffect(() => {
    set(hints)
    return () => set(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [set, JSON.stringify(hints)])
}

interface Tab {
  path: string
  label: string
}
const TABS_KEY = 'mizan.tabs'

function readTabs(): Tab[] {
  try {
    const v = JSON.parse(sessionStorage.getItem(TABS_KEY) ?? '[]') as Tab[]
    return Array.isArray(v) ? v : []
  } catch {
    return []
  }
}

function Tabs({ activePath, activeLabel }: { activePath: string; activeLabel: string }) {
  const [tabs, setTabs] = useState<Tab[]>(readTabs)
  const navigate = useNavigate()
  useEffect(() => {
    setTabs((t) => (t.some((x) => x.path === activePath) ? t : [...t, { path: activePath, label: activeLabel }]))
  }, [activePath, activeLabel])
  useEffect(() => {
    try {
      sessionStorage.setItem(TABS_KEY, JSON.stringify(tabs))
    } catch {
      /* ignore */
    }
  }, [tabs])
  const close = (path: string) => {
    const i = tabs.findIndex((t) => t.path === path)
    const next = tabs.filter((t) => t.path !== path)
    setTabs(next)
    if (path === activePath) navigate((next[Math.max(0, i - 1)] ?? { path: '/' }).path)
  }
  return (
    <div className="tabs" role="tablist" aria-label="صفحه‌های باز">
      {tabs.map((t) => (
        <div key={t.path} role="presentation" className={'tab' + (t.path === activePath ? ' on' : '')}>
          <Link role="tab" aria-selected={t.path === activePath} to={t.path}>
            {t.label}
          </Link>
          {tabs.length > 1 && (
            <u role="button" tabIndex={0} aria-label={`بستن ${t.label}`} onClick={() => close(t.path)} onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && close(t.path)}>
              ×
            </u>
          )}
        </div>
      ))}
    </div>
  )
}

const NEW_YEAR = '__new'

export function AppShell() {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const route = routeFor(pathname)
  const profile = useProfile()
  const { logout, state } = useAuth()
  const { years, year, selectYear } = useFiscal()
  const period = useCurrentPeriod()
  const [hints, setHints] = useState<Hint[] | null>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const setH = useMemo(() => setHints, [])

  useEffect(() => {
    document.title = `${route?.label ?? 'میزان'} · میزان`
    setMenuOpen(false)
  }, [route])

  return (
    <StatusCtx.Provider value={setH}>
      <div className="app">
        <div className="navwrap" data-open={menuOpen} style={{ flex: '0 0 216px', display: 'flex' }}>
          <Nav key={route?.key} activeKey={route?.key ?? 'Main'} />
        </div>
        <div className="main">
          <header className="top">
            <Button small className="menu-btn" aria-expanded={menuOpen} aria-label="منوی اصلی" onClick={() => setMenuOpen((o) => !o)}>
              ☰ منو
            </Button>
            <span className="chip">
              سال مالی
              <Select
                label="سال مالی"
                width="130px"
                value={String(year?.id ?? '')}
                placeholder="—"
                options={[...years.map((y) => ({ value: String(y.id), label: toFa(y.name) })), { value: NEW_YEAR, label: '＋ سال مالی جدید…' }]}
                onChange={(v) => (v === NEW_YEAR ? navigate('/periods') : selectYear(Number(v)))}
              />
            </span>
            {period && (
              <span className="chip">
                دورهٔ جاری <b>{MONTHS[period.number - 1]}</b> <Badge tone={period.status === 'open' ? 'ok' : 'warn'}>{period.status === 'open' ? 'باز' : 'بسته'}</Badge>
              </span>
            )}
            <span className="grow" />
            <span className="sub">
              {profile.name} · {profile.title}
            </span>
            <Button small onClick={() => void logout()}>
              خروج
            </Button>
          </header>
          <Tabs activePath={route?.path ?? pathname} activeLabel={route?.label ?? 'صفحه'} />
          <main className="page">
            <Suspense
              fallback={
                <div className="card">
                  <Skeleton />
                </div>
              }
            >
              <Outlet />
            </Suspense>
          </main>
          <footer className="status">
            {(hints ?? DEFAULT_HINTS).map((h) => (
              <span key={h.text} className="sc">
                {h.keys.map((k, i) => (
                  <span key={k} style={{ display: 'contents' }}>
                    {i > 0 && '+'}
                    <Kbd>{k}</Kbd>
                  </span>
                ))}{' '}
                {h.text}
              </span>
            ))}
            <span className="grow" />
            <span>متصل به سرور · نسخهٔ ۱٫۰</span>
          </footer>
        </div>
        {state.status === 'in' && state.expired && <ReloginDialog />}
      </div>
    </StatusCtx.Provider>
  )
}
