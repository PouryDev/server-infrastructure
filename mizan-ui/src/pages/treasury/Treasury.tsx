import { useState } from 'react'
import { Link } from 'react-router-dom'
import { treasury, type Cheque, type BankAccount } from '@/api/treasury'
import { ACTION_LABEL, allowedActions, chequeStatus, type ChequeAction } from '@/domain/cheque'
import { fmt } from '@/shared/digits'
import { isoToJalali, showDate } from '@/shared/jalali'
import { Badge, Button, DataTable, ErrorMessage, MasterDetail, Message, PageBar, Seg, Skeleton, Stat, Steps, SubTabs, useListState, useResource, useToast, type Column } from '@/ui'
import { FormDialog, val } from '@/ui/FormDialog'
import { itemsOf } from '@/api/commerce'

type Tab = 'banks' | 'cheques' | 'settlements'
type Dir = 'receivable' | 'payable'
const STEPS: Record<string, number> = { in_hand: 0, issued: 0, deposited: 1, cleared: 2, bounced: 2, returned: 2, endorsed: 2 }

export default function Treasury() {
  const toast = useToast()
  const [tab, setTab] = useState<Tab>('cheques')
  const [dir, setDir] = useState<Dir>('receivable')
  const [st, setSt] = useState<string>('all')
  const list = useListState({}, 20)
  const [sel, setSel] = useState<Cheque>()
  const [action, setAction] = useState<ChequeAction | null>(null)
  const pos = useResource(() => treasury.position(), [])
  const banks = useResource(async () => itemsOf(await treasury.bankAccounts()), [])
  const filter = { direction: dir, ...(st !== 'all' ? { status: st } : {}), ...Object.fromEntries(Object.entries(list.filters).filter(([, v]) => v).map(([k, v]) => [k === 'counterparty_ref' ? 'counterparty_ref:like' : k, v])) }
  const cheques = useResource((s) => treasury.cheques({ page: list.page, pageSize: list.pageSize, sort: 'due_date', filter }, s), [dir, st, list.page, JSON.stringify(list.filters)])
  const settlements = useResource((s) => treasury.settlements({ pageSize: 50, sort: '-settled_on' }).then((r) => (s.aborted ? r : r)), [tab === 'settlements'])

  const cols: Column<Cheque>[] = [
    { id: 's', header: 'شناسهٔ صیادی', num: true, filter: 'sayad_id', cell: (c) => c.sayad_id || c.number },
    { id: 'p', header: 'طرف حساب', filter: 'counterparty_ref', cell: (c) => c.counterparty_ref },
    { id: 'd', header: 'سررسید', filter: 'due_date', cell: (c) => showDate(c.due_date_jalali) },
    { id: 'a', header: 'مبلغ (ریال)', num: true, cell: (c) => fmt(c.amount) },
    { id: 'st', header: 'وضعیت', cell: (c) => <Badge tone={chequeStatus[c.status].tone}>{chequeStatus[c.status].label}</Badge> },
  ]
  const bankCols: Column<BankAccount>[] = [
    { id: 'n', header: 'حساب', cell: (b) => b.name }, { id: 'c', header: 'کد دفتر کل', num: true, cell: (b) => b.ledger_code },
    { id: 'i', header: 'شبا', num: true, cell: (b) => b.iban || '—' }, { id: 'b', header: 'موجودی (ریال)', num: true, cell: (b) => fmt(pos.data?.accounts.find((a) => a.id === b.id)?.balance) },
  ]
  const done = (m: string) => () => { setAction(null); toast.push({ tone: 'ok', title: m }); cheques.reload(); pos.reload(); setSel(undefined) }
  const allowed = sel ? allowedActions(sel) : []

  return (
    <>
      <PageBar title="خزانه‌داری و چک">
        <Link className="btn" to="/treasury/new">حساب بانکی جدید</Link>
        <Link className="btn pri" to="/treasury/new">ثبت چک</Link>
      </PageBar>
      {pos.error != null ? <ErrorMessage error={pos.error} onRetry={pos.reload} /> : (
        <div className="grid g4">
          <Stat label="موجودی بانک‌ها" value={pos.data ? fmt(pos.data.banks) : '…'} />
          <Stat label="چک دریافتی (وصول‌نشده)" value={pos.data ? fmt(pos.data.cheques_in) : '…'} />
          <Stat label="چک پرداختی (پاس‌نشده)" value={pos.data ? fmt(pos.data.cheques_out) : '…'} />
          <Stat label="پیش‌بینی نهایی" value={pos.data ? fmt(pos.data.projected) : '…'} />
        </div>
      )}
      <SubTabs<Tab> value={tab} onChange={setTab} options={[{ value: 'banks', label: 'حساب‌های بانکی' }, { value: 'cheques', label: 'چک‌ها' }, { value: 'settlements', label: 'تسویهٔ درگاه' }]} />
      {tab === 'banks' && <DataTable caption="حساب‌های بانکی" columns={bankCols} rows={banks.data} rowKey={(b) => b.id} loading={banks.loading} error={banks.error} onRetry={banks.reload} empty={{ title: 'هنوز حساب بانکی ثبت نشده', action: <Link className="btn pri" to="/treasury/new">حساب بانکی جدید</Link> }} />}
      {tab === 'settlements' && (
        <DataTable caption="تسویهٔ درگاه" columns={[{ id: 'g', header: 'درگاه', cell: (x) => x.gateway }, { id: 'r', header: 'مرجع', num: true, cell: (x) => x.ref }, { id: 'd', header: 'تاریخ', cell: (x) => showDate(isoToJalali(x.settled_on)) }, { id: 'gr', header: 'ناخالص', num: true, cell: (x) => fmt(x.gross) }, { id: 'f', header: 'کارمزد', num: true, cell: (x) => fmt(x.fee) }, { id: 'n', header: 'واریزی خالص', num: true, cell: (x) => fmt(x.net) }]} rows={settlements.data?.items} rowKey={(x) => x.id} loading={settlements.loading} error={settlements.error} onRetry={settlements.reload} empty={{ title: 'هنوز تسویه‌ای ثبت نشده', action: <Link className="btn pri" to="/treasury/new">ثبت تسویهٔ درگاه</Link> }} />
      )}
      {tab === 'cheques' && (
        <>
          <div className="bar">
            <Seg<Dir> label="جهت" value={dir} onChange={(v) => (setDir(v), setSt('all'), list.setPage(1), setSel(undefined))} options={[{ value: 'receivable', label: 'دریافتی' }, { value: 'payable', label: 'پرداختی' }]} />
            <Seg<string> label="وضعیت" value={st} onChange={(v) => (setSt(v), list.setPage(1))} options={[{ value: 'all', label: 'همه' }, { value: dir === 'receivable' ? 'in_hand' : 'issued', label: dir === 'receivable' ? 'در دست' : 'صادرشده' }, { value: 'deposited', label: 'واگذار به بانک' }, { value: 'bounced', label: 'برگشتی' }, { value: 'cleared', label: 'وصول‌شده' }]} />
          </div>
          <MasterDetail
            listBasis={560}
            detailBasis={380}
            list={<DataTable caption="چک‌ها" columns={cols} rows={cheques.data?.items} rowKey={(c) => c.id} loading={cheques.loading} error={cheques.error} onRetry={cheques.reload} empty={{ title: 'چکی ثبت نشده', action: <Link className="btn pri" to="/treasury/new">ثبت چک</Link> }} filters={list.filters} onFilters={list.setFilters} meta={cheques.data?.meta} onPage={list.setPage} onRowClick={(c) => setSel(c)} rowClass={(c) => (c.id === sel?.id ? 'sel' : undefined)} />}
            detail={sel ? (
              <>
                <div className="ch">چک {sel.number}<Badge tone={chequeStatus[sel.status].tone}>{chequeStatus[sel.status].label}</Badge></div>
                <div className="cb" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                  <Steps steps={['در دست', 'واگذار', sel.status === 'bounced' ? 'برگشت' : 'وصول']} current={STEPS[sel.status] ?? 0} />
                  {sel.status === 'bounced' && <Message tone="warn" title="">بستانکار شدن حساب مشتری و کارمزد بانک ثبت شد. سند را با حسابدار خود تأیید کنید.</Message>}
                  <div className="bar">{allowed.map((a) => <Button key={a} variant={a === 'bounce' ? 'dng' : 'plain'} onClick={() => setAction(a)}>{ACTION_LABEL[a]}</Button>)}</div>
                  {allowed.length === 0 && <div className="sub">برای این وضعیت عملیاتی وجود ندارد.</div>}
                </div>
              </>
            ) : cheques.loading ? <Skeleton /> : undefined}
          />
        </>
      )}
      {action && sel && <ChequeActionDialog cheque={sel} action={action} banks={banks.data ?? []} onClose={() => setAction(null)} onDone={done(`${ACTION_LABEL[action]} ثبت شد`)} />}
    </>
  )
}

