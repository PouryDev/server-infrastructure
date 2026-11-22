import { Button, Card, Empty, Message, PageBar, Skeleton } from '@/ui'
import { friendly, ApiError } from '@/api/errors'

const Cap = ({ children }: { children: string }) => <div style={{ fontWeight: 700, marginBottom: 6 }}>{children}</div>

/** The design's "حالت‌های سیستمی" board, rendered with the same components every screen uses. */
export default function SystemStates() {
  const f = (e: unknown) => { const x = friendly(e); return <Message tone={x.tone} title={x.title}>{x.text}</Message> }
  return (
    <>
      <PageBar title="حالت‌های سیستمی" sub="در همهٔ فهرست‌ها و فرم‌ها یکسان استفاده می‌شوند" />
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16 }}>
        <div style={{ flex: '1 1 420px' }}><Cap>فهرست خالی</Cap><Card><Empty title="هنوز سندی ثبت نشده" action={<Button variant="pri">سند جدید</Button>}>با «سند جدید» اولین سند را ثبت کنید.</Empty></Card></div>
        <div style={{ flex: '1 1 420px' }}><Cap>جستجو بدون نتیجه</Cap><Card><Empty title="نتیجه‌ای پیدا نشد" action={<Button>پاک‌کردن فیلترها</Button>}>فیلترها را تغییر دهید یا پاک کنید.</Empty></Card></div>
        <div style={{ flex: '1 1 420px' }}><Cap>در حال بارگذاری</Cap><Card><Skeleton /></Card></div>
        <div style={{ flex: '1 1 420px' }}><Cap>خطای ارتباط</Cap>{f(new ApiError(0, 'network', ''))}</div>
        <div style={{ flex: '1 1 420px' }}><Cap>عدم دسترسی</Cap>{f(new ApiError(403, 'forbidden', ''))}</div>
        <div style={{ flex: '1 1 420px' }}><Cap>نشست منقضی</Cap>{f(new ApiError(401, 'unauthorized', ''))}</div>
        <div style={{ flex: '1 1 420px' }}><Cap>درخواست تکراری</Cap><Message tone="ok" title="این عملیات قبلاً انجام شده">برای جلوگیری از ثبت دوباره، همان نتیجهٔ قبلی نمایش داده شد.</Message></div>
        <div style={{ flex: '1 1 420px' }}><Cap>تضاد داده</Cap>{f(new ApiError(409, 'conflict', 'tracking number is already used'))}</div>
        <div style={{ flex: '1 1 420px' }}><Cap>دورهٔ بسته</Cap>{f(new ApiError(409, 'period_closed', ''))}</div>
      </div>
    </>
  )
}
