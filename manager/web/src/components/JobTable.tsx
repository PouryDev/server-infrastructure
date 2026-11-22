import { LoaderCircle } from "lucide-react";
import { date } from "../format";
import { jobStates, messages, phases } from "../shipping-labels";
import type { ShipJob } from "../shipping-types";

export function JobTable({ jobs }: { jobs: ShipJob[] }) {
  return (
    <div className="table-scroll">
      <table className="audit-table">
        <thead>
          <tr>
            <th>پروژه / شناسه</th>
            <th>وضعیت</th>
            <th>مرحله و نتیجه</th>
            <th>زمان</th>
          </tr>
        </thead>
        <tbody>
          {jobs.map((j) => (
            <tr key={j.id}>
              <td>
                <strong dir="ltr">{j.project || "Image transfer"}</strong>
                <small dir="ltr">{j.id.slice(0, 12)}</small>
              </td>
              <td>
                <span
                  className={`badge ${j.status === "succeeded" ? "green" : j.status === "running" || j.status === "queued" ? "amber" : "red"}`}
                >
                  {j.status === "running" && (
                    <LoaderCircle size={12} className="spin" />
                  )}
                  {jobStates[j.status] ?? j.status}
                </span>
              </td>
              <td>
                <strong>{phases[j.phase] ?? j.phase}</strong>
                <small>{messages[j.message] ?? j.message}</small>
              </td>
              <td>{date(j.updatedAt)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
