import { useCallback, useEffect, useState, type FormEvent } from "react";
import { AlertCircle, CheckCircle2, Globe2, LoaderCircle, Plus, Rocket, Save, Upload, X, Eye, EyeOff, PackageCheck } from "lucide-react";
import { APIError, request } from "../api";
import { bytes, date } from "../format";
import { Modal } from "../components/Modal";

type Settings = {
  name: string; domain: string; service: string; port: number; migrationService: string;
  packageID: string; revision: string; services: string[]; images: string[]; envKeys: string[];
  provisioned: boolean; deployed: boolean; configured: boolean;
};
type Job = { id: string; name: string; action: string; status: string; phase: string; message?: string; createdAt: number };
type Catalog = { projects: Settings[]; jobs: Job[]; appsDir: string; dataDir: string; maxUploadBytes: number; edgePort: number };
type Package = { id: string; name: string; services: string[]; images: string[] };
const empty = (): Settings => ({ name: "", domain: "", service: "", port: 8080, migrationService: "", packageID: "", revision: "", services: [], images: [], envKeys: [], provisioned: false, deployed: false, configured: false });
const phases: Record<string, string> = { "preserving-limits": "حفظ محدودیت‌های فعلی مقصد", queued: "در صف", provisioning: "ساخت منابع مشترک", routing: "بررسی و اعمال nginx", images: "بارگذاری و بررسی image", migrating: "اجرای migration", starting: "انتظار برای سلامت سرویس‌ها", rollback: "بازگردانی نسخهٔ قبلی", ready: "انجام شد" };

