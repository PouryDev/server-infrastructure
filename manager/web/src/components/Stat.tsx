import { type ReactNode } from "react";

export function Stat({
  icon,
  label,
  value,
  hint,
  accent = false,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  hint: ReactNode;
  accent?: boolean;
}) {
  return (
    <article className={`stat-card ${accent ? "accent" : ""}`}>
      <div className="stat-top">
        <span>{label}</span>
        <span className="stat-icon">{icon}</span>
      </div>
      <strong dir="auto">{value}</strong>
      <div className="stat-hint">{hint}</div>
    </article>
  );
}
