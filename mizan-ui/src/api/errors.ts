export interface FieldError {
  field: string
  message: string
}

export interface ErrorBody {
  error?: { code: string; message: string; fields?: FieldError[]; request_id?: string }
}

/** An error answered by the API (or a network failure, status 0). The machine code is kept for the details line only. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly fields: FieldError[] = [],
    readonly requestId?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export interface Friendly {
  title: string
  text: string
  /** err = red, warn = amber, ok = green (duplicate request: the earlier result is shown) */
  tone: 'err' | 'warn' | 'ok'
}

// Friendly Persian texts from the design's states screens. The API code is shown only in "details".
const BY_CODE: Record<string, Friendly> = {
  network: { title: 'ارتباط با سرور برقرار نشد', text: 'اطلاعات واردشده حفظ شده است. دوباره تلاش کنید.', tone: 'err' },
  unauthorized: { title: 'نشست شما تمام شد', text: 'برای ادامه دوباره وارد شوید. سند در دست ثبت پاک نمی‌شود.', tone: 'warn' },
  forbidden: { title: 'دسترسی ندارید', text: 'این بخش مخصوص نقش مدیر مالی است.', tone: 'err' },
  account_locked: { title: 'حساب قفل شده است', text: 'پس از ۵ تلاش ناموفق، حساب برای ۱۵ دقیقه قفل می‌شود. بعداً دوباره تلاش کنید.', tone: 'err' },
  period_closed: { title: 'ثبت در دورهٔ بسته ممکن نیست', text: 'تاریخ سند را به دورهٔ باز تغییر دهید یا از مدیر مالی بخواهید دوره را باز کند.', tone: 'err' },
  no_fiscal_year: { title: 'سال مالی برای این تاریخ تعریف نشده', text: 'تاریخ را بررسی کنید یا سال مالی جدید باز کنید.', tone: 'err' },
  unknown_account: { title: 'حساب پیدا نشد', text: 'حسابی با این کد وجود ندارد.', tone: 'err' },
  account_inactive: { title: 'حساب غیرفعال است', text: 'روی حساب غیرفعال سند ثبت نمی‌شود.', tone: 'err' },
  already_reversed: { title: 'این سند قبلاً برگشت خورده', text: 'برای هر سند فقط یک سند برگشتی ممکن است.', tone: 'err' },
  cannot_reverse_reversal: { title: 'سند برگشتی را نمی‌شود برگشت زد', text: 'در صورت نیاز یک سند جدید ثبت کنید.', tone: 'err' },
  insufficient_funds: { title: 'موجودی کافی نیست', text: 'مبلغ از موجودی بیشتر است.', tone: 'err' },
  idempotency_busy: { title: 'درخواست در حال پردازش است', text: 'چند لحظه بعد دوباره تلاش کنید.', tone: 'warn' },
  idempotency_key_reused: { title: 'درخواست تکراری با محتوای متفاوت', text: 'صفحه را نوسازی کنید و دوباره تلاش کنید.', tone: 'err' },
  idempotency_key_required: { title: 'کلید یکتایی درخواست ارسال نشد', text: 'صفحه را نوسازی کنید و دوباره تلاش کنید.', tone: 'err' },
  year_not_closable: { title: 'سال مالی بسته نمی‌شود', text: 'موانع بستن سال را برطرف کنید.', tone: 'err' },
}

const BY_STATUS: Record<number, Friendly> = {
  400: { title: 'اطلاعات واردشده درست نیست', text: 'فیلدهای مشخص‌شده را اصلاح کنید.', tone: 'err' },
  401: BY_CODE.unauthorized!,
  403: BY_CODE.forbidden!,
  404: { title: 'پیدا نشد', text: 'رکورد مورد نظر وجود ندارد یا حذف شده است.', tone: 'err' },
  409: { title: 'تضاد داده', text: 'این عملیات با وضعیت فعلی داده‌ها هم‌خوان نیست.', tone: 'err' },
  503: { title: 'سرویس موقتاً در دسترس نیست', text: 'چند لحظه بعد دوباره تلاش کنید.', tone: 'warn' },
}

/** Maps any thrown value to the message shown to the accountant. */
export function friendly(e: unknown): Friendly & { details?: string } {
  if (e instanceof ApiError) {
    const f = BY_CODE[e.code] ?? BY_STATUS[e.status] ?? { title: 'خطای غیرمنتظره', text: 'عملیات انجام نشد. اگر تکرار شد با پشتیبانی تماس بگیرید.', tone: 'err' as const }
    // the server's own message is shown only for conflict/validation errors without a dedicated text
    const generic = !BY_CODE[e.code] && (e.status === 400 || e.status === 409) && e.message
    return { ...f, text: generic ? `${f.text} (${e.message})` : f.text, details: `${e.code}${e.requestId ? ' · ' + e.requestId : ''}` }
  }
  return { title: 'خطای غیرمنتظره', text: 'عملیات انجام نشد.', tone: 'err', details: String(e) }
}

/** Field-level messages keyed by the API field name. */
export function fieldErrors(e: unknown): Record<string, string> {
  return e instanceof ApiError ? Object.fromEntries(e.fields.map((f) => [f.field, f.message])) : {}
}
