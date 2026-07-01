import { type ReactNode } from "react";
import { Typography } from "antd";
import { CloseOutlined, DownOutlined, HistoryOutlined, PlusOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { type SelfEvolutionWorkbenchTab } from "../types";
import type { SelfEvolutionChatMessage } from "../types";
import type { SelfEvolutionSessionSummary } from "./types";

const { Paragraph, Text, Title } = Typography;

export function WorkbenchSidebar({
  activeStepText,
  routeThreadId,
  isRestoringThread,
  threadRestoreError,
  activeWorkbenchTab,
  activeStageLabel,
  activeSession,
  displayedMessages,
  chatSessionsCount,
  artifactNavigationPanel,
  isArtifactPanelOpen,
  onCloseArtifactPanel,
  onWorkbenchTabChange,
  onRetryRestoreThread,
  onOpenHistorySessionModal,
  onCloseSession,
  onCreateSession,
  onMessageAnchorClick,
}: {
  activeStepText: string;
  routeThreadId?: string;
  isRestoringThread: boolean;
  threadRestoreError: string;
  activeWorkbenchTab?: SelfEvolutionWorkbenchTab;
  activeStageLabel: string;
  activeSession: SelfEvolutionSessionSummary;
  displayedMessages: SelfEvolutionChatMessage[];
  chatSessionsCount: number;
  artifactNavigationPanel: ReactNode;
  isArtifactPanelOpen: boolean;
  onCloseArtifactPanel: () => void;
  onWorkbenchTabChange: (tab?: SelfEvolutionWorkbenchTab) => void;
  onRetryRestoreThread: () => void;
  onOpenHistorySessionModal: () => void;
  onCloseSession: (sessionId: string) => void;
  onCreateSession: () => void;
  onMessageAnchorClick: (messageId: string) => void;
}) {
  const { t } = useTranslation();
  const userMessageAnchors = displayedMessages
    .map((item, index) => ({ ...item, index }))
    .filter((item) => item.role === "user");
  const getMessageNavTitle = (content: string) => content.replace(/\s+/g, " ").trim() || t("selfEvolutionRun.emptyMessage");

  const renderStageNavigationPanel = () => (
    <div className="self-evolution-artifact-sidebar is-navigation">
      {artifactNavigationPanel}
    </div>
  );
  const renderSidebarSection = (key: SelfEvolutionWorkbenchTab, title: string, desc: string, body: ReactNode) => {
    const isExpanded = activeWorkbenchTab === key;
    return (
      <section className={`self-evolution-workbench-accordion-section${isExpanded ? " is-active" : ""}`}>
        <button
          type="button"
          className="self-evolution-workbench-accordion-toggle"
          onClick={() => onWorkbenchTabChange(isExpanded ? undefined : key)}
          aria-expanded={isExpanded}
          aria-controls={`self-evolution-workbench-sidebar-${key}`}
        >
          <DownOutlined className="self-evolution-workbench-accordion-arrow" />
          <span>
            <strong>{title}</strong>
            <small>{desc}</small>
          </span>
        </button>
        {isExpanded && (
          <div id={`self-evolution-workbench-sidebar-${key}`} className="self-evolution-workbench-accordion-body">
            {body}
          </div>
        )}
      </section>
    );
  };
  const renderMessagesNavigationPanel = () => (
    <div className="self-evolution-message-nav-card">
      <div className="self-evolution-message-nav-summary">
        <strong>{activeSession.title}</strong>
        <span>{routeThreadId ? t("selfEvolutionRun.threadLabelShort", { id: routeThreadId }) : t("selfEvolutionRun.localSession")}</span>
        <span>{displayedMessages.length ? t("selfEvolutionRun.messageCountLabel", { count: displayedMessages.length }) : t("selfEvolutionRun.waitingMessages")}</span>
      </div>
      <div className="self-evolution-message-nav-list">
        {userMessageAnchors.length ? (
          userMessageAnchors.map((item, index) => (
            <button key={item.id} type="button" onClick={() => onMessageAnchorClick(item.id)}>
              <strong>{t("selfEvolutionRun.userMessageLabel", { index: index + 1 })}</strong>
              <span>{getMessageNavTitle(item.content)}</span>
              <em>{item.time}</em>
            </button>
          ))
        ) : (
          <span className="self-evolution-message-nav-empty">{t("selfEvolutionRun.noUserMessages")}</span>
        )}
      </div>
    </div>
  );

  return (
    <aside
      className="self-evolution-workbench-nav"
      aria-label={t("selfEvolutionRun.workbenchNavAria")}
      onClick={isArtifactPanelOpen ? onCloseArtifactPanel : undefined}
    >
      <div className="self-evolution-workbench-nav-head">
        <Title level={3}>{t("selfEvolutionRun.executionOrchestration")}</Title>
        <Paragraph>{t("selfEvolutionRun.currentFocus", { step: activeStepText })}</Paragraph>
        {routeThreadId && (
          <Text className="self-evolution-detail-thread">
            {t("selfEvolutionRun.threadIdWithRestore", { id: routeThreadId, restoring: isRestoringThread ? t("selfEvolutionRun.restoringDetailSuffix") : "" })}
          </Text>
        )}
        {threadRestoreError && routeThreadId && (
          <div className="self-evolution-restore-error" role="alert">
            <span>{threadRestoreError}</span>
            <button type="button" onClick={onRetryRestoreThread}>
              {t("selfEvolutionRun.retry")}
            </button>
          </div>
        )}
      </div>
      <div className="self-evolution-workbench-accordion">
        {renderSidebarSection("messages", t("selfEvolutionRun.navInteractionTitle"), t("selfEvolutionRun.navInteractionDesc"), renderMessagesNavigationPanel())}
        {renderSidebarSection("processes", t("selfEvolutionRun.navStageOverviewTitle"), activeStageLabel, renderStageNavigationPanel())}
      </div>
      <div className="self-evolution-workbench-sidebar-actions">
        {chatSessionsCount > 1 && (
          <button type="button" onClick={() => onCloseSession(activeSession.id)} title={t("selfEvolutionRun.closeCurrentSession")}>
            <CloseOutlined />
          </button>
        )}
        <button type="button" onClick={onCreateSession} title={t("selfEvolutionRun.newSession")}>
          <PlusOutlined />
          <span>{t("selfEvolutionRun.new")}</span>
        </button>
        <button type="button" onClick={onOpenHistorySessionModal} title={t("selfEvolutionRun.openHistoryAria")}>
          <HistoryOutlined />
          <span>{t("selfEvolutionRun.history")}</span>
        </button>
      </div>
    </aside>
  );
}
