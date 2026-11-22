import { useEffect, useState } from "react";
import {
  ArrowLeft,
  Check,
  CheckCircle2,
  ExternalLink,
  Loader2,
  Plus,
  Store,
  AlertCircle,
} from "lucide-react";
import { APIError, request } from "../api";

type Theme = {
  id: string;
  name: string;
  category: string;
  layout: string;
  accent: string;
  background: string;
};
type Job = {
  id: string;
  name: string;
  domain: string;
  status: string;
  phase: string;
  message: string;
  url: string;
  createdAt: number;
};
type Catalog = {
  images: string[];
  themes: Theme[];
  modules: { id: string; name: string }[];
  jobs: Job[];
};
const phaseNames: Record<string, string> = {
  queued: "در صف",
  preparing: "آماده‌سازی",
  provisioning: "تخصیص منابع",
  "resources-ready": "منابع آماده",
  starting: "اجرای کانتینر و migration",
  routing: "اتصال دامنه",
  ready: "آماده",
  failed: "نیازمند بررسی",
  interrupted: "اجرای متوقف‌شده",
};
export function Storefronts({
  csrf,
  onUnauthorized,
}: {
  csrf: string;
  onUnauthorized: () => void;
}) {
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [error, setError] = useState("");
  const [wizard, setWizard] = useState(false);
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [draft, setDraft] = useState({
    key: crypto.randomUUID(),
    name: "",
    domain: "",
    title: "",
    smsKey: "",
    smsTemplate: "",
    theme: "catalog",
    accent: "#17655d",
    ownerPhone: "",
    adminPhone: "",
    demo: true,
    image: "",
    modules: ["blog", "attachments", "discounts", "wishlist", "analytics"],
  });
  async function refresh() {
    try {
      const result = await request<Catalog>("/storefront/catalog");
      setCatalog(result);
      setError("");
      setDraft((d) => ({ ...d, image: d.image || result.images[0] || "" }));
    } catch (e) {
      if (e instanceof APIError && e.status === 401) onUnauthorized();
      else setError((e as Error).message);
    }
  }
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 4000);
    return () => clearInterval(timer);
  }, []);
  function validStep() {
    if (step === 0)
      return (
        /^[a-z][a-z0-9-]{1,30}$/.test(draft.name) &&
        draft.title.trim() &&
        /^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$/.test(
          draft.domain,
        ) &&
        draft.image &&
        (!draft.demo || draft.domain.endsWith(".localhost"))
      );
    if (step === 2)
      return (
        /^\+989[0-9]{9}$/.test(draft.ownerPhone) &&
        /^\+989[0-9]{9}$/.test(draft.adminPhone) &&
        draft.ownerPhone !== draft.adminPhone &&
        (draft.demo || (draft.smsKey && draft.smsTemplate))
      );
    return true;
  }
  async function create() {
    setBusy(true);
    setError("");
    try {
      await request(
        "/storefront/create",
        { method: "POST", body: JSON.stringify(draft) },
        csrf,
      );
      setWizard(false);
      setStep(0);
      setDraft((d) => ({
        ...d,
        key: crypto.randomUUID(),
        name: "",
        domain: "",
        title: "",
        smsKey: "",
        smsTemplate: "",
      }));
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="store-builder">
      <section className="panel builder-welcome">
        <div>
          <span className="builder-eyebrow">STOREFRONT STUDIO</span>
          <h2>از یک انتخاب، تا یک فروشگاه.</h2>
          <p>
            طراحی، امکانات و مالک را انتخاب کنید؛ تخصیص منابع و راه‌اندازی روی
            همین میزبان انجام می‌شود.
          </p>
        </div>
        <button
          className="primary"
          onClick={() => {
            setWizard(true);
            setStep(0);
          }}
          disabled={!catalog?.images.length}
        >
          <Plus size={17} /> ساخت فروشگاه
        </button>
      </section>
      {error && (
        <div className="alert danger" role="alert">
          <AlertCircle size={17} />
          {error}
          <button onClick={() => void refresh()}>تلاش دوباره</button>
        </div>
      )}
      {!catalog && !error && (
        <div className="empty">
          <Loader2 className="spin" /> دریافت فروشگاه‌ها…
        </div>
      )}
      {catalog && !catalog.images.length && (
        <div className="alert warning">
          هنوز image فروشگاه ساخته نشده است. روی میزبان، فرمان make
          storefront-image را اجرا کنید.
        </div>
      )}
      {wizard && catalog && (
        <section className="panel builder-launcher">
          <div className="panel-heading">
            <h2>فروشگاه تازه</h2>
            <button
              className="ghost"
              onClick={() => setWizard(false)}
              disabled={busy}
            >
              بستن
            </button>
          </div>
          <div className="builder-steps">
            {["مشخصات", "طراحی", "مالک و امکانات", "مرور و ساخت"].map(
              (title, i) => (
                <button
                  key={title}
                  disabled={busy || i > step}
                  className={step === i ? "active" : i < step ? "done" : ""}
                  onClick={() => setStep(i)}
                >
                  <span>{i < step ? <Check size={12} /> : i + 1}</span>
                  {title}
                </button>
              ),
            )}
          </div>
          {step === 0 && (
            <div className="builder-fields">
              <label>
                نام فروشگاه
                <input
                  value={draft.title}
                  onChange={(e) =>
                    setDraft({ ...draft, title: e.target.value })
                  }
                  placeholder="مثلاً هِرا"
                />
              </label>
              <label>
                شناسه یکتا
                <input
                  dir="ltr"
                  value={draft.name}
                  onChange={(e) => {
                    const name = e.target.value.toLowerCase();
                    setDraft({
                      ...draft,
                      name,
                      domain: draft.demo ? name + ".localhost" : draft.domain,
                    });
                  }}
                  placeholder="my-shop"
                />
                <small>حروف انگلیسی کوچک، عدد و خط تیره</small>
              </label>
              <label>
                دامنه
                <input
                  dir="ltr"
                  value={draft.domain}
                  onChange={(e) =>
                    setDraft({ ...draft, domain: e.target.value.toLowerCase() })
                  }
                  placeholder="my-shop.localhost"
                />
              </label>
              <label>
                نسخه برنامه
                <select
                  dir="ltr"
                  value={draft.image}
                  onChange={(e) =>
                    setDraft({ ...draft, image: e.target.value })
                  }
                >
                  {catalog.images.map((i) => (
                    <option key={i}>{i}</option>
                  ))}
                </select>
              </label>
              <label className="builder-checkbox">
                <input
                  type="checkbox"
                  checked={draft.demo}
                  onChange={(e) =>
                    setDraft({ ...draft, demo: e.target.checked })
                  }
                />{" "}
                محیط آزمایشی محلی؛ پرداخت و OTP واقعی انجام نمی‌شود
              </label>
              <p className="hint">
                برای محیط زنده، دامنه، DNS، اطلاعات پیامک و پذیرندگی باید روی
                همین میزبان آماده باشند. حساب‌های اولیه با رمز تصادفی ساخته
                می‌شوند.
              </p>
            </div>
          )}
          {step === 1 && (
            <>
              <p className="hint">
                انتخاب طراحی فقط در دسترس شماست. فروشنده تنظیمات برند و محتوای
                فروشگاه خودش را می‌بیند.
              </p>
              <label className="builder-brand-color">
                رنگ برند؛ مستقل از سبک قالب{" "}
                <input
                  type="color"
                  value={draft.accent}
                  onChange={(e) =>
                    setDraft({ ...draft, accent: e.target.value })
                  }
                />
                <small>
                  هر قالب با رنگ دلخواه و محتوای فروشگاه شما اجرا می‌شود.
                </small>
              </label>
              <div className="builder-themes">
                {catalog.themes.map((t) => (
                  <button
                    key={t.id}
                    className={
                      "builder-theme " +
                      (draft.theme === t.id ? "selected" : "")
                    }
                    data-theme={t.id}
                    data-layout={t.layout}
                    onClick={() =>
                      setDraft({
                        ...draft,
                        theme: t.id,
                        accent:
                          draft.accent ===
                          catalog.themes.find((x) => x.id === draft.theme)
                            ?.accent
                            ? t.accent
                            : draft.accent,
                      })
                    }
                  >
                    <div
                      className="builder-theme-art"
                      style={{ background: t.background, color: t.accent }}
                    >
                      <i />
                      <div>
                        <b />
                        <span />
                      </div>
                      <footer>
                        <i />
                        <i />
                        <i />
                      </footer>
                    </div>
                    <strong>{t.name}</strong>
                    <small>{t.category}</small>
                    {draft.theme === t.id && <CheckCircle2 size={16} />}
                  </button>
                ))}
              </div>
            </>
          )}
          {step === 2 && (
            <>
              <div className="builder-fields">
                <label>
                  موبایل مالک فروشگاه
                  <input
                    dir="ltr"
                    type="tel"
                    value={draft.ownerPhone}
                    onChange={(e) =>
                      setDraft({ ...draft, ownerPhone: e.target.value })
                    }
                    placeholder="+989121234567"
                  />
                  <small>ورود با کد پیامکی یا رمز عبور</small>
                </label>
                <label>
                  موبایل توسعه‌دهنده / ادمین
                  <input
                    dir="ltr"
                    type="tel"
                    value={draft.adminPhone}
                    onChange={(e) =>
                      setDraft({ ...draft, adminPhone: e.target.value })
                    }
                    placeholder="+989121234568"
                  />
                  <small>
                    ادمین به پنل فروشنده و تنظیمات طراحی دسترسی دارد.
                  </small>
                </label>
                {!draft.demo && (
                  <>
                    <label>
                      کلید API کاوه‌نگار
                      <input
                        type="password"
                        dir="ltr"
                        autoComplete="off"
                        value={draft.smsKey}
                        onChange={(e) =>
                          setDraft({ ...draft, smsKey: e.target.value })
                        }
                      />
                      <small>فقط در تنظیمات خصوصی نمونه ذخیره می‌شود.</small>
                    </label>
                    <label>
                      نام الگوی OTP
                      <input
                        dir="ltr"
                        value={draft.smsTemplate}
                        onChange={(e) =>
                          setDraft({ ...draft, smsTemplate: e.target.value })
                        }
                      />
                    </label>
                  </>
                )}
              </div>
              <h3>امکانات فعال</h3>
              <div className="builder-modules">
                {catalog.modules.map((m) => (
                  <label key={m.id}>
                    <input
                      type="checkbox"
                      checked={draft.modules.includes(m.id)}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          modules: e.target.checked
                            ? [...draft.modules, m.id]
                            : draft.modules.filter((id) => id !== m.id),
                        })
                      }
                    />
                    <strong>{m.name}</strong>
                  </label>
                ))}
              </div>
            </>
          )}
          {step === 3 && (
            <div className="builder-review">
              <div>
                <Store size={30} />
                <h2>{draft.title}</h2>
                <p dir="ltr">{draft.domain}</p>
              </div>
              <dl>
                <dt>شناسه</dt>
                <dd dir="ltr">{draft.name}</dd>
                <dt>طراحی</dt>
                <dd>
                  {catalog.themes.find((t) => t.id === draft.theme)?.name}
                </dd>
                <dt>امکانات</dt>
                <dd>
                  {catalog.modules
                    .filter((m) => draft.modules.includes(m.id))
                    .map((m) => m.name)
                    .join("، ")}
                </dd>
                <dt>مالک</dt>
                <dd dir="ltr">{draft.ownerPhone}</dd>
                <dt>ادمین</dt>
                <dd dir="ltr">{draft.adminPhone}</dd>
                <dt>محیط</dt>
                <dd>{draft.demo ? "آزمایشی محلی" : "زنده"}</dd>
              </dl>
              <p className="hint">
                ساخت با job مستقل ادامه پیدا می‌کند. بستن این صفحه آن را متوقف
                نمی‌کند. داده‌های فروشگاه و دسترسی منابع مستقل خواهند بود.
              </p>
            </div>
          )}
          <div className="builder-actions">
            <button
              className="secondary"
              disabled={step === 0 || busy}
              onClick={() => setStep(step - 1)}
            >
              مرحله قبل
            </button>
            {step < 3 ? (
              <button
                className="primary"
                disabled={!validStep()}
                onClick={() => setStep(step + 1)}
              >
                ادامه <ArrowLeft size={16} />
              </button>
            ) : (
              <button
                className="primary"
                disabled={busy}
                onClick={() => void create()}
              >
                {busy ? (
                  <Loader2 className="spin" size={16} />
                ) : (
                  <Plus size={16} />
                )}
                ساخت و راه‌اندازی فروشگاه
              </button>
            )}
          </div>
        </section>
      )}
      {catalog && (
        <section className="panel">
          <div className="panel-heading">
            <div>
              <h2>فروشگاه‌های ساخته‌شده</h2>
              <p>
                وضعیت ساخت از runner دریافت می‌شود؛ اطلاعات محرمانه در این صفحه
                نمایش داده نمی‌شود.
              </p>
            </div>
            <span className="badge">
              {catalog.jobs.length} فروشگاه / درخواست
            </span>
          </div>
          {!catalog.jobs.length ? (
            <div className="empty">
              <Store />
              <h3>اولین فروشگاه را بسازید</h3>
              <p>قالب و امکانات را انتخاب کنید و ساخت را به زیرساخت بسپارید.</p>
            </div>
          ) : (
            <div className="builder-jobs">
              {catalog.jobs.map((j) => (
                <article key={j.id}>
                  <div className="builder-job-icon">
                    <Store size={20} />
                  </div>
                  <div>
                    <strong>{j.name}</strong>
                    <small dir="ltr">{j.domain}</small>
                    <p>{phaseNames[j.phase] || j.phase}</p>
                    {j.message && <p className="danger-text">{j.message}</p>}
                  </div>
                  <span
                    className={
                      "badge " + (j.status === "ready" ? "success" : "")
                    }
                  >
                    {j.status === "ready"
                      ? "آماده"
                      : j.status === "running"
                        ? "در حال ساخت"
                        : phaseNames[j.status] || j.status}
                  </span>
                  {j.status === "ready" && j.url && (
                    <a
                      className="secondary"
                      href={j.url}
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      مشاهده <ExternalLink size={14} />
                    </a>
                  )}
                </article>
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