export function Deployments({ csrf, onUnauthorized }: { csrf: string; onUnauthorized: () => void }) {
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [draft, setDraft] = useState<Settings>(empty);
  const [selected, setSelected] = useState("");
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState("");
  const [active, setActive] = useState("");
  const [confirmation, setConfirmation] = useState(false);
  const [env, setEnv] = useState<{ key: string; value: string; visible: boolean }[]>([]);
  const [removed, setRemoved] = useState<string[]>([]);
  const fail = useCallback((e: unknown) => {
    if (e instanceof APIError && e.status === 401) onUnauthorized();
    else setError((e as Error).message);
  }, [onUnauthorized]);
  const load = useCallback(async () => {
    try { setCatalog(await request<Catalog>("/deployments/catalog")); }
    catch (e) { fail(e); }
  }, [fail]);
  useEffect(() => { void load(); const timer = setInterval(() => void load(), 4000); return () => clearInterval(timer); }, [load]);
  useEffect(() => {
    if (!active || !catalog) return;
    const job = catalog.jobs.find(j => j.id === active);
    if (!job || job.status === "queued" || job.status === "running") return;
    setActive("");
    if (job.status === "ready") {
      const saved = catalog.projects.find(p => p.name === job.name);
      if (saved) { setDraft(saved); setSelected(saved.name); setDirty(false); setEnv([]); setRemoved([]); }
      setNotice(job.action === "save" ? "دامنه و env روی همین سرور ذخیره شدند؛ برای اجرای نسخه، دیپلوی را بزنید." : "نسخهٔ پروژه با موفقیت دیپلوی شد.");
    } else setError(job.message || "عملیات کامل نشد؛ مرحلهٔ ثبت‌شده را بررسی کنید.");
  }, [active, catalog]);
  function choose(name: string) {
    setSelected(name); setDraft(catalog?.projects.find(p => p.name === name) ?? empty());
    setEnv([]); setRemoved([]); setDirty(false); setError(""); setNotice("");
  }
  function edit(patch: Partial<Settings>) { setDraft(d => ({ ...d, ...patch })); setDirty(true); setNotice(""); }
  const running = !!active || !!catalog?.jobs.some(j => j.status === "queued" || j.status === "running");
  const disabled = !!busy || running;
  async function upload(file: File) {
    setBusy("upload"); setError("");
    try {
      if (file.size > (catalog?.maxUploadBytes ?? 2 ** 31)) throw new Error("حداکثر اندازهٔ بسته ۲ گیگابایت است.");
      const response = await fetch(`/api/deployments/upload?project=${encodeURIComponent(draft.name)}`, {
        method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/x-tar", "X-CSRF-Token": csrf }, body: file,
      });
      if (response.status === 401) { onUnauthorized(); return; }
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || "بارگذاری بسته ناموفق بود.");
      const pack = result as Package;
      edit({ packageID: pack.id, services: pack.services, images: pack.images,
        service: pack.services.includes(draft.service) ? draft.service : pack.services.find(s => s.endsWith("-api") || s.endsWith("-web")) ?? pack.services[0],
        migrationService: pack.services.includes(draft.migrationService) ? draft.migrationService : "" });
      setNotice("بسته بررسی شد؛ دامنه و env را ذخیره کنید و بعد دیپلوی را بزنید.");
    } catch (e) { fail(e); } finally { setBusy(""); }
  }
  async function save(event: FormEvent) {
    event.preventDefault(); setBusy("save"); setError(""); setNotice("");
    try {
      if (new Set(env.map(row => row.key)).size !== env.length) throw new Error("نام متغیرهای جدید نباید تکراری باشد.");
      const job = await request<Job>("/deployments/save", { method: "POST", body: JSON.stringify({
        key: crypto.randomUUID(), name: draft.name, revision: draft.revision, domain: draft.domain, service: draft.service,
        port: draft.port, migrationService: draft.migrationService, packageID: draft.packageID,
        environment: Object.fromEntries(env.map(row => [row.key, row.value])), removeEnv: removed,
      }) }, csrf);
      setActive(job.id); await load();
    } catch (e) { fail(e); } finally { setBusy(""); }
  }
  async function deploy() {
    setBusy("deploy"); setError(""); setNotice("");
    try {
      const job = await request<Job>("/deployments/deploy", { method: "POST", body: JSON.stringify({ key: crypto.randomUUID(), name: draft.name, revision: draft.revision }) }, csrf);
      setActive(job.id); setConfirmation(false); await load();
    } catch (e) { fail(e); } finally { setBusy(""); }
  }
  return <div className="deployment-page">
    <section className="panel deployment-intro">
      <PackageCheck size={28}/><div><h2>از پروژهٔ خودت تا دامنهٔ آماده</h2><p>بسته را از سیستم توسعه بارگذاری کن؛ env، داده‌ها و تنظیمات دامنه روی سرور مقصد باقی می‌مانند.</p>
        <ol><li>image را با یک tag تازه بساز و بستهٔ دیپلوی بگیر.</li><li>بسته، دامنه و env مقصد را ذخیره کن.</li><li>دیپلوی کن؛ دفعهٔ بعد فقط بستهٔ نسخهٔ جدید را انتخاب کن.</li></ol>
        <code dir="ltr">make manager-package PROJECT_DIR=/path/to/project TAG=your-tag OUTPUT=/tmp/project.tar</code>
        <small>این فرمان در پوشهٔ shared infrastructure اجرا می‌شود؛ imageها باید قبلاً برای linux/amd64 ساخته شده باشند.</small>
      </div>
    </section>
    {error && <div className="alert" role="alert"><AlertCircle size={18}/>{error}</div>}
    {notice && <div className="toast" role="status"><CheckCircle2 size={18}/>{notice}</div>}
    {!catalog ? <p role="status">در حال دریافت پروژه‌های مقصد…</p> : <>
      <section className="panel deployment-editor">
        <div className="panel-heading"><div><h2>پروژه و مقصد</h2><p>همین میزبان · <span dir="ltr">{catalog.appsDir}</span> · داده‌ها: <span dir="ltr">{catalog.dataDir}/apps</span></p></div><Globe2 size={22}/></div>
        <form onSubmit={event => void save(event)}>
          <fieldset disabled={disabled}>
            <div className="deployment-fields">
              <label>پروژه<select aria-label="انتخاب پروژه برای دیپلوی" value={selected} onChange={e => choose(e.target.value)}><option value="">پروژهٔ جدید</option>{catalog.projects.map(p => <option key={p.name} value={p.name}>{p.name}</option>)}</select></label>
              <label>شناسهٔ پروژه<input aria-label="شناسهٔ پروژه" dir="ltr" required pattern="[a-z][a-z0-9-]{1,30}" value={draft.name} readOnly={!!selected || !!draft.packageID} onChange={e => edit({ name: e.target.value })} placeholder="didchi-platform"/></label>
            </div>
            <div className="deployment-package">
              <Upload size={22}/><div><label htmlFor="deployment-file">بستهٔ نسخهٔ جدید</label><small>فایل tar ساخته‌شده با manager-package؛ بدون env و دیتابیس</small>
                <input id="deployment-file" type="file" accept=".tar" disabled={disabled || !/^[a-z][a-z0-9-]{1,30}$/.test(draft.name)} onChange={e => { const file = e.target.files?.[0]; if (file) void upload(file); e.target.value = ""; }}/>
              </div>{busy === "upload" && <LoaderCircle className="spin" aria-label="در حال بارگذاری"/>}
            </div>
            {draft.images.length > 0 && <div className="deployment-images" dir="ltr">{draft.images.map(image => <code key={image}>{image}</code>)}</div>}
            <div className="deployment-fields">
              <label>دامنه<input aria-label="دامنهٔ پروژه" dir="ltr" required placeholder="app.example.com" value={draft.domain} onChange={e => edit({ domain: e.target.value.trim().toLowerCase() })}/><small>فقط نام دامنه؛ مبدأ HTTP در CDN باید IP همین سرور و پورت {catalog.edgePort ?? 80} باشد.</small></label>
              <label>سرویس HTTP<select aria-label="سرویس HTTP" required value={draft.service} onChange={e => edit({ service: e.target.value })}><option value="">انتخاب سرویس</option>{draft.services.map(s => <option key={s} value={s}>{s}</option>)}</select></label>
              <label>پورت داخل کانتینر<input aria-label="پورت داخل کانتینر" type="number" dir="ltr" min={1} max={65535} required value={draft.port} onChange={e => edit({ port: Number(e.target.value) })}/></label>
              <label>سرویس migration<select aria-label="سرویس migration" value={draft.migrationService} onChange={e => edit({ migrationService: e.target.value })}><option value="">بدون migration خودکار</option>{draft.services.filter(s => s !== draft.service).map(s => <option key={s} value={s}>{s}</option>)}</select><small>در هر دیپلوی، پیش از اجرای سرویس‌های اصلی یک بار اجرا می‌شود.</small></label>
            </div>
            <div className="deployment-env">
              <h3>env برنامه روی سرور مقصد</h3><p>مقادیر فعلی مخفی‌اند. برای تغییر، همان نام را با مقدار جدید اضافه کن. فایل اتصال منابع مشترک حفظ می‌شود؛ این مقدارها به‌عنوان override برنامه اعمال می‌شوند.</p>
              <div className="deployment-env-keys">{draft.envKeys.length === 0 && <span>هنوز متغیر برنامه‌ای ذخیره نشده است.</span>}{draft.envKeys.map(key => <button type="button" key={key} className={removed.includes(key) ? "env-key removed" : "env-key"} onClick={() => { setRemoved(list => list.includes(key) ? list.filter(k => k !== key) : [...list, key]); setDirty(true); }} title={removed.includes(key) ? "لغو حذف" : "حذف در ذخیرهٔ بعدی"}><code>{key}</code><X size={12}/></button>)}</div>
              {env.map((row, i) => <div className="deployment-env-row" key={i}>
                <input aria-label={`نام متغیر ${i + 1}`} required pattern="[A-Za-z_][A-Za-z0-9_]{0,127}" placeholder="APP_KEY" dir="ltr" value={row.key} onChange={e => { setEnv(items => items.map((v, n) => n === i ? { ...v, key: e.target.value } : v)); setDirty(true); }}/>
                <input aria-label={`مقدار متغیر ${i + 1}`} autoComplete="off" type={row.visible ? "text" : "password"} dir="ltr" value={row.value} onChange={e => { setEnv(items => items.map((v, n) => n === i ? { ...v, value: e.target.value } : v)); setDirty(true); }}/>
                <button type="button" className="icon-button" aria-label={row.visible ? "مخفی‌کردن مقدار" : "نمایش مقدار"} onClick={() => setEnv(items => items.map((v, n) => n === i ? { ...v, visible: !v.visible } : v))}>{row.visible ? <EyeOff size={16}/> : <Eye size={16}/>}</button>
                <button type="button" className="icon-button" aria-label={`حذف تغییر ${i + 1}`} onClick={() => { setEnv(items => items.filter((_, n) => n !== i)); setDirty(true); }}><X size={16}/></button>
              </div>)}
              <button type="button" className="secondary" onClick={() => { setEnv(items => [...items, { key: "", value: "", visible: false }]); setDirty(true); }}><Plus size={15}/>افزودن یا تغییر متغیر</button>
            </div>
            <p className="deployment-limits-note">محدودیت‌های فعلی کانتینرهای مقصد، از جمله CPU و حافظه، در هر دیپلوی حفظ می‌شوند. محدودیت‌های بسته فقط برای سرویس‌های جدید اعمال می‌شوند.</p>
            <div className="deployment-actions"><button className="primary" type="submit" disabled={disabled || !draft.services.length}><Save size={16}/>ذخیرهٔ دامنه و env مقصد</button>
              <button className="secondary" type="button" disabled={disabled || dirty || !draft.configured} onClick={() => setConfirmation(true)}><Rocket size={16}/>دیپلوی نسخهٔ ذخیره‌شده</button>
              {dirty && <small>پیش از دیپلوی، تغییرات را ذخیره کن.</small>}</div>
          </fieldset>
        </form>
      </section>
      <section className="panel"><div className="panel-heading"><div><h2>عملیات دیپلوی</h2><p>ثبت دامنه، آماده‌سازی منابع، migration و نتیجهٔ سلامت سرویس‌ها</p></div></div>
        <div className="table-scroll"><table className="project-table"><thead><tr><th>پروژه</th><th>عملیات</th><th>مرحله</th><th>وضعیت</th><th>زمان</th></tr></thead><tbody>{catalog.jobs.map(job => <tr key={job.id}><td><b dir="ltr">{job.name}</b>{job.message && <p role="status" className="deployment-job-message">{job.message}</p>}</td><td>{job.action === "save" ? "ذخیرهٔ تنظیمات" : "دیپلوی"}</td><td>{phases[job.phase] ?? job.phase}</td><td>{({ queued: "در صف", running: "در حال اجرا", ready: "موفق", failed: "ناموفق", interrupted: "قطع‌شده" } as Record<string, string>)[job.status] ?? job.status}</td><td>{date(new Date(job.createdAt * 1000).toISOString())}</td></tr>)}</tbody></table>{!catalog.jobs.length && <p className="deployment-empty">هنوز عملیاتی اجرا نشده است. حداکثر اندازهٔ بسته: {bytes(catalog.maxUploadBytes)}</p>}</div>
      </section>
    </>}
    {confirmation && <Modal title="دیپلوی پروژه روی همین سرور" onClose={() => setConfirmation(false)}>
      <div className="deployment-confirm"><p><b dir="ltr">{draft.name}</b> روی <b dir="ltr">{draft.domain}</b> دیپلوی می‌شود؛ env ذخیره‌شدهٔ همین مقصد استفاده می‌شود.</p><div dir="ltr">{draft.images.map(image => <p key={image}><code>{image}</code></p>)}</div>
        {draft.migrationService && <p>سرویس <b dir="ltr">{draft.migrationService}</b> پیش از برنامه اجرا می‌شود. بازگشت کانتینرها، migration دیتابیس را برنمی‌گرداند.</p>}
        <p>اگر نسخهٔ جدید سالم نشود، کانتینرها و env آخرین نسخهٔ سالم بازگردانده می‌شوند. در اولین دیپلوی، نسخهٔ قبلی وجود ندارد.</p>
        <button className="primary" disabled={!!busy} onClick={() => void deploy()}><Rocket size={16}/>تأیید و دیپلوی پروژه</button>
      </div>
    </Modal>}
  </div>;
}
