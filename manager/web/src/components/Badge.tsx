import { statuses } from "../format";

export function Badge({ status }: { status: string }) {
  const s = statuses[status] ?? statuses.offline;
  return (
    <span className={`badge ${s.tone}`}>
      <span className={`dot ${s.tone}`} />
      {s.label}
    </span>
  );
}
