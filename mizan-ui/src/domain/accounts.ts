import type { AccountType, CodingNode, TrialBalanceRow } from '@/api/accounting'
import { normSearch } from '@/shared/digits'

/** Level names of the chart: group, general, subsidiary (the postable one), detail. */
export const LEVEL_NAME: Record<number, string> = { 1: 'گروه', 2: 'کل', 3: 'معین', 4: 'تفصیلی' }

export interface DirectoryEntry {
  /** what the user sees and types: the chart code, or the ledger code when the account is not in the chart */
  code: string
  name: string
  /** "دارایی‌های جاری › موجودی نقد و بانک" */
  path: string
  level: number
  levelName: string
  /** a ledger account exists, or the chart node is a subsidiary account that is created on first use */
  postable: boolean
  /** ledger account code to post to; empty when the account still has to be created */
  ledgerCode: string
  type?: AccountType
  /** nesting depth for the tree modal */
  depth: number
  parent?: string
}

/**
 * The accounts the accountant can pick from. The ledger knows accounts (code like "bank.main", optionally mapped to a chart
 * code); the chart (کدینگ) knows names and the tree. This joins them: the chart gives the names and levels, a mapped ledger
 * account makes its node postable, and every subsidiary (معین) node is postable too: its ledger account is created when first used.
 */
export function buildDirectory(nodes: CodingNode[], ledger: TrialBalanceRow[]): DirectoryEntry[] {
  const byCode = new Map(nodes.map((n) => [n.code, n]))
  const ledgerByCoding = new Map<string, TrialBalanceRow>()
  for (const r of ledger) if (r.coding_code) ledgerByCoding.set(r.coding_code, r)

  const pathOf = (n: CodingNode): string => {
    const parts: string[] = []
    for (let p = n.parent ? byCode.get(n.parent) : undefined; p; p = p.parent ? byCode.get(p.parent) : undefined) parts.unshift(p.title)
    return parts.join(' › ')
  }
  const depthOf = (n: CodingNode): number => n.level - 1

  const out: DirectoryEntry[] = nodes.map((n) => {
    const acc = ledgerByCoding.get(n.code)
    return {
      code: n.code,
      name: n.title,
      path: pathOf(n),
      level: n.level,
      levelName: LEVEL_NAME[n.level] ?? '',
      postable: !!acc || n.level === 3,
      ledgerCode: acc?.code ?? '',
      type: acc?.type ?? n.type_hint,
      depth: depthOf(n),
      parent: n.parent,
    }
  })
  // ledger accounts that are not in the chart: postable subsidiary accounts under their own name
  for (const r of ledger) {
    if (r.coding_code && byCode.has(r.coding_code)) continue
    out.push({ code: r.code, name: r.name, path: '', level: 3, levelName: LEVEL_NAME[3]!, postable: true, ledgerCode: r.code, type: r.type, depth: 0 })
  }
  return out.sort((a, b) => (a.code < b.code ? -1 : a.code > b.code ? 1 : 0))
}

/** Case/digit-insensitive search by code prefix or name; postable accounts first, at most `limit` results. */
export function searchAccounts(dir: DirectoryEntry[], q: string, limit = 8): DirectoryEntry[] {
  const s = normSearch(q)
  const hit = s ? dir.filter((a) => a.code.startsWith(s) || normSearch(a.name).includes(s)) : dir
  const sorted = [...hit].sort((a, b) => Number(b.postable) - Number(a.postable))
  return sorted.slice(0, limit)
}

export function findPostable(dir: DirectoryEntry[], code: string): DirectoryEntry | undefined {
  const c = normSearch(code)
  return dir.find((a) => a.code === c && a.postable)
}
