import type { ReactNode } from 'react'

/** The design's list + detail-card row: the list takes the space, the card wraps under it on narrow screens. */
export function MasterDetail({ list, detail, listBasis = 520, detailBasis = 400 }: { list: ReactNode; detail?: ReactNode; listBasis?: number; detailBasis?: number }) {
  return (
    <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap' }}>
      <div style={{ flex: `999 1 ${listBasis}px`, minWidth: 0 }}>{list}</div>
      {detail && (
        <div className="card" style={{ flex: `1 1 ${detailBasis}px`, minWidth: 340 }}>
          {detail}
        </div>
      )}
    </div>
  )
}
