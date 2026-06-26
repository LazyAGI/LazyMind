import { Modal, Tag, Typography } from "antd";
import { HistoryOutlined, LoadingOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  type SelfEvolutionLaunchOptionCard,
  type SelfEvolutionSummaryItem,
  type SelfEvolutionWorkflowStep,
} from "./types";
import { getSelfEvolutionWorkflowImageSrc } from "../shared";

const { Paragraph, Text } = Typography;

type LaunchOptionGridProps = {
  optionCards: SelfEvolutionLaunchOptionCard[];
  className?: string;
};

export function LaunchOptionGrid({ optionCards, className = "" }: LaunchOptionGridProps) {
  const { t } = useTranslation();
  return (
    <div
      className={`self-evolution-launch-compact-grid ${className}`.trim()}
      role="list"
      aria-label={t("selfEvolutionRun.launchOptionsAria")}
    >
      {optionCards.map((item) => (
        <article
          key={item.key}
          className={`self-evolution-launch-compact-item ${item.toneClassName}${item.isHighlighted ? " is-highlighted" : ""}`}
          role="listitem"
        >
          <div className="self-evolution-launch-compact-meta">
            <span className="self-evolution-launch-card-icon" aria-hidden>
              {item.icon}
            </span>
            <div className="self-evolution-launch-compact-copy">
              <Text className="self-evolution-launch-card-title">{item.title}</Text>
              <Text className="self-evolution-launch-card-current-value">{t("selfEvolutionRun.currentValue", { value: item.currentValue })}</Text>
              <Text className={`self-evolution-launch-compact-desc${item.isDescSingleLine ? " is-single-line" : ""}`}>
                {item.description}
              </Text>
            </div>
          </div>
          {item.control}
        </article>
      ))}
    </div>
  );
}

type LaunchSummaryProps = {
  summaryItems: SelfEvolutionSummaryItem[];
  id?: string;
  ariaLabel: string;
};

export function LaunchSummary({ summaryItems, id, ariaLabel }: LaunchSummaryProps) {
  return (
    <div className="self-evolution-launch-summary" id={id} aria-label={ariaLabel}>
      {summaryItems.map((item) => (
        <div key={item.label} className="self-evolution-launch-summary-pill">
          <Text className="self-evolution-launch-summary-label">{item.label}</Text>
          <Text className="self-evolution-launch-summary-value">{item.value}</Text>
        </div>
      ))}
    </div>
  );
}

type NewSessionConfigModalProps = {
  open: boolean;
  optionCards: SelfEvolutionLaunchOptionCard[];
  summaryItems: SelfEvolutionSummaryItem[];
  isStepOneDone: boolean;
  isStepTwoDone: boolean;
  isStepThreeDone: boolean;
  isStepFourDone: boolean;
  isConfirmDisabled: boolean;
  isConfirming?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
};

export function NewSessionConfigModal({
  open,
  optionCards,
  summaryItems,
  isStepOneDone,
  isStepTwoDone,
  isStepThreeDone,
  isStepFourDone,
  isConfirmDisabled,
  isConfirming = false,
  onCancel,
  onConfirm,
}: NewSessionConfigModalProps) {
  const { t } = useTranslation();
  return (
    <Modal
      open={open}
      onCancel={onCancel}
      footer={null}
      width={980}
      centered
      maskClosable={false}
      className="self-evolution-new-session-modal"
      destroyOnClose={false}
      title={null}
    >
      <section className="self-evolution-new-session-shell" aria-label={t("selfEvolutionRun.newSessionConfigAria")}>
        <header className="self-evolution-new-session-head">
          <Text className="self-evolution-new-session-kicker">{t("selfEvolutionRun.newSessionKicker")}</Text>
          <Typography.Title level={4} className="self-evolution-new-session-title">
            {t("selfEvolutionRun.newSessionTitle")}
          </Typography.Title>
          <Text className="self-evolution-new-session-subtitle">
            {t("selfEvolutionRun.launchConfigHint")}
          </Text>
        </header>

        <div className="self-evolution-new-session-step-rail" aria-label={t("selfEvolutionRun.fiveStepStatusAria")}>
          <span className={`self-evolution-new-session-step-chip${isStepOneDone ? " is-done" : ""}`}>
            {t("selfEvolutionRun.stepChipKnowledgeBase")}
          </span>
          <span className={`self-evolution-new-session-step-chip${isStepTwoDone ? " is-done" : ""}`}>
            {t("selfEvolutionRun.stepChipExistingEval")}
          </span>
          <span className={`self-evolution-new-session-step-chip${isStepThreeDone ? " is-done" : ""}`}>
            {t("selfEvolutionRun.stepChipExtraEval")}
          </span>
          <span className={`self-evolution-new-session-step-chip${isStepFourDone ? " is-done" : ""}`}>
            {t("selfEvolutionRun.stepChipIntervention")}
          </span>
          <span className="self-evolution-new-session-step-chip is-focus">{t("selfEvolutionRun.stepChipStart")}</span>
        </div>

        <LaunchOptionGrid optionCards={optionCards} className="self-evolution-new-session-grid" />

        <footer className="self-evolution-launch-start-bar self-evolution-new-session-start-bar">
          <div className="self-evolution-launch-start-copy">
            <Text className="self-evolution-launch-start-step">{t("selfEvolutionRun.stepChipStart")}</Text>
            <Text className="self-evolution-launch-start-title">{t("selfEvolutionRun.newSessionStartTitle")}</Text>
            <LaunchSummary summaryItems={summaryItems} ariaLabel={t("selfEvolutionRun.newSessionSummaryAria")} />
          </div>

          <div className="self-evolution-new-session-actions">
            <button type="button" className="self-evolution-new-session-cancel" onClick={onCancel}>
              {t("common.cancel")}
            </button>
            <button
              type="button"
              className="self-evolution-chatlike-start-button"
              onClick={onConfirm}
              disabled={isConfirmDisabled}
            >
              {isConfirming ? t("selfEvolutionRun.starting") : t("selfEvolutionRun.startNewSession")}
            </button>
          </div>
        </footer>
      </section>
    </Modal>
  );
}

