import {
  Box,
  CalendarClock,
  CircleHelp,
  Database,
  Pause,
  Play,
  AlertCircle,
} from "lucide-react";
import { bytes, date, number } from "../format";
import { type Project, type Change, type Container } from "../types";
import { Badge } from "./Badge";
import { ContainersTable } from "./ContainersTable";

const metricNames: Record<string, string> = {
  postgres_storage: "فضای دیتابیس",
  postgres_connections: "اتصال PostgreSQL",
  postgres_transactions: "تراکنش دیتابیس",
  rabbitmq_messages: "پیام‌های RabbitMQ",
  rabbitmq_memory: "حافظهٔ صف‌ها",
  redis_operations: "عملیات Redis",
  requests: "درخواست‌های HTTP",
  uptime: "پاسخ‌گویی عمومی",
};
const metricUnits: Record<string, string> = {
  connections: "اتصال",
  messages: "پیام",
  "ops/s": "عملیات/ث",
  "req/s": "درخواست/ث",
};
export function ProjectDetail({
  project: p,
  metricsError,
  metricsAt,
  onChange,
  onResources,
}: {
  project: Project;
  metricsError: string;
  metricsAt: string | null;
  onChange: (action: Change["action"]) => void;
  onResources?: (container: Container) => void;
}) {
  const canManage = p.containers.some((c) => c.managed);
  return (
    <>
      <div className="detail-title">
        <span className="detail-monogram">
          {p.name.slice(0, 2).toUpperCase()}
        </span>
        <div>
          <h2 dir="ltr">{p.name}</h2>
          <Badge status={p.status} />
        </div>
      </div>
      <section className="lifecycle">
        <div>
          <span className="eyebrow">چرخهٔ عمر پروژه</span>
          <strong>
            <CalendarClock size={17} />
            {date(p.policy.expiresAt)}
          </strong>
          <p>
            {p.policy.suspended
              ? "توقف دستی فعال است. برای روشن‌شدن، پروژه را تمدید کنید."
              : p.status === "expired"
                ? "اعتبار تمام شده است. تمدید، سرویس‌های متوقف‌شده توسط مدیر را روشن می‌کند."
                : "با رسیدن زمان انقضا، سرویس‌های قابل مدیریت خاموش می‌شوند."}
          </p>
        </div>
        <div className="lifecycle-actions">
          <button
            className="primary"
            disabled={!canManage}
            onClick={() => onChange("renew")}
          >
            <Play size={15} /> تمدید و فعال‌سازی
          </button>
          <button
            className="secondary"
            disabled={!canManage}
            onClick={() => onChange("expiration")}
          >
            <CalendarClock size={15} /> تنظیم انقضا
          </button>
          <button
            className="danger-text"
            disabled={
              !canManage || p.status === "suspended" || p.status === "expired"
            }
            onClick={() => onChange("suspend")}
          >
            <Pause size={15} /> توقف پروژه
          </button>
        </div>
      </section>
      <h3 className="section-title">
        <Box size={18} /> سرویس‌های پروژه
        <span>{number(p.containers.length, 0)}</span>
      </h3>
      <ContainersTable containers={p.containers} onResources={onResources} />
      <div className="section-heading">
        <h3 className="section-title">
          <Database size={18} /> مصرف زیرساخت به تفکیک پروژه
        </h3>
        <span className="muted">
          {metricsAt ? date(metricsAt) : "در انتظار نمونهٔ اول"}
        </span>
      </div>
      {metricsError && (
        <div className="alert">
          <AlertCircle size={16} /> بخشی از داده‌های مانیتورینگ در دسترس نیست.
        </div>
      )}
      <div className="project-metrics">
        {Object.entries(metricNames).map(([key, label]) => {
          const rows = p.metrics.filter((m) => m.name === key);
          const m = rows.length
            ? { ...rows[0], value: rows.reduce((s, x) => s + x.value, 0) }
            : null;
          return (
            <article key={key}>
              <span>{label}</span>
              <strong dir="auto">
                {m
                  ? m.unit === "bytes"
                    ? bytes(m.value)
                    : m.unit === "ratio"
                      ? m.value === 1
                        ? "پاسخ‌گو"
                        : "بدون پاسخ"
                      : `${number(m.value)} ${metricUnits[m.unit] ?? m.unit}`
                  : "—"}
              </strong>
            </article>
          );
        })}
      </div>
      <p className="footnote">
        <CircleHelp size={16} /> خط تیره یعنی داده موجود نیست. آمار دیتابیس با
        نام استاندارد پروژه، صف با vhost و Redis با تله‌متری برنامه نسبت داده
        می‌شود. CPU و RAM مشترک قابل تفکیک دقیق نیستند.
      </p>
      <p className="footnote">
        توقف، RAM و CPU کانتینرهای برنامه را آزاد می‌کند. دیتابیس، فایل‌ها و
        داده‌های Redis و صف‌ها حفظ می‌شوند؛ مصرف آن‌ها ممکن است باقی بماند.
      </p>
    </>
  );
}
