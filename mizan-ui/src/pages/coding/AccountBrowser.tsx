import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { accounting } from '@/api/accounting'
import { LEVEL_NAME, type DirectoryEntry } from '@/domain/accounts'
import { useDirectory } from '@/domain/useDirectory'
import { fmt, normSearch, toFa } from '@/shared/digits'
import { Badge, Button, Card, ErrorMessage, Input, LinkButton, PageBar, Skeleton, useResource } from '@/ui'

const TONE: Record<number, 'pri' | 'info' | 'ok' | ''> = { 1: 'pri', 2: 'info', 3: 'ok', 4: '' }

/**
 * Account browser: walk the chart of accounts level by level (group › general › subsidiary), step through it code by code
 * from the first to the last, or jump straight to a code. The position is in the URL (?code=), so the back button works.
 */
export default function AccountBrowser() {
  const [params, setParams] = useSearchParams()
  const { dir, loading, error, reload } = useDirectory()
  const bal = useResource(async () => new Map((await accounting.codingTrialBalance()).nodes.map((n) => [n.code, n])), [])
  const [filter, setFilter] = useState('')
  const [jump, setJump] = useState('')
  const [jumpErr, setJumpErr] = useState('')

  // only the chart's own nodes (ledger accounts outside the chart have no place in the tree)
  const chart = useMemo(() => dir.filter((a) => a.level === 1 || a.parent), [dir])
  const byCode = useMemo(() => new Map(chart.map((a) => [a.code, a])), [chart])
  const kids = useMemo(() => {
    const m = new Map<string, DirectoryEntry[]>()
    for (const a of chart) {
      const k = a.parent ?? ''
      m.set(k, [...(m.get(k) ?? []), a])
    }
    return m
  }, [chart])

  const code = params.get('code') ?? ''
  const cur = byCode.get(code)
  const children = kids.get(cur ? cur.code : '') ?? []
  const s = normSearch(filter)
  const shown = s ? children.filter((a) => a.code.startsWith(s) || normSearch(a.name).includes(s)) : children
  const idx = cur ? chart.findIndex((a) => a.code === cur.code) : -1

  const go = (c: string) => {
    setFilter('')
    setJumpErr('')
    setParams(c ? { code: c } : {})
  }
  const trail = useMemo(() => {
    const t: DirectoryEntry[] = []
    for (let n = cur; n; n = n.parent ? byCode.get(n.parent) : undefined) t.unshift(n)
    return t
  }, [cur, byCode])

  const doJump = () => {
    const c = normSearch(jump)
    if (!c) return
    const exact = byCode.get(c)
    const hit = exact ?? chart.find((a) => a.code.startsWith(c)) // a partial code lands on the first account starting with it
    if (!hit) return setJumpErr(`کدی با «${toFa(c)}» شروع نمی‌شود`)
    setJump('')
    go(hit.code)
  }

  const b = cur ? bal.data?.get(cur.code) : undefined
  const isLeaf = cur ? (kids.get(cur.code) ?? []).length === 0 : false
  const total = (a: DirectoryEntry) => bal.data?.get(a.code)?.balance

  return (
    <>
      <PageBar title="مرور حساب‌ها" sub="از کدینگ گام‌به‌گام پایین بروید، کد به کد جلو و عقب بروید، یا مستقیم به یک کد بپرید" />
      {error != null && <ErrorMessage error={error} onRetry={reload} />}
      {bal.error != null && <ErrorMessage error={bal.error} onRetry={bal.reload} />}

      <Card>
        <div className="row" style={{ alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <nav aria-label="مسیر" className="crumbs">
            <button type="button" className={!cur ? 'on' : ''} onClick={() => go('')}>
              همهٔ گروه‌ها
            </button>
            {trail.map((t) => (
              <span key={t.code} style={{ display: 'contents' }}>
                <span aria-hidden="true">‹</span>
                <button type="button" className={t.code === cur?.code ? 'on' : ''} onClick={() => go(t.code)}>
                  <span className="cd">{toFa(t.code)}</span> {t.name}
                </button>
              </span>
            ))}
          </nav>
          <span className="sp" />
          <Button small disabled={!cur?.parent && !cur} onClick={() => go(cur?.parent ?? '')}>
            ↑ سطح بالاتر
          </Button>
          <Button small disabled={idx <= 0} onClick={() => go(chart[idx - 1]!.code)}>
            ▸ کد قبلی
          </Button>
          <Button small disabled={idx < 0 || idx >= chart.length - 1} onClick={() => go(chart[idx + 1]!.code)}>
            کد بعدی ◂
          </Button>
          <Button small disabled={chart.length === 0} onClick={() => go(chart[0]!.code)}>
            اولین
          </Button>
          <Button small disabled={chart.length === 0} onClick={() => go(chart[chart.length - 1]!.code)}>
            آخرین
          </Button>
        </div>
        <div className="row" style={{ marginTop: 10, alignItems: 'center', gap: 8 }}>
          <Input aria-label="پرش مستقیم به کد" w="md" num placeholder="پرش به کد (مثلاً ۱۱۱۰۰۱)" value={jump} onChange={(e) => (setJump(e.target.value), setJumpErr(''))} onKeyDown={(e) => e.key === 'Enter' && doJump()} />
          <Button small variant="pri" onClick={doJump}>
            برو
          </Button>
          {jumpErr && <span className="sub" style={{ color: 'var(--err)' }}>{jumpErr}</span>}
          <span className="sp" />
          <Input aria-label="فیلتر این سطح" w="md" placeholder="فیلتر همین سطح" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
      </Card>

      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title={cur ? `زیرمجموعهٔ ${toFa(cur.code)} · ${cur.name}` : 'گروه‌های حساب'} extra={<span className="sub">{toFa(shown.length)} مورد</span>}>
          {loading ? (
            <Skeleton rows={8} />
          ) : chart.length === 0 ? (
            <div className="empty"><b>کدینگی فعال نیست</b><Link to="/coding">به صفحهٔ کدینگ بروید</Link></div>
          ) : isLeaf ? (
            <div className="empty">این حساب زیرمجموعه ندارد؛ جزئیاتش در کادر کناری است.</div>
          ) : shown.length === 0 ? (
            <div className="empty">موردی پیدا نشد.</div>
          ) : (
            <div className="blist">
              {shown.map((a) => {
                const n = (kids.get(a.code) ?? []).length
                const t = total(a)
                return (
                  <button key={a.code} type="button" className="brow" onClick={() => go(a.code)}>
                    <span className="cd">{toFa(a.code)}</span>
                    <span className="tn">{a.name}</span>
                    <Badge tone={TONE[a.level] ?? ''}>{LEVEL_NAME[a.level]}</Badge>
                    <span className="sp" />
                    {n > 0 && <span className="sub">{toFa(n)} زیرحساب</span>}
                    <span className="bal">{t === undefined ? '' : fmt(t)}</span>
                    <span aria-hidden="true">{n > 0 ? '◂' : ''}</span>
                  </button>
                )
              })}
            </div>
          )}
        </Card>

        <Card title={cur ? `${toFa(cur.code)} · ${cur.name}` : 'حساب'} extra={cur && <Badge tone={TONE[cur.level] ?? ''}>{LEVEL_NAME[cur.level]}</Badge>}>
          {!cur ? (
            <div className="empty">یک گروه را باز کنید یا کدی را وارد کنید.</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {cur.path && <div className="sub">{cur.path}</div>}
              <dl className="kv">
                <dt>مانده (ریال)</dt>
                <dd>{b ? fmt(b.balance) : '—'}</dd>
                <dt>گردش بدهکار</dt>
                <dd>{b ? fmt(b.debit) : '—'}</dd>
                <dt>گردش بستانکار</dt>
                <dd>{b ? fmt(b.credit) : '—'}</dd>
                <dt>حساب دفتر</dt>
                <dd>{cur.ledgerCode || (cur.level === 3 ? 'با اولین سند ساخته می‌شود' : 'دسته‌بندی (قابل ثبت نیست)')}</dd>
              </dl>
              <div className="div" />
              <div className="bar">
                <LinkButton to={`/ledger?account=${cur.code}`} pri>
                  گردش حساب
                </LinkButton>
                {cur.postable && <LinkButton to="/journals/new">سند جدید</LinkButton>}
              </div>
            </div>
          )}
        </Card>
      </div>
    </>
  )
}
