// Per-browser conveniences only (saved descriptions, unsent drafts). Never state that must be reliable or shared.
export function readJSON<T>(key: string, fallback: T, store: 'local' | 'session' = 'local'): T {
  try {
    const s = store === 'local' ? localStorage : sessionStorage
    const v = s.getItem(key)
    return v ? (JSON.parse(v) as T) : fallback
  } catch {
    return fallback
  }
}

export function writeJSON(key: string, value: unknown, store: 'local' | 'session' = 'local'): void {
  try {
    const s = store === 'local' ? localStorage : sessionStorage
    s.setItem(key, JSON.stringify(value))
  } catch {
    /* storage blocked or full: the convenience just does not persist */
  }
}

export function remove(key: string, store: 'local' | 'session' = 'local'): void {
  try {
    ;(store === 'local' ? localStorage : sessionStorage).removeItem(key)
  } catch {
    /* ignore */
  }
}

const DESC_KEY = 'mizan.savedDescriptions'

export const savedDescriptions = {
  list: (): string[] => readJSON<string[]>(DESC_KEY, []),
  add(text: string): void {
    const t = text.trim()
    if (!t) return
    writeJSON(DESC_KEY, [t, ...savedDescriptions.list().filter((x) => x !== t)].slice(0, 30))
  },
  remove(text: string): string[] {
    const next = savedDescriptions.list().filter((x) => x !== text)
    writeJSON(DESC_KEY, next)
    return next
  },
}
