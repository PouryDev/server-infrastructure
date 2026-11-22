import { useState } from 'react'
import { accounting, type Period } from '@/api/accounting'
import { friendly } from '@/api/errors'
import { treasury } from '@/api/treasury'
import { useFiscal } from '@/app/Fiscal'
import { fmt, toFa } from '@/shared/digits'
import { MONTHS } from '@/shared/jalali'
import { Badge, Button, Card, Dialog, ErrorMessage, LinkButton, Message, PageBar, Skeleton, useResource, useToast } from '@/ui'

const today = () => new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Tehran' }).format(new Date())

/** Fiscal years and their monthly periods; closing a period stops posting in that month. */
export default function Periods() {
  const { years, year, selectYear, reload, error } = useFiscal()
  const toast = useToast()
  const [closing, setClosing] = useState<Period | null>(null)

  const counts = useResource(
    async (signal) => {
      if (!year) return {}
      const entries = await Promise.all(
        year.periods.map(async (p) => {
          const page = await accounting.journals({ pageSize: 1, filter: { 'date:gte': p.from, 'date:lte': p.to } }, signal)
          return [p.id, page.meta.total ?? page.items.length] as const
        }),
      )
      return Object.fromEntries(entries)
    },
    [year?.id],
  )

  const toggle = async (p: Period) => {
    try {
      await accounting.setPeriod(p.id, p.status === 'open' ? 'closed' : 'open')
      toast.push({ tone: 'ok', title: p.status === 'open' ? `دورهٔ ${MONTHS[p.number - 1]} بسته شد` : `دورهٔ ${MONTHS[p.number - 1]} باز شد` })
      reload()
    } catch (e) {
      toast.error(e)
    }
    setClosing(null)
  }

  return (
    <>
      <PageBar title="سال مالی و دوره‌ها" sub="بستن دوره ثبت سند در آن ماه را متوقف می‌کند">
        <LinkButton to="/coding/new" pri>گشایش سال مالی جدید</LinkButton>
      </PageBar>
      {error != null && <ErrorMessage error={error} onRetry={reload} />}
      <div className="grid g12" style={{ alignItems: 'start' }}>
        <Card title="سال‌های مالی" bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}>
            <table className="tbl" aria-label="سال‌های مالی">
              <thead><tr><th>سال</th><th>بازه</th><th>وضعیت</th></tr></thead>
              <tbody>
                {years.map((y) => (
                  <tr key={y.id} tabIndex={0} className={y.id === year?.id ? 'sel' : undefined} onClick={() => selectYear(y.id)} onKeyDown={(e) => e.key === 'Enter' && selectYear(y.id)} style={{ cursor: 'pointer' }}>
                    <td>{toFa(y.name)}</td>
                    <td className="n">{toFa(`${y.from.slice(0, 4)}`)}</td>
                    <td>{y.status === 'open' ? <Badge tone="ok">{y.from <= today() && today() <= y.to ? 'جاری' : 'باز'}</Badge> : <Badge>بسته</Badge>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
        <Card title={`دوره‌های سال ${toFa(year?.name ?? '')}`} bare>
          {!year ? (
            <Skeleton />
          ) : (
            <div className="tw" style={{ border: 0, borderRadius: 0 }}>
              <table className="tbl" aria-label="دوره‌ها">
                <thead><tr><th>ماه</th><th>اسناد</th><th>وضعیت</th><th /></tr></thead>
                <tbody>
                  {year.periods.map((p) => {
                    const current = p.from <= today() && today() <= p.to
                    return (
                      <tr key={p.id} className={current ? 'sel' : undefined}>
                        <td>{MONTHS[p.number - 1]}</td>
                        <td className="n mut">{counts.data ? fmt(counts.data[p.id] ?? 0) : '…'}</td>
                        <td><Badge tone={p.status === 'open' ? 'ok' : ''}>{p.status === 'open' ? 'باز' : 'بسته'}</Badge></td>
                        <td><Button small onClick={() => (p.status === 'open' ? setClosing(p) : void toggle(p))}>{p.status === 'open' ? 'بستن' : 'بازکردن'}</Button></td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </div>
      {closing && <CloseDialog period={closing} onClose={() => setClosing(null)} onConfirm={() => void toggle(closing)} />}
    </>
  )
}

/** Before closing: what is still open (unmatched bank lines, bounced cheques), as the design's warning. */
function CloseDialog({ period, onClose, onConfirm }: { period: Period; onClose(): void; onConfirm(): void }) {
  const chk = useResource(async () => {
    const [lines, bounced] = await Promise.all([treasury.lines({ pageSize: 1, filter: { status: 'unmatched' } }), treasury.cheques({ pageSize: 1, filter: { status: 'bounced' } })])
    return { lines: lines.meta.total ?? lines.items.length, bounced: bounced.meta.total ?? bounced.items.length }
  }, [])
  const d = chk.data
  const f = chk.error ? friendly(chk.error) : null
  return (
    <Dialog title={`پیش از بستن ${MONTHS[period.number - 1]}`} onClose={onClose} footer={<><Button variant="dng" onClick={onConfirm}>بستن دوره</Button><Button onClick={onClose}>انصراف</Button></>}>
      {!d && !f && <Skeleton rows={2} />}
      {f && <Message tone="warn" title="بررسی موارد باز انجام نشد">{f.text}</Message>}
      {d && (d.lines > 0 || d.bounced > 0) ? (
        <Message tone="warn" title="موارد باز">
          {d.lines > 0 && <>{fmt(d.lines)} ردیف صورت‌حساب بانکی تطبیق نشده. </>}
          {d.bounced > 0 && <>{fmt(d.bounced)} چک برگشتی بی‌تکلیف است. </>}
          بستن دوره مجاز است، ولی بازکردنش نیاز به دسترسی مدیر مالی دارد.
        </Message>
      ) : d ? (
        <div>موردی باز نیست. با بستن دوره، ثبت سند در این ماه متوقف می‌شود.</div>
      ) : null}
    </Dialog>
  )
}
