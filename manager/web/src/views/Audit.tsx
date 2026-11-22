import { useEffect, useRef, useState } from "react";
import { ArrowDownLeft, History, RefreshCw } from "lucide-react";
import { APIError, request } from "../api";
import { date, auditDetail } from "../format";
import { type AuditEvent, type Project } from "../types";
import { Empty } from "../components/Empty";

const actionNames: Record<string, string> = {
  "resources.apply": "تغییر محدودیت منابع",
  login: "ورود به پنل",
  logout: "خروج از پنل",
  suspend: "درخواست توقف",
  expiration: "تغییر انقضا",
  renew: "تمدید اعتبار",
  stop: "خاموش‌کردن سرویس",
  start: "روشن‌کردن سرویس",
  sync: "همگام‌سازی دستی",
  ship_target: "تغییر مقصد سراسری",
  ship_probe: "بررسی اتصال مقصد",
  ship_plan: "پیش‌نمایش ارسال",
  ship: "ارسال و Deploy",
  ship_progress: "مرحلهٔ ارسال",
};
const outcomeNames: Record<string, string> = {
  partial: "نیازمند پیگیری",
  success: "موفق",
  failure: "ناموفق",
  accepted: "ذخیره‌شده",
  intent: "شروع عملیات",
  succeeded: "موفق",
  failed: "ناموفق",
  interrupted: "قطع‌شده",
  rolled_back: "بازگشت به نسخهٔ قبل",
  preflight: "بررسی اولیه",
  transfer: "انتقال image",
  verified: "تأیید image",
  deploy: "راه‌اندازی",
  healthy: "آماده",
};
export function Audit({
  projects,
  version,
  onUnauthorized,
}: {
  projects: Project[];
  version: number;
  onUnauthorized: () => void;
}) {
  const [project, setProject] = useState("");
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [historyMode, setHistoryMode] = useState(false);
  const [reload, setReload] = useState(0);
  const browsingHistory = useRef(false);
  useEffect(() => {
    let stopped = false;
    async function load() {
      try {
        const rows = await request<AuditEvent[]>(
          `/audit?project=${encodeURIComponent(project)}`,
        );
        if (!stopped) {
          setEvents(rows);
          setMore(rows.length === 50);
          setError("");
        }
      } catch (e) {
        if (e instanceof APIError && e.status === 401) onUnauthorized();
        if (!stopped) setError("دریافت تاریخچه ممکن نشد.");
      } finally {
        if (!stopped) setLoading(false);
      }
    }
    setEvents([]);
    setLoading(true);
    browsingHistory.current = false;
    setHistoryMode(false);
    void load();
    const id = setInterval(() => {
      if (!browsingHistory.current) void load();
    }, 15000);
    return () => {
      stopped = true;
      clearInterval(id);
    };
  }, [project, version, onUnauthorized, reload]);
  async function older() {
    browsingHistory.current = true;
    setHistoryMode(true);
    setBusy(true);
    try {
      const rows = await request<AuditEvent[]>(
        `/audit?project=${encodeURIComponent(project)}&before=${events.at(-1)?.id ?? ""}`,
      );
      setEvents((old) => [...old, ...rows]);
      setMore(rows.length === 50);
    } catch {
      setError("دریافت رویدادهای قدیمی‌تر ممکن نشد.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel audit-panel">
      <div className="panel-heading">
        <div>
          <h2>
            <History size={18} /> دفتر فعالیت‌ها
          </h2>
          <p>رویدادهای پایدار، از جدیدترین به قدیمی‌ترین</p>
        </div>
        <select
          aria-label="فیلتر تاریخچه بر اساس پروژه"
          value={project}
          onChange={(e) => setProject(e.target.value)}
        >
          <option value="">تمام پروژه‌ها</option>
          {projects.map((p) => (
            <option value={p.name} key={p.name}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
      {error && (
        <div className="alert" role="alert">
          {error}
        </div>
      )}
      {historyMode && (
        <div className="table-footer">
          <span>برای خواندن سوابق، به‌روزرسانی زنده موقتاً متوقف است.</span>
          <button className="secondary" onClick={() => setReload((v) => v + 1)}>
            <RefreshCw size={13} /> آخرین رویدادها
          </button>
        </div>
      )}
      {loading && (
        <div className="loading-state">
          <RefreshCw className="spin" />
          <p>در حال دریافت تاریخچه…</p>
        </div>
      )}
      <div className="table-scroll">
        <table className="audit-table">
          <thead>
            <tr>
              <th>رویداد / جزئیات</th>
              <th>پروژه</th>
              <th>عامل</th>
              <th>نتیجه</th>
              <th>زمان</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.id}>
                <td>
                  <strong>{actionNames[e.action] ?? e.action}</strong>
                  <small dir="auto">{auditDetail(e.detail)}</small>
                </td>
                <td dir="ltr">{e.project || "—"}</td>
                <td>
                  {e.actor === "reconciler"
                    ? "همگام‌ساز خودکار"
                    : e.actor === "authentication"
                      ? "احراز هویت"
                      : e.actor}
                </td>
                <td>
                  <span
                    className={`badge ${e.outcome === "failure" ? "red" : e.outcome === "success" ? "green" : "gray"}`}
                  >
                    {outcomeNames[e.outcome] ?? e.outcome}
                  </span>
                </td>
                <td>{date(e.at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {events.length === 0 && !error && !loading && (
        <Empty
          icon={<History />}
          title="رویدادی ثبت نشده است"
          text="عملیات بعدی به‌صورت خودکار در این بخش ظاهر می‌شود."
        />
      )}
      {more && (
        <div className="load-more">
          <button
            className="secondary"
            onClick={() => void older()}
            disabled={busy}
          >
            {busy ? "در حال دریافت…" : "رویدادهای قدیمی‌تر"}
            <ArrowDownLeft size={15} />
          </button>
        </div>
      )}
    </section>
  );
}
