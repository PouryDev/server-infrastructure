import { useState, type FormEvent } from "react";
import { Eye, EyeOff, KeyRound, Plus, Trash2, X } from "lucide-react";
import { envNamePattern, type EnvChange } from "../env-types";

export type ScopeOption = { value: string; label: string };

export function scopeLabel(scope: string): string {
  return scope === "" ? "کل پروژه" : scope;
}

// Pending edits are held in the browser only until they are saved or shipped; values
// are never printed back (the list shows names, scope and the action).
export function EnvChangeEditor({
  scopes,
  changes,
  onChange,
  disabled = false,
  initial,
  embedded = false,
}: {
  scopes: ScopeOption[];
  changes: EnvChange[];
  onChange: (next: EnvChange[]) => void;
  disabled?: boolean;
  initial?: { scope: string; name: string };
  embedded?: boolean;
}) {
  const [scope, setScope] = useState(initial?.scope ?? scopes[0]?.value ?? "");
  const [name, setName] = useState(initial?.name ?? "");
  const [value, setValue] = useState("");
  const [show, setShow] = useState(false);
  const [error, setError] = useState("");
  function upsert(action: "set" | "delete") {
    const cleanName = name.trim();
    if (!envNamePattern.test(cleanName)) {
      setError("نام متغیر باید با حرف بزرگ انگلیسی یا _ شروع شود و فقط شامل A-Z، 0-9 و _ باشد.");
      return;
    }
    if (action === "set" && /[\n\r\0]/.test(value)) {
      setError("مقدار نمی‌تواند خط جدید داشته باشد.");
      return;
    }
    const rest = changes.filter(
      (c) => !(c.scope === scope && c.name === cleanName),
    );
    onChange([
      ...rest,
      action === "set"
        ? { scope, name: cleanName, action, value }
        : { scope, name: cleanName, action },
    ]);
    setName("");
    setValue("");
    setError("");
  }
  function submit(e: FormEvent) {
    e.preventDefault();
    upsert("set");
  }
  const Wrapper = embedded ? "div" : "form";
  return (
    <div className="env-editor">
      {changes.length > 0 && (
        <ul className="env-pending" aria-label="تغییرهای در انتظار ذخیره">
          {changes.map((c) => (
            <li key={c.scope + "/" + c.name}>
              <span className={`badge ${c.action === "delete" ? "red" : "green"}`}>
                {c.action === "delete" ? "حذف" : "افزودن / ویرایش"}
              </span>
              <bdi dir="ltr">{c.name}</bdi>
              <small>{scopeLabel(c.scope)}</small>
              <button
                type="button"
                className="icon-button"
                aria-label={`لغو تغییر ${c.name}`}
                disabled={disabled}
                onClick={() =>
                  onChange(
                    changes.filter(
                      (x) => !(x.scope === c.scope && x.name === c.name),
                    ),
                  )
                }
              >
                <X size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
      <Wrapper className="env-add" onSubmit={embedded ? undefined : submit} onKeyDown={e => { if (embedded && e.key === "Enter") { e.preventDefault(); upsert("set"); } }}>
        <label>
          محدوده
          <select
            aria-label="محدودهٔ متغیر"
            value={scope}
            disabled={disabled}
            onChange={(e) => setScope(e.target.value)}
          >
            {scopes.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          نام
          <input
            aria-label="نام متغیر"
            dir="ltr"
            value={name}
            disabled={disabled}
            placeholder="API_TOKEN"
            autoComplete="off"
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          مقدار
          <span className="env-value-input">
            <input
              aria-label="مقدار متغیر"
              dir="ltr"
              type={show ? "text" : "password"}
              value={value}
              disabled={disabled}
              autoComplete="off"
              onChange={(e) => setValue(e.target.value)}
            />
            <button
              type="button"
              className="icon-button"
              aria-label={show ? "پنهان کردن مقدار" : "نمایش مقدار"}
              onClick={() => setShow((v) => !v)}
            >
              {show ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </span>
        </label>
        <div className="env-add-actions">
          <button type={embedded ? "button" : "submit"} onClick={embedded ? () => upsert("set") : undefined} className="secondary" disabled={disabled || !name}>
            <Plus size={14} /> افزودن یا ویرایش
          </button>
          <button
            type="button"
            className="secondary danger-text"
            disabled={disabled || !name}
            onClick={() => upsert("delete")}
          >
            <Trash2 size={14} /> حذف متغیر
          </button>
        </div>
      </Wrapper>
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      <p className="footnote">
        <KeyRound size={14} /> مقدارها فقط در همین فرم وارد می‌شوند و هرگز در
        فهرست‌ها، پیش‌نمایش یا تاریخچه نمایش داده نمی‌شوند.
      </p>
    </div>
  );
}
