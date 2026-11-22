import type { ReactNode } from 'react'
import { useMemo } from 'react'
import { Link } from 'react-router-dom'
import { accounting } from '@/api/accounting'
import { approvals } from '@/api/approvals'
import { payments } from '@/api/payments'
import { treasury } from '@/api/treasury'
import { vat } from '@/api/vat'
import { useFiscal } from '@/app/Fiscal'
import { buildTasks, type Task } from '@/domain/tasks'
import { fmt, toFa } from '@/shared/digits'
import { jalaliToday, MONTHS, showDate, showDateTime } from '@/shared/jalali'
import { useDirectory } from '@/domain/useDirectory'
import { Badge, Card, ErrorMessage, KV, LinkButton, PageBar, Skeleton, Stat, useResource } from '@/ui'

const settled = async <T,>(p: Promise<T>): Promise<{ ok: true; v: T } | { ok: false; e: unknown }> => p.then((v) => ({ ok: true as const, v }), (e: unknown) => ({ ok: false as const, e }))

/** Dashboard: every block loads on its own, so one failing endpoint never blanks the page. */
export default function Dashboard() {
  const { year } = useFiscal()
  const { dir } = useDirectory()
  const data = useResource(
    async () => {
      const [pos, vp, unsettled, pending, journals, bounced, refunds, lines] = await Promise.all([
        settled(treasury.position()),
        settled(vat.position()),
        settled(payments.unsettled()),
        settled(approvals.list('pending')),
        settled(accounting.journals({ pageSize: 4 })),
        settled(treasury.cheques({ pageSize: 20, filter: { status: 'bounced' } })),
        settled(payments.refunds({ pageSize: 20, filter: { status: ['in', 'pending,processing'], method: 'manual_card_to_card' } })),
        settled(treasury.lines({ pageSize: 1, filter: { status: 'unmatched' } })),
      ])
      return { pos, vp, unsettled, pending, journals, bounced, refunds, lines }
    },
    [],
  )
  const d = data.data
  const tasks: Task[] = useMemo(
    () =>
      d
        ? buildTasks({
            approvals: d.pending.ok ? d.pending.v.items : [],
            bounced: d.bounced.ok ? d.bounced.v.items : [],
            refunds: d.refunds.ok ? d.refunds.v.items : [],
            unmatchedLines: d.lines.ok ? (d.lines.v.meta.total ?? d.lines.v.items.length) : 0,
            now: Date.now(),
          })
        : [],
    [d],
  )

  const pos = d?.pos.ok ? d.pos.v : undefined
  const unsettled = d?.unsettled.ok ? d.unsettled.v : undefined
  const pending = d?.pending.ok ? d.pending.v.items : undefined
  const oldest = pending?.length ? Math.max(...pending.map((p) => Math.floor((Date.now() - new Date(p.created_at).getTime()) / 864e5))) : 0

  return (
    <>
      <PageBar title="داشبورد" sub={`امروز ${showDate(jalaliToday())} · آخرین به‌روزرسانی ${showDateTime(new Date().toISOString()).split(' ')[1]}`}>
        <LinkButton to="/invoices/new">فاکتور جدید</LinkButton>
        <LinkButton to="/journals/new" pri>
          سند جدید
        </LinkButton>
      </PageBar>

      <div className="grid g4">
        {d ? (
          <>
            <Stat label="موجودی نقد و بانک" value={pos ? fmt(pos.banks) : '—'} unit="ریال" note={pos ? `پس از چک‌های وصول‌نشده: ${fmt(pos.projected)}` : 'در دسترس نیست'} />
            <Stat label="مالیات بر ارزش افزوده پرداختنی" value={d.vp.ok ? fmt(d.vp.v.owed) : '—'} unit="ریال" note={d.vp.ok && d.vp.v.credit > 0 ? `اعتبار مالیاتی: ${fmt(d.vp.v.credit)}` : undefined} />
            <Stat label="پرداخت‌های تسویه‌نشده" value={unsettled ? fmt(unsettled.length) : '—'} unit="مورد" note={unsettled ? `جمع ${fmt(unsettled.reduce((s, p) => s + p.amount, 0))} ریال` : undefined} />
            <Stat label="در انتظار تأیید شما" value={pending ? fmt(pending.length) : '—'} unit="درخواست" note={pending?.length ? `قدیمی‌ترین: ${fmt(oldest)} روز` : undefined} />
          </>
        ) : (
          [0, 1, 2, 3].map((i) => (
            <div className="card" key={i}>
              <Skeleton rows={3} />
            </div>
          ))
        )}
      </div>
      {data.error != null && <ErrorMessage error={data.error} onRetry={data.reload} />}

      <div className="grid g21">
        <Card title="کارتابل: نیازمند اقدام" extra={<span className="sub">{fmt(tasks.length)} مورد</span>} bare>
          <div className="tw" style={{ border: 0, borderRadius: 0 }}>
            <table className="tbl" aria-label="کارتابل">
              <thead>
                <tr>
                  <th>نوع</th>
                  <th>شرح</th>
                  <th className="n">مبلغ (ریال)</th>
                  <th>سن</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {tasks.map((t) => (
                  <tr key={t.id}>
                    <td>
                      <Badge tone={t.tone}>{t.kind}</Badge>
                    </td>
                    <td>{t.text}</td>
                    <td className="n">{t.amount == null ? '—' : fmt(t.amount)}</td>
                    <td className="mut">{t.age}</td>
                    <td>
                      <Link className="btn sm" to={t.to}>
                        {t.action}
                      </Link>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {d && tasks.length === 0 && <div className="empty"><b>کارتابل خالی است</b><span>موردی نیازمند اقدام نیست.</span></div>}
          </div>
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <Card title="موقعیت نقدی">
            {pos ? (
              <KV
                rows={[
                  ...pos.accounts.map((a): [string, ReactNode] => [dir.find((x) => x.ledgerCode === a.ledger_code)?.name ?? a.name, <span className="n">{fmt(a.balance)}</span>]),
                  ['تسویهٔ درگاه در راه', <span className="n">{fmt(pos.gateway_pending)}</span>],
                  ['چک دریافتی (وصول‌نشده)', <span className="n">{fmt(pos.cheques_in)}</span>],
                  ['چک پرداختی (پاس‌نشده)', <span className="n neg">{fmt(-pos.cheques_out)}</span>],
                ]}
              />
            ) : (
              <Skeleton rows={4} />
            )}
          </Card>
          <Card title="وضعیت دوره‌ها">
            <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
              {MONTHS.map((n, i) => {
                const p = year?.periods.find((x) => x.number === i + 1)
                const today = new Date().toISOString().slice(0, 10)
                const current = p && p.from <= today && today <= p.to
                const tone = !p ? '' : current ? 'ok' : p.status === 'closed' ? '' : 'info'
                return (
                  <span key={n} className={`badge ${tone} sq`} style={{ minWidth: 64, justifyContent: 'center' }} title={p ? (p.status === 'open' ? 'باز' : 'بسته') : 'تعریف نشده'}>
                    {n}
                  </span>
                )
              })}
            </div>
          </Card>
        </div>
      </div>

      <Card title="آخرین اسناد" extra={<Link className="btn sm" to="/journals">همهٔ اسناد</Link>} bare>
        <div className="tw" style={{ border: 0, borderRadius: 0 }}>
          <table className="tbl" aria-label="آخرین اسناد">
            <thead>
              <tr>
                <th>شمارهٔ سند</th>
                <th>تاریخ</th>
                <th>شرح</th>
                <th className="n">بدهکار (ریال)</th>
                <th className="n">بستانکار (ریال)</th>
                <th>وضعیت</th>
              </tr>
            </thead>
            <tbody>
              {d?.journals.ok &&
                d.journals.v.items.map((j) => (
                  <tr key={j.id}>
                    <td className="n">{toFa(j.number)}</td>
                    <td>{showDate(j.date_jalali)}</td>
                    <td>{j.description}</td>
                    <td className="n">{fmt(j.total)}</td>
                    <td className="n">{fmt(j.total)}</td>
                    <td>{j.reverses_id ? <Badge tone="warn">سند اصلاحی</Badge> : <Badge tone="ok">ثبت‌شده</Badge>}</td>
                  </tr>
                ))}
            </tbody>
          </table>
          {d?.journals.ok && d.journals.v.items.length === 0 && <div className="empty"><b>هنوز سندی ثبت نشده</b><span>با «سند جدید» اولین سند را ثبت کنید.</span></div>}
          {d && !d.journals.ok && <ErrorMessage error={d.journals.e} onRetry={data.reload} />}
        </div>
      </Card>
    </>
  )
}
