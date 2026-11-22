import { useCallback, useState } from 'react'

/** Page + column filters of a list; changing a filter goes back to page 1. */
export function useListState(initialFilters: Record<string, string> = {}, pageSize = 20) {
  const [page, setPage] = useState(1)
  const [filters, setFiltersState] = useState<Record<string, string>>(initialFilters)
  const setFilters = useCallback((f: Record<string, string>) => {
    setFiltersState(f)
    setPage(1)
  }, [])
  return { page, setPage, filters, setFilters, pageSize }
}
