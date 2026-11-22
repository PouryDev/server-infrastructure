import { useCallback, useEffect, useState } from "react";
import { Layers3, AlertCircle } from "lucide-react";
import { APIError, request } from "./api";
import { type Session } from "./types";
import { Login } from "./views/Login";
import { Dashboard } from "./views/Dashboard";

export function App() {
  const [session, setSession] = useState<Session | null>();
  const [initialError, setInitialError] = useState("");
  const checkSession = useCallback(async () => {
    setInitialError("");
    try {
      setSession(await request<Session>("/session"));
    } catch (e) {
      if (e instanceof APIError && e.status === 401) setSession(null);
      else setInitialError("دریافت وضعیت نشست ممکن نشد. اتصال را بررسی کنید.");
    }
  }, []);
  useEffect(() => {
    void checkSession();
  }, [checkSession]);
  if (initialError)
    return (
      <div className="boot">
        <AlertCircle />
        <p>{initialError}</p>
        <button onClick={() => void checkSession()}>تلاش دوباره</button>
      </div>
    );
  if (session === undefined)
    return (
      <div className="boot">
        <Layers3 className="boot-mark" />
        <p>در حال اتصال به مرکز کنترل…</p>
      </div>
    );
  if (session === null) return <Login onLogin={setSession} />;
  return <Dashboard session={session} onLogout={() => setSession(null)} />;
}
