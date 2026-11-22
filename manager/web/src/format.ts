import type { Container } from "./types";
export const number = (n: number, digits = 1) =>
  new Intl.NumberFormat("fa-IR", { maximumFractionDigits: digits }).format(n);
export function bytes(value: number | null): string {
  if (value === null) return "—";
  if (value < 1024) return `${number(value, 0)} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let n = value / 1024;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${number(n)} ${units[i]}`;
}
export function sum(
  containers: Container[],
  field: "cpu" | "memory" | "networkRX" | "networkTX",
): number | null {
  const running = containers.filter(
    (c) => c.state === "running" || c.state === "paused",
  );
  if (running.length === 0) return 0;
  // Partial totals must not look like complete resource measurements.
  if (running.some((c) => c[field] === null)) return null;
  return running.reduce((total, c) => total + (c[field] ?? 0), 0);
}
export const date = (value: string | null) =>
  value
    ? new Intl.DateTimeFormat("fa-IR", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(new Date(value))
    : "بدون انقضا";
export function localInput(value: Date): string {
  const local = new Date(value.getTime() - value.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 16);
}
export const statuses: Record<string, { label: string; tone: string }> = {
  online: { label: "فعال", tone: "green" },
  degraded: { label: "نیازمند بررسی", tone: "amber" },
  offline: { label: "خاموش", tone: "gray" },
  suspended: { label: "متوقف‌شده", tone: "gray" },
  expired: { label: "منقضی‌شده", tone: "red" },
  stopping: { label: "در حال توقف", tone: "amber" },
  missing: { label: "بدون کانتینر", tone: "gray" },
};

export function sumKnown(
  containers: Container[],
  field: "networkRX" | "networkTX",
): number | null {
  const values = containers.filter(
    (c) => (c.state === "running" || c.state === "paused") && c[field] !== null,
  );
  return values.length
    ? values.reduce((total, c) => total + (c[field] ?? 0), 0)
    : null;
}

export function auditDetail(raw: string): string {
  if (raw === "No expiration") return "بدون انقضا";
  try {
    const change = JSON.parse(raw);
    if (
      typeof change.before?.suspended !== "boolean" ||
      typeof change.after?.suspended !== "boolean"
    )
      return raw;
    return `اعتبار: ${date(change.before.expiresAt)} ← ${date(change.after.expiresAt)}؛ توقف دستی: ${change.before.suspended ? "فعال" : "غیرفعال"} ← ${change.after.suspended ? "فعال" : "غیرفعال"}`;
  } catch {
    return raw;
  }
}
