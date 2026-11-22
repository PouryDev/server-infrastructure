import { useCallback, useEffect, useState } from "react";
import { CheckCircle2, Network, RefreshCw } from "lucide-react";
import { APIError, request } from "../api";

type VPNStatus = {
  configured: boolean;
  enabled: boolean;
  running: boolean;
  protocol: string;
  server: string;
  revision: string;
};

export function VPN({ csrf, onUnauthorized }: { csrf: string; onUnauthorized: () => void }) {
  const [status, setStatus] = useState<VPNStatus | null>(null);
  const [link, setLink] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const fail = useCallback((e: unknown) => {
    if (e instanceof APIError && e.status === 401) onUnauthorized();
    else setError((e as Error).message);
  }, [onUnauthorized]);
  const load = useCallback(async () => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const out = await request<VPNStatus>("/vpn");
      setStatus(out);
      setEnabled(out.configured ? out.enabled : true);
    } catch (e) { fail(e); }
    finally { setBusy(false); }
  }, [fail]);
  useEffect(() => { void load(); }, [load]);

  async function save() {
    if (!status) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const next = await request<VPNStatus>("/vpn", {
        method: "PUT", body: JSON.stringify({ link: link.trim(), enabled, revision: status.revision }),
      }, csrf);
      setStatus(next); setEnabled(next.enabled); setLink("");
      setNotice(next.enabled ? "تنظیمات ذخیره و Xray اجرا شد. برای بررسی مسیر اینترنت، تست اتصال را بزنید." : "تنظیمات ذخیره و اتصال غیرفعال شد.");
    } catch (e) { fail(e); }
    finally { setBusy(false); }
  }
  async function test() {
    setBusy(true); setError(""); setNotice("");
    try {
      await request("/vpn/test", { method: "POST" }, csrf);
      setNotice("تست موفق بود؛ دسترسی HTTPS از مسیر VPN برقرار است.");
    } catch (e) { fail(e); }
    finally { setBusy(false); }
  }
  return <div className="shipping-view">
    {error && <div className="alert" role="alert">{error}</div>}
    {notice && <div className="ship-notice" role="status"><CheckCircle2 size={17} />{notice}</div>}
    <section className="panel env-panel">
      <div className="panel-heading">
        <div><h2><Network size={19} /> کلاینت VPN / Xray</h2>
          <p>لینک اتصال را وارد کنید تا سرویس‌های دلخواه از پراکسی مشترک استفاده کنند.</p></div>
        <button className="icon-button" aria-label="بازخوانی وضعیت VPN" disabled={busy} onClick={() => void load()}>
          <RefreshCw size={17} className={busy ? "spin" : ""} />
        </button>
      </div>
      {status && <p className="footnote" aria-live="polite">
        وضعیت: <strong>{!status.configured ? "تنظیم نشده" : !status.enabled ? "غیرفعال" : status.running ? "Xray در حال اجرا" : "Xray متوقف است؛ در انتظار راه‌اندازی خودکار"}</strong>
        {status.configured && <> · <bdi dir="ltr">{status.protocol} / {status.server}</bdi></>}
      </p>}
      <form onSubmit={(e) => { e.preventDefault(); void save(); }}>
        <div className="ship-fields">
          <label>لینک اتصال VPN
            <input aria-label="لینک اتصال VPN" type="password" dir="ltr" autoComplete="off" spellCheck={false}
              placeholder="vless://… / vmess://… / trojan://… / ss://…" maxLength={16384}
              value={link} disabled={busy || !status} onChange={(e) => setLink(e.target.value)} />
          </label>
        </div>
        <p className="footnote">برای حفظ لینک قبلی، این فیلد را خالی بگذارید. لینک اشتراک (subscription) پشتیبانی نمی‌شود.</p>
        <label className="vpn-toggle"><input type="checkbox" checked={enabled} disabled={busy || !status} onChange={(e) => setEnabled(e.target.checked)} />فعال بودن اتصال</label>
        <div className="modal-actions env-save-row">
          <button className="primary" type="submit" disabled={busy || !status || (!status.configured && !link.trim())}>ذخیره و اعمال</button>
          <button className="secondary" type="button" disabled={busy || !status?.running || !status.enabled} onClick={() => void test()}>تست اتصال VPN</button>
        </div>
      </form>
    </section>
    <section className="panel env-panel">
      <div className="panel-heading"><div><h2>استفاده در سرویس‌ها</h2><p>در متغیرهای محیطی، محدودهٔ سرویس دلخواه متصل به شبکهٔ infra را انتخاب کنید؛ این مقدارها را ذخیره و سپس روی سرویس اعمال کنید.</p></div></div>
      <pre className="vpn-example" dir="ltr">{`HTTP_PROXY=http://xray:10809\nHTTPS_PROXY=http://xray:10809\nALL_PROXY=socks5h://xray:10808\nNO_PROXY=localhost,127.0.0.1,::1,pgbouncer,postgres,redis,rabbitmq,otel-collector`}</pre>
      <p className="footnote">دامنه‌ها و نام سرویس‌های داخلی پروژه را هم به NO_PROXY اضافه کنید. برنامه باید از متغیرهای پراکسی پشتیبانی کند. روشن بودن Xray به معنی معتبر بودن لینک نیست؛ تست اتصال را اجرا کنید.</p>
    </section>
  </div>;
}
