import { ArrowDownLeft, ArrowUpRight, ChevronLeft, Clock3 } from "lucide-react";
import { bytes, date, number, sum } from "../format";
import { type Project } from "../types";
import { Badge } from "./Badge";

export function ProjectRow({
  project: p,
  index,
  onOpen,
}: {
  project: Project;
  index: number;
  onOpen: () => void;
}) {
  const runtime = p.containers.filter((c) => c.managed);
  const cpu = sum(p.containers, "cpu");
  const memory = sum(p.containers, "memory");
  return (
    <tr>
      <td>
        <button className="project-name" onClick={onOpen}>
          <span className={`project-monogram color-${index % 4}`}>
            {p.name.slice(0, 2).toUpperCase()}
          </span>
          <span>
            <b dir="ltr">{p.name}</b>
            <small>
              {number(runtime.filter((c) => c.state === "running").length, 0)}{" "}
              از {number(runtime.length, 0)} سرویس فعال
            </small>
          </span>
        </button>
      </td>
      <td>
        <Badge status={p.status} />
      </td>
      <td>
        <span className="metric-cell" dir="ltr">
          {cpu === null ? "—" : `${number(cpu)}%`}
        </span>
        <progress
          max={Math.max(100, cpu ?? 100)}
          value={cpu ?? 0}
          aria-label={`CPU ${p.name}`}
        />
      </td>
      <td>
        <span className="metric-cell" dir="ltr">
          {bytes(memory)}
        </span>
      </td>
      <td>
        <div className="network-cell" dir="ltr">
          <span>
            <ArrowDownLeft size={12} />
            {bytes(sum(p.containers, "networkRX"))}
          </span>
          <span>
            <ArrowUpRight size={12} />
            {bytes(sum(p.containers, "networkTX"))}
          </span>
        </div>
      </td>
      <td>
        <div
          className={`expiry-cell ${p.status === "expired" ? "expired-text" : ""}`}
        >
          <Clock3 size={14} />
          <span>{date(p.policy.expiresAt)}</span>
        </div>
      </td>
      <td>
        <button
          className="row-open"
          aria-label={`جزئیات ${p.name}`}
          onClick={onOpen}
        >
          <ChevronLeft size={18} />
        </button>
      </td>
    </tr>
  );
}
