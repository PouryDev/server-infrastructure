import { useMemo, useState } from 'react'
import { normSearch } from '@/shared/digits'
import type { DirectoryEntry } from '@/domain/accounts'
import { AccountTree } from '@/shared/AccountTree'
import { Dialog, Input } from '@/ui'

/** The "…" modal of the journal grid: the whole chart as a tree; only postable accounts can be chosen. */
export function AccountTreeDialog({ dir, onPick, onClose }: { dir: DirectoryEntry[]; onPick(e: DirectoryEntry): void; onClose(): void }) {
  const [q, setQ] = useState('')
  const list = useMemo(() => {
    const s = normSearch(q)
    return s ? dir.filter((a) => a.code.startsWith(s) || normSearch(a.name).includes(s)) : dir
  }, [dir, q])
  return (
    <Dialog title="انتخاب حساب" width={600} onClose={onClose}>
      <Input data-autofocus w="xl" placeholder="جستجوی کد یا نام حساب" value={q} onChange={(e) => setQ(e.target.value)} aria-label="جستجوی حساب" />
      {list.length === 0 ? (
        <div className="empty">حسابی پیدا نشد</div>
      ) : (
        <AccountTree
          dir={list}
          flat={q !== ''}
          maxHeight={380}
          onSelect={onPick}
          disabled={(a) => !a.postable}
          badge={(a) => <span className={`badge ${a.postable ? 'ok' : ''}`}>{a.levelName || 'معین'}</span>}
        />
      )}
      <div className="sub">حساب‌های «کل» فقط برای دسته‌بندی‌اند؛ روی حساب معین کلیک کنید.</div>
    </Dialog>
  )
}
