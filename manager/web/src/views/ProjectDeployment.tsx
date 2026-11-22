import { useEffect, useState } from "react";
import { BuildShip } from "./BuildShip";
import { Deployments } from "./Deployments";
import { APIError, request } from "../api";

export function ProjectDeployment(props: { csrf: string; onUnauthorized: () => void }) {
  const [mode, setMode] = useState<"automatic" | "host" | null>(null);
  useEffect(() => {
    let cancelled = false;
    request<unknown[]>("/shipping/sources").then(sources => { if (!cancelled) setMode(sources.length ? "automatic" : "host"); })
      .catch(e => { if (e instanceof APIError && e.status === 401) props.onUnauthorized(); else if (!cancelled) setMode("host"); });
    return () => { cancelled = true; };
  }, [props.onUnauthorized]);
  if (!mode) return <p role="status">در حال بررسی روش‌های دیپلوی این میزبان…</p>;
  return <div className="deployment-page">
    <div className="deployment-mode" role="group" aria-label="روش دیپلوی پروژه">
      <button className={mode === "automatic" ? "primary" : "secondary"} onClick={() => setMode("automatic")}>ساخت محلی و دیپلوی خودکار با SSH</button>
      <button className={mode === "host" ? "primary" : "secondary"} onClick={() => setMode("host")}>پروژه‌های همین سرور / بستهٔ آماده</button>
    </div>
    {mode === "automatic" ? <BuildShip {...props} automatic/> : <Deployments {...props}/>}
  </div>;
}
