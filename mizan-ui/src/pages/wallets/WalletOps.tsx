import { useState } from 'react'
import { walletApi, type Wallet } from '@/api/commerce'
import { friendly } from '@/api/errors'
import { fmt, fmtOrDash } from '@/shared/digits'
import { showDate } from '@/shared/jalali'
import { Badge, Button, Card, ErrorMessage, Field, Input, Message, PageBar, Select, Skeleton, useResource, useToast } from '@/ui'
import { newIdempotencyKey } from '@/shared/idempotency'
import { val } from '@/ui/FormDialog'

async function find(owner: string): Promise<Wallet | undefined> {
  return (await walletApi.byOwner(owner.trim()))[0]
}

/** Wallet operations: open, top up through a gateway, transfer, status and statement. */
export default function WalletOps() {
  const toast = useToast()
  // open
  const [newOwner, setNewOwner] = useState('')
  // top up
  const [tOwner, setTOwner] = useState('')
  const [tAmount, setTAmount] = useState('')
  const [gateway, setGateway] = useState('zarinpal')
  const [pay, setPay] = useState<string>()
  // transfer
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [amount, setAmount] = useState('')
  const [desc, setDesc] = useState('')
  const [err, setErr] = useState<unknown>()
  // status/statement
  const [sOwner, setSOwner] = useState('')
  const [sw, setSw] = useState<Wallet>()
  const st = useResource(async () => (sw ? walletApi.statement(sw.id) : undefined), [sw?.id])

  const guard = async (f: () => Promise<void>) => { setErr(undefined); try { await f() } catch (e) { setErr(e); toast.error(e) } }

  return (
    <>
      <PageBar title="عملیات کیف پول" sub="هر عملیات پولی با شناسهٔ یکتا ثبت می‌شود تا تکرار آن دو بار اعمال نشود" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="کیف پول جدید">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Field label="مالک (شناسه)" required>{(id) => <Input id={id} w="md" dir="ltr" num value={newOwner} onChange={(e) => setNewOwner(e.target.value)} />}</Field>
            <div className="bar"><Button variant="pri" disabled={!newOwner.trim()} onClick={() => void guard(async () => { await walletApi.open(newOwner.trim()); toast.push({ tone: 'ok', title: 'کیف پول ایجاد شد' }); setNewOwner('') })}>ایجاد کیف پول</Button></div>
          </div>
        </Card>
        <Card title="شارژ کیف پول (درگاه)">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="مالک">{(id) => <Input id={id} w="md" dir="ltr" num value={tOwner} onChange={(e) => setTOwner(e.target.value)} />}</Field>
              <Field label="مبلغ (ریال)">{(id) => <Input id={id} w="amt" num value={tAmount} onChange={(e) => setTAmount(e.target.value)} />}</Field>
              <Field label="درگاه">{() => <Select label="درگاه" width="160px" value={gateway} options={[{ value: 'zarinpal', label: 'زرین‌پال' }, { value: 'idpay', label: 'آی‌دی‌پی' }]} onChange={setGateway} />}</Field>
            </div>
            <div className="bar"><Button variant="pri" disabled={!tOwner.trim() || !(val.amount(tAmount) > 0)} onClick={() => void guard(async () => { const w = await find(tOwner); if (!w) throw new Error('wallet not found'); const r = await walletApi.topup(w.id, val.amount(tAmount), gateway, `${location.origin}/wallets/ops`, newIdempotencyKey('topup')); setPay(r.pay_url) })}>ساخت لینک پرداخت</Button></div>
            {pay && <Message tone="ok" title="لینک پرداخت ساخته شد"><a href={pay} target="_blank" rel="noreferrer" className="n">{pay}</a></Message>}
            <Message tone="info" title="">آدرس بازگشت باید در فهرست دامنه‌های مجاز باشد.</Message>
          </div>
        </Card>
        <Card title="انتقال بین کیف پول‌ها">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="row">
              <Field label="از (مالک)">{(id) => <Input id={id} w="md" dir="ltr" num value={from} onChange={(e) => setFrom(e.target.value)} />}</Field>
              <Field label="به (مالک)">{(id) => <Input id={id} w="md" dir="ltr" num value={to} onChange={(e) => setTo(e.target.value)} />}</Field>
            </div>
            <div className="row">
              <Field label="مبلغ (ریال)">{(id) => <Input id={id} w="amt" num value={amount} onChange={(e) => setAmount(e.target.value)} />}</Field>
              <Field label="شرح" grow>{(id) => <Input id={id} w="xl" value={desc} onChange={(e) => setDesc(e.target.value)} />}</Field>
            </div>
            {err != null && <Message tone={friendly(err).tone} title={friendly(err).title}>{friendly(err).text}</Message>}
            <div className="bar"><Button variant="pri" disabled={!from.trim() || !to.trim() || !(val.amount(amount) > 0)} onClick={() => void guard(async () => { const [a, b] = await Promise.all([find(from), find(to)]); if (!a || !b) throw new Error('wallet not found'); await walletApi.transfer(a.id, b.id, val.amount(amount), desc.trim(), newIdempotencyKey('transfer')); toast.push({ tone: 'ok', title: 'انتقال انجام شد' }) })}>انتقال</Button></div>
          </div>
        </Card>
        <Card title="وضعیت و صورت‌حساب کیف پول">
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <form className="row" onSubmit={(e) => (e.preventDefault(), void guard(async () => setSw(await find(sOwner))))}>
              <Field label="مالک">{(id) => <Input id={id} w="md" dir="ltr" num value={sOwner} onChange={(e) => setSOwner(e.target.value)} />}</Field>
              <Button type="submit">نمایش</Button>
            </form>
            {sw && (
              <div className="row" style={{ alignItems: 'center' }}>
                <Badge tone={sw.status === 'active' ? 'ok' : 'err'}>{sw.status === 'active' ? 'فعال' : 'مسدود'}</Badge>
                <Button small variant="dng" onClick={() => void guard(async () => { await walletApi.setStatus(sw.id, 'frozen'); setSw({ ...sw, status: 'frozen' }) })}>مسدود کردن</Button>
                <Button small onClick={() => void guard(async () => { await walletApi.setStatus(sw.id, 'active'); setSw({ ...sw, status: 'active' }) })}>فعال‌سازی</Button>
              </div>
            )}
            {st.error != null ? <ErrorMessage error={st.error} /> : st.loading && sw ? <Skeleton rows={2} /> : st.data && (
              <div className="tw"><table className="tbl" aria-label="صورت‌حساب"><thead><tr><th>تاریخ</th><th>شرح</th><th className="n">مبلغ</th><th className="n">مانده</th></tr></thead>
                <tbody>{st.data.rows.map((r, i) => <tr key={i}><td>{showDate(r.date_jalali)}</td><td>{r.description}</td><td className={`n ${r.debit ? 'neg' : ''}`}>{r.credit ? fmt(r.credit) : fmtOrDash(-r.debit)}</td><td className="n">{fmt(r.balance)}</td></tr>)}</tbody></table></div>
            )}
          </div>
        </Card>
      </div>
    </>
  )
}
