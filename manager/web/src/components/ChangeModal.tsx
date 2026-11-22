import { useState, type FormEvent } from "react";
import { CalendarClock, Check, History, Pause } from "lucide-react";
import { request } from "../api";
import { localInput, number } from "../format";
import { type Change } from "../types";
import { Modal } from "./Modal";

export function ChangeModal({
  change,
  csrf,
  onClose,
  onSaved,
}: {
  change: Change;
  csrf: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { project, action } = change;
  const [expiry, setExpiry] = useState(
    localInput(new Date(Date.now() + 30 * 86400000)),
  );
  const [unlimited, setUnlimited] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const title =
    action === "suspend"
      ? "توقف پروژه"
      : action === "renew"
        ? "تمدید و فعال‌سازی پروژه"
        : "تنظیم زمان انقضا";
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    if (
      action !== "suspend" &&
      !unlimited &&
      (!expiry || new Date(expiry).getTime() <= Date.now())
    ) {
      setError("یک زمان معتبر در آینده انتخاب کنید.");
      return;
    }
    setBusy(true);
    try {
      await request(
        `/projects/${encodeURIComponent(project.name)}/${action}`,
        {
          method: "POST",
          body: JSON.stringify({
            expiresAt:
              action === "suspend" || unlimited
                ? null
                : new Date(expiry).toISOString(),
            revision: project.policy.revision,
          }),
        },
        csrf,
      );
      onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title={title}
      onClose={() => {
        if (!busy) onClose();
      }}
    >
      <form onSubmit={submit}>
        <span className={`change-icon ${action === "suspend" ? "danger" : ""}`}>
          {action === "suspend" ? <Pause /> : <CalendarClock />}
        </span>
        <h3 className="change-title">
          {title} <b dir="ltr">{project.name}</b>
        </h3>
        <p className="change-description">
          {action === "suspend"
            ? "سرویس‌های برنامه خاموش می‌شوند و پروژه تا زمان تمدید در دسترس نخواهد بود. کانتینرها و داده‌ها حفظ می‌شوند."
            : action === "renew"
              ? "اعتبار جدید ذخیره می‌شود و سرویس‌هایی که مدیر متوقف کرده، دوباره روشن می‌شوند."
              : "در زمان انتخاب‌شده، سرویس‌های برنامه به‌صورت خودکار خاموش می‌شوند. این تنظیم، توقف دستی فعال را لغو نمی‌کند."}
        </p>
        {action !== "suspend" && (
          <>
            <label className="field-label" htmlFor="expiration">
              اعتبار تا تاریخ و ساعت <small>(زمان محلی مرورگر)</small>
            </label>
            <input
              id="expiration"
              type="datetime-local"
              dir="ltr"
              value={expiry}
              min={localInput(new Date())}
              onChange={(e) => setExpiry(e.target.value)}
              disabled={unlimited}
              required={!unlimited}
            />
            <div className="quick-dates">
              {[1, 7, 30, 90].map((days) => (
                <button
                  type="button"
                  className="chip"
                  key={days}
                  disabled={busy}
                  onClick={() => {
                    setUnlimited(false);
                    setExpiry(
                      localInput(new Date(Date.now() + days * 86400000)),
                    );
                  }}
                >
                  {number(days, 0)} روز
                </button>
              ))}
            </div>
            <label className="checkbox">
              <input
                type="checkbox"
                checked={unlimited}
                onChange={(e) => setUnlimited(e.target.checked)}
              />
              بدون تاریخ انقضا
            </label>
          </>
        )}
        {error && (
          <p role="alert" className="form-error">
            {error}
          </p>
        )}
        <div className="change-audit">
          <History size={15} /> این عملیات در تاریخچه ثبت می‌شود.
        </div>
        <div className="modal-actions">
          <button
            className={action === "suspend" ? "danger-button" : "primary"}
            disabled={busy}
          >
            {busy
              ? "در حال ذخیره…"
              : action === "suspend"
                ? "توقف سرویس‌های پروژه"
                : "ذخیرهٔ تغییرات"}
            {!busy && <Check size={16} />}
          </button>
          <button
            className="secondary"
            type="button"
            onClick={onClose}
            disabled={busy}
          >
            انصراف
          </button>
        </div>
      </form>
    </Modal>
  );
}
