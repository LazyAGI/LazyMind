import { Typography } from "antd";
import { CheckCircleFilled, ClockCircleFilled, CloseOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { WorkflowResultKind } from "../../shared";
import type { SelfEvolutionFinalResultSummary } from "./types";

const { Paragraph, Text, Title } = Typography;

export function FinalResultCard({
  finalResultSummary,
  onOpenArtifact,
}: {
  finalResultSummary?: SelfEvolutionFinalResultSummary;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
}) {
  const { t } = useTranslation();

  if (!finalResultSummary) {
    return (
      <section className="self-evolution-final-result is-loading" aria-label={t("selfEvolutionRun.finalResultAria")}>
        <div className="self-evolution-final-result-main">
          <span className="self-evolution-final-result-icon">
            <ClockCircleFilled />
          </span>
          <div>
            <Text>{t("selfEvolutionRun.finalResultTitle")}</Text>
            <Title level={4}>{t("selfEvolutionRun.finalResultLoading")}</Title>
            <Paragraph>{t("selfEvolutionRun.finalResultLoadingDesc")}</Paragraph>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className={`self-evolution-final-result is-${finalResultSummary.verdict}`} aria-label={t("selfEvolutionRun.finalResultAria")}>
      <div className="self-evolution-final-result-main">
        <span className="self-evolution-final-result-icon">
          {finalResultSummary.verdict === "reject" ? <CloseOutlined /> : <CheckCircleFilled />}
        </span>
        <div>
          <Text>{t("selfEvolutionRun.finalResultTitle")}</Text>
          <Title level={4}>{finalResultSummary.title}</Title>
          <Paragraph>{finalResultSummary.desc}</Paragraph>
        </div>
      </div>
      {finalResultSummary.metrics.length > 0 && (
        <div className="self-evolution-final-result-metrics">
          {finalResultSummary.metrics.map((item) => (
            <span key={item.label} className={`is-${item.tone}`}>
              <small>{item.label}</small>
              <strong>{item.value}</strong>
            </span>
          ))}
        </div>
      )}
      {finalResultSummary.reasons.length > 0 && (
        <div className="self-evolution-final-result-reasons">
          {finalResultSummary.reasons.map((reason) => <span key={reason}>{reason}</span>)}
        </div>
      )}
      <button
        type="button"
        className="self-evolution-final-result-action"
        onClick={(event) => {
          event.stopPropagation();
          onOpenArtifact("abtests");
        }}
      >
        {t("selfEvolutionRun.viewABTestDetail")}
      </button>
    </section>
  );
}
