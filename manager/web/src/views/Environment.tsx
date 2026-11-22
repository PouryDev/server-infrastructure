import { useCallback, useEffect, useState } from "react";
import {
  CheckCircle2,
  Eye,
  EyeOff,
  KeyRound,
  Lock,
  LoaderCircle,
  Pencil,
  RefreshCw,
  Rocket,
  Save,
  Trash2,
  XCircle,
} from "lucide-react";
import { APIError, request } from "../api";
import { Modal } from "../components/Modal";
import { EnvChangeEditor } from "../components/EnvChangeEditor";
import { JobTable } from "../components/JobTable";
import type { EnvChange, EnvProject, EnvState } from "../env-types";
import { describeLimit } from "../shipping-format";
import { warnings } from "../shipping-labels";
import type { ShipJob, ShipPlan } from "../shipping-types";

export function Environment({
  csrf,
  onUnauthorized,
}: {
  csrf: string;
  onUnauthorized: () => void;
}) {
  const [projects, setProjects] = useState<EnvProject[] | null>(null);
  const [project, setProject] = useState("");
  const [state, setState] = useState<EnvState | null>(null);
  const [scope, setScope] = useState("");
  const [pending, setPending] = useState<EnvChange[]>([]);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [seed, setSeed] = useState({ key: 0, scope: "", name: "" });
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
  const [applying, setApplying] = useState(false);
  const fail = useCallback(
    (e: unknown) => {
      if (e instanceof APIError && e.status === 401) onUnauthorized();
      else setError((e as Error).message);
    },
    [onUnauthorized],
  );
  const loadProjects = useCallback(async () => {
    setBusy("projects");
    try {
      const list = await request<EnvProject[]>("/env/projects");
      setProjects(list);
      setProject((current) => current || list[0]?.name || "");
      setError("");
    } catch (e) {
      fail(e);
      setProjects([]);
    } finally {
      setBusy("");
    }
  }, [fail]);
  const loadState = useCallback(
    async (name: string) => {
      if (!name) return;
      setBusy("state");
      try {
        setState(await request<EnvState>(`/env/${name}`));
        setRevealed({});
        setError("");
      } catch (e) {
        setState(null);
        fail(e);
      } finally {
        setBusy("");
      }
    },
    [fail],
  );
  useEffect(() => {
    void loadProjects();
  }, [loadProjects]);
  useEffect(() => {
    setPending([]);
    setScope("");
    void loadState(project);
  }, [project, loadState]);

  const scopes = [
    { value: "", label: "کل پروژه (.env)" },
    ...(state?.services ?? []).map((s) => ({ value: s.name, label: s.name })),
  ];
  const file = state?.files.find((f) => f.scope === scope);
  const queued = (name: string) =>
    pending.find((c) => c.scope === scope && c.name === name);

  async function reveal(name: string) {
    const key = scope + "/" + name;
    if (revealed[key] !== undefined) {
      setRevealed(({ [key]: _gone, ...rest }) => rest);
      return;
    }
    try {
      const out = await request<{ value: string }>(
        `/env/${project}/reveal`,
        { method: "POST", body: JSON.stringify({ scope, name }) },
        csrf,
      );
      setRevealed((old) => ({ ...old, [key]: out.value }));
    } catch (e) {
      fail(e);
    }
  }
  async function save() {
    if (!state || pending.length === 0) return;
    setBusy("save");
    setError("");
    try {
      const next = await request<EnvState>(
        `/env/${project}`,
        {
          method: "PUT",
          body: JSON.stringify({ revision: state.revision, changes: pending }),
        },
        csrf,
      );
      setState(next);
      setPending([]);
      setRevealed({});
      setNotice(
        "تغییرها در فایل‌های مقصد ذخیره شد و از نسخهٔ قبلی پشتیبان گرفته شد. برای اثر روی سرویس‌ها «اعمال روی سرویس‌ها» را بزنید.",
      );
    } catch (e) {
      fail(e);
      if (e instanceof APIError && e.status === 409) void loadState(project);
    } finally {
      setBusy("");
    }
  }

  if (!projects)
    return (
      <div className="panel loading-state">
        <RefreshCw className="spin" />
        <p>در حال دریافت پروژه‌های مقصد…</p>
      </div>
    );
  return (
    <div className="shipping-view env-view">
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
            aria-label="بستن پیام"
            onClick={() => setNotice("")}
          >
            ×
          </button>
        </div>
      )}
      <section className="panel env-panel">
        <div className="panel-heading">
          <div>
            <h2>
              <KeyRound size={19} /> متغیرهای محیطی پروژه
            </h2>
            <p>
              فایل‌های مقصد منبع حقیقت هستند: <bdi dir="ltr">.env</bdi> برای کل
              پروژه و <bdi dir="ltr">env.d/&lt;service&gt;.env</bdi> برای یک
              سرویس. مقدارها همیشه پوشیده نمایش داده می‌شوند.
            </p>
          </div>
          <button
            className="icon-button"
            aria-label="بازخوانی از مقصد"
            disabled={!!busy}
            onClick={() => void loadProjects().then(() => loadState(project))}
          >
            <RefreshCw size={16} className={busy ? "spin" : ""} />
          </button>
        </div>
        {projects.length === 0 ? (
          <div className="empty-state">
            <KeyRound />
            <h3>پروژه‌ای در مقصد پیدا نشد</h3>
            <p>
              پس از اولین Deploy، پروژه اینجا نمایش داده می‌شود. برای پروژهٔ
              جدید، متغیرها را در مرحلهٔ «ساخت و ارسال» اضافه کنید.
            </p>
          </div>
        ) : (
          <>
            <div className="ship-fields">
              <label>
                پروژه
                <select
                  aria-label="پروژهٔ مقصد"
                  value={project}
                  onChange={(e) => setProject(e.target.value)}
                >
                  {projects.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                محدودهٔ نمایش
                <select
                  aria-label="محدودهٔ متغیرها"
                  value={scope}
                  onChange={(e) => setScope(e.target.value)}
                >
                  {scopes.map((s) => (
                    <option key={s.value} value={s.value}>
                      {s.label}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {busy === "state" && !state ? (
              <p className="footnote">
                <LoaderCircle className="spin" size={14} /> در حال خواندن از مقصد…
              </p>
            ) : (
              state && (
                <div className="table-scroll">
                  <table className="containers-table env-table">
                    <thead>
                      <tr>
                        <th>نام</th>
                        <th>مقدار</th>
                        <th>عملیات</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(file?.variables ?? []).length === 0 && (
                        <tr>
                          <td colSpan={3} className="muted">
                            {scope === ""
                              ? "فایل .env متغیری ندارد."
                              : `برای ${scope} متغیر اختصاصی تعریف نشده است.`}
                          </td>
                        </tr>
                      )}
                      {(file?.variables ?? []).map((v) => {
                        const key = scope + "/" + v.name;
                        const q = queued(v.name);
                        return (
                          <tr key={v.name} className={q ? "env-queued" : ""}>
                            <td dir="ltr">
                              {v.sensitive && <Lock size={12} />} {v.name}
                              {q && (
                                <small className="muted">
                                  {q.action === "delete"
                                    ? "حذف می‌شود"
                                    : "ویرایش می‌شود"}
                                </small>
                              )}
                            </td>
                            <td dir="ltr">
                              {revealed[key] !== undefined ? (
                                <code>{revealed[key] || "(خالی)"}</code>
                              ) : (
                                <span className="env-mask">
                                  {v.empty ? "(خالی)" : "••••••••"}
                                </span>
                              )}
                            </td>
                            <td className="env-actions">
                              <button
                                className="icon-button"
                                aria-label={
                                  revealed[key] !== undefined
                                    ? `پنهان کردن ${v.name}`
                                    : `نمایش ${v.name}`
                                }
                                onClick={() => void reveal(v.name)}
                              >
                                {revealed[key] !== undefined ? (
                                  <EyeOff size={15} />
                                ) : (
                                  <Eye size={15} />
                                )}
                              </button>
                              <button
                                className="icon-button"
                                aria-label={`ویرایش ${v.name}`}
                                onClick={() =>
                                  setSeed({
                                    key: seed.key + 1,
                                    scope,
                                    name: v.name,
                                  })
                                }
                              >
                                <Pencil size={15} />
                              </button>
                              <button
                                className="icon-button"
                                aria-label={`حذف ${v.name}`}
                                onClick={() =>
                                  setPending((old) => [
                                    ...old.filter(
                                      (c) =>
                                        !(c.scope === scope && c.name === v.name),
                                    ),
                                    { scope, name: v.name, action: "delete" },
                                  ])
                                }
                              >
                                <Trash2 size={15} />
                              </button>
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )
            )}
            {state && (
              <>
                <h3 className="env-subtitle">افزودن، ویرایش یا حذف</h3>
                <EnvChangeEditor
                  key={seed.key + ":" + project}
                  scopes={scopes}
                  changes={pending}
                  onChange={setPending}
                  disabled={busy === "save"}
                  initial={
                    seed.key > 0
                      ? { scope: seed.scope, name: seed.name }
                      : { scope, name: "" }
                  }
                />
                <div className="modal-actions env-save-row">
                  <button
                    className="primary"
                    disabled={pending.length === 0 || busy === "save"}
                    onClick={() => void save()}
                  >
                    {busy === "save" ? (
                      <LoaderCircle className="spin" size={16} />
                    ) : (
                      <Save size={16} />
                    )}
                    ذخیره در مقصد ({pending.length})
                  </button>
                  <button
                    className="secondary"
                    disabled={pending.length === 0 || busy === "save"}
                    onClick={() => setPending([])}
                  >
                    لغو تغییرها
                  </button>
                  <button
                    className="secondary"
                    disabled={!!busy || pending.length > 0}
                    onClick={() => setApplying(true)}
                  >
                    <Rocket size={15} /> اعمال روی سرویس‌ها
                  </button>
                </div>
                <p className="footnote">
                  ذخیره فقط فایل‌های مقدار را عوض می‌کند (با پشتیبان
                  زمان‌دار و حفظ مجوز و توضیح‌ها). کانتینرها تا «اعمال روی
                  سرویس‌ها» همان مقدارهای قبلی را دارند.
                </p>
              </>
            )}
          </>
        )}
      </section>
      {applying && state && (
        <ApplyModal
          project={project}
          services={state.services.filter((s) => !s.oneShot).map((s) => s.name)}
          csrf={csrf}
          onClose={() => setApplying(false)}
          onUnauthorized={onUnauthorized}
        />
      )}
    </div>
  );
}

function ApplyModal({
  project,
  services,
  csrf,
  onClose,
  onUnauthorized,
}: {
  project: string;
  services: string[];
  csrf: string;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const [service, setService] = useState("");
  const [plan, setPlan] = useState<ShipPlan | null>(null);
  const [job, setJob] = useState<ShipJob | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const fail = (e: unknown) => {
    if (e instanceof APIError && e.status === 401) onUnauthorized();
    else setError((e as Error).message);
  };
  useEffect(() => {
    if (!job || !["queued", "running"].includes(job.status)) return;
    const id = setInterval(() => {
      void request<ShipJob[]>("/shipping/jobs")
        .then((jobs) => {
          const found = jobs.find((j) => j.id === job.id);
          if (found) setJob(found);
        })
        .catch(() => undefined);
    }, 2000);
    return () => clearInterval(id);
  }, [job]);
  async function preview() {
    setBusy(true);
    setError("");
    try {
      setPlan(
        await request<ShipPlan>(
          "/shipping/plans",
          {
            method: "POST",
            body: JSON.stringify({
              mode: "apply",
              project,
              service,
              image: "",
              includeEnv: false,
            }),
          },
          csrf,
        ),
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  async function confirm() {
    if (!plan) return;
    setBusy(true);
    setError("");
    try {
      setJob(
        await request<ShipJob>(
          "/shipping/jobs",
          { method: "POST", body: JSON.stringify({ planId: plan.id }) },
          csrf,
        ),
      );
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title="اعمال متغیرها روی سرویس‌ها" wide onClose={onClose}>
      <p className="change-description">
        بدون ساخت image جدید: فقط کانتینرهایی که پیکربندی‌شان با متغیرهای
        فعلی فرق دارد دوباره ساخته می‌شوند. اگر اعمال ناموفق باشد، فایل‌های
        قبلی بازگردانده می‌شوند.
      </p>
      {!job && (
        <div className="ship-fields">
          <label>
            محدودهٔ اعمال
            <select
              aria-label="محدودهٔ اعمال"
              value={service}
              disabled={!!plan || busy}
              onChange={(e) => setService(e.target.value)}
            >
              <option value="">همهٔ سرویس‌های پروژه</option>
              {services.map((s) => (
                <option key={s} value={s}>
                  فقط {s}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      {plan && (
        <div className="ship-review">
          <p className="ship-review-services">
            سرویس‌های هدف: <bdi dir="ltr">{plan.services.join("، ")}</bdi>
          </p>
          <p className="ship-review-services">
            احتمالاً دوباره ساخته می‌شوند:{" "}
            <bdi dir="ltr">
              {plan.affected.length ? plan.affected.join("، ") : "—"}
            </bdi>
          </p>
          {plan.envChanges.length > 0 && (
            <details open className="ship-file-list">
              <summary>
                نام متغیرهای تغییرکرده ({plan.envChanges.length}) — بدون مقدار
              </summary>
              <ul dir="ltr">
                {plan.envChanges.map((c) => (
                  <li key={c.scope + c.name}>
                    {c.scope || "project"} · {c.name} ·{" "}
                    {c.action === "delete" ? "removed" : c.exists ? "changed" : "added"}
                  </li>
                ))}
              </ul>
            </details>
          )}
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
          <div className="ship-review-warnings">
            {plan.warnings
              .filter((w) => warnings[w])
              .map((w) => (
                <p key={w}>{warnings[w]}</p>
              ))}
          </div>
        </div>
      )}
      {job && (
        <div className="ship-review">
          <p>
            <span
              className={`badge ${job.status === "succeeded" ? "green" : ["queued", "running"].includes(job.status) ? "amber" : "red"}`}
            >
              {job.status === "succeeded"
                ? "موفق"
                : ["queued", "running"].includes(job.status)
                  ? "در حال اجرا"
                  : "ناموفق یا بازگردانده‌شده"}
            </span>{" "}
            {job.message}
          </p>
          <JobTable jobs={[job]} />
        </div>
      )}
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      <div className="modal-actions">
        {!plan && !job && (
          <button className="primary" disabled={busy} onClick={() => void preview()}>
            {busy ? <LoaderCircle className="spin" size={16} /> : <Rocket size={16} />}
            پیش‌نمایش
          </button>
        )}
        {plan && !job && (
          <button className="primary" disabled={busy} onClick={() => void confirm()}>
            {busy ? <LoaderCircle className="spin" size={16} /> : <Rocket size={16} />}
            تأیید و اعمال
          </button>
        )}
        <button className="secondary" onClick={onClose}>
          {job ? "بستن" : "انصراف"}
        </button>
      </div>
    </Modal>
  );
}

