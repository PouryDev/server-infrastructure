import { useRef, useState } from 'react'
import { accounting, type Journal } from '@/api/accounting'
import { friendly } from '@/api/errors'
import { actionKey } from '@/shared/idempotency'
import { toFa } from '@/shared/digits'
import { Button, Dialog, Field, Input, Message } from '@/ui'

/** Confirmation to issue a reversing journal (design "تأیید سند برگشتی"). The original is never changed. */
export function ReverseDialog({ journal, onClose, onDone }: { journal: Journal; onClose(): void; onDone(j: Journal): void }) {
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  const key = useRef(actionKey('reverse'))
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    try {
      const j = await accounting.reverseJournal(journal.id, { reason: reason.trim() }, key.current.current())
      key.current.done()
      onDone(j)
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }
  const f = error ? friendly(error) : null
  return (
    <Dialog
      title={`صدور سند برگشتی برای سند ${toFa(journal.number)}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="dng" disabled={busy || !reason.trim()} onClick={() => void submit()}>
            {busy ? 'در حال صدور…' : 'صدور سند برگشتی'}
          </Button>
          <Button onClick={onClose}>انصراف</Button>
        </>
      }
    >
      <Message tone="warn" title="">
        سند اصلی تغییر نمی‌کند. یک سند جدید با ردیف‌های معکوس و تاریخ امروز ثبت می‌شود. این کار برای هر سند فقط یک بار ممکن است.
      </Message>
      {f && <Message tone={f.tone} title={f.title}>{f.text}</Message>}
      <Field label="دلیل" required>{(id) => <Input id={id} data-autofocus w="xl" value={reason} onChange={(e) => setReason(e.target.value)} />}</Field>
    </Dialog>
  )
}
