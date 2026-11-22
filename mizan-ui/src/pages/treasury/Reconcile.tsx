import { useState } from 'react'
import { itemsOf } from '@/api/commerce'
import { treasury, type StatementLine } from '@/api/treasury'
import { fmt, toFa } from '@/shared/digits'
import { showDate } from '@/shared/jalali'
import { Badge, Button, DataTable, ErrorMessage, MasterDetail, Message, PageBar, Select, Skeleton, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { Link } from 'react-router-dom'

const ST = { unmatched: { l: 'تطبیق‌نشده', t: 'warn' as const }, matched: { l: 'تطبیق‌شده', t: 'ok' as const }, ignored: { l: 'نادیده', t: '' as const } }
const KIND: Record<string, string> = { cheque: 'چک', settlement: 'تسویهٔ درگاه', fee: 'کارمزد', ignored: 'نادیده' }

/** Bank reconciliation: statement lines, suggestions for the selected line, and the matching actions. */
export default function Reconcile() {
  const toast = useToast()
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const [bank, setBank] = useState('')
  const chosen = bank || banks.data?.[0]?.id || ''
  const list = useListState({}, 20)
  const [sel, setSel] = useState<StatementLine>()
  const [ignore, setIgnore] = useState(false)
  const [note, setNote] = useState('')
  const lines = useResource((s) => (chosen ? treasury.lines({ page: list.page, pageSize: list.pageSize, sort: '-line_date', filter: { bank_account_id: chosen } }, s) : Promise.resolve(undefined)), [chosen, list.page])
  const sugg = useResource(async () => (sel && sel.status === 'unmatched' ? treasury.suggestions(sel.id) : []), [sel?.id])
  const unmatched = useResource(async () => (chosen ? (await treasury.lines({ pageSize: 1, filter: { bank_account_id: chosen, status: 'unmatched' } })).meta.total ?? 0 : 0), [chosen, lines.data])
  const cols: Column<StatementLine>[] = [
    { id: 'd', header: 'تاریخ', cell: (l) => showDate(l.date_jalali) }, { id: 't', header: 'شرح بانک', cell: (l) => l.description || l.reference || '—' },
    { id: 'a', header: 'مبلغ (ریال)', num: true, cell: (l) => <span className={l.amount < 0 ? 'neg' : ''}>{fmt(l.amount)}</span> }, { id: 's', header: 'وضعیت', cell: (l) => <Badge tone={ST[l.status].t}>{ST[l.status].l}</Badge> },
  ]
  const after = () => { lines.reload(); sugg.reload(); setSel(undefined) }
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast.push({ tone: 'ok', title: msg }); after() } catch (e) { toast.error(e) } }

  return (
    <>
      <PageBar title="تطبیق بانکی" sub={`${unmatched.data != null ? `${toFa(unmatched.data)} ردیف تطبیق‌نشده` : ''}`}>
        <Select label="حساب بانکی" width="220px" value={chosen} options={(banks.data ?? []).map((b) => ({ value: b.id, label: b.name }))} onChange={setBank} />
        <Link className="btn" to="/treasury/new">درون‌ریزی صورت‌حساب (CSV)</Link>
        <Button variant="pri" disabled={!chosen} onClick={() => void act(async () => { const r = await treasury.autoMatch(chosen); toast.push({ tone: 'info', title: `تطبیق خودکار: ${toFa(Object.values(r).reduce((a, b) => a + Number(b), 0))} مورد` }) }, 'تطبیق خودکار انجام شد')}>تطبیق خودکار</Button>
      </PageBar>
      <Message tone="info" title="">تطبیق خودکار فقط ردیفی را می‌پذیرد که شناسهٔ صیادی یا مرجع تسویه در متن آن باشد. تنها یکی بودن مبلغ هرگز پول را جابه‌جا نمی‌کند.</Message>
      <MasterDetail
        listBasis={560}
        list={<DataTable caption="ردیف‌های صورت‌حساب" columns={cols} rows={lines.data?.items} rowKey={(l) => l.id} loading={lines.loading} error={lines.error} onRetry={lines.reload} empty={{ title: chosen ? 'ردیفی وارد نشده' : 'حساب بانکی انتخاب کنید' }} meta={lines.data?.meta} onPage={list.setPage} onRowClick={(l) => setSel(l)} rowClass={(l) => (l.id === sel?.id ? 'sel' : undefined)} />}
        detail={sel ? (
          <>
            <div className="ch">پیشنهادها برای ردیف انتخابی</div>
            <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
              {sel.status !== 'unmatched' ? <Message tone="ok" title={`تطبیق ${KIND[sel.match_kind ?? ''] ?? ''}`}>توسط {sel.matched_by ?? '—'}</Message> : sugg.error != null ? <ErrorMessage error={sugg.error} /> : sugg.loading ? <Skeleton rows={2} /> : (
                <>
                  <div className="tw"><table className="tbl" aria-label="پیشنهادها"><tbody>{(sugg.data ?? []).map((s) => <tr key={s.item_id}><td>{KIND[s.kind] ?? s.kind} {s.item_id.slice(0, 8)}</td><td>{s.high_confidence ? <Badge tone="ok">اطمینان بالا</Badge> : <Badge>هم‌مبلغ</Badge>}</td><td><Button small variant="pri" onClick={() => void act(() => treasury.match(sel.id, { kind: s.kind, item_id: s.item_id }), 'تطبیق ثبت شد')}>تطبیق</Button></td></tr>)}</tbody></table>{sugg.data?.length === 0 && <div className="empty">پیشنهادی نیست.</div>}</div>
                  <div className="bar"><Button onClick={() => void act(() => treasury.match(sel.id, { kind: 'fee' }), 'کارمزد ثبت شد')}>ثبت کارمزد</Button><Button onClick={() => setIgnore(true)}>نادیده</Button></div>
                </>
              )}
            </div>
          </>
        ) : undefined}
      />
      {ignore && sel && <FormDialog title="نادیده‌گرفتن ردیف" submitLabel="نادیده" fields={[{ name: 'note', label: 'یادداشت', required: true, hint: 'برای نادیده‌گرفتن الزامی است.', initial: note }]} onSubmit={(v) => treasury.match(sel.id, { kind: 'ignored', note: val.text(v.note) })} onClose={() => (setIgnore(false), setNote(''))} onDone={() => (setIgnore(false), toast.push({ tone: 'ok', title: 'نادیده گرفته شد' }), after())} />}
    </>
  )
}
