import { useCallback, useEffect, useRef, useState } from "react";
import {
  Activity,
  CalendarClock,
  CheckCircle2,
  ChevronLeft,
  CircleHelp,
  Cpu,
  Filter,
  History,
  Layers3,
  LockKeyhole,
  LogOut,
  MemoryStick,
  Network,
  RefreshCw,
  Search,
  Server,
  ShieldCheck,
  X,
  AlertCircle,
  Rocket,
  Hammer,
  KeyRound,
} from "lucide-react";
import { APIError, request } from "../api";
import { bytes, number, statuses, sum, sumKnown } from "../format";
import { type Session, type Snapshot, type Page, type Change, type Container } from "../types";
import { Stat } from "../components/Stat";
import { ProjectRow } from "../components/ProjectRow";
import { ContainersTable } from "../components/ContainersTable";
import { ProjectDetail } from "../components/ProjectDetail";
import { Modal } from "../components/Modal";
import { ResourceModal } from "../components/ResourceModal";
import { ChangeModal } from "../components/ChangeModal";
import { Audit } from "./Audit";
import { Empty } from "../components/Empty";
import { BuildShip } from "./BuildShip";
import { Environment } from "./Environment";
import { VPN } from "./VPN";
import { Shipping } from "./Shipping";
import { Storefronts } from "./Storefronts";
import { ProjectDeployment } from "./ProjectDeployment";

const titles: Record<Page, string> = {
  vpn: "اتصال VPN",
  projects: "پروژه‌ها",
  infrastructure: "زیرساخت مشترک",
  audit: "تاریخچهٔ فعالیت‌ها",
  shipping: "ارسال و Deploy",
  buildship: "ساخت و ارسال",
  environment: "متغیرهای محیطی",
  storefront: "فروشگاه‌ساز",
  deployments: "دیپلوی پروژه",
};

