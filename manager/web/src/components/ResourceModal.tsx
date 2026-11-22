import { useEffect, useState } from "react";
import {
  ArrowLeft,
  Check,
  CheckCircle2,
  Cpu,
  GitBranch,
  LoaderCircle,
  MemoryStick,
  SlidersHorizontal,
  TriangleAlert,
} from "lucide-react";
import { APIError, request } from "../api";
import { number } from "../format";
import { type Container } from "../types";
import {
  type ResourceRequest,
  type ResourcePreview,
  type ResourceResult,
} from "../resource-types";
import { Modal } from "./Modal";

const modes = {
  local: {
    title: "فقط ذخیره و اعمال محلی",
    text: "فایل Compose و کانتینرها به‌روز می‌شوند؛ بدون commit و push.",
  },
  commit: {
    title: "اعمال و commit خودکار",
    text: "تغییر همین سرویس در شاخهٔ فعلی ثبت می‌شود؛ بدون push.",
  },
  push: {
    title: "اعمال، commit و push",
    text: "commit جدید به origin ارسال می‌شود. نیازمند فعال‌سازی در سرور.",
  },
};
export function ResourceModal({
  container,
  csrf,
  onClose,
  onApplied,
  onUnauthorized,
}: {
  container: Container;
  csrf: string;
  onClose: () => void;
  onApplied: () => void;
  onUnauthorized: () => void;
}) {
  const [memory, setMemory] = useState("512");
  const [cpu, setCPU] = useState("0.5");
  const [mode, setMode] = useState<ResourceRequest["gitMode"]>("local");
  const [current, setCurrent] = useState<ResourcePreview | null>(null);
  const [preview, setPreview] = useState<ResourcePreview | null>(null);
  const [result, setResult] = useState<ResourceResult | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const body = (
    memoryMiB = Number(memory),
    cpus = Number(cpu),
  ): ResourceRequest => ({
    project: container.project,
    service: container.service,
    memoryMiB,
    cpus,
    gitMode: mode,
    revision: "",
  });
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void request<ResourcePreview>(
      "/resources/preview",
      {
        method: "POST",
        body: JSON.stringify({
          project: container.project,
          service: container.service,
          memoryMiB: 512,
          cpus: 0.5,
          gitMode: "local",
          revision: "",
        }),
      },
      csrf,
    )
      .then((p) => {
        if (cancelled) return;
        setCurrent(p);
        const c = p.containers[0];
        if (c) {
          setMemory(String(c.memoryMiB || 512));
          setCPU(String(c.cpus || 0.5));
        }
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof APIError && e.status === 401) onUnauthorized();
        setError((e as Error).message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [container.project, container.service, csrf, attempt, onUnauthorized]);
  const valid =
    Number.isInteger(Number(memory)) &&
    Number(memory) >= 6 &&
    Number(memory) <= 1048576 &&
    Number(cpu) >= 0.01 &&
    Number(cpu) <= 1024 &&
    Math.abs(Number(cpu) * 100 - Math.round(Number(cpu) * 100)) < 0.000001;
  async function review() {
    setBusy(true);
    setError("");
    try {
      const p = await request<ResourcePreview>(
        "/resources/preview",
        { method: "POST", body: JSON.stringify(body()) },
        csrf,
      );
      setCurrent(p);
      setPreview(p);
    } catch (e) {
      if (e instanceof APIError && e.status === 401) onUnauthorized();
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    if (!preview) return;
    setBusy(true);
    setError("");
    try {
      const r = await request<ResourceResult>(
        "/resources/apply",
        {
          method: "POST",
          body: JSON.stringify({ ...body(), revision: preview.revision }),
        },
        csrf,
      );
      setResult(r);
      onApplied();
    } catch (e) {
      if (e instanceof APIError && e.status === 401) onUnauthorized();
      setError((e as Error).message);
      setPreview(null);
    } finally {
      setBusy(false);
    }
  }
  const modeUnavailable =
    (mode === "commit" && !current?.gitReady) ||
    (mode === "push" && (!current?.gitReady || !current?.pushEnabled));
  return (
    <Modal
      title="تنظیم محدودیت منابع"
      wide
      scrollKey={result ? "result" : preview ? "review" : "edit"}
      footer={
        !loading && current && !result ? (
          <div className="resource-footer">
            <span>
              {busy
                ? "در حال پردازش؛ تا دریافت نتیجه صبر کنید…"
                : preview
                  ? "پس از تأیید، تغییر روی همین میزبان اجرا می‌شود."
                  : "در مرحلهٔ بعد، تغییرات را قبل از اجرا مرور کنید."}
            </span>
            <div>
              <button
                className="secondary"
                disabled={busy}
                onClick={() => (preview ? setPreview(null) : onClose())}
              >
                {preview ? "ویرایش مقادیر" : "انصراف"}
              </button>
              <button
                className="primary"
                disabled={busy || !valid || modeUnavailable}
                onClick={() => void (preview ? apply() : review())}
              >
                {busy ? (
                  <LoaderCircle size={16} className="spin" />
                ) : preview ? (
                  <Check size={16} />
                ) : (
                  <ArrowLeft size={16} />
                )}{" "}
                {preview ? "تأیید و اعمال محدودیت‌ها" : "بازبینی تغییرات"}
              </button>
            </div>
          </div>
        ) : undefined
      }
      onClose={() => {
        if (!busy) onClose();
      }}
    >
      <div className="resource-identity">
        <span className="resource-icon">
          <SlidersHorizontal size={24} />
        </span>
        <div>
          <span className="eyebrow">
            {container.project === "infra" ? "سرویس زیرساخت" : "سرویس پروژه"}
          </span>
          <h3 dir="ltr">
            {container.project} <span>/</span> {container.service}
          </h3>
          <p>تغییر پایدار در Compose؛ اعمال مستقیم روی کانتینرهای همین سرویس</p>
        </div>
      </div>
      {error && (
        <div className="alert" role="alert">
          <TriangleAlert size={18} />
          <span>{error}</span>
        </div>
      )}
      {loading ? (
        <div className="resource-loading" role="status">
          <LoaderCircle className="spin" /> در حال خواندن محدودیت‌های فعلی…
        </div>
      ) : !current ? (
        <button className="secondary" onClick={() => setAttempt((x) => x + 1)}>
          تلاش دوباره
        </button>
      ) : result ? (
        <div className="resource-result" role="status">
          <CheckCircle2 size={42} />
          <h3>محدودیت‌های سرویس به‌روز شد</h3>
          <p>
            {memory} MiB حافظه و {cpu} هستهٔ CPU برای همهٔ کانتینرهای این سرویس
            اعمال شد.
          </p>
          <p>
            {result.pushed
              ? "commit در origin منتشر شد."
              : result.commit
                ? "commit محلی ثبت شد؛ چیزی push نشد."
                : "فایل Compose ذخیره شد؛ چیزی commit یا push نشد."}
          </p>
          {result.commit && <code dir="ltr">{result.commit}</code>}
          {result.warning && (
            <div className="alert">
              <TriangleAlert size={18} />
              <span>
                {result.warning.startsWith(
                  "Limits applied and committed locally; Git push failed",
                )
                  ? "محدودیت‌ها اعمال و commit محلی ثبت شد، اما push ناموفق بود. همین commit را به‌صورت دستی push کنید."
                  : result.warning}
              </span>
            </div>
          )}
          <button className="primary" onClick={onClose}>
            بازگشت به سرویس‌ها
          </button>
        </div>
      ) : (
        <>
          <div className="resource-steps" aria-label="مراحل تنظیم منابع">
            <span className={!preview ? "active" : ""}>
              <b>{preview ? <Check size={14} /> : "۱"}</b> تنظیم منابع
            </span>
            <i />
            <span className={preview ? "active" : ""}>
              <b>۲</b> بازبینی و اعمال
            </span>
          </div>
          <div className="resource-layout">
            <aside className="resource-current">
              <span className="eyebrow">محدودیت فعلی Docker</span>
              <strong>{number(current.containers.length, 0)} کانتینر</strong>
              <p>مقادیر، سقف مجاز مصرف هستند.</p>
              {current.containers.map((c) => (
                <div className="resource-replica" key={c.id}>
                  <code>{c.id.slice(0, 12)}</code>
                  <span>
                    <MemoryStick size={15} />
                    <b dir="ltr">
                      {c.memoryMiB ? `${c.memoryMiB} MiB` : "بدون محدودیت"}
                    </b>
                  </span>
                  <span>
                    <Cpu size={15} />
                    <b dir="ltr">{c.cpus ? `${c.cpus} CPU` : "بدون محدودیت"}</b>
                  </span>
                </div>
              ))}
              <div className="resource-source">
                <GitBranch size={15} />
                <span>
                  <code dir="ltr">{current.file}</code>
                  <small>{current.branch || "بدون شاخهٔ Git"}</small>
                </span>
              </div>
            </aside>
            <div className="resource-editor">
              {preview ? (
                <>
                  <span className="eyebrow">تغییر پیشنهادی</span>
                  <h3>یک تنظیم برای تمام replicaها</h3>
                  <div className="resource-review-values">
                    <div>
                      <MemoryStick size={20} />
                      <span>حافظهٔ هر کانتینر</span>
                      <strong dir="ltr">
                        {memory} <small>MiB</small>
                      </strong>
                    </div>
                    <div>
                      <Cpu size={20} />
                      <span>سقف پردازنده</span>
                      <strong dir="ltr">
                        {cpu} <small>CPU</small>
                      </strong>
                    </div>
                  </div>
                  <div className="resource-diff" dir="ltr">
                    {preview.changes.length ? (
                      preview.changes.map((line, i) => (
                        <code key={i}>{line}</code>
                      ))
                    ) : (
                      <code>Compose already matches these limits.</code>
                    )}
                  </div>
                  <div className="resource-selected-mode">
                    <GitBranch size={18} />
                    <div>
                      <strong>{modes[mode].title}</strong>
                      <p>{modes[mode].text}</p>
                    </div>
                  </div>
                  <p className="resource-hint">
                    <TriangleAlert size={16} /> کاهش حافظه به زیر مصرف فعلی ممکن
                    است باعث خطای برنامه شود. سرویس restart نمی‌شود؛ مقادیر
                    داخلی مثل GOMEMLIMIT و سقف Redis را جداگانه تنظیم کنید.
                  </p>
                </>
              ) : (
                <>
                  <span className="eyebrow">بودجهٔ منابع سرویس</span>
                  <h3>محدودیت جدید هر کانتینر</h3>
                  <p className="resource-hint">
                    مقدار دلخواه را وارد کنید یا از گزینه‌های آماده کمک بگیرید.
                  </p>
                  <div className="resource-fields">
                    <div className="resource-field">
                      <label htmlFor="resource-memory">
                        <MemoryStick size={18} /> حافظهٔ RAM <small>MiB</small>
                      </label>
                      <input
                        id="resource-memory"
                        type="number"
                        min="6"
                        max="1048576"
                        step="1"
                        dir="ltr"
                        value={memory}
                        onChange={(e) => setMemory(e.target.value)}
                      />
                      <div className="resource-presets">
                        {[256, 512, 1024, 2048].map((n) => (
                          <button
                            key={n}
                            aria-pressed={Number(memory) === n}
                            onClick={() => setMemory(String(n))}
                          >
                            {n >= 1024 ? `${n / 1024} GiB` : `${n} MiB`}
                          </button>
                        ))}
                      </div>
                    </div>
                    <div className="resource-field">
                      <label htmlFor="resource-cpu">
                        <Cpu size={18} /> پردازنده <small>هسته</small>
                      </label>
                      <input
                        id="resource-cpu"
                        type="number"
                        min="0.01"
                        max="1024"
                        step="0.01"
                        dir="ltr"
                        value={cpu}
                        onChange={(e) => setCPU(e.target.value)}
                      />
                      <div className="resource-presets">
                        {[0.25, 0.5, 1, 2].map((n) => (
                          <button
                            key={n}
                            aria-pressed={Number(cpu) === n}
                            onClick={() => setCPU(String(n))}
                          >
                            {n} CPU
                          </button>
                        ))}
                      </div>
                    </div>
                  </div>
                  {!valid && (
                    <p className="expired-text">
                      حافظه باید عدد صحیح از ۶ تا ۱۰۴۸۵۷۶ MiB و CPU بین ۰٫۰۱ و
                      ۱۰۲۴ با حداکثر دو رقم اعشار باشد.
                    </p>
                  )}
                  <fieldset className="resource-modes">
                    <legend>ذخیره در Git</legend>
                    {Object.entries(modes).map(([key, v]) => (
                      <label
                        key={key}
                        className={mode === key ? "selected" : ""}
                      >
                        <input
                          type="radio"
                          name="resource-git"
                          value={key}
                          checked={mode === key}
                          disabled={
                            key !== "local" &&
                            (!current.gitReady ||
                              (key === "push" && !current.pushEnabled))
                          }
                          onChange={() =>
                            setMode(key as ResourceRequest["gitMode"])
                          }
                        />
                        <span>
                          <strong>{v.title}</strong>
                          <small>{v.text}</small>
                        </span>
                      </label>
                    ))}
                  </fieldset>
                  {!current.gitReady && (
                    <p className="resource-hint">
                      برای commit، فایل Compose باید در یک مخزن Git بدون تغییرات
                      ثبت‌نشده باشد. مسیر کد پروژه را در تنظیمات runner معرفی
                      کنید.
                    </p>
                  )}
                  {!current.pushEnabled && (
                    <p className="resource-local-note">
                      <CheckCircle2 size={15} /> push روی این سرور غیرفعال است؛
                      مناسب تست محلی.
                    </p>
                  )}
                </>
              )}
            </div>
          </div>
        </>
      )}
    </Modal>
  );
}