function ChequeActionDialog({ cheque, action, banks, onClose, onDone }: { cheque: Cheque; action: ChequeAction; banks: BankAccount[]; onClose(): void; onDone(): void }) {
  const dateF = { name: 'date', label: 'تاریخ', kind: 'date' as const, w: 'date' as const }
  const bankOpts = banks.map((b) => ({ value: b.id, label: b.name }))
  const cfg = {
    deposit: { f: [{ name: 'bank', label: 'حساب بانکی مقصد', kind: 'select' as const, required: true, options: bankOpts }, dateF], run: (v: Record<string, string>) => treasury.deposit(cheque.id, v.bank!, val.iso(v.date)), intro: undefined },
    clear: { f: [dateF], run: (v: Record<string, string>) => treasury.clear(cheque.id, val.iso(v.date)), intro: 'با وصول، مبلغ از «چک در جریان وصول» به حساب بانک منتقل می‌شود. این کار برگشت‌پذیر نیست؛ اشتباه را فقط با سند اصلاحی می‌توان درست کرد.' },
    bounce: { f: [{ name: 'reason', label: 'دلیل', required: true }, { name: 'fee', label: 'کارمزد برگشت (ریال)', kind: 'amount' as const, w: 'amt' as const }, dateF], run: (v: Record<string, string>) => treasury.bounce(cheque.id, val.text(v.reason), val.amount(v.fee) || undefined, val.iso(v.date)), intro: 'سند برگشت (بازگرداندن به حساب مشتری) و کارمزد بانک ثبت می‌شود.' },
    return: { f: [{ name: 'reason', label: 'دلیل', required: true }], run: (v: Record<string, string>) => treasury.returnCheque(cheque.id, val.text(v.reason)), intro: undefined },
    endorse: { f: [{ name: 'to', label: 'به (طرف حساب)', required: true }, { name: 'acct', label: 'حساب مقابل (کد دفتر کل)', required: true, kind: 'latin' as const, w: 'md' as const, initial: 'payable.suppliers' }, dateF], run: (v: Record<string, string>) => treasury.endorse(cheque.id, val.text(v.to), val.text(v.acct), val.iso(v.date)), intro: undefined },
    'take-back': { f: [{ name: 'reason', label: 'دلیل', required: true }], run: (v: Record<string, string>) => treasury.takeBack(cheque.id, val.text(v.reason)), intro: undefined },
  }[action]
  return <FormDialog title={`${ACTION_LABEL[action]} – چک ${cheque.number}`} submitLabel="ثبت" danger={action === 'bounce'} intro={cfg.intro} fields={cfg.f} onSubmit={(v) => cfg.run(v)} onClose={onClose} onDone={onDone} />
}