export function Dashboard({
  session,
  onLogout,
}: {
  session: Session;
  onLogout: () => void;
}) {
  const [page, setPage] = useState<Page>("projects");
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [sort, setSort] = useState("name");
  const [selected, setSelected] = useState<string | null>(null);
  const [resource, setResource] = useState<Container | null>(null);
  const [change, setChange] = useState<Change | null>(null);
  const [clock, setClock] = useState(Date.now());
  const [auditVersion, setAuditVersion] = useState(0);
  const inFlight = useRef(false);
  const refresh = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      setSnapshot(await request<Snapshot>("/snapshot"));
      setError("");
    } catch (e) {
      if (e instanceof APIError && e.status === 401) onLogout();
      else
        setError(
          "همگام‌سازی با سرور قطع شده است؛ اطلاعات نمایش‌داده‌شده ممکن است قدیمی باشد.",
        );
    } finally {
      inFlight.current = false;
    }
  }, [onLogout]);
  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => {
      setClock(Date.now());
      void refresh();
    }, 5000);
    return () => window.clearInterval(id);
  }, [refresh]);
  useEffect(() => {
    if (!notice) return;
    const id = setTimeout(() => setNotice(""), 7000);
    return () => clearTimeout(id);
  }, [notice]);
  async function sync() {
    setBusy(true);
    try {
      await request("/sync", { method: "POST" }, session.csrf);
      setNotice("همگام‌سازی درخواست شد؛ وضعیت سرویس‌ها به‌روز می‌شود.");
      await refresh();
      setAuditVersion((v) => v + 1);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  async function logout() {
    try {
      await request("/logout", { method: "POST" }, session.csrf);
      onLogout();
    } catch (e) {
      setError((e as Error).message);
    }
  }
  const projects = snapshot?.projects ?? [];
  const containers = projects.flatMap((p) => p.containers);
  const filtered = projects
    .filter(
      (p) =>
        p.name.toLowerCase().includes(search.toLowerCase()) &&
        (filter === "all" || p.status === filter),
    )
    .sort((a, b) =>
      sort === "name"
        ? a.name.localeCompare(b.name)
        : (sum(b.containers, sort as "cpu" | "memory") ?? -1) -
          (sum(a.containers, sort as "cpu" | "memory") ?? -1),
    );
  const active = projects.filter(
    (p) => p.status === "online" || p.status === "degraded",
  ).length;
  const upcoming = projects.filter(
    (p) =>
      p.policy.expiresAt &&
      new Date(p.policy.expiresAt).getTime() > clock &&
      new Date(p.policy.expiresAt).getTime() - clock < 7 * 86400000 &&
      !p.policy.suspended,
  ).length;
  const age = snapshot?.syncedAt
    ? Math.max(
        0,
        Math.floor((clock - new Date(snapshot.syncedAt).getTime()) / 1000),
      )
    : null;
  const stale = age === null || age > 45 || !!error || !!snapshot?.error;
  const detail = projects.find((p) => p.name === selected);
  const menu = [
    { id: "vpn" as const, icon: Network, title: "اتصال VPN" },
    { id: "projects" as const, icon: Layers3, title: "پروژه‌ها" },
    { id: "infrastructure" as const, icon: Server, title: "زیرساخت مشترک" },
    { id: "deployments" as const, icon: Rocket, title: "دیپلوی پروژه" },
    { id: "shipping" as const, icon: Rocket, title: "ارسال و Deploy" },
    { id: "buildship" as const, icon: Hammer, title: "ساخت و ارسال" },
    { id: "environment" as const, icon: KeyRound, title: "متغیرهای محیطی" },
 {id:"storefront" as const,icon:Layers3,title:"فروشگاه‌ساز"},
    { id: "audit" as const, icon: History, title: "تاریخچهٔ فعالیت‌ها" },
  ];
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-icon">
            <Layers3 />
          </span>
          <span>
            مدیر زیرساخت<small>INFRA MANAGER</small>
          </span>
        </div>
        <div className="workspace">
          <span className="workspace-icon">
            <Server size={18} />
          </span>
          <div>
            زیرساخت مشترک
            <small>
              <span className={`dot ${stale ? "amber" : "green"}`} />
              {stale ? "بررسی اتصال" : "متصل به Docker"}
            </small>
          </div>
          <span className="workspace-tag">LOCAL</span>
        </div>
        <span className="nav-label">فضای کاری</span>
        <nav>
          {menu.map((item) => (
            <button
              key={item.id}
              onClick={() => {
                setPage(item.id);
                setSelected(null);
              }}
              className={page === item.id ? "nav-item active" : "nav-item"}
            >
              <item.icon size={19} />
              <span>{item.title}</span>
              {item.id === "projects" && (
                <span className="nav-count">{number(projects.length, 0)}</span>
              )}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="private-note">
            <ShieldCheck size={20} />
            <div>
              کنترل‌شده و قابل پیگیری<small>هر تغییر، یک ردپای مشخص</small>
            </div>
          </div>
          <div className="user">
            <span className="avatar">
              {session.user.slice(0, 1).toUpperCase()}
            </span>
            <div>
              <b dir="ltr">{session.user}</b>
              <small>مدیر سیستم</small>
            </div>
            <button
              className="icon-button"
              aria-label="خروج از حساب"
              title="خروج از حساب"
              onClick={() => void logout()}
            >
              <LogOut size={18} />
            </button>
          </div>
        </div>
      </aside>
      <div className="main-shell">
        <header className="topbar">
          <div className="breadcrumb">
            فضای کاری
            <ChevronLeft size={14} />
            <span>{titles[page]}</span>
          </div>
          <div className="topbar-end">
            <span className="private-pill">
              <LockKeyhole size={13} /> پنل خصوصی
            </span>
            <span className="top-date">
              {new Intl.DateTimeFormat("fa-IR", {
                month: "long",
                day: "numeric",
                weekday: "long",
              }).format(clock)}
            </span>
          </div>
        </header>
        <main className="content">
          <section className="page-heading">
            <div>
              <div className="eyebrow">
                مرکز کنترل /{" "}
                {page === "projects"
                  ? "PROJECTS"
                  : page === "storefront"
                    ? "STOREFRONT STUDIO"
                    : page === "deployments"
                    ? "PROJECT DEPLOYMENT"
                    : page === "shipping"
                    ? "SHIP & DEPLOY"
                    : page === "buildship"
                    ? "BUILD & SHIP"
                    : page === "vpn"
                    ? "VPN CLIENT"
                    : page === "environment"
                    ? "ENVIRONMENT"
                    : page === "audit"
                      ? "ACTIVITY"
                      : "INFRASTRUCTURE"}
              </div>
              <h1>
                {page === "projects"
                  ? "همهٔ پروژه‌ها، یک نمای روشن"
                  : titles[page]}
              </h1>
              <p>
                {page === "projects"
                  ? "وضعیت سرویس‌ها، مصرف منابع و زمان اعتبار پروژه‌ها را زیر نظر داشته باشید."
                  : page === "storefront"
                    ? "فروشگاه‌های مستقل را با طراحی و امکانات دلخواه روی همین میزبان راه‌اندازی کنید."
                    : page === "deployments"
                    ? "پروژه را از همین سیستم بسازید و روی مقصد انتخاب‌شده دیپلوی کنید؛ دامنه و env مقصد همین‌جا قابل تنظیم‌اند."
                    : page === "shipping"
                    ? "imageهای محلی را مستقیم به مقصد بفرستید و زیرساخت یا پروژه‌ها را راه‌اندازی کنید."
                    : page === "buildship"
                    ? "از پوشهٔ فعلی پروژه روی همین سیستم image بسازید و با بازگشت خودکار به مقصد بفرستید."
                    : page === "vpn"
                    ? "لینک اتصال را وارد کنید و دسترسی اینترنت سرویس‌های دلخواه را از مسیر VPN برقرار کنید."
                    : page === "environment"
                    ? "متغیرهای پروژه‌ها را روی مقصد ببینید، ویرایش کنید و بدون ساخت image روی سرویس‌ها اعمال کنید."
                    : page === "audit"
                      ? "از تصمیم تا اجرا؛ تمام تغییرات دستی و خودکار با زمان و عامل عملیات ثبت می‌شوند."
                      : "سلامت و مصرف سرویس‌هایی که بین پروژه‌ها مشترک هستند."}
              </p>
            </div>
            <button
              className="secondary"
              onClick={() => void sync()}
              disabled={busy}
            >
              <RefreshCw size={16} className={busy ? "spin" : ""} />
              همگام‌سازی
            </button>
          </section>
          {notice && (
            <div className="toast" role="status">
              <CheckCircle2 size={18} />
              {notice}
              <button
                className="icon-button"
                aria-label="بستن پیام"
                onClick={() => setNotice("")}
              >
                <X size={16} />
              </button>
            </div>
          )}
          {(error || snapshot?.error) && (
            <div className="alert" role="alert">
              <AlertCircle size={18} />
              <span>
                {error ||
                  "بخشی از عملیات همگام‌سازی ناموفق بوده و خودکار دوباره تلاش می‌شود. تاریخچه را بررسی کنید."}
              </span>
            </div>
          )}
          <div className={`sync-strip ${stale ? "is-stale" : ""}`}>
            <span className={`dot ${stale ? "amber" : "green"}`} />
            <strong>
              {stale ? "در انتظار همگام‌سازی" : "وضعیت زیرساخت به‌روز است"}
            </strong>
            <span className="sync-divider" />
            <span>
              بررسی خودکار هر {number(snapshot?.syncIntervalSeconds ?? 15, 0)}{" "}
              ثانیه
            </span>
            <span className="sync-time">
              <RefreshCw size={13} />
              {age === null
                ? "اولین دریافت…"
                : `${number(age, 0)} ثانیه از آخرین بررسی`}
            </span>
          </div>
          {!snapshot ? (
            <div className="loading-state">
              <RefreshCw className="spin" />
              <p>در حال دریافت وضعیت پروژه‌ها…</p>
            </div>
          ) : (
            <>
              {page === "projects" && (
                <>
                  <section className="stats-grid">
                    <Stat
                      icon={<Layers3 />}
                      label="کل پروژه‌ها"
                      value={number(projects.length, 0)}
                      hint={
                        <>
                          <span className="dot green" />
                          {number(active, 0)} پروژه در حال اجرا
                        </>
                      }
                    />
                    <Stat
                      icon={<Cpu />}
                      label="مصرف پردازنده"
                      value={
                        sum(containers, "cpu") === null
                          ? "—"
                          : `${number(sum(containers, "cpu")!)}٪`
                      }
                      hint="۱۰۰٪ معادل یک هستهٔ پردازنده"
                    />
                    <Stat
                      icon={<MemoryStick />}
                      label="حافظهٔ پروژه‌ها"
                      value={bytes(sum(containers, "memory"))}
                      hint="حافظهٔ مصرفی بدون page cache"
                    />
                    <Stat
                      icon={<CalendarClock />}
                      label="انقضای نزدیک"
                      value={number(upcoming, 0)}
                      hint="پروژه با اعتبار کمتر از ۷ روز"
                      accent={upcoming > 0}
                    />
                  </section>
                  <section className="panel projects-panel">
                    <div className="panel-heading">
                      <div>
                        <h2>
                          پروژه‌های متصل{" "}
                          <span className="count-pill">
                            {number(projects.length, 0)}
                          </span>
                        </h2>
                        <p>
                          پروژه‌ها به‌صورت خودکار از شبکهٔ مشترک شناسایی
                          می‌شوند.
                        </p>
                      </div>
                      <span className="live-label">
                        <span className={`dot ${stale ? "amber" : "green"}`} />
                        {stale ? "اطلاعات قدیمی" : "زنده"}
                      </span>
                    </div>
                    <div className="toolbar">
                      <div className="search">
                        <Search size={17} />
                        <input
                          aria-label="جست‌وجوی پروژه"
                          placeholder="جست‌وجوی نام پروژه…"
                          value={search}
                          onChange={(e) => setSearch(e.target.value)}
                        />
                      </div>
                      <div className="filters">
                        <label className="select-wrap">
                          <Filter size={14} />
                          <select
                            aria-label="فیلتر وضعیت"
                            value={filter}
                            onChange={(e) => setFilter(e.target.value)}
                          >
                            <option value="all">همهٔ وضعیت‌ها</option>
                            {Object.entries(statuses).map(([key, s]) => (
                              <option value={key} key={key}>
                                {s.label}
                              </option>
                            ))}
                          </select>
                        </label>
                        <select
                          className="sort-select"
                          aria-label="مرتب‌سازی پروژه‌ها"
                          value={sort}
                          onChange={(e) => setSort(e.target.value)}
                        >
                          <option value="name">نام پروژه</option>
                          <option value="cpu">بیشترین CPU</option>
                          <option value="memory">بیشترین حافظه</option>
                        </select>
                      </div>
                    </div>
                    <div className="table-scroll">
                      <table className="project-table">
                        <thead>
                          <tr>
                            <th>پروژه / سرویس‌ها</th>
                            <th>وضعیت</th>
                            <th>پردازنده</th>
                            <th>حافظه</th>
                            <th>
                              ترافیک شبکه <small>از شروع کانتینر</small>
                            </th>
                            <th>اعتبار پروژه</th>
                            <th>
                              <span className="sr-only">جزئیات</span>
                            </th>
                          </tr>
                        </thead>
                        <tbody>
                          {filtered.map((p, i) => (
                            <ProjectRow
                              key={p.name}
                              project={p}
                              index={i}
                              onOpen={() => setSelected(p.name)}
                            />
                          ))}
                        </tbody>
                      </table>
                    </div>
                    {filtered.length === 0 && (
                      <Empty
                        icon={<Search />}
                        title="پروژه‌ای پیدا نشد"
                        text={
                          projects.length
                            ? "جست‌وجو یا فیلتر وضعیت را تغییر دهید."
                            : "پروژه‌های Compose متصل به شبکهٔ infra، اینجا نمایش داده می‌شوند."
                        }
                      />
                    )}
                    <div className="table-footer">
                      <span>
                        {number(filtered.length, 0)} پروژه نمایش داده می‌شود
                      </span>
                      <span>
                        <ShieldCheck size={14} /> توقف سرویس‌ها با حفظ کانتینرها
                        و داده‌ها
                      </span>
                    </div>
                  </section>
                  <div className="info-grid">
                    <div className="info-card">
                      <span className="info-icon">
                        <CalendarClock />
                      </span>
                      <div>
                        <h3>اعتبار پروژه، تحت کنترل شما</h3>
                        <p>
                          زمان انقضا تعیین کنید؛ سرویس‌ها در موعد مقرر متوقف و
                          پس از تمدید دوباره روشن می‌شوند.
                        </p>
                      </div>
                    </div>
                    <div className="info-card">
                      <span className="info-icon">
                        <Activity />
                      </span>
                      <div>
                        <h3>واقعیت منابع، بدون حدس</h3>
                        <p>
                          مصرف هر پروژه از Docker خوانده می‌شود. منابع مشترک و
                          داده‌های ناموجود جدا مشخص‌اند.
                        </p>
                      </div>
                    </div>
                  </div>
                </>
              )}
              {page === "infrastructure" && (
                <>
                  <section className="stats-grid">
                    <Stat
                      icon={<Server />}
                      label="سرویس‌های مشترک"
                      value={number(snapshot.infrastructure.length, 0)}
                      hint={`${number(snapshot.infrastructure.filter((c) => c.state === "running").length, 0)} سرویس در حال اجرا`}
                    />
                    <Stat
                      icon={<Cpu />}
                      label="CPU زیرساخت"
                      value={
                        sum(snapshot.infrastructure, "cpu") === null
                          ? "—"
                          : `${number(sum(snapshot.infrastructure, "cpu")!)}٪`
                      }
                      hint="مجموع سرویس‌های مشترک"
                    />
                    <Stat
                      icon={<MemoryStick />}
                      label="حافظهٔ زیرساخت"
                      value={bytes(sum(snapshot.infrastructure, "memory"))}
                      hint="مصرف کل سرویس‌های زیرساخت"
                    />
                    <Stat
                      icon={<Network />}
                      label="دریافت شبکه"
                      value={bytes(
                        sumKnown(snapshot.infrastructure, "networkRX"),
                      )}
                      hint="تجمعی؛ فقط سرویس‌های دارای سنجهٔ شبکه"
                    />
                  </section>
                  <section className="panel">
                    <div className="panel-heading">
                      <div>
                        <h2>سرویس‌های زیرساخت</h2>
                        <p>
                          این سرویس‌ها حفاظت شده‌اند و از پنل متوقف نمی‌شوند.
                        </p>
                      </div>
                      <ShieldCheck className="muted" />
                    </div>
                    <ContainersTable containers={snapshot.infrastructure} onResources={setResource} />
                  </section>
                  <p className="footnote">
                    <CircleHelp size={15} /> CPU و RAM سرویس‌های مشترک به کل
                    زیرساخت تعلق دارند. مصرف قابل انتساب به هر پروژه در جزئیات
                    آن آمده است.
                  </p>
                </>
              )}
              {page === "audit" && (
                <Audit
                  version={auditVersion}
                  projects={projects}
                  onUnauthorized={onLogout}
                />
              )}
              {page === "deployments" && <ProjectDeployment csrf={session.csrf} onUnauthorized={onLogout} />}
              {page === "storefront" && <Storefronts csrf={session.csrf} onUnauthorized={onLogout}/>}
              {page === "shipping" && (
                <Shipping csrf={session.csrf} onUnauthorized={onLogout} />
              )}
              {page === "buildship" && (
                <BuildShip csrf={session.csrf} onUnauthorized={onLogout} />
              )}
              {page === "vpn" && <VPN csrf={session.csrf} onUnauthorized={onLogout} />}
              {page === "environment" && (
                <Environment csrf={session.csrf} onUnauthorized={onLogout} />
              )}
            </>
          )}
          <footer className="footer">
            <span>
              <Layers3 size={13} /> مدیر زیرساخت
            </span>
            <span>فضای خصوصی · همگام با واقعیت سرور</span>
          </footer>
        </main>
      </div>
      {detail && !change && !resource && (
        <Modal title="جزئیات پروژه" wide onClose={() => setSelected(null)}>
          <ProjectDetail
            project={detail}
            metricsError={snapshot?.metricsError ?? ""}
            metricsAt={snapshot?.metricsAt ?? null}
            onChange={(action) => setChange({ project: detail, action })}
            onResources={setResource}
          />
        </Modal>
      )}
      {resource && <ResourceModal container={resource} csrf={session.csrf} onClose={() => setResource(null)} onUnauthorized={onLogout} onApplied={() => { setAuditVersion(v => v + 1); void refresh(); }} />}
      {change && (
        <ChangeModal
          change={change}
          csrf={session.csrf}
          onClose={() => setChange(null)}
          onSaved={() => {
            setChange(null);
            setNotice(
              "درخواست ذخیره شد؛ نتیجهٔ اجرا در همگام‌سازی بعدی و تاریخچه نمایش داده می‌شود.",
            );
            setAuditVersion((v) => v + 1);
            void refresh();
          }}
        />
      )}
    </div>
  );
}
