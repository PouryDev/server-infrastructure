import type { ReactNode } from 'react'
import { Card, Kbd, Message, PageBar } from '@/ui'

const K = (...keys: string[]): ReactNode => keys.map((k, i) => <span key={k} style={{ display: 'contents' }}>{i > 0 && ' + '}<Kbd>{k}</Kbd></span>)

const JOURNAL: [ReactNode, ReactNode][] = [
  [K('F3'), 'جابه‌جایی مبلغ بدهکار و بستانکار ردیف'], [K('F4'), 'کپی فیلد از ردیف بالا'], [K('F8'), 'تکثیر ردیف'], [K('Ctrl', 'Ins'), 'افزودن ردیف'], [K('Ctrl', 'Del'), 'حذف ردیف'],
  [K('Alt', 'Shift', '↓'), <>فهرست شرح‌های ذخیره‌شده (حذف با <Kbd>Del</Kbd>)</>],
]
const ALL: [ReactNode, ReactNode][] = [[K('Ctrl', 'F'), 'نمایش یا بازگرداندن ردیف فیلتر زیر ستون‌ها'], [K('Ctrl', 'M'), 'روشن‌کردن انتخاب چندگانه در فهرست'], [K('Ctrl', 'H'), 'نمایش ثبت‌کننده و زمان ثبت یا ویرایش']]

const Table = ({ rows, label }: { rows: [ReactNode, ReactNode][]; label: string }) => (
  <div className="tw" style={{ border: 0, borderRadius: 0 }}><table className="tbl" aria-label={label}><tbody>{rows.map(([k, t], i) => <tr key={i}><td>{k}</td><td>{t}</td></tr>)}</tbody></table></div>
)

export default function Shortcuts() {
  return (
    <>
      <PageBar title="کلیدهای میانبر" sub="همان کلیدهای نرم‌افزار سپیدار، برای حسابدارانی که به آن عادت دارند" />
      <div className="grid g2" style={{ alignItems: 'start' }}>
        <Card title="فرم سند حسابداری" bare><Table rows={JOURNAL} label="کلیدهای فرم سند" /></Card>
        <Card title="همهٔ فرم‌ها و فهرست‌ها" bare><Table rows={ALL} label="کلیدهای فهرست‌ها" /></Card>
        <Message tone="info" title="کلیدهای تأییدنشده">برای ذخیره، سند جدید و بستن پنجره منبع معتبری از سپیدار پیدا نشد. فعلاً این کارها فقط با دکمه انجام می‌شوند. اگر فهرست رسمی سپیدار را دارید، همین‌جا اضافه می‌شود.</Message>
      </div>
    </>
  )
}