export type SelfEvolutionHomeViewProps = {
  isLoadingThreadHistoryList: boolean;
  workflowSteps: SelfEvolutionWorkflowStep[];
  launchOptionCards: SelfEvolutionLaunchOptionCard[];
  launchSummaryItems: SelfEvolutionSummaryItem[];
  isLaunchConfigValid: boolean;
  isStartingSession: boolean;
  onOpenHistorySessionModal: () => void;
  onStartSession: () => void;
};

export function SelfEvolutionHomeView({
  isLoadingThreadHistoryList,
  workflowSteps,
  launchOptionCards,
  launchSummaryItems,
  isLaunchConfigValid,
  isStartingSession,
  onOpenHistorySessionModal,
  onStartSession,
}: SelfEvolutionHomeViewProps) {
  const { t, i18n } = useTranslation();
  const workflowImageSrc = getSelfEvolutionWorkflowImageSrc(i18n.resolvedLanguage || i18n.language);
  return (
    <div className="self-evolution-chatlike-page admin-page">
      <header className="self-evolution-chatlike-top">
        <Tag color="blue" className="self-evolution-chatlike-tag">
          {t("selfEvolutionRun.singleThreadSession")}
        </Tag>
        <div className="self-evolution-chatlike-top-actions">
          <button
            type="button"
            className="self-evolution-chatlike-top-history"
            onClick={onOpenHistorySessionModal}
            aria-label={t("selfEvolutionRun.openHistoryAria")}
          >
            {isLoadingThreadHistoryList ? <LoadingOutlined spin /> : <HistoryOutlined />}
            <span>{t("selfEvolutionRun.historySessions")}</span>
          </button>
        </div>
      </header>

      <section className="self-evolution-welcome-container" aria-label={t("selfEvolutionRun.welcomeConfigAria")}>
        <div className="self-evolution-welcome-shell">
          <figure className="self-evolution-welcome-visual">
            <div className="self-evolution-welcome-visual-frame">
              <img
                className="self-evolution-welcome-visual-image"
                src={workflowImageSrc}
                alt={t("selfEvolutionRun.workflowImageAlt")}
              />
            </div>
            <figcaption className="self-evolution-welcome-visual-meta">
              <Text className="self-evolution-welcome-visual-title">{t("selfEvolutionRun.executionPath")}</Text>
              <div className="self-evolution-welcome-visual-badges" role="list" aria-label={t("selfEvolutionRun.workflowStatusAria")}>
                {workflowSteps.map((step) => (
                  <span
                    key={`welcome-badge-${step.renderKey || step.id}`}
                    className={`self-evolution-welcome-visual-badge is-${step.status}`}
                    role="listitem"
                  >
                    {step.title}
                  </span>
                ))}
              </div>
            </figcaption>
          </figure>

          <div className="self-evolution-chatlike-launchpad-content">
            <div className="self-evolution-chatlike-launchpad-header">
              <Text className="self-evolution-chatlike-launchpad-kicker">{t("selfEvolutionRun.launchConfig")}</Text>
              <Paragraph className="self-evolution-chatlike-launchpad-subtitle">
                {t("selfEvolutionRun.launchConfigHint")}
              </Paragraph>
            </div>

            <LaunchOptionGrid optionCards={launchOptionCards} />

            <div className="self-evolution-launch-start-bar" aria-labelledby="self-evolution-launch-start-title">
              <div className="self-evolution-launch-start-copy">
                <Text className="self-evolution-launch-start-step">{t("selfEvolutionRun.stepChipStart")}</Text>
                <Text className="self-evolution-launch-start-title" id="self-evolution-launch-start-title">
                  {t("selfEvolutionRun.startCurrentOptimization")}
                </Text>
                <LaunchSummary
                  summaryItems={launchSummaryItems}
                  id="self-evolution-launch-summary"
                  ariaLabel={t("selfEvolutionRun.currentSummaryAria")}
                />
              </div>

              <button
                type="button"
                className="self-evolution-chatlike-start-button"
                onClick={onStartSession}
                disabled={!isLaunchConfigValid || isStartingSession}
                aria-describedby="self-evolution-launch-summary"
              >
                {isStartingSession ? t("selfEvolutionRun.starting") : t("selfEvolutionRun.start")}
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  );
}
