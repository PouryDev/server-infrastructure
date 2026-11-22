import { useEffect, useState } from 'react'
import { accounting } from '@/api/accounting'
import { buildDirectory, type DirectoryEntry } from './accounts'

let cache: Promise<DirectoryEntry[]> | null = null

/** Drop the cached directory (after creating an account or enabling a chart). */
export function invalidateDirectory(): void {
  cache = null
}

function load(): Promise<DirectoryEntry[]> {
  cache ??= Promise.all([accounting.coding(), accounting.trialBalance({ include_zero: true })])
    .then(([nodes, tb]) => buildDirectory(nodes, tb.rows))
    .catch((e) => {
      cache = null
      throw e
    })
  return cache
}

/** The picker data (chart + ledger accounts), loaded once per page load. */
export function useDirectory(): { dir: DirectoryEntry[]; loading: boolean; error: unknown; reload(): void } {
  const [state, setState] = useState<{ dir: DirectoryEntry[]; loading: boolean; error: unknown }>({ dir: [], loading: true, error: undefined })
  const [tick, setTick] = useState(0)
  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, loading: true, error: undefined }))
    load().then(
      (dir) => alive && setState({ dir, loading: false, error: undefined }),
      (error) => alive && setState({ dir: [], loading: false, error }),
    )
    return () => {
      alive = false
    }
  }, [tick])
  return { ...state, reload: () => (invalidateDirectory(), setTick((t) => t + 1)) }
}
