import { useCallback, useEffect, useRef, useState } from 'react'

export interface Resource<T> {
  data: T | undefined
  error: unknown
  loading: boolean
  reload(): void
}

/** Loads data when `deps` change (aborting the previous request) and exposes loading / error / reload for the UI states. */
export function useResource<T>(load: (signal: AbortSignal) => Promise<T>, deps: unknown[]): Resource<T> {
  const [data, setData] = useState<T>()
  const [error, setError] = useState<unknown>()
  const [loading, setLoading] = useState(true)
  const [tick, setTick] = useState(0)
  const loadRef = useRef(load)
  loadRef.current = load

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(undefined)
    loadRef
      .current(ac.signal)
      .then((d) => !ac.signal.aborted && setData(d))
      .catch((e) => !ac.signal.aborted && setError(e))
      .finally(() => !ac.signal.aborted && setLoading(false))
    return () => ac.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick])

  const reload = useCallback(() => setTick((t) => t + 1), [])
  return { data, error, loading, reload }
}
