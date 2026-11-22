import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  ArrowLeft,
  Boxes,
  CheckCircle2,
  Clock3,
  FolderGit2,
  Hammer,
  KeyRound,
  LoaderCircle,
  PackageCheck,
  RefreshCw,
  ShieldCheck,
  Upload,
  XCircle,
} from "lucide-react";
import { APIError, request } from "../api";
import { EnvChangeEditor, scopeLabel } from "../components/EnvChangeEditor";
import { JobTable } from "../components/JobTable";
import { ShipProgress } from "../components/ShipProgress";
import { Modal } from "../components/Modal";
import type { BuildSource, EnvChange, EnvProject } from "../env-types";
import { number } from "../format";
import { describeLimit } from "../shipping-format";
import { warnings } from "../shipping-labels";
import type { ShipJob, ShipPlan, ShipTarget } from "../shipping-types";

const signature = (changes: EnvChange[]) =>
  JSON.stringify(
    [...changes].sort((a, b) =>
      (a.scope + a.name).localeCompare(b.scope + b.name),
    ),
  );

export function BuildShip({
  csrf,
  onUnauthorized,
  automatic = false,
}: {
  csrf: string;
  onUnauthorized: () => void;
  automatic?: boolean;
}) {
  const [route, setRoute] = useState({ domain: "", service: "", port: 8080, migrationService: "", revision: "" });
  const [nativeServices, setNativeServices] = useState<string[]>([]);
  const [edgePort, setEdgePort] = useState(80);
  const [nativeKeys, setNativeKeys] = useState<string[]>([]);
  const [nativeReady, setNativeReady] = useState(false);
  const [nativeRefresh, setNativeRefresh] = useState(0);
  const [planRoute, setPlanRoute] = useState("");
  const [sources, setSources] = useState<BuildSource[] | null>(null);
  const [target, setTarget] = useState<ShipTarget | null>(null);
  const [envProjects, setEnvProjects] = useState<EnvProject[]>([]);
  const [jobs, setJobs] = useState<ShipJob[]>([]);
  const [source, setSource] = useState("");
  const [service, setService] = useState("");
  const [useCompose, setUseCompose] = useState(false);
  const [env, setEnv] = useState<EnvChange[]>([]);
  const [plan, setPlan] = useState<ShipPlan | null>(null);
  const [planEnv, setPlanEnv] = useState("[]");
  const [planCompose, setPlanCompose] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
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
      const [s, t, j] = await Promise.all([
        request<BuildSource[]>("/shipping/sources"),
        request<ShipTarget>("/shipping/target"),
        request<ShipJob[]>("/shipping/jobs"),
      ]);
      setSources(s);
      setTarget(t);
      setJobs(j);
      setSource((cur) => cur || (automatic ? s.find(x => x.id === "didchi-platform" && !x.error)?.id : "") || s.find((x) => !x.error)?.id || "");
      // The destination's services are optional context (the project may be new).
      request<EnvProject[]>("/env/projects")
        .then(setEnvProjects)
        .catch(() => setEnvProjects([]));
      setError("");
    } catch (e) {
      setSources([]);
      fail(e);
    } finally {
      setBusy("");
    }
  }, [fail, automatic]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const id = setInterval(() => {
      void request<ShipJob[]>("/shipping/jobs").then(setJobs).catch(fail);
    }, 3000);
    return () => clearInterval(id);
  }, [fail]);
  const active = jobs.find((j) => j.status === "running" || j.status === "queued");
  const chosen = sources?.find((s) => s.id === source);
  const known = envProjects.find((p) => p.name === chosen?.app);
  const latestJob = jobs[0] ? jobs[0].id + jobs[0].status : "";
  useEffect(() => {
    if (!automatic || !chosen || !target) return;
    let cancelled = false;
    setNativeReady(false);
    setError("");
    request<{ catalog: { edgePort: number; projects: { name: string; domain: string; service: string; port: number; migrationService: string; revision: string; envKeys: string[] }[] }; services: string[] }>(`/shipping/deployment?source=${encodeURIComponent(chosen.id)}`)
      .then(state => {
        if (cancelled) return;
        const saved = state.catalog.projects.find(p => p.name === chosen.app);
        setEdgePort(state.catalog.edgePort ?? 80);
        setNativeServices(state.services); setNativeKeys(saved?.envKeys ?? []);
        setRoute({ domain: saved?.domain ?? "", service: saved?.service || state.services.find(s => s.endsWith("-api") || s.endsWith("-web")) || "", port: saved?.port ?? 8080,
          migrationService: saved?.migrationService ?? (state.services.find(s => s.endsWith("-migrate")) || ""), revision: saved?.revision ?? "" });
        setNativeReady(true);
      }).catch(e => { if (!cancelled) fail(e); });
    return () => { cancelled = true; };
  }, [automatic, chosen?.id, target?.revision, latestJob, nativeRefresh, fail]);
  const scopes = automatic ? [{ value: "", label: "env برنامه روی مقصد (.runtime.env)" }] : [
    { value: "", label: "کل پروژه (.env)" },
    ...(known?.services ?? []).map((s) => ({ value: s, label: s })),
  ];
  const stale =
    !!plan && (signature(env) !== planEnv || useCompose !== planCompose || (automatic && JSON.stringify(route) !== planRoute));

  async function prepare() {
    setBusy("plan");
    setError("");
    try {
      const result = await request<ShipPlan>(
        "/shipping/plans",
        {
          method: "POST",
          body: JSON.stringify({
            mode: "build",
            source,
            project: "",
            service: automatic ? "" : service,
            ...(automatic ? { deployment: route } : {}),
            image: "",
            includeEnv: false,
            useComposeLimits: useCompose,
            env,
          }),
        },
        csrf,
      );
      setPlan(result);
      setPlanEnv(signature(env));
      setPlanCompose(useCompose);
      setPlanRoute(JSON.stringify(route));
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  async function commit() {
    if (!plan || stale) return;
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
      setEnv([]);
      setNotice(
        "Job ثبت شد: ساخت از پوشهٔ فعلی، ارسال و راه‌اندازی در پس‌زمینه انجام می‌شود؛ می‌توانید صفحه را ببندید.",
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy("");
    }
  }
  if (!sources || !target)
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
            <p>در حال دریافت پروژه‌های محلی…</p>
          </>
        )}
      </div>
    );
  return (
    <div className="shipping-view">
      {error && !plan && (
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
            aria-label="بستن پیام"
            onClick={() => setNotice("")}
          >
            ×
          </button>
        </div>
      )}
      <div className="ship-grid">
        <section className="panel ship-form-panel">
          <div className="panel-heading">
            <div>
              <h2>
                <Hammer size={19} /> ساخت از پوشهٔ فعلی و ارسال
              </h2>
              <p>
                image از همین سیستم و از فایل‌های فعلی پروژه ساخته می‌شود؛ commit
                یا push لازم نیست.
              </p>
            </div>
            <button
              className="icon-button"
              aria-label="بازخوانی پروژه‌های محلی"
              disabled={!!busy}
              onClick={() => void load()}
            >
              <RefreshCw size={16} />
            </button>
          </div>
          {sources.length === 0 ? (
            <div className="empty-state">
              <FolderGit2 />
              <h3>پروژه‌ای ثبت نشده است</h3>
              <p>
                مسیر پروژه‌ها را مالک سیستم در فایل manager/builder.local.yaml
                ثبت می‌کند؛ مرورگر هیچ مسیری انتخاب نمی‌کند.
              </p>
            </div>
          ) : (
            <form
              className="ship-form"
              onSubmit={(e) => {
                e.preventDefault();
                void prepare();
              }}
            >
              <div className="ship-fields">
                <label>
                  پروژهٔ محلی
                  <select
                    aria-label="پروژهٔ محلی"
                    value={source}
                    onChange={(e) => {
                      setSource(e.target.value);
                      setService("");
                      setEnv([]);
                    }}
                  >
                    {sources.map((s) => (
                      <option key={s.id} value={s.id} disabled={!!s.error}>
                        {s.id}
                        {s.subdir ? ` / ${s.subdir}` : ""}
                        {s.error ? " (ship.conf نامعتبر)" : ""}
                      </option>
                    ))}
                  </select>
                </label>
                {!automatic && <label>
                  محدودهٔ Deploy
                  <select
                    aria-label="سرویس مقصد"
                    value={service}
                    onChange={(e) => setService(e.target.value)}
                  >
                    <option value="">همهٔ سرویس‌های پروژه</option>
                    {(known?.services ?? []).map((s) => (
                      <option key={s} value={s}>
                        فقط {s}
                      </option>
                    ))}
                  </select>
                </label>}
              </div>
              {automatic && <div className="deployment-fields">
                <label>دامنهٔ مقصد<input aria-label="دامنهٔ مقصد" dir="ltr" required placeholder="app.example.com" value={route.domain} disabled={!nativeReady || !!active} onChange={e => setRoute(r => ({ ...r, domain: e.target.value.trim().toLowerCase() }))}/></label>
                <label>سرویس HTTP<select aria-label="سرویس HTTP مقصد" required value={route.service} disabled={!nativeReady || !!active} onChange={e => setRoute(r => ({ ...r, service: e.target.value }))}><option value="">انتخاب سرویس</option>{nativeServices.map(s => <option key={s} value={s}>{s}</option>)}</select></label>
                <label>پورت داخل کانتینر<input aria-label="پورت HTTP مقصد" type="number" min={1} max={65535} value={route.port} disabled={!nativeReady || !!active} onChange={e => setRoute(r => ({ ...r, port: Number(e.target.value) }))}/></label>
                <label>migration پیش از اجرا<select aria-label="migration مقصد" value={route.migrationService} disabled={!nativeReady || !!active} onChange={e => setRoute(r => ({ ...r, migrationService: e.target.value }))}><option value="">بدون migration خودکار</option>{nativeServices.filter(s => s !== route.service).map(s => <option key={s} value={s}>{s}</option>)}</select></label>
              </div>}
              {chosen && !chosen.error && (
                <div className="ship-help">
                  <Boxes size={15} />
                  <p>
                    <bdi dir="ltr">{chosen.app}</bdi> ·{" "}
                    <bdi dir="ltr">{chosen.platform}</bdi> · imageها:{" "}
                    <bdi dir="ltr">
                      {chosen.images.map((i) => i.service).join("، ")}
                    </bdi>
                  </p>
                </div>
              )}
              {chosen?.error && (
                <p role="alert" className="form-error">
                  {chosen.error}
                </p>
              )}
              {!automatic && <label className="checkbox ship-env">
                <input
                  type="checkbox"
                  checked={useCompose}
                  onChange={(e) => setUseCompose(e.target.checked)}
                />
                <span>
                  استفاده از محدودیت‌های Compose به‌جای سرور
                  <small>
                    پیش‌فرض خاموش است: محدودیت CPU/RAM که روی سرور تغییر داده‌اید
                    حفظ می‌شود.
                  </small>
                </span>
              </label>}
              {automatic && <>
                <p className="ship-help">مقصد SSH: <bdi>{target.user}@{target.host}:{target.port}</bdi> · <bdi>{target.appsDir}/{chosen?.app}</bdi></p>
                <p className="footnote">مبدأ HTTP دامنه در CDN: <bdi>{target.host}:{edgePort}</bdi></p>
                <p className="footnote">دامنه و env از سرور مقصد خوانده و برای دفعات بعد حفظ می‌شوند. محدودیت‌های فعلی کانتینرها همیشه حفظ می‌شوند؛ env لوکال منتقل نمی‌شود.</p>
                {!nativeReady && <p role="status">مقصد SSH و نسخهٔ manager سرور را بررسی کنید. برای تنظیم مقصد به «ارسال و Deploy ← تنظیم مقصد سراسری» بروید.</p>}
                <button type="button" className="secondary" onClick={() => setNativeRefresh(v => v + 1)}>بازخوانی تنظیمات مقصد</button>
                <h4 className="env-subtitle">env همین دیپلوی روی مقصد</h4>
                {nativeKeys.length > 0 && <p className="footnote">نام متغیرهای ذخیره‌شده (مقادیر مخفی): <bdi>{nativeKeys.join("، ")}</bdi></p>}
                <EnvChangeEditor scopes={scopes} changes={env} onChange={setEnv} disabled={!!busy || !!active} embedded/>
              </>}
              <button
                className="primary ship-preview-button"
                disabled={!!busy || !!active || !source || !!chosen?.error || (automatic && !nativeReady)}
              >
                {busy === "plan" ? (
                  <LoaderCircle className="spin" size={16} />
                ) : (
                  <PackageCheck size={16} />
                )}
                پیش‌نمایش ساخت و ارسال
                <ArrowLeft size={16} />
              </button>
              {active && (
                <p className="footnote">
                  تا تکمیل job فعال، کار دیگری شروع نمی‌شود.
                </p>
              )}
            </form>
          )}
        </section>
        <aside className="panel ship-explainer">
          <span className="ship-illustration">
            <FolderGit2 size={43} />
            <ArrowLeft size={23} />
            <Upload size={43} />
          </span>
          <div className="eyebrow">LOCAL TREE → BUILD → SHIP</div>
          <h3>همان چیزی که روی دیسک شماست.</h3>
          <p>
            tag از commit ساخته می‌شود و اگر تغییر ثبت‌نشده باشد با پسوند dirty
            مشخص می‌شود. پس از ساخت، همان مسیر ارسال امن: انتقال، بررسی شناسه،
            راه‌اندازی و بازگشت خودکار.
          </p>
          <div className="ship-privacy">
            <ShieldCheck size={17} /> مقدار build argها و متغیرها در خروجی، audit
            و پیش‌نمایش نمایش داده نمی‌شود.
          </div>
        </aside>
      </div>
      <section className="panel ship-jobs">
        <div className="panel-heading">
          <div>
            <h2>
              <Clock3 size={18} /> کارهای اخیر{" "}
              <span className="count-pill">{number(jobs.length, 0)}</span>
            </h2>
            <p>ساخت و ارسال مستقل از بازبودن صفحه اجرا می‌شود.</p>
          </div>
        </div>
        {(active || jobs[0]) && <ShipProgress job={active || jobs[0]}/>}
        {jobs.length > 0 && <JobTable jobs={jobs} />}
      </section>
      {plan && (
        <Modal
          title="بررسی ساخت و ارسال"
          wide
          onClose={() => {
            if (!busy) setPlan(null);
          }}
        >
          <div className="ship-review-heading">
            <span className="ship-target-icon">
              <Hammer />
            </span>
            <div>
              <h3>
                {plan.request.service
                  ? `ساخت و Deploy سرویس ${plan.request.service}`
                  : `ساخت و Deploy کل پروژهٔ ${plan.project}`}
              </h3>
              <p>
                {plan.target.mode === "local"
                  ? "همین سیستم · مقصد آزمایشی"
                  : `${plan.target.user}@${plan.target.host}:${plan.target.port}`}
              </p>
            </div>
          </div>
          {plan.request.deployment && <div className="ship-review-services"><p>دامنه: <bdi>{plan.request.deployment.domain}</bdi></p><p>سرویس: <bdi>{plan.request.deployment.service}:{plan.request.deployment.port}</bdi></p><p>migration: <bdi>{plan.request.deployment.migrationService || "—"}</bdi></p><p>دامنه و env روی مقصد ذخیره می‌شوند. migration دیتابیس با بازگشت کانتینرها برگردانده نمی‌شود.</p></div>}
          {plan.build && (
            <div className="ship-review-path">
              <span>tag</span>
              <bdi dir="ltr">{plan.build.tag}</bdi>
              {plan.build.dirty ? (
                <span className="badge amber">
                  <AlertTriangle size={12} /> تغییر ثبت‌نشده (dirty)
                </span>
              ) : plan.build.head ? (
                <span className="badge green">commit تمیز</span>
              ) : (
                <span className="badge amber">بدون Git؛ tag زمانی</span>
              )}
            </div>
          )}
          <div className="table-scroll">
            <table className="containers-table">
              <thead>
                <tr>
                  <th>imageهایی که ساخته می‌شوند</th>
                  <th>context / Dockerfile</th>
                  <th>معماری</th>
                </tr>
              </thead>
              <tbody>
                {plan.build?.images.map((i) => (
                  <tr key={i.service}>
                    <td dir="ltr">
                      {plan.build?.app}-{i.service}:{plan.build?.tag}
                    </td>
                    <td dir="ltr">
                      {i.context}
                      <small className="muted">
                        {i.dockerfile || "Dockerfile"}
                      </small>
                    </td>
                    <td dir="ltr">{plan.build?.platform}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {plan.build && plan.build.buildArgNames.length > 0 && (
            <p className="ship-review-services">
              build argها (مقدار از env پروژه): {" "}
              <bdi dir="ltr">{plan.build.buildArgNames.join("، ")}</bdi>
            </p>
          )}
          <p className="ship-review-services">
            سرویس‌های Compose: <bdi dir="ltr">{plan.services.join("، ")}</bdi>
          </p>
          {plan.preservedLimits.length > 0 && (
            <div className="ship-limits">
              <strong>محدودیت‌های حفظ‌شده از سرور</strong>
              {plan.preservedLimits.map((l) => (
                <p key={l.service}>
                  <bdi dir="ltr">{l.service}</bdi>: {describeLimit(l)}
                </p>
              ))}
            </div>
          )}
          {plan.envChanges.length > 0 && (
            <details open className="ship-file-list">
              <summary>
                تغییر متغیرها همراه این Deploy ({plan.envChanges.length}) — فقط
                نام‌ها
              </summary>
              <ul dir="ltr">
                {plan.envChanges.map((c) => (
                  <li key={c.scope + c.name}>
                    {c.scope || "project"} · {c.name} ·{" "}
                    {c.action === "delete"
                      ? "removed"
                      : c.exists
                        ? "updated"
                        : "added"}
                  </li>
                ))}
              </ul>
            </details>
          )}
          <h4 className="env-subtitle">
            <KeyRound size={14} /> متغیرهای همین Deploy
          </h4>
          <p className="footnote">
            تغییرها همراه این Deploy اتمیک اعمال می‌شوند: پشتیبان، نوشتن، راه‌اندازی؛
            در صورت شکست، فایل‌ها همراه tag قبلی بازمی‌گردند. پس از تغییر،
            پیش‌نمایش را به‌روزرسانی کنید.
          </p>
          <EnvChangeEditor
            scopes={scopes}
            changes={env}
            onChange={setEnv}
            disabled={!!busy}
          />
          <div className="ship-review-warnings">
            {plan.warnings.map((w) => (
              <p key={w}>
                <ShieldCheck size={14} />
                {warnings[w] ?? w}
              </p>
            ))}
          </div>
          {error && (
            <p role="alert" className="form-error">
              {error}
            </p>
          )}
          <div className="modal-actions">
            {stale ? (
              <button
                className="primary"
                disabled={!!busy}
                onClick={() => void prepare()}
              >
                {busy === "plan" ? (
                  <LoaderCircle className="spin" size={16} />
                ) : (
                  <RefreshCw size={16} />
                )}
                به‌روزرسانی پیش‌نمایش
              </button>
            ) : (
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
                تأیید، ساخت و ارسال
              </button>
            )}
            <button
              className="secondary"
              disabled={!!busy}
              onClick={() => setPlan(null)}
            >
              بازگشت
            </button>
          </div>
          {env.length > 0 && (
            <p className="footnote">
              محدوده‌ها: {env.map((c) => scopeLabel(c.scope)).join("، ")}
            </p>
          )}
        </Modal>
      )}
    </div>
  );
}
