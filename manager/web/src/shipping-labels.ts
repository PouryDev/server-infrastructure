// Persian labels for shipping phases, job states and server messages (shared by the panel views).
export const phases: Record<string, string> = {
  queued: "در صف اجرا",
  build: "ساخت image روی این سیستم",
  provisioning: "آماده‌سازی پروژه در مقصد",
  configuring: "ذخیرهٔ تنظیمات مقصد",
  routing: "اعمال دامنه در nginx",
  migrating: "اجرای migration",
  starting: "بررسی سلامت سرویس‌ها",
  "preserving-limits": "حفظ محدودیت‌های فعلی مقصد",
  rollback: "بازگردانی نسخهٔ سالم",
  ready: "مقصد آماده است",
  preflight: "بررسی اولیه",
  transfer: "انتقال imageها",
  verified: "تطبیق شناسهٔ image",
  deploy: "راه‌اندازی سرویس‌ها",
  healthy: "سرویس‌ها آماده‌اند",
  complete: "تکمیل شد",
  failed: "ناموفق",
  interrupted: "قطع‌شده",
};
export const jobStates: Record<string, string> = {
  queued: "در صف",
  running: "در حال اجرا",
  succeeded: "موفق",
  failed: "ناموفق",
  rolled_back: "بازگشت به نسخهٔ قبل",
  interrupted: "نیازمند بررسی مقصد",
};
export const messages: Record<string, string> = {
  "Building local image": "ساخت image از پوشهٔ پروژه در حال اجراست.",
  "Destination image content matches the reviewed local images": "محتوای imageهای دریافت‌شده با نسخهٔ محلی بررسی‌شده یکسان است.",
  "Configuring the destination project, saved domain and app env": "پروژه، دامنه و env روی سرور مقصد آماده می‌شوند.",
  "Deployment failed; previous containers and env restored. Database migrations are never rolled back automatically": "دیپلوی ناموفق بود؛ کانتینرها و env قبلی بازگردانده شدند. migration دیتابیس خودکار برگردانده نمی‌شود.",
  "Preparing local build": "ساخت از پوشهٔ فعلی پروژه آماده می‌شود.",
  "Shipment queued": "درخواست در صف اجرا قرار گرفت.",
  "Shipment completed": "ارسال و عملیات انتخاب‌شده با موفقیت تکمیل شد.",
  "Verifying local image IDs and configuration fingerprint":
    "imageهای محلی و تنظیمات با پیش‌نمایش تطبیق داده می‌شوند.",
  "Streaming existing local images to destination Docker (no registry)":
    "imageهای موجود از همین سیستم به Docker مقصد فرستاده می‌شوند.",
  "Destination image IDs match the reviewed local images":
    "شناسهٔ imageهای دریافت‌شده با نسخهٔ محلی یکسان است.",
  "Applying Compose configuration and waiting for selected services":
    "Compose اعمال شده؛ در انتظار آماده‌شدن سرویس‌های انتخاب‌شده.",
  "Compose deployment completed; destination manifest activated":
    "سرویس‌ها آماده‌اند و تنظیمات جدید در مقصد فعال شد.",
  "deployment failed health checks and was rolled back to the previous manifest":
    "آماده‌شدن سرویس‌ها ناموفق بود؛ تنظیمات نسخهٔ قبلی بازگردانده شد.",
  "Manager restarted; inspect destination before retrying":
    "مدیر restart شده است؛ وضعیت مقصد را پیش از تلاش مجدد بررسی کنید.",
  "Shipment interrupted; inspect destination before retrying":
    "ارسال قطع شده است؛ پیش از تکرار، وضعیت مقصد را بررسی کنید.",
};
export const warnings: Record<string, string> = {
  "Destination domain and app env are saved by its manager; local env files are never sent.": "دامنه و env برنامه روی مقصد ذخیره می‌شوند؛ فایل env لوکال منتقل نمی‌شود.",
  "All current destination container resource limits are preserved, including during rollback.": "تمام محدودیت‌های فعلی کانتینرهای مقصد، حتی هنگام بازگشت به نسخهٔ قبل، حفظ می‌شوند.",
  "Remote data, databases, volumes and queues are never copied or deleted.":
    "داده‌ها، دیتابیس، volumeها و صف‌های مقصد کپی یا حذف نمی‌شوند.",
  "Infrastructure deployment may restart shared services; bootstrap the destination data directories first.":
    "Deploy زیرساخت می‌تواند سرویس‌های مشترک را restart کند. مسیر داده‌ها و دسترسی‌های مقصد باید از قبل آماده باشند.",
  "Same-host sandbox: separate project/network/data paths; published ports and host networking are disabled.":
    "مقصد همین سیستم است: نام پروژه، شبکه و مسیر داده‌ها جداست؛ انتشار port و شبکهٔ host در این حالت غیرفعال است.",
  "Local env files will be transmitted privately in this release; existing destination env files will not be overwritten.":
    "فایل‌های env محلی در این release ارسال می‌شوند. فایل env فعلیِ مقصد بازنویسی نمی‌شود.",
  "CPU/memory limits changed on the server are kept; tick 'use limits from compose' to reset them.":
    "محدودیت‌های CPU/RAM که روی سرور تغییر داده‌اید حفظ می‌شوند؛ برای بازگشت به مقدار Compose گزینهٔ مربوط را فعال کنید.",
  "Limits from the Compose file will replace any limits set manually on the server.":
    "محدودیت‌های فایل Compose جایگزین تغییرهای دستی روی سرور می‌شوند.",
  "No previous apply record exists; every variable is listed because changes cannot be told apart.":
    "سابقهٔ اعمال قبلی وجود ندارد؛ چون تغییرها قابل تشخیص نیستند همهٔ متغیرها فهرست شده‌اند.",
  "No pending variable changes were found for the selected services; Compose will recreate only containers whose configuration changed.":
    "تغییر اعمال‌نشده‌ای برای سرویس‌های انتخابی پیدا نشد؛ Compose فقط کانتینرهایی را که پیکربندی‌شان تغییر کرده دوباره می‌سازد.",
  "Compose recreates only containers whose configuration actually changed; if the apply fails the previous variable files are restored.":
    "Compose فقط کانتینرهایی را که پیکربندی‌شان واقعاً تغییر کرده دوباره می‌سازد؛ در صورت شکست، فایل‌های قبلی متغیرها بازگردانده می‌شوند.",
  "Destination env files are preserved and must already exist when referenced.":
    "فایل‌های env مقصد حفظ می‌شوند و در صورت استفاده باید از قبل وجود داشته باشند.",
};


for (const [phase, label] of Object.entries(phases)) messages[`Destination project: ${phase}`] = label;
