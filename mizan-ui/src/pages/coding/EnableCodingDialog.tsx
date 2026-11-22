import { useState } from 'react'
import { accounting } from '@/api/accounting'
import { friendly } from '@/api/errors'
import { Button, Dialog, Message, Seg } from '@/ui'

/** "فعال‌سازی کدینگ نمونه": choose the small template or the full chart, with the design's warning. */
export function EnableCodingDialog({ onClose, onDone }: { onClose(): void; onDone(): void }) {
  const [full, setFull] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<unknown>()
  const run = async () => {
    setBusy(true)
    try {
      await accounting.enableCoding(full)
      onDone()
    } catch (e) {
      setError(e)
    } finally {
      setBusy(false)
    }
  }
  const f = error ? friendly(error) : null
  return (
    <Dialog
      title="فعال‌سازی کدینگ نمونه"
      onClose={onClose}
      footer={
        <>
          <Button variant="pri" disabled={busy} onClick={() => void run()}>فعال‌سازی</Button>
          <Button onClick={onClose}>انصراف</Button>
        </>
      }
    >
      <Seg<string> label="الگو" value={full ? 'full' : 'small'} onChange={(v) => setFull(v === 'full')} options={[{ value: 'small', label: 'الگوی کوچک' }, { value: 'full', label: 'الگوی کامل (۲۹۰ معین)' }]} />
      <Message tone="warn" title="این یک نمونه است، نه استاندارد قانونی">
        کدینگ رسمی اجباری پیدا نشد. حساب‌های سیستمی به این کدینگ نگاشت می‌شوند (مثلاً بانک به ۱۱۱۰۰۵) و پیشنهادی هستند؛ بازبینی کنید. همیشه یکی را انتخاب کنید.
      </Message>
      {f && <Message tone={f.tone} title={f.title}>{f.text}</Message>}
    </Dialog>
  )
}
