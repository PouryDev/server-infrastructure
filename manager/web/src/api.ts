export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}
export async function request<T>(
  path: string,
  init: RequestInit = {},
  csrf?: string,
): Promise<T> {
  const response = await fetch(`/api${path}`, {
    ...init,
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(csrf ? { "X-CSRF-Token": csrf } : {}),
      ...init.headers,
    },
  });
  if (!response.ok) {
    let message = "ارتباط با سرور برقرار نشد. دوباره تلاش کنید.";
    try {
      const body = await response.json();
      message = errors[body.error] ?? body.error ?? message;
    } catch {
      /* An upstream may return HTML. */
    }
    throw new APIError(message, response.status);
  }
  return response.status === 204 ? (undefined as T) : response.json();
}
const errors: Record<string, string> = {
  "Invalid VPN request": "درخواست VPN معتبر نیست؛ وضعیت را بازخوانی کنید.",
  "Invalid VPN link": "لینک اتصال معتبر نیست؛ یک لینک تکی کامل وارد کنید.",
  "Unsupported VPN protocol": "پروتکل لینک پشتیبانی نمی‌شود؛ VLESS، VMess، Trojan یا Shadowsocks وارد کنید.",
  "Unsupported VPN link options": "بعضی گزینه‌های این لینک پشتیبانی نمی‌شوند.",
  "Unsupported VPN transport": "نوع انتقال این لینک پشتیبانی نمی‌شود.",
  "Unsupported VPN security": "نوع امنیت این لینک پشتیبانی نمی‌شود.",
  "Invalid REALITY settings": "لینک REALITY باید SNI و کلید عمومی معتبر داشته باشد.",
  "Insecure VPN links are not allowed": "لینک با اعتبارسنجی غیرفعال گواهی پذیرفته نمی‌شود.",
  "VPN settings changed; refresh before saving": "تنظیمات VPN تغییر کرده؛ وضعیت را بازخوانی و دوباره ذخیره کنید.",
  "Enter a VPN link first": "ابتدا لینک اتصال VPN را وارد کنید.",
  "Xray rejected VPN configuration": "Xray پیکربندی این لینک را نپذیرفت؛ تنظیمات قبلی حفظ شد.",
  "Xray could not start": "اجرای Xray ناموفق بود؛ وضعیت سرویس را بررسی کنید.",
  "VPN storage unavailable": "ذخیره‌سازی تنظیمات VPN در دسترس نیست.",
  "VPN change failed; previous connection could not restart": "تغییر ناموفق بود و اتصال قبلی هم اجرا نشد؛ وضعیت Xray را بررسی کنید.",
  "Enable VPN before testing": "ابتدا اتصال VPN را فعال کنید.",
  "VPN connectivity test failed": "دسترسی اینترنت از مسیر VPN برقرار نشد؛ لینک و اتصال سرور را بررسی کنید.",
  "VPN service unavailable": "سرویس Xray در دسترس نیست؛ وضعیت کانتینر را بررسی کنید.",
  "automatic project deployment requires an SSH destination": "برای دیپلوی خودکار، مقصد SSH را در ارسال و Deploy ← تنظیم مقصد سراسری تنظیم کنید.",
  "destination manager bridge unavailable; update the destination manager and check SSH": "ارتباط با manager مقصد برقرار نشد؛ نسخهٔ manager سرور، کلید SSH و مسیر زیرساخت را بررسی کنید.",
  "destination paths do not match the target manager": "مسیرهای مقصد باید با تنظیمات manager سرور یکی باشند: /srv/infra، /srv/apps و /srv/data.",
  "Project settings changed; refresh before saving": "تنظیمات مقصد تغییر کرده است؛ آن را بازخوانی و دوباره پیش‌نمایش بگیرید.",
  "Domain is already routed by another project": "این دامنه در nginx به پروژهٔ دیگری متصل است.",
  "Enter a domain without scheme, port or path": "فقط نام دامنه را بدون http، پورت و مسیر وارد کنید.",

  "invalid resource limits": "حافظه یا CPU خارج از محدودهٔ مجاز است.",
  "resource source is unavailable": "مسیر فایل‌های پروژه برای ویرایش در دسترس نیست؛ تنظیمات runner را بررسی کنید.",
  "Compose source is missing or symlinked": "فایل Compose پیدا نشد یا مسیر آن لینک است. مسیر اصلی را در runner معرفی کنید.",
  "resource preview is stale; preview again": "محدودیت‌ها یا فایل Compose تغییر کرده است؛ دوباره بازبینی کنید.",
  "Git requires a clean repository and tracked Compose file": "برای commit، مخزن باید بدون تغییر ثبت‌نشده و فایل Compose در Git ثبت شده باشد.",
  "Git push is disabled on this server": "push روی این سرور غیرفعال است.",
  "configure an origin upstream before pushing": "پیش از push، شاخهٔ فعلی باید upstream هم‌نام در origin داشته باشد.",
  "Git fetch failed; nothing changed": "دریافت وضعیت Git ناموفق بود؛ تغییری اعمال نشد.",
  "Git branch must match origin before pushing": "شاخه با origin همگام نیست؛ ابتدا commitهای محلی یا تغییرات remote را بررسی کنید.",
  "no eligible service containers": "کانتینری برای این سرویس پیدا نشد.",
  "container discovery failed": "دریافت فهرست کانتینرها ناموفق بود.",
  "container inspection failed": "خواندن محدودیت‌های فعلی کانتینر ناموفق بود.",
  "container is outside resource scope": "کانتینر خارج از محدودهٔ مجاز تغییر منابع است.",
  "one-shot services cannot be resized": "تغییر منابع سرویس‌های موقت مهاجرت و راه‌اندازی پشتیبانی نمی‌شود.",
  "service requires a block-style Compose definition": "سرویس باید به‌صورت چندخطی در فایل Compose تعریف شود.",
  "resource limits require block-style fields": "محدودیت‌های Compose را به‌صورت چندخطی تعریف کنید.",
  "deploy limits require block-style fields": "محدودیت‌های deploy را به‌صورت چندخطی تعریف کنید.",
  "Compose source is not writable": "دسترسی نوشتن فایل Compose وجود ندارد.",
  "another resource change is active": "تغییر منابع دیگری در حال اجراست؛ کمی بعد دوباره تلاش کنید.",
  "resource runner unavailable; inspect service state before retrying": "ارتباط با سرویس تنظیم منابع قطع شد. قبل از تکرار، وضعیت سرویس و تاریخچه را بررسی کنید.",
  "Compose validation failed; previous limits restored": "اعتبارسنجی Compose ناموفق بود؛ تنظیمات قبلی بازگردانده شد.",
  "Container update failed; previous limits restored": "اعمال محدودیت ناموفق بود؛ تنظیمات قبلی بازگردانده شد.",
  "Git commit failed; previous limits restored": "ثبت commit ناموفق بود؛ تنظیمات قبلی بازگردانده شد.",
  "choose local or ssh target": "نوع مقصد را انتخاب کنید.",
  "invalid SSH port": "پورت SSH باید بین ۱ و ۶۵۵۳۵ باشد.",
  "invalid SSH host, user or identity":
    "Host، کاربر SSH یا نام فایل کلید معتبر نیست.",
  "use distinct absolute destination paths":
    "برای مقصد، مسیرهای مطلق و مستقل انتخاب کنید.",
  "destination directories must not overlap":
    "مسیر زیرساخت، پروژه‌ها و داده‌ها نباید داخل یکدیگر باشند.",
  "local targets must stay below /tmp/infra-manager-target/":
    "مسیرهای آزمایشی محلی باید زیر /tmp/infra-manager-target/ باشند.",
  "deployment preview expired; prepare it again":
    "پیش‌نمایش منقضی شده یا مقصد تغییر کرده؛ دوباره پیش‌نمایش بگیرید.",
  "a shipment is already active or this preview was already used":
    "یک ارسال در حال اجراست یا این پیش‌نمایش قبلاً اجرا شده است.",
  "renew the suspended project before deployment":
    "پیش از Deploy، اعتبار پروژهٔ متوقف‌شده را تمدید کنید.",
  "SSH identity is unavailable": "فایل کلید SSH در دسترس نیست.",
  "trusted SSH known_hosts file is missing":
    "فایل known_hosts معتبر در پوشهٔ کلیدهای مدیر موجود نیست.",
  "destination command failed; check connectivity, host key, permissions and Docker prerequisites":
    "ارتباط یا اجرای دستور مقصد ناموفق بود. اتصال، کلید معتبر میزبان، دسترسی کاربر و نصب Docker/Compose را بررسی کنید.",
  "shipping runner unavailable; inspect any active destination deployment":
    "سرویس ارسال در دسترس نیست. اگر job فعالی دارید، وضعیت مقصد را پیش از تکرار بررسی کنید.",
  "Invalid username or password": "نام کاربری یا رمز عبور درست نیست.",
  "Too many attempts. Try again in five minutes.":
    "تعداد تلاش‌ها زیاد است. پنج دقیقه دیگر تلاش کنید.",
  "Please sign in": "نشست شما تمام شده؛ دوباره وارد شوید.",
  "project policy changed; refresh and retry":
    "تنظیمات پروژه تغییر کرده است. پس از همگام‌سازی دوباره تلاش کنید.",
  "Choose a future expiration time": "زمان انقضا باید در آینده باشد.",
  "Change could not be persisted; retry after synchronization":
    "تغییر ذخیره نشد؛ پس از همگام‌سازی دوباره تلاش کنید.",
  "Audit storage unavailable": "ثبت رویدادها در دسترس نیست. دوباره تلاش کنید.",
};
