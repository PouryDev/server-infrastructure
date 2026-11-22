import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  ArrowLeft,
  Box,
  CheckCircle2,
  ChevronLeft,
  CircleHelp,
  Clock3,
  Globe2,
  HardDrive,
  KeyRound,
  Laptop,
  LoaderCircle,
  PackageCheck,
  RefreshCw,
  Rocket,
  Server,
  Settings2,
  ShieldCheck,
  Upload,
  XCircle,
} from "lucide-react";
import { APIError, request } from "../api";
import { bytes, date, number } from "../format";
import type {
  ShipCatalog,
  ShipJob,
  ShipPlan,
  ShipTarget,
} from "../shipping-types";
import { Modal } from "../components/Modal";
import { describeLimit } from "../shipping-format";
import { jobStates, messages, phases, warnings } from "../shipping-labels";

export function Shipping({
  csrf,
  onUnauthorized,
}: {
  csrf: string;
  onUnauthorized: () => void;
}) {
  const [target, setTarget] = useState<ShipTarget | null>(null);
  const [draft, setDraft] = useState<ShipTarget | null>(null);
  const [catalog, setCatalog] = useState<ShipCatalog | null>(null);
  const [jobs, setJobs] = useState<ShipJob[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
  const [settings, setSettings] = useState(false);
  const [mode, setMode] = useState<"deploy" | "image">("deploy");
  const [project, setProject] = useState("infra");
  const [service, setService] = useState("");
  const [image, setImage] = useState("");
  const [includeEnv, setIncludeEnv] = useState(false);
  const [useCompose, setUseCompose] = useState(false);
  const [plan, setPlan] = useState<ShipPlan | null>(null);
  const fail = useCallback(
    (e: unknown) => {
      if (e instanceof APIError && e.status === 401) onUnauthorized();
      else setError((e as Error).message);
    },
    [onUnauthorized],
  );
  const load = useCallback(async () => {
    setBusy("load");
    try {
      const [t, c, j] = await Promise.all([
        request<ShipTarget>("/shipping/target"),
        request<ShipCatalog>("/shipping/catalog"),
        request<ShipJob[]>("/shipping/jobs"),
      ]);
      setTarget(t);
      setDraft(t);
      setCatalog(c);
      setJobs(j);
      setError("");
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }, [fail]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const id = setInterval(() => {
      void request<ShipJob[]>("/shipping/jobs").then(setJobs).catch(fail);
    }, 3000);
    return () => clearInterval(id);
  }, [fail]);
  const active = jobs.find(
    (j) => j.status === "running" || j.status === "queued",
  );
  async function saveTarget(e: FormEvent) {
    e.preventDefault();
    if (!draft) return;
    setBusy("settings");
    setError("");
    try {
      const saved = await request<ShipTarget>(
        "/shipping/target",
        { method: "PUT", body: JSON.stringify(draft) },
        csrf,
      );
      setTarget(saved);
      setDraft(saved);
      setPlan(null);
      setSettings(false);
      setNotice("مقصد سراسری ذخیره شد؛ پیش‌نمایش‌های قبلی اعتبار ندارند.");
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  async function probe() {
    setBusy("probe");
    setError("");
    try {
      const p = await request<{
        platform: string;
        docker: string;
        compose: string;
      }>("/shipping/probe", { method: "POST" }, csrf);
      setNotice(
        `اتصال موفق · ${p.platform} · Docker ${p.docker} · Compose ${p.compose}`,
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  async function prepare(e: FormEvent) {
    e.preventDefault();
    setBusy("plan");
    setError("");
    try {
      setPlan(
        await request<ShipPlan>(
          "/shipping/plans",
          {
            method: "POST",
            body: JSON.stringify({
              project: mode === "deploy" ? project : "",
              service: mode === "deploy" ? service : "",
              image: mode === "image" || service ? image : "",
              mode,
              includeEnv: mode === "deploy" && includeEnv,
              useComposeLimits: mode === "deploy" && useCompose,
            }),
          },
          csrf,
        ),
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  async function commit() {
    if (!plan) return;
    setBusy("commit");
    setError("");
    try {
      const job = await request<ShipJob>(
        "/shipping/jobs",
        { method: "POST", body: JSON.stringify({ planId: plan.id }) },
        csrf,
      );
      setJobs((old) => [job, ...old]);
      setPlan(null);
      setNotice(
        "Job ثبت شد؛ می‌توانید صفحه را ببندید و بعداً نتیجه را ببینید.",
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  if (!target || !catalog)
    return (
      <div className="panel loading-state">
        {error ? (
          <>
            <XCircle />
            <p role="alert">{error}</p>
            <button className="secondary" onClick={() => void load()}>
              تلاش دوباره
            </button>
          </>
        ) : (
          <>
            <RefreshCw className="spin" />
            <p>در حال دریافت مقصد و imageهای محلی…</p>
          </>
        )}
      </div>
    );
  return (
    <div className="shipping-view">
      {error && (
        <div className="alert" role="alert">
          <XCircle size={17} />
          {error}
        </div>
      )}
      {notice && (
        <div className="ship-notice" role="status">
          <CheckCircle2 size={17} />
          <span>{notice}</span>
          <button
            className="icon-button"
            aria-label="بستن پیام ارسال"
            onClick={() => setNotice("")}
          >
            ×
          </button>
        </div>
      )}
      <section className="ship-target panel">
        <span className="ship-target-icon">
          {target.mode === "local" ? <Laptop /> : <Globe2 />}
        </span>
        <div>
          <div className="eyebrow">مقصد سراسری / GLOBAL TARGET</div>
          <h2>
            {target.mode === "local" ? (
              "همین سیستم · محیط آزمایشی"
            ) : (
              <bdi>
                {target.user}@{target.host}:{target.port}
              </bdi>
            )}
          </h2>
          <p dir="ltr">
            {target.infraDir}
            <ChevronLeft size={12} />
            {target.appsDir}
          </p>
        </div>
        <div className="ship-target-actions">
          <span className="badge green">
            <ShieldCheck size={12} />
            {target.mode === "local"
              ? "مقصد محلی و جدا"
              : "SSH با host key معتبر"}
          </span>
          <button
            className="secondary"
            disabled={!!busy}
            onClick={() => void probe()}
          >
            <RefreshCw size={14} className={busy === "probe" ? "spin" : ""} />{" "}
            بررسی اتصال
          </button>
          <button
            className="icon-button"
            aria-label="تنظیم مقصد سراسری"
            title="تنظیم مقصد سراسری"
            onClick={() => {
              setDraft(target);
              setError("");
              setSettings(true);
            }}
          >
            <Settings2 />
          </button>
        </div>
      </section>
      <div className="ship-grid">
        <section className="panel ship-form-panel">
          <div className="panel-heading">
            <div>
              <h2>
                <Rocket size={19} /> ارسال از سیستم محلی
              </h2>
              <p>
                image موجود را انتخاب کنید؛ build یا registry در مقصد لازم نیست.
              </p>
            </div>
            <button
              className="icon-button"
              disabled={!!busy}
              aria-label="بازخوانی imageهای محلی"
              onClick={() => void load()}
            >
              <RefreshCw size={16} />
            </button>
          </div>
          <form className="ship-form" onSubmit={prepare}>
            <div className="ship-mode">
              <button
                type="button"
                aria-pressed={mode === "deploy"}
                className={mode === "deploy" ? "selected" : ""}
                onClick={() => {
                  setMode("deploy");
                  setImage("");
                }}
              >
                <Rocket size={16} />
                <span>
                  ارسال و Deploy<small>کل پروژه یا یک سرویس</small>
                </span>
              </button>
              <button
                type="button"
                aria-pressed={mode === "image"}
                className={mode === "image" ? "selected" : ""}
                onClick={() => {
                  setMode("image");
                  setImage("");
                }}
              >
                <Upload size={16} />
                <span>
                  فقط ارسال Image<small>بدون تغییر سرویس‌های مقصد</small>
                </span>
              </button>
            </div>
            {mode === "deploy" && (
              <div className="ship-fields">
                <label>
                  پروژه
                  <select
                    aria-label="پروژه برای ارسال"
                    value={project}
                    onChange={(e) => {
                      setProject(e.target.value);
                      setService("");
                      setImage("");
                    }}
                  >
                    {catalog.projects.map((p) => (
                      <option key={p.name} value={p.name}>
                        {p.name === "infra" ? "infra · زیرساخت مشترک" : p.name}
                        {p.error ? " (تنظیمات در دسترس نیست)" : ""}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  محدودهٔ Deploy
                  <select
                    aria-label="سرویس برای ارسال"
                    value={service}
                    onChange={(e) => {
                      setService(e.target.value);
                      setImage("");
                    }}
                  >
                    <option value="">همهٔ سرویس‌های پروژه</option>
                    {catalog.projects
                      .find((p) => p.name === project)
                      ?.services.map((s) => (
                        <option value={s} key={s}>
                          {s}
                        </option>
                      ))}
                  </select>
                </label>
              </div>
            )}
            {(mode === "image" || service) && (
              <label className="ship-field">
                {mode === "image" ? "Image محلی" : "Image سرویس"}
                <select
                  aria-label="image محلی"
                  value={image}
                  onChange={(e) => setImage(e.target.value)}
                  required={mode === "image"}
                >
                  <option value="">
                    {mode === "image"
                      ? "یک image انتخاب کنید"
                      : "نسخهٔ فعلی در Compose محلی"}
                  </option>
                  {catalog.images.map((i) => (
                    <option value={i.ref} key={i.ref}>
                      {i.ref} · {bytes(i.size)} · {i.platform}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {mode === "deploy" && (
              <label className="checkbox ship-env">
                <input
                  type="checkbox"
                  checked={includeEnv}
                  onChange={(e) => setIncludeEnv(e.target.checked)}
                />
                <span>
                  ارسال فایل‌های env محلی
                  <small>
                    شامل اطلاعات اتصال و secretها؛ پیش‌فرض، استفاده از env موجود
                    در مقصد است.
                  </small>
                </span>
              </label>
            )}
            {mode === "deploy" && (
              <label className="checkbox ship-env">
                <input
                  type="checkbox"
                  checked={useCompose}
                  onChange={(e) => setUseCompose(e.target.checked)}
                />
                <span>
                  استفاده از محدودیت‌های Compose به‌جای سرور
                  <small>
                    پیش‌فرض خاموش: محدودیت CPU/RAM که روی سرور تغییر داده‌اید
                    با Deploy بازنشانی نمی‌شود.
                  </small>
                </span>
              </label>
            )}
            <div className="ship-help">
              <CircleHelp size={15} />
              <p>
                {mode === "image"
                  ? "Image در Docker مقصد بارگذاری و شناسهٔ آن تطبیق داده می‌شود. هیچ کانتینری restart نمی‌شود."
                  : service
                    ? "فقط سرویس انتخاب‌شده با --no-deps به‌روز می‌شود. تنظیمات سایر سرویس‌های مقصد حفظ می‌شود."
                    : "تمام imageهای لازم ارسال می‌شوند و Compose پروژه در مقصد راه‌اندازی می‌شود."}
              </p>
            </div>
            <button
              className="primary ship-preview-button"
              disabled={!!busy || !!active}
            >
              {busy === "plan" ? (
                <LoaderCircle className="spin" size={16} />
              ) : (
                <PackageCheck size={16} />
              )}
              پیش‌نمایش ارسال
              <ArrowLeft size={16} />
            </button>
            {active && (
              <p className="footnote">
                تا تکمیل job فعال، ارسال دیگری شروع نمی‌شود.
              </p>
            )}
          </form>
        </section>
        <aside className="panel ship-explainer">
          <span className="ship-illustration">
            <Box size={43} />
            <ArrowLeft size={23} />
            <Server size={43} />
          </span>
          <div className="eyebrow">LOCAL → DESTINATION</div>
          <h3>همان image، در مقصد.</h3>
          <p>
            نسخهٔ بررسی‌شده با شناسهٔ ثابت ارسال می‌شود. پیش از deploy، شناسهٔ
            image در مقصد دوباره کنترل می‌شود.
          </p>
          <ol>
            <li>
              <span>۱</span>
              <div>
                انتخاب و پیش‌نمایش
                <small>مقصد، سرویس‌ها، imageها و فایل‌های ارسالی</small>
              </div>
            </li>
            <li>
              <span>۲</span>
              <div>
                انتقال مستقیم<small>Docker save / load بدون registry</small>
              </div>
            </li>
            <li>
              <span>۳</span>
              <div>
                راه‌اندازی و بررسی
                <small>انتظار برای آمادگی؛ بازگشت در صورت شکست</small>
              </div>
            </li>
          </ol>
          <div className="ship-privacy">
            <ShieldCheck size={17} /> محتویات env و کلید SSH وارد خروجی پنل و
            audit نمی‌شوند.
          </div>
        </aside>
      </div>
      <section className="panel ship-jobs">
        <div className="panel-heading">
          <div>
            <h2>
              <Clock3 size={18} /> ارسال‌های اخیر{" "}
              <span className="count-pill">{number(jobs.length, 0)}</span>
            </h2>
            <p>jobها مستقل از بازبودن صفحه اجرا و در تاریخچه ثبت می‌شوند.</p>
          </div>
          {active && (
            <span className="live-label">
              <span className="dot green" />
              در حال اجرا
            </span>
          )}
        </div>
        {jobs.length === 0 ? (
          <div className="empty-state">
            <Upload />
            <h3>اولین ارسال را آماده کنید</h3>
            <p>پس از تأیید پیش‌نمایش، روند اجرا اینجا نمایش داده می‌شود.</p>
          </div>
        ) : (
          <div className="table-scroll">
            <table className="audit-table">
              <thead>
                <tr>
                  <th>پروژه / شناسه</th>
                  <th>وضعیت</th>
                  <th>مرحله و نتیجه</th>
                  <th>زمان</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((j) => (
                  <tr key={j.id}>
                    <td>
                      <strong dir="ltr">{j.project || "Image transfer"}</strong>
                      <small dir="ltr">{j.id.slice(0, 12)}</small>
                    </td>
                    <td>
                      <span
                        className={`badge ${j.status === "succeeded" ? "green" : j.status === "running" || j.status === "queued" ? "amber" : "red"}`}
                      >
                        {j.status === "running" && (
                          <LoaderCircle size={12} className="spin" />
                        )}
                        {jobStates[j.status] ?? j.status}
                      </span>
                    </td>
                    <td>
                      <strong>{phases[j.phase] ?? j.phase}</strong>
                      <small>{messages[j.message] ?? j.message}</small>
                    </td>
                    <td>{date(j.updatedAt)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      {settings && draft && (
        <Modal
          title="تنظیم مقصد سراسری"
          onClose={() => {
            if (!busy) setSettings(false);
          }}
          wide
        >
          <form className="ship-settings" onSubmit={saveTarget}>
            <p className="change-description">
              این مقصد برای ارسال زیرساخت، پروژه‌ها و imageها مشترک است. تغییر
              تنظیمات، اتصال یا deploy را خودکار شروع نمی‌کند.
            </p>
            <div className="ship-mode">
              <button
                type="button"
                className={draft.mode === "local" ? "selected" : ""}
                onClick={() =>
                  setDraft({
                    ...draft,
                    mode: "local",
                    infraDir: "/tmp/infra-manager-target/infrastructure",
                    appsDir: "/tmp/infra-manager-target/apps",
                    dataDir: "/tmp/infra-manager-target/data",
                  })
                }
              >
                <Laptop size={17} />
                همین سیستم (آزمایشی)
              </button>
              <button
                type="button"
                className={draft.mode === "ssh" ? "selected" : ""}
                onClick={() =>
                  setDraft({
                    ...draft,
                    mode: "ssh",
                    infraDir: "/srv/infrastructure",
                    appsDir: "/srv/apps",
                    dataDir: "/srv/data",
                  })
                }
              >
                <Globe2 size={17} />
                سرور از طریق SSH
              </button>
            </div>
            {draft.mode === "ssh" && (
              <>
                <div className="ship-fields">
                  <label>
                    IP یا Host
                    <input
                      aria-label="IP یا Host مقصد"
                      dir="ltr"
                      value={draft.host}
                      onChange={(e) =>
                        setDraft({ ...draft, host: e.target.value })
                      }
                      required
                    />
                  </label>
                  <label>
                    پورت SSH
                    <input
                      type="number"
                      min="1"
                      max="65535"
                      dir="ltr"
                      value={draft.port}
                      onChange={(e) =>
                        setDraft({ ...draft, port: Number(e.target.value) })
                      }
                      required
                    />
                  </label>
                  <label>
                    کاربر SSH
                    <input
                      aria-label="کاربر SSH"
                      dir="ltr"
                      value={draft.user}
                      onChange={(e) =>
                        setDraft({ ...draft, user: e.target.value })
                      }
                      required
                    />
                  </label>
                  <label>
                    نام فایل کلید خصوصی
                    <input
                      aria-label="فایل کلید SSH"
                      list="ship-identities"
                      dir="ltr"
                      value={draft.identity}
                      onChange={(e) =>
                        setDraft({ ...draft, identity: e.target.value })
                      }
                      required
                    />
                    <datalist id="ship-identities">
                      {catalog.identities.map((key) => (
                        <option key={key} value={key} />
                      ))}
                    </datalist>
                  </label>
                </div>
                <p className="footnote">
                  <KeyRound size={15} /> کلید خصوصی و فایل known_hosts معتبر را
                  در manager/ssh قرار دهید. پنل محتویات کلید را دریافت یا نمایش
                  نمی‌دهد.
                </p>
              </>
            )}
            <div className="ship-fields paths">
              {(
                [
                  ["infraDir", "مسیر زیرساخت در مقصد"],
                  ["appsDir", "مسیر پروژه‌ها در مقصد"],
                  ["dataDir", "مسیر داده‌ها در مقصد"],
                ] as const
              ).map(([key, label]) => (
                <label key={key}>
                  {label}
                  <input
                    aria-label={label}
                    value={draft[key]}
                    dir="ltr"
                    onChange={(e) =>
                      setDraft({ ...draft, [key]: e.target.value })
                    }
                    required
                  />
                </label>
              ))}
            </div>
            {draft.mode === "local" && (
              <div className="ship-help">
                <HardDrive size={15} />
                <p>
                  مسیرهای محلی باید زیر /tmp/infra-manager-target باشند.
                  داده‌های مقصد از داده‌های پروژه‌های فعلی جدا می‌مانند.
                </p>
              </div>
            )}
            {error && (
              <p role="alert" className="form-error">
                {error}
              </p>
            )}
            <div className="modal-actions">
              <button className="primary" disabled={!!busy}>
                {busy === "settings" ? "در حال ذخیره…" : "ذخیرهٔ مقصد سراسری"}
              </button>
              <button
                className="secondary"
                type="button"
                onClick={() => setSettings(false)}
                disabled={!!busy}
              >
                انصراف
              </button>
            </div>
          </form>
        </Modal>
      )}
      {plan && (
        <Modal
          title="بررسی و تأیید ارسال"
          onClose={() => {
            if (!busy) setPlan(null);
          }}
          wide
        >
          <div className="ship-review-heading">
            <span className="ship-target-icon">
              <Rocket />
            </span>
            <div>
              <h3>
                {plan.request.mode === "image"
                  ? "ارسال image بدون Deploy"
                  : plan.request.service
                    ? `Deploy سرویس ${plan.request.service}`
                    : `Deploy کل پروژهٔ ${plan.request.project}`}
              </h3>
              <p>
                {plan.target.mode === "local"
                  ? "همین سیستم · مقصد آزمایشی"
                  : `${plan.target.user}@${plan.target.host}:${plan.target.port}`}
              </p>
            </div>
          </div>
          <div className="ship-review-path">
            <span>مسیر مقصد</span>
            <bdi>{plan.directory}</bdi>
          </div>
          {plan.services.length > 0 && (
            <p className="ship-review-services">
              سرویس‌ها: <bdi>{plan.services.join("، ")}</bdi>
            </p>
          )}
          <div className="table-scroll">
            <table className="containers-table">
              <thead>
                <tr>
                  <th>Image محلی</th>
                  <th>اندازه / معماری</th>
                  <th>شناسهٔ ثابت</th>
                </tr>
              </thead>
              <tbody>
                {plan.images.map((i) => (
                  <tr key={i.destinationRef}>
                    <td dir="ltr">
                      {i.ref}
                      <small className="muted">{i.destinationRef}</small>
                    </td>
                    <td dir="ltr">
                      {bytes(i.size)}
                      <small className="muted">{i.platform}</small>
                    </td>
                    <td>
                      <code>{i.id.slice(7, 23)}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {plan.preservedLimits?.length > 0 && (
            <div className="ship-limits">
              <strong>محدودیت‌های حفظ‌شده از سرور</strong>
              {plan.preservedLimits.map((l) => (
                <p key={l.service}>
                  <bdi dir="ltr">{l.service}</bdi>: {describeLimit(l)}
                </p>
              ))}
            </div>
          )}
          <div className="ship-review-warnings">
            {plan.warnings.map((w) => (
              <p key={w}>
                <ShieldCheck size={14} />
                {warnings[w] ?? w}
              </p>
            ))}
          </div>
          {plan.files.length > 0 && (
            <details className="ship-file-list">
              <summary>
                {number(plan.files.length, 0)} فایل تنظیمات همراه این ارسال
              </summary>
              <ul dir="ltr">
                {plan.files.map((f) => (
                  <li key={f}>{f}</li>
                ))}
              </ul>
            </details>
          )}
          {error && (
            <p role="alert" className="form-error">
              {error}
            </p>
          )}
          <div className="modal-actions">
            <button
              className="primary"
              disabled={!!busy || !!active}
              onClick={() => void commit()}
            >
              {busy === "commit" ? (
                <LoaderCircle className="spin" size={16} />
              ) : (
                <Upload size={16} />
              )}
              تأیید و شروع{" "}
              {plan.request.mode === "image" ? "ارسال image" : "ارسال و Deploy"}
            </button>
            <button
              className="secondary"
              disabled={!!busy}
              onClick={() => setPlan(null)}
            >
              بازگشت
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
