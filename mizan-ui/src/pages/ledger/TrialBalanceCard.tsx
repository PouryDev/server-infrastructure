import { useState } from 'react'
import { accounting } from '@/api/accounting'
import { LEVEL_NAME } from '@/domain/accounts'
import { useDirectory } from '@/domain/useDirectory'
import { fmt, fmtOrDash, toFa } from '@/shared/digits'
import { Card, ErrorMessage, Seg, Skeleton, useResource } from '@/ui'

type Level = 1 | 2 | 3
type By = 'account' | 'coding'

/** Trial balance (تراز آزمایشی) by ledger account, or rolled up along the chart (کل / معین / گروه). */
export function TrialBalanceCard({ from, to, title = 'تراز آزمایشی (پیش‌نمایش)' }: { from?: string; to?: string; title?: string }) {
  const { dir } = useDirectory()
  const [by, setBy] = useState<By>('account')
  const [level, setLevel] = useState<Level>(3)
  const res = useResource(
    async (signal) => {
      if (by === 'account') {
        const tb = await accounting.trialBalance({ from, to }, signal)
        return {
          rows: tb.rows.map((r) => ({ code: r.coding_code || r.code, name: dir.find((a) => a.code === r.coding_code)?.name ?? r.name, debit: r.debit, credit: r.credit })),
          total: { debit: tb.debit, credit: tb.credit },
          unclassified: 0,
        }
      }
      const r = await accounting.codingTrialBalance({ from, to }, signal)
      const rows = r.nodes.filter((n) => n.level === level).map((n) => ({ code: n.code, name: n.title, debit: n.debit, credit: n.credit }))
      return { rows, total: { debit: rows.reduce((s, x) => s + x.debit, 0), credit: rows.reduce((s, x) => s + x.credit, 0) }, unclassified: r.unclassified.length }
    },
    [by, level, from, to, dir],
  )
  const d = res.data
  const net = (x: { debit: number; credit: number }) => x.debit - x.credit
  return (
    <Card
      title={title}
      bare
      extra={
        <>
          {by === 'coding' && (
            <Seg<string> label="سطح" value={String(level)} onChange={(v) => setLevel(Number(v) as Level)} options={[{ value: '1', label: LEVEL_NAME[1]! }, { value: '2', label: LEVEL_NAME[2]! }, { value: '3', label: LEVEL_NAME[3]! }]} />
          )}
          <Seg<By> label="مبنا" value={by} onChange={setBy} options={[{ value: 'account', label: 'بر اساس حساب' }, { value: 'coding', label: 'بر اساس کدینگ' }]} />
        </>
      }
    >
      {res.error != null ? (
        <div className="cb"><ErrorMessage error={res.error} onRetry={res.reload} /></div>
      ) : !d ? (
        <Skeleton />
      ) : (
        <div className="tw" style={{ border: 0, borderRadius: 0 }}>
          <table className="tbl" aria-label="تراز آزمایشی">
            <thead>
              <tr><th>کد</th><th>نام حساب</th><th className="n">گردش بدهکار</th><th className="n">گردش بستانکار</th><th className="n">مانده بدهکار</th><th className="n">مانده بستانکار</th></tr>
            </thead>
            <tbody>
              {d.rows.map((r) => (
                <tr key={r.code}>
                  <td className="n">{toFa(r.code)}</td>
                  <td>{r.name}</td>
                  <td className="n">{fmt(r.debit)}</td>
                  <td className="n">{fmt(r.credit)}</td>
                  <td className="n">{fmtOrDash(Math.max(net(r), 0))}</td>
                  <td className="n">{fmtOrDash(Math.max(-net(r), 0))}</td>
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <td colSpan={2}>جمع</td>
                <td className="n">{fmt(d.total.debit)}</td>
                <td className="n">{fmt(d.total.credit)}</td>
                <td className="n">{fmt(d.rows.reduce((s, r) => s + Math.max(net(r), 0), 0))}</td>
                <td className="n">{fmt(d.rows.reduce((s, r) => s + Math.max(-net(r), 0), 0))}</td>
              </tr>
            </tfoot>
          </table>
          {d.rows.length === 0 && <div className="empty"><b>هنوز گردشی ثبت نشده</b></div>}
          {d.unclassified > 0 && <div className="sub" style={{ padding: 10 }}>{fmt(d.unclassified)} حساب در کدینگ نگاشت نشده و در «طبقه‌بندی‌نشده» است.</div>}
        </div>
      )}
    </Card>
  )
}
