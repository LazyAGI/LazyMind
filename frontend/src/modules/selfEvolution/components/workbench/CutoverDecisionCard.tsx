import { message, Popconfirm } from "antd";
import { CheckCircleFilled } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { CheckpointWaitPrompt, EvoProcessDashboard, WorkflowResultKind } from "../../shared";

export function CutoverDecisionCard({
  processDashboard,
  checkpointDecisionPrompt,
  cutoverDecisionEvidence,
  isSendingMessage,
  onSend,
  onOpenArtifact,
}: {
  processDashboard: EvoProcessDashboard;
  checkpointDecisionPrompt?: CheckpointWaitPrompt;
  cutoverDecisionEvidence: EvoProcessDashboard["cutoverActivities"];
  isSendingMessage: boolean;
  onSend: (command?: string) => void;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
}) {
  const { t } = useTranslation();

  return (
    <div className="self-evolution-cutover-decision" aria-label={t("selfEvolutionRun.abtestCutoverAria")}>
      <div className="self-evolution-cutover-decision-head">
        <CheckCircleFilled />
        <span>
          <strong>{processDashboard.cutoverCompleted ? t("selfEvolutionRun.candidateCutoverDone") : t("selfEvolutionRun.abtestPassed")}</strong>
          <small>{processDashboard.cutoverCompleted ? t("selfEvolutionRun.chatServiceUsingCandidate") : t("selfEvolutionRun.chatServiceUsingOriginal")}</small>
        </span>
      </div>
      <div className="self-evolution-cutover-decision-evidence">
        {cutoverDecisionEvidence.length ? (
          cutoverDecisionEvidence.map((item) => (
            <p key={item.key}>
              <strong>{item.title}</strong>
              <span>{item.detail}</span>
            </p>
          ))
        ) : (
          <p>
            <strong>{t("selfEvolutionRun.candidateMeetsCriteria")}</strong>
            <span>{checkpointDecisionPrompt?.message || t("selfEvolutionRun.confirmWillSwitch")}</span>
          </p>
        )}
        {processDashboard.cutoverCompleted ? (
          <p>
            <strong>{t("selfEvolutionRun.switchCompleted")}</strong>
            <span>{t("selfEvolutionRun.candidateRegisteredAndSwitched")}</span>
          </p>
        ) : (
          <p>
            <strong>{t("selfEvolutionRun.notSwitchedYet")}</strong>
            <span>{t("selfEvolutionRun.switchAfterConfirm")}</span>
          </p>
        )}
      </div>
      {!processDashboard.cutoverCompleted && (
        <div className="self-evolution-cutover-decision-actions">
          <Popconfirm
            title={t("selfEvolutionRun.confirmSwitchChatServiceTitle")}
            description={t("selfEvolutionRun.confirmSwitchChatServiceDesc")}
            okText={t("selfEvolutionRun.confirmCutover")}
            cancelText={t("selfEvolutionRun.cancel")}
            onConfirm={(event) => {
              event?.stopPropagation();
              if (checkpointDecisionPrompt?.command) {
                onSend(checkpointDecisionPrompt.command);
              }
            }}
            onCancel={(event) => event?.stopPropagation()}
          >
            <button
              type="button"
              className="self-evolution-cutover-decision-primary"
              disabled={!checkpointDecisionPrompt?.command || isSendingMessage}
              onClick={(event) => event.stopPropagation()}
            >
              {checkpointDecisionPrompt?.command || t("selfEvolutionRun.confirmCutover")}
            </button>
          </Popconfirm>
          <button
            type="button"
            className="self-evolution-cutover-decision-secondary"
            onClick={(event) => {
              event.stopPropagation();
              onOpenArtifact("abtests");
            }}
          >
            {t("selfEvolutionRun.viewABTestDetail")}
          </button>
          <button
            type="button"
            className="self-evolution-cutover-decision-neutral"
            onClick={(event) => {
              event.stopPropagation();
              void message.info(t("selfEvolutionRun.keptCurrentVersionMsg"), 1.6);
            }}
          >
            {t("selfEvolutionRun.keepCurrentVersion")}
          </button>
        </div>
      )}
      {processDashboard.cutoverCompleted && (
        <button
          type="button"
          className="self-evolution-cutover-decision-secondary"
          onClick={(event) => {
            event.stopPropagation();
            onOpenArtifact("abtests");
          }}
        >
          {t("selfEvolutionRun.viewABTestDetail")}
        </button>
      )}
    </div>
  );
}
