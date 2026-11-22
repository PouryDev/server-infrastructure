import { useState, type FormEvent } from "react";
import {
  ArrowLeft,
  Box,
  Cpu,
  Database,
  Layers3,
  LockKeyhole,
  Network,
  ShieldCheck,
} from "lucide-react";
import { request } from "../api";
import { type Session } from "../types";

export function Login({ onLogin }: { onLogin: (session: Session) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const fields = new FormData(e.currentTarget);
    setBusy(true);
    setError("");
    try {
      onLogin(
        await request<Session>("/login", {
          method: "POST",
          body: JSON.stringify({
            username: fields.get("username"),
            password: fields.get("password"),
          }),
        }),
      );
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login-shell">
      <div className="login-story">
        <div className="brand">
          <span className="brand-icon">
            <Layers3 />
          </span>
          <span>
            مدیر زیرساخت<small>INFRASTRUCTURE MANAGER</small>
          </span>
        </div>
        <div className="login-copy">
          <span className="eyebrow">همه‌چیز، زیر یک سقف</span>
          <h1>
            یک نگاه روشن
            <br />
            به تمام پروژه‌ها.
          </h1>
          <p>
            منابع، وضعیت سرویس‌ها و چرخهٔ عمر پروژه‌ها را
            <br />
            از یک نقطه مدیریت کنید.
          </p>
          <div className="login-orbit" aria-hidden="true">
            <div className="orbit-center">
              <Layers3 size={42} />
            </div>
            <span className="orbit-node n1">
              <Cpu />
            </span>
            <span className="orbit-node n2">
              <Database />
            </span>
            <span className="orbit-node n3">
              <Network />
            </span>
            <span className="orbit-node n4">
              <Box />
            </span>
          </div>
        </div>
        <span className="login-footer">
          <ShieldCheck size={16} /> دسترسی خصوصی · ثبت تمام عملیات
        </span>
      </div>
      <section className="login-form">
        <span className="login-lock">
          <LockKeyhole size={27} />
        </span>
        <h2>ورود به مرکز کنترل</h2>
        <p>برای مدیریت زیرساخت، وارد حساب خود شوید.</p>
        <form onSubmit={submit}>
          <label htmlFor="username">نام کاربری</label>
          <input
            id="username"
            name="username"
            autoComplete="username"
            dir="ltr"
            required
            autoFocus
          />
          <label htmlFor="password">رمز عبور</label>
          <input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            dir="ltr"
            required
          />
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <button className="primary login-button" disabled={busy}>
            {busy ? "در حال بررسی…" : "ورود به پنل"}
            <ArrowLeft size={18} />
          </button>
        </form>
        <div className="login-note">
          <LockKeyhole size={14} /> این پنل برای استفادهٔ داخلی است.
        </div>
      </section>
    </main>
  );
}
