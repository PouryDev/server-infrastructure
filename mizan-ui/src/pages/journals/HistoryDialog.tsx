import { accounting, type JournalSummary } from '@/api/accounting'
import { refKey } from '@/domain/journalView'
import { toFa } from '@/shared/digits'
import { showDateTime } from '@/shared/jalali'
import { Dialog, ErrorMessage, KV, Skeleton, useResource } from '@/ui'

/** Ctrl+H: who created the document, and whether it was reversed. mizan has no document-approval step, so that row says so. */
export function HistoryDialog({ journal, onClose }: { journal: JournalSummary; onClose(): void }) {
  const rev = useResource(async () => {
    const page = await accounting.journals({ pageSize: 50, filter: { ref_type: journal.ref_type, ref_id: journal.ref_id, kind: 'reversal' } })
    return page.items.find((j) => refKey(j) === refKey(journal))
  }, [journal.id])
  return (
    <Dialog title={`تاریخچهٔ سند ${toFa(journal.number)}`} onClose={onClose}>
      {rev.error != null ? (
        <ErrorMessage error={rev.error} onRetry={rev.reload} />
      ) : rev.loading ? (
        <Skeleton rows={3} />
      ) : (
        <KV
          rows={[
            ['ثبت', `${journal.created_by || '—'} · ${showDateTime(journal.created_at)}`],
            ['تأیید', 'برای اسناد حسابداری مرحلهٔ تأیید وجود ندارد'],
            ['برگشت', rev.data && rev.data.id !== journal.id ? `سند ${toFa(rev.data.number)} · ${rev.data.created_by} · ${showDateTime(rev.data.created_at)}` : '—'],
          ]}
        />
      )}
    </Dialog>
  )
}
