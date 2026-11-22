import type { ShipJob } from "../shipping-types";
import { phases, messages } from "../shipping-labels";
import { number } from "../format";

export function ShipProgress({ job }: { job: ShipJob }) {
  const steps = job.steps ?? [];
  const completed = steps.filter(step => step.status === "complete").length;
  const running = job.status === "running" || job.status === "queued";
  const work = job.progress;
  return <section className="ship-progress" aria-label="پیشرفت عملیات">
    <div className="ship-progress-heading"><strong>{phases[job.phase] ?? job.phase}</strong><span>{steps.length > 0 ? `${number(completed, 0)} از ${number(steps.length, 0)} مرحله تکمیل شده` : "در انتظار گزارش اجرا"}</span></div>
    {steps.length > 0 && <progress aria-label="مراحل تکمیل‌شده" max={steps.length} value={completed}/>}
    <ol className="ship-progress-steps">{steps.map(step => <li key={step.phase} data-status={step.status}><span aria-hidden="true">{step.status === "complete" ? "✓" : step.status === "failed" ? "×" : step.status === "running" ? "●" : "○"}</span> {phases[step.phase] ?? step.phase}</li>)}</ol>
    <p>{messages[job.message] ?? job.message}</p>
    {running && <progress className="ship-progress-working" aria-label="اجرای مرحلهٔ جاری"/>}
    {work?.unit === "bytes" && <small>{number(work.current / 1024 / 1024, 1)} مگابایت واقعاً به جریان ارسال تحویل داده شده است؛ تأیید دریافت در مرحلهٔ تطبیق image انجام می‌شود.</small>}
    {work?.unit === "vertices" && <small>{number(work.current, 0)} از {number(work.total ?? 0, 0)} کار شناسایی‌شدهٔ BuildKit تکمیل شده؛ تعداد کارها حین ساخت ممکن است بیشتر شود.</small>}
    <small>نوار بر اساس مراحل تأییدشده است؛ تخمین زمان باقی‌مانده نیست.</small>
  </section>;
}
