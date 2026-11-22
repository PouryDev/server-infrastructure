import { Box, SlidersHorizontal } from "lucide-react";
import { bytes, number } from "../format";
import { type Container } from "../types";
import { Empty } from "./Empty";

export function ContainersTable({
  containers,
  onResources,
}: {
  containers: Container[];
  onResources?: (container: Container) => void;
}) {
  const stateNames: Record<string, string> = {
    running: "در حال اجرا",
    exited: "خاموش",
    created: "ایجادشده",
    restarting: "در حال راه‌اندازی",
    paused: "معلق",
    dead: "خراب",
  };
  const healthNames: Record<string, string> = {
    healthy: "سالم",
    unhealthy: "ناسالم",
    starting: "در حال بررسی",
  };
  return (
    <div className="table-scroll">
      <table className="containers-table">
        <thead>
          <tr>
            <th>سرویس</th>
            <th>وضعیت / سلامت</th>
            <th>CPU</th>
            <th>حافظه / سقف</th>
            <th>شبکه ↓ / ↑</th>
            {onResources && <th>محدودیت منابع</th>}
          </tr>
        </thead>
        <tbody>
          {containers.map((c) => (
            <tr key={c.id}>
              <td>
                <span className="service-name" dir="ltr">
                  <Box size={15} />
                  {c.service}
                </span>
                {!c.managed && c.project !== "infra" && (
                  <small className="muted">خارج از کنترل چرخهٔ عمر</small>
                )}
                {c.error && (
                  <small className="expired-text">
                    دریافت منابع یا اجرای عملیات ناموفق بود.
                  </small>
                )}
              </td>
              <td>
                <span
                  className={`dot ${c.state === "running" && c.health !== "unhealthy" ? "green" : "gray"}`}
                />{" "}
                {stateNames[c.state] ?? c.state}
                <small className="muted">
                  {c.health
                    ? (healthNames[c.health] ?? c.health)
                    : c.state === "running"
                      ? "بدون healthcheck"
                      : ""}
                </small>
              </td>
              <td dir="ltr">
                {c.state === "running"
                  ? c.cpu === null
                    ? "—"
                    : `${number(c.cpu)}%`
                  : "—"}
              </td>
              <td dir="ltr">
                {bytes(c.memory)}{" "}
                <small className="muted">/ {bytes(c.memoryLimit)}</small>
              </td>
              <td dir="ltr">
                {bytes(c.networkRX)} / {bytes(c.networkTX)}
              </td>
              {onResources && (
                <td>
                  <button
                    className="resource-table-button"
                    onClick={() => onResources(c)}
                    aria-label={`تنظیم منابع ${c.service}`}
                  >
                    <SlidersHorizontal size={15} /> تنظیم منابع
                  </button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      {containers.length === 0 && (
        <Empty
          icon={<Box />}
          title="کانتینری موجود نیست"
          text="پروژه پس از اتصال مجدد به شبکه شناسایی می‌شود."
        />
      )}
    </div>
  );
}
