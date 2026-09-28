import { useId } from "react";
import { useTranslation } from "react-i18next";
import { BulbOutlined, CheckOutlined, CloseCircleFilled } from "@ant-design/icons";
import type { PublicProcessStep } from "@/modules/chat/types/ordinaryTask";
import type { OrdinaryTaskState } from "./taskTimeline";

const statusClass = (status: string) => status === "succeeded" ? "complete"
  : status === "pending" ? "waiting" : status;

export function TaskProcessTimeline({ steps, legacyPlan = [], state, progress }: {
  steps: PublicProcessStep[];
  legacyPlan?: string[];
  state: OrdinaryTaskState;
  progress?: number;
}) {
  const { t } = useTranslation();
  const headingId = useId();
  const sorted = [...steps].sort((a, b) => a.order - b.order || a.step_id.localeCompare(b.step_id));
  const hasPlan = legacyPlan.length > 0 && sorted.length === 0;
  // A plan is an estimate from overall progress. Only task success completes it.
  const percentage = state === "complete" ? 100 : state === "waiting" ? 0
    : progress !== undefined && Number.isFinite(progress) ? Math.max(0, Math.min(99, Math.round(progress))) : undefined;
  const completed = percentage === undefined ? 0 : Math.floor(percentage / 100 * legacyPlan.length);
  const descriptionId = `${headingId}-description`;
  const labels: Record<string, string> = {
    pending: t("taskCenter.statusPending"), running: t("taskCenter.ordinaryStatusRunning"),
    succeeded: t("taskCenter.statusSucceeded"), failed: t("taskCenter.statusFailed"),
    interrupted: t("taskCenter.statusInterrupted"), canceled: t("taskCenter.statusCanceled"),
  };
  return (
    <section className={`ordinary-activity-section ordinary-thinking-section${hasPlan ? " ordinary-plan-section" : ""}`} aria-labelledby={headingId}>
      <h3 className="ordinary-section-heading" id={headingId}>
        <BulbOutlined aria-hidden="true" /><span>{t(hasPlan ? "taskCenter.ordinaryPlan" : "taskCenter.ordinaryThinking")}</span>
      </h3>
      {sorted.length === 0 ? (legacyPlan.length === 0 && <p className="ordinary-process-empty">{t("taskCenter.ordinaryNoProcessSteps")}</p>) : (
        <ol className="ordinary-thinking-list">
          {sorted.map(step => (
            <li className={`ordinary-thinking-item is-${statusClass(step.status)}`} key={step.step_id}
              aria-current={step.status === "running" ? "step" : undefined}>
              <span className="ordinary-thinking-marker" aria-hidden="true">
                {step.status === "succeeded" ? <CheckOutlined /> : step.status === "running"
                  ? <span className="ordinary-task-spinner" /> : step.status === "failed"
                    ? <CloseCircleFilled /> : <span className="ordinary-thinking-dot" />}
              </span>
              <span className="ordinary-thinking-copy">
                <strong>{step.title}</strong>
                {step.summary && <span>{step.summary}</span>}
                <span className="ordinary-process-status">{labels[step.status] ?? t("taskCenter.statusPending")}</span>
              </span>
            </li>
          ))}
        </ol>
      )}
      {hasPlan && (
        <>
          <p className="ordinary-plan-description" id={descriptionId}>{t("taskCenter.ordinaryPlanDescription")}</p>
          <div className={`ordinary-plan-progress is-${state}`}>
            <span>{t("taskCenter.ordinaryPlanProgress", { current: percentage === undefined ? "—" : completed, total: legacyPlan.length })}</span>
            <strong>{percentage === undefined ? t(state === "running" ? "taskCenter.ordinaryStatusRunning" : "taskCenter.ordinaryProgressUnknown") : `${percentage}%`}</strong>
            <progress aria-label={t("taskCenter.ordinaryPlan")} aria-describedby={descriptionId}
              className={percentage === undefined && state === "running" ? "is-indeterminate" : undefined}
              value={percentage} max={100} />
          </div>
          <ol className="ordinary-thinking-list" aria-describedby={descriptionId}>
            {legacyPlan.map((title, index) => {
              const stepState = index < completed ? "complete" : index === completed ? state : "waiting";
              return <li className={`ordinary-thinking-item ordinary-plan-item is-${stepState}`} key={index}
                aria-current={stepState === "running" ? "step" : undefined}>
                <span className="ordinary-thinking-marker" aria-hidden="true">
                  {stepState === "complete" ? <CheckOutlined /> : stepState === "running" ? <span className="ordinary-plan-active-dot" />
                    : ["failed", "interrupted", "canceled"].includes(stepState) ? <CloseCircleFilled /> : index + 1}
                </span>
                <span className="ordinary-thinking-copy"><strong>{title}</strong></span>
              </li>;
            })}
          </ol>
        </>
      )}
    </section>
  );
}
