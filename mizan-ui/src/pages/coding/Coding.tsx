import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { accounting } from '@/api/accounting'
import { buildDirectory, LEVEL_NAME, type DirectoryEntry } from '@/domain/accounts'
import { invalidateDirectory } from '@/domain/useDirectory'
import { normSearch, toFa } from '@/shared/digits'
import { Badge, Button, Card, ErrorMessage, Field, Input, LinkButton, Message, PageBar, Select, Skeleton, useResource, useToast } from '@/ui'
import { AccountTree } from '@/shared/AccountTree'
import { EnableCodingDialog } from './EnableCodingDialog'

const TONE: Record<number, 'pri' | 'info' | 'ok' | ''> = { 1: 'pri', 2: 'info', 3: 'ok', 4: '' }
const NATURE = [{ value: 'debit', label: 'بدهکار' }, { value: 'credit', label: 'بستانکار' }]

/** Chart of accounts: the tree, and the mapping of a ledger account to a chart code. */
export default function Coding() {
  const toast = useToast()
  const res = useResource(async () => {
    const [nodes, tb] = await Promise.all([accounting.coding(), accounting.trialBalance({ include_zero: true })])
    return { dir: buildDirectory(nodes, tb.rows), ledger: tb.rows }
  }, [])
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<string>()
  const [enable, setEnable] = useState(false)
  const [codeEdit, setCodeEdit] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const dir = res.data?.dir
  const shown = useMemo(() => {
    const s = normSearch(q)
    return (dir ?? []).filter((a) => !s || a.code.startsWith(s) || normSearch(a.name).includes(s))
  }, [dir, q])
  const cur: DirectoryEntry | undefined = dir?.find((a) => a.code === selected)
  const acc = res.data?.ledger.find((r) => r.code === cur?.ledgerCode)

  const saveMapping = async () => {
    if (!acc || codeEdit === null) return
    setBusy(true)
    try {
      await accounting.mapAccount(acc.code, codeEdit.trim())
      toast.push({ tone: 'ok', title: 'نگاشت ذخیره شد' })
      invalidateDirectory()
      res.reload()
      setCodeEdit(null)
    } catch (e) {
      toast.error(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <PageBar title="کدینگ حساب‌ها" sub="درخت حساب‌ها، نگاشت به کدینگ و حساب‌های سیستمی">
        <Button onClick={() => setEnable(true)}>فعال‌سازی کدینگ نمونه</Button>
        <LinkButton to="/coding/new" pri>
          حساب جدید
        </LinkButton>
      </PageBar>
      <Message tone="info" title="">
        کدینگ نمونه یک الگوست، نه استاندارد قانونی. پیش از استفاده با حسابدار خود تطبیق دهید.
      </Message>
      {res.error != null && <ErrorMessage error={res.error} onRetry={res.reload} />}
      <div className="grid g21" style={{ alignItems: 'start' }}>
        <Card title="درخت حساب‌ها" extra={<Input aria-label="جستجوی کد یا نام" w="md" placeholder="جستجوی کد یا نام" style={{ height: 26 }} value={q} onChange={(e) => setQ(e.target.value)} />}>
          {!dir ? (
            <Skeleton rows={8} />
          ) : dir.length === 0 ? (
            <div className="empty"><b>کدینگی فعال نیست</b><span>با «فعال‌سازی کدینگ نمونه» یک الگو بارگذاری کنید.</span></div>
          ) : (
            <AccountTree
              dir={shown}
              flat={q !== ''}
              fa
              selected={selected}
              onSelect={(a) => (setSelected(a.code), setCodeEdit(null))}
              badge={(a) => <Badge tone={TONE[a.level] ?? ''}>{LEVEL_NAME[a.level]}</Badge>}
            />
          )}
        </Card>

        <Card title={cur ? `حساب ${toFa(cur.code)} · ${cur.name}` : 'حساب'} extra={cur && acc && <Badge tone={acc ? 'ok' : ''}>فعال</Badge>}>
          {!cur ? (
            <div className="empty">یک حساب از درخت انتخاب کنید.</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <div className="row">
                <Field label="کد حساب">{(id) => <Input id={id} w="code" num ro value={cur.code} />}</Field>
                <Field label="نام" hint={cur.path || undefined}>{(id) => <Input id={id} w="md" ro value={cur.name} />}</Field>
                <Field label="ماهیت">
                  {(id) => <Select id={id} label="ماهیت" width="140px" disabled value={cur.type === 'liability' || cur.type === 'equity' || cur.type === 'revenue' ? 'credit' : 'debit'} options={NATURE} onChange={() => undefined} />}
                </Field>
                <Field label="کد در کدینگ">
                  {(id) => <Input id={id} w="code" num ro={!acc} value={codeEdit ?? acc?.coding_code ?? cur.code} onChange={(e) => setCodeEdit(e.target.value)} />}
                </Field>
              </div>
              {acc ? (
                <Field label="کلید سیستمی" hint={acc.code.includes('.') ? 'حساب سیستمی؛ حذف یا تغییر ماهیت آن ممکن نیست.' : undefined}>
                  {(id) => <Input id={id} w="md" num ro value={acc.code} />}
                </Field>
              ) : (
                <div className="sub">{cur.level === 3 ? 'این حساب معین هنوز حسابی در دفتر ندارد؛ هنگام اولین سند ساخته می‌شود.' : 'حساب‌های سطح بالا فقط برای دسته‌بندی‌اند و در سند ثبت نمی‌شوند.'}</div>
              )}
              <div className="div" />
              <div className="bar">
                <Button variant="pri" disabled={!acc || codeEdit === null || busy} onClick={() => void saveMapping()}>
                  ذخیرهٔ نگاشت
                </Button>
                <Link className="btn" to={`/ledger?account=${cur.code}`}>
                  گردش حساب
                </Link>
              </div>
            </div>
          )}
        </Card>
      </div>
      {enable && (
        <EnableCodingDialog
          onClose={() => setEnable(false)}
          onDone={() => {
            setEnable(false)
            invalidateDirectory()
            res.reload()
            toast.push({ tone: 'ok', title: 'کدینگ فعال شد' })
          }}
        />
      )}
    </>
  )
}
