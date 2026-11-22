import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'
import { accounting, type FiscalYear } from '@/api/accounting'
import { currentPeriod, defaultYear } from '@/domain/fiscal'
import { useResource } from '@/ui'

interface FiscalValue {
  years: FiscalYear[]
  year: FiscalYear | undefined
  selectYear(id: number): void
  reload(): void
  loading: boolean
  error: unknown
}

const Ctx = createContext<FiscalValue | null>(null)
const KEY = 'mizan.fiscalYear'

function readStored(): number | undefined {
  try {
    const v = sessionStorage.getItem(KEY)
    return v ? Number(v) : undefined
  } catch {
    return undefined
  }
}

/** The fiscal years of the book and the one the accountant is working in (a per-tab convenience kept in sessionStorage). */
export function FiscalProvider({ children }: { children: ReactNode }) {
  const res = useResource(() => accounting.fiscalYears(), [])
  const [picked, setPicked] = useState<number | undefined>(readStored)
  const value = useMemo<FiscalValue>(() => {
    const years = res.data ?? []
    const year = years.find((y) => y.id === picked) ?? defaultYear(years)
    return {
      years,
      year,
      selectYear: (id) => {
        setPicked(id)
        try {
          sessionStorage.setItem(KEY, String(id))
        } catch {
          /* private mode: the choice just lasts until reload */
        }
      },
      reload: res.reload,
      loading: res.loading,
      error: res.error,
    }
  }, [res.data, res.reload, res.loading, res.error, picked])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useFiscal(): FiscalValue {
  const v = useContext(Ctx)
  if (!v) throw new Error('useFiscal outside FiscalProvider')
  return v
}

export function useCurrentPeriod() {
  const { year } = useFiscal()
  return currentPeriod(year)
}
