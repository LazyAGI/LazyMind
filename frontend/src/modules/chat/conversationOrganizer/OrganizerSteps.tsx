import { MinusCircleOutlined } from "@ant-design/icons";
import { Progress, Steps, type StepsProps } from "antd";
import { useTranslation } from "react-i18next";
import type { OrganizerRun } from "./api";

export default function OrganizerSteps({ run }: { run: OrganizerRun }) {
  const { t } = useTranslation();
  if (!run.steps?.length) return null;
  return <div className="organizer-steps" aria-label={t("conversationOrganizer.steps.label")}>
    <Steps direction="vertical" size="small" items={run.steps.map<NonNullable<StepsProps["items"]>[number]>((step) => {
      const active = step.status === "active";
      const detail = step.status === "failed" || step.status === "canceled" ? t(`conversationOrganizer.steps.${step.status}`)
        : step.detail ? t(`conversationOrganizer.steps.${step.detail}`)
        : step.status === "completed" ? t("conversationOrganizer.steps.completed")
        : active && step.total > 0 ? t("conversationOrganizer.steps.batch", { current: step.current, total: step.total })
        : t(`conversationOrganizer.steps.${active ? step.id + "Detail" : step.status}`);
      return {
        title: <span aria-current={active ? "step" : undefined}>{t(`conversationOrganizer.steps.${step.id}`)}</span>,
        status: step.status === "completed" ? "finish" : step.status === "failed" ? "error" : active ? "process" : "wait",
        icon: step.status === "canceled" ? <MinusCircleOutlined /> : undefined,
        description: <>
          {(active || step.status !== "pending") && <div>{detail}</div>}
          {active && step.total > 0 && step.detail !== "canceling" && <Progress showInfo={false} size="small" percent={Math.min(100, Math.floor(100 * step.completed / step.total))} />}
        </>,
      };
    })} />
  </div>;
}
