import { useEffect, useState, type MouseEvent, type ReactNode, type RefObject, type WheelEvent } from "react";
import { message, Popconfirm, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  CheckCircleFilled,
  CloseOutlined,
  ClockCircleFilled,
  DownOutlined,
  EyeOutlined,
  HistoryOutlined,
  PlusOutlined,
} from "@ant-design/icons";
import {
  ChatComposer,
  ChatMessageStream,
  HistorySessionModal,
  NewSessionConfigModal,
} from ".";
import {
  type SelfEvolutionChatMessage,
  type SelfEvolutionHistoryEntry,
  type SelfEvolutionLaunchOptionCard,
  type SelfEvolutionSummaryItem,
  type SelfEvolutionWorkbenchTab,
} from "./types";
import {
  type CheckpointWaitPrompt,
  type EvoCaseProgressItem,
  type EvoProcessDashboard,
  type WorkflowResultKind,
  type WorkflowStep as SelfEvolutionRuntimeWorkflowStep,
} from "../shared";

const { Paragraph, Text, Title } = Typography;

type SelfEvolutionSessionSummary = {
  id: string;
  title: string;
};

export type SelfEvolutionFinalResultSummary = {
  verdict: "accept" | "reject" | "done";
  title: string;
  desc: string;
  metrics: { label: string; value: string; tone: "good" | "bad" | "neutral" }[];
  reasons: string[];
};

export type SelfEvolutionObservationKind = "eval" | "abtest";

export type SelfEvolutionWorkbenchViewProps = {
  processDashboard: EvoProcessDashboard;
  finalResultSummary?: SelfEvolutionFinalResultSummary;
  abtestPreviewPanel: ReactNode;
  activeWorkbenchTab?: SelfEvolutionWorkbenchTab;
  artifactNavigationPanel: ReactNode;
  artifactPanel: ReactNode;
  isArtifactPanelOpen: boolean;
  activeStepText: string;
  routeThreadId?: string;
  isRestoringThread: boolean;
  threadRestoreError: string;
  activeSession: SelfEvolutionSessionSummary;
  chatSessionsCount: number;
  historySessionEntries: SelfEvolutionHistoryEntry[];
  deletingHistoryKeys: string[];
  displayedMessages: SelfEvolutionChatMessage[];
  chatStreamRef: RefObject<HTMLDivElement>;
  isAutoMode: boolean;
  isAutoInteractionActive: boolean;
  isPlanningNextStep: boolean;
  isSendingMessage: boolean;
  displayedCheckpointWaitPrompt?: CheckpointWaitPrompt;
  prompt: string;
  selectedViewStage?: string;
  isHistorySessionModalOpen: boolean;
  threadHistoryListError: string;
  isLoadingThreadHistoryList: boolean;
  isNewSessionConfigOpen: boolean;
  newSessionOptionCards: SelfEvolutionLaunchOptionCard[];
  newSessionSummaryItems: SelfEvolutionSummaryItem[];
  isNewSessionStepOneDone: boolean;
  isNewSessionStepTwoDone: boolean;
  isNewSessionStepThreeDone: boolean;
  isNewSessionStepFourDone: boolean;
  isNewSessionConfirmDisabled: boolean;
  isConfirmingNewSession: boolean;
  getStepStatusLabel: (status: SelfEvolutionRuntimeWorkflowStep["status"]) => string;
  renderKnowledgeAndModeTools: () => ReactNode;
  renderSendButton: () => ReactNode;
  onRetryRestoreThread: () => void;
  onCloseSession: (sessionId: string) => void;
  onSelectHistorySession: (entry: SelfEvolutionHistoryEntry) => void;
  onEnterHistorySession: (entry: SelfEvolutionHistoryEntry) => void;
  onDeleteHistorySession: (
    entry: SelfEvolutionHistoryEntry,
    event: MouseEvent<HTMLElement>,
  ) => void;
  onCreateSession: () => void;
  onOpenHistorySessionModal: () => void;
  onPromptChange: (value: string) => void;
  onSend: (command?: string) => void;
  onConfirmIntentCheckpoint: () => void;
  onContinueCheckpoint: (command?: string) => void;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
  onOpenObservation: (kind: SelfEvolutionObservationKind) => void;
  onOpenCaseArtifact: (kind: WorkflowResultKind, artifactId: string, title: string, caseId?: string) => void;
  onWorkbenchTabChange: (tab?: SelfEvolutionWorkbenchTab) => void;
  onCloseArtifactPanel: () => void;
  onCloseHistorySessionModal: () => void;
  onRetryThreadHistoryList: () => void;
  onCancelCreateSession: () => void;
  onConfirmCreateSession: () => void;
};

export function SelfEvolutionWorkbenchView({
  processDashboard,
  finalResultSummary,
  abtestPreviewPanel,
  activeWorkbenchTab,
  artifactNavigationPanel,
  artifactPanel,
  isArtifactPanelOpen,
  activeStepText,
  routeThreadId,
  isRestoringThread,
  threadRestoreError,
  activeSession,
  chatSessionsCount,
  historySessionEntries,
  deletingHistoryKeys,
  displayedMessages,
  chatStreamRef,
  isAutoMode,
  isAutoInteractionActive,
  isPlanningNextStep,
  isSendingMessage,
  displayedCheckpointWaitPrompt,
  prompt,
  selectedViewStage,
  isHistorySessionModalOpen,
  threadHistoryListError,
  isLoadingThreadHistoryList,
  isNewSessionConfigOpen,
  newSessionOptionCards,
  newSessionSummaryItems,
  isNewSessionStepOneDone,
  isNewSessionStepTwoDone,
  isNewSessionStepThreeDone,
  isNewSessionStepFourDone,
  isNewSessionConfirmDisabled,
  isConfirmingNewSession,
  getStepStatusLabel,
  renderKnowledgeAndModeTools,
  renderSendButton,
  onRetryRestoreThread,
  onCloseSession,
  onSelectHistorySession,
  onEnterHistorySession,
  onDeleteHistorySession,
  onCreateSession,
  onOpenHistorySessionModal,
  onPromptChange,
  onSend,
  onConfirmIntentCheckpoint,
  onContinueCheckpoint,
  onOpenArtifact,
  onOpenObservation,
  onOpenCaseArtifact,
  onWorkbenchTabChange,
  onCloseArtifactPanel,
  onCloseHistorySessionModal,
  onRetryThreadHistoryList,
  onCancelCreateSession,
  onConfirmCreateSession,
}: SelfEvolutionWorkbenchViewProps) {
  const { t } = useTranslation();
  const [isEndedChatOpen, setIsEndedChatOpen] = useState(false);
  const [isInteractionChatOpen, setIsInteractionChatOpen] = useState(false);
  const [caseProgressPageByStage, setCaseProgressPageByStage] = useState<Record<string, number>>({});

  const activeStageTitles: Record<string, string> = {
    dataset: t("selfEvolutionRun.stageTitle.dataset"),
    eval: t("selfEvolutionRun.stageTitle.eval"),
    analysis: t("selfEvolutionRun.stageTitle.analysis"),
    repair: t("selfEvolutionRun.stageTitle.repair"),
    abtest: t("selfEvolutionRun.stageTitle.abtest"),
  };

  const displayStage = selectedViewStage || processDashboard.activeStage;
  const activeStageOverview = displayStage ? processDashboard.overview.find((item) => item.stage === displayStage) : undefined;
  const activeStageLabel =
    displayStage
      ? activeStageTitles[displayStage] || activeStageOverview?.step.title || activeStepText
      : activeStepText;
  const checkpointDecisionPrompt = processDashboard.checkpoint || displayedCheckpointWaitPrompt;
  const isCutoverDecision = Boolean(
    !processDashboard.cutoverCompleted && checkpointDecisionPrompt?.checkpointKind === "manual_cutover",
  );
  const isIntentConfirmation = checkpointDecisionPrompt?.checkpointKind === "intent_confirmation";
  const shouldShowCutoverCard = displayStage === "abtest" && (isCutoverDecision || processDashboard.cutoverCompleted);
  const checkpointDecisionDesc =
    checkpointDecisionPrompt?.nextOperationLabel
      ? t("selfEvolutionRun.checkpointNextStep", { label: checkpointDecisionPrompt.nextOperationLabel })
      : checkpointDecisionPrompt?.message || t("selfEvolutionRun.checkpointContinueDefault");
  const cutoverDecisionEvidence = processDashboard.cutoverActivities.filter((item) => item.tone !== "auto").slice(0, 2).map((item) => ({
    ...item,
    title: item.title === "abtest · compare" ? t("selfEvolutionRun.cutoverThreshold") : item.title,
  }));
  const activeStageStatusKey = activeStageOverview?.step.status || processDashboard.activeStep?.status || "pending";
  const activeStageStatus = displayStage === "abtest" && processDashboard.cutoverCompleted
    ? t("selfEvolutionRun.cutoverDone")
    : displayStage === "abtest" && isCutoverDecision
    ? t("selfEvolutionRun.cutoverPending")
    : activeStageStatusKey === "running"
    ? t("selfEvolutionRun.statusRunning")
    : getStepStatusLabel(activeStageStatusKey);
  const hasThreadRestoreError = Boolean(threadRestoreError && routeThreadId);
  const normalizedThreadRestoreError = threadRestoreError.trim().toLowerCase();
  const isThreadRestoreNotFound =
    normalizedThreadRestoreError.includes("thread not found") ||
    threadRestoreError.includes(t("selfEvolutionRun.threadNotFoundTitle"));
  const threadRestoreNoticeTitle = isThreadRestoreNotFound
    ? t("selfEvolutionRun.threadNotFoundTitle")
    : t("selfEvolutionRun.threadLoadFailedTitle");
  const threadRestoreNoticeDesc = isThreadRestoreNotFound
    ? t("selfEvolutionRun.threadNotFoundDesc", { id: routeThreadId })
    : threadRestoreError;
  const userMessageAnchors = displayedMessages
    .map((item, index) => ({ ...item, index }))
    .filter((item) => item.role === "user");
  const latestUserMessageIndex = displayedMessages.reduce((latestIndex, item, index) => item.role === "user" ? index : latestIndex, -1);
  const latestDialogueMessages = latestUserMessageIndex >= 0 ? displayedMessages.slice(latestUserMessageIndex) : displayedMessages.slice(-1);
  const visibleInteractionMessages = isInteractionChatOpen ? displayedMessages : latestDialogueMessages;
  const getMessageNavTitle = (content: string) => content.replace(/\s+/g, " ").trim() || t("selfEvolutionRun.emptyMessage");
  const scrollToMessage = (messageId: string) => {
    const target = Array.from(chatStreamRef.current?.querySelectorAll<HTMLElement>("[data-self-evolution-message-id]") || [])
      .find((item) => item.dataset.selfEvolutionMessageId === messageId);
    if (!target) return;
    target.scrollIntoView({ block: "center", behavior: "smooth" });
    target.classList.add("is-targeted");
    window.setTimeout(() => target.classList.remove("is-targeted"), 1500);
  };
  const handleMessageAnchorClick = (messageId: string) => {
    onWorkbenchTabChange("messages");
    setIsEndedChatOpen(true);
    setIsInteractionChatOpen(true);
    window.requestAnimationFrame(() => window.requestAnimationFrame(() => scrollToMessage(messageId)));
  };
  useEffect(() => {
    setIsInteractionChatOpen(false);
    setIsEndedChatOpen(false);
  }, [activeSession.id]);
  const handleActivityListWheel = (event: WheelEvent<HTMLDivElement>) => {
    const maxScrollTop = event.currentTarget.scrollHeight - event.currentTarget.clientHeight;
    if (maxScrollTop <= 0 || event.deltaY === 0) return;
    const nextScrollTop = Math.max(0, Math.min(maxScrollTop, event.currentTarget.scrollTop + event.deltaY));
    if (nextScrollTop === event.currentTarget.scrollTop) return;
    event.preventDefault();
    event.stopPropagation();
    event.currentTarget.scrollTop = nextScrollTop;
  };
  const keyActivities = processDashboard.recentActivities
    .filter((item) => item.artifactKind || item.artifactId || item.stage || ["checkpoint", "auto", "error", "message", "progress"].includes(item.tone))
    .slice(0, 16);
  const visibleKeyActivities = keyActivities.length ? keyActivities : processDashboard.recentActivities.slice(0, 16);
  const selectedStageActivities = displayStage ? processDashboard.recentActivities.filter((item) => item.stage === displayStage).slice(0, 16) : visibleKeyActivities;
  const activeCaseProgressGroup = processDashboard.caseProgressGroups.find((group) => group.stage === displayStage);
  const isReadOnlyEnded = Boolean(!checkpointDecisionPrompt && processDashboard.overview.every((item) => item.step.status === "done"));
  const shouldShowFinalResultCard = isReadOnlyEnded && !selectedViewStage;
  const shouldShowStageDetail = !isReadOnlyEnded || Boolean(selectedViewStage);
  const renderFinalResultCard = () => finalResultSummary ? (
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
  ) : (
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
  const renderStageNavigationPanel = () => (
    <div className="self-evolution-artifact-sidebar is-navigation">
      {artifactNavigationPanel}
    </div>
  );
  const renderThreadRestoreNotice = () => (
    <div className="self-evolution-restore-notice" role="alert">
      <span className="self-evolution-restore-notice-icon">
        <CloseOutlined />
      </span>
      <Text>{t("selfEvolutionRun.selfEvolutionDetail")}</Text>
      <Title level={4}>{threadRestoreNoticeTitle}</Title>
      <Paragraph>{threadRestoreNoticeDesc}</Paragraph>
      <div className="self-evolution-restore-notice-actions">
        <button type="button" onClick={onRetryRestoreThread}>
          {t("selfEvolutionRun.retry")}
        </button>
        <button type="button" onClick={onOpenHistorySessionModal}>
          {t("selfEvolutionRun.viewHistory")}
        </button>
      </div>
    </div>
  );
  const renderActivityRows = (activities: EvoProcessDashboard["recentActivities"], emptyText: string) => (
    activities.length === 0 ? (
      <Paragraph className="self-evolution-process-activity-empty">
        {emptyText}
      </Paragraph>
    ) : (
      activities.map((item) => {
        const activityStageDone = item.stage && processDashboard.overview.find((overviewItem) => overviewItem.stage === item.stage)?.step.status === "done";
        return (
          <div key={item.key} className={`self-evolution-process-activity-row is-${item.tone}`}>
            <span className="self-evolution-process-activity-dot" />
            <div className="self-evolution-process-activity-content">
              <div className="self-evolution-process-activity-title">
                <strong>{item.title}</strong>
                <span>{item.time}</span>
              </div>
              <Paragraph>{item.detail}</Paragraph>
            </div>
            {item.artifactKind && activityStageDone && (
              <button
                type="button"
                className="self-evolution-process-activity-action"
                onClick={(event) => {
                  event.stopPropagation();
                  onOpenArtifact(item.artifactKind!);
                }}
              >
                {item.artifactLabel || t("selfEvolutionRun.viewActivity")}
              </button>
            )}
          </div>
        );
      })
    )
  );
  const renderCaseProgressRow = (item: EvoCaseProgressItem) => (
    <div key={item.caseId} className={`self-evolution-case-row is-${item.status}`}>
      <strong className="self-evolution-case-title">{item.title}</strong>
      <div className="self-evolution-case-step-list" aria-label={t("selfEvolutionRun.caseProgressAria", { caseId: item.caseId })}>
        {item.steps.map((step) => (
          <span key={step.key} className={`self-evolution-case-step is-${step.status}`} title={`${step.label} · ${getStepStatusLabel(step.status)}`}>
            {step.label}
          </span>
        ))}
      </div>
      <span className="self-evolution-case-count">{`${item.completed}/${item.total}`}</span>
      <span className={`self-evolution-case-status is-${item.status}`}>{getStepStatusLabel(item.status)}</span>
      <button
        type="button"
        disabled={!item.artifactId}
        title={item.artifactLabel}
        onClick={(event) => {
          event.stopPropagation();
          if (item.artifactId) {
            onOpenCaseArtifact(item.artifactKind, item.artifactId, `${item.title} · ${item.artifactLabel}`, item.caseId);
          }
        }}
      >
        {t("selfEvolutionRun.viewDetail")}
      </button>
    </div>
  );
  const renderCaseProgressPanel = () => {
    if (!activeCaseProgressGroup) {
      return renderActivityRows(selectedStageActivities.length ? selectedStageActivities : visibleKeyActivities, t("selfEvolutionRun.activityEmptyDefault"));
    }
    const pageSize = activeCaseProgressGroup.pageSize;
    const totalPages = Math.max(1, Math.ceil(activeCaseProgressGroup.cases.length / pageSize));
    const currentPage = Math.min(caseProgressPageByStage[activeCaseProgressGroup.stage] || 1, totalPages);
    const pageCases = activeCaseProgressGroup.cases.slice((currentPage - 1) * pageSize, currentPage * pageSize);
    const completedCases = activeCaseProgressGroup.cases.filter((item) => item.status === "done").length;
    const setPage = (page: number) => setCaseProgressPageByStage((prev) => ({ ...prev, [activeCaseProgressGroup.stage]: Math.max(1, Math.min(totalPages, page)) }));
    return (
      <div className="self-evolution-case-progress">
        <div className="self-evolution-case-progress-summary">
          <span>{t("selfEvolutionRun.caseCompletedSummary", { title: activeCaseProgressGroup.title, completed: completedCases, total: activeCaseProgressGroup.cases.length })}</span>
          <div className="self-evolution-case-progress-pager">
            <button type="button" disabled={currentPage <= 1} onClick={() => setPage(currentPage - 1)}>{t("selfEvolutionRun.prevPage")}</button>
            <span>{`${currentPage}/${totalPages}`}</span>
            <button type="button" disabled={currentPage >= totalPages} onClick={() => setPage(currentPage + 1)}>{t("selfEvolutionRun.nextPage")}</button>
          </div>
        </div>
        <div className="self-evolution-case-list">
          {pageCases.map(renderCaseProgressRow)}
        </div>
      </div>
    );
  };
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
            <button key={item.id} type="button" onClick={() => handleMessageAnchorClick(item.id)}>
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
  const renderWorkbenchNavigationPanel = () => (
    <div className="self-evolution-workbench-accordion">
      {renderSidebarSection("messages", t("selfEvolutionRun.navInteractionTitle"), t("selfEvolutionRun.navInteractionDesc"), renderMessagesNavigationPanel())}
      {renderSidebarSection("processes", t("selfEvolutionRun.navStageOverviewTitle"), activeStageLabel, renderStageNavigationPanel())}
    </div>
  );
  const renderInteractionFeed = () => isReadOnlyEnded ? (
    <details
      className="self-evolution-workbench-chat-feed is-ended"
      open={isEndedChatOpen}
      onToggle={(event) => setIsEndedChatOpen(event.currentTarget.open)}
    >
      <summary>
        <span>
          <Text>{t("selfEvolutionRun.interactionHistory")}</Text>
        </span>
        <strong>{displayedMessages.length ? t("selfEvolutionRun.messageCountLabel", { count: displayedMessages.length }) : t("selfEvolutionRun.noMessages")}</strong>
        <DownOutlined />
      </summary>
      {isEndedChatOpen && (
        <div className="self-evolution-workbench-tab-body">
          <ChatMessageStream
            isAutoInteractionActive={isAutoInteractionActive}
            messages={displayedMessages}
            streamRef={chatStreamRef}
          />
        </div>
      )}
    </details>
  ) : (
    <div className={`self-evolution-workbench-chat-feed is-collapsible${isInteractionChatOpen ? " is-open" : ""}`}>
      <button
        type="button"
        className="self-evolution-workbench-chat-summary"
        onClick={() => setIsInteractionChatOpen((prev) => !prev)}
        aria-expanded={isInteractionChatOpen}
      >
        <span>
          <Text>{t("selfEvolutionRun.navInteractionTitle")}</Text>
        </span>
        {isPlanningNextStep && <em className="self-evolution-planning-pulse">{t("selfEvolutionRun.planningNextStep")}</em>}
        <strong>{displayedMessages.length ? t("selfEvolutionRun.messageCountLabel", { count: displayedMessages.length }) : t("selfEvolutionRun.waitingMessages")}</strong>
        <em>{isInteractionChatOpen ? t("selfEvolutionRun.collapse") : t("selfEvolutionRun.viewDetail")}</em>
        <DownOutlined />
      </button>
      <div className="self-evolution-workbench-tab-body">
        <ChatMessageStream
          isAutoInteractionActive={isAutoInteractionActive}
          messages={visibleInteractionMessages}
          streamRef={chatStreamRef}
        />
      </div>
    </div>
  );
  const renderMainComposer = () => (
    <div className="self-evolution-main-composer">
      {checkpointDecisionPrompt && !shouldShowCutoverCard && (
        <div className="self-evolution-composer-checkpoint">
          <span>{checkpointDecisionDesc}</span>
          <button
            type="button"
            disabled={!checkpointDecisionPrompt.command || isSendingMessage}
            onClick={(event) => {
              event.stopPropagation();
              if (checkpointDecisionPrompt.command) {
                if (isIntentConfirmation) {
                  onConfirmIntentCheckpoint();
                } else {
                  onContinueCheckpoint(checkpointDecisionPrompt.command);
                }
              }
            }}
          >
            {checkpointDecisionPrompt.command || t("selfEvolutionRun.continueExecution")}
          </button>
        </div>
      )}
      <ChatComposer
        activeStepText={activeStepText}
        isAutoMode={isAutoMode}
        isReadOnlyEnded={isReadOnlyEnded}
        isSendingMessage={isSendingMessage}
        pendingCheckpointWaitPrompt={displayedCheckpointWaitPrompt}
        prompt={prompt}
        onPromptChange={onPromptChange}
        onSend={onSend}
        renderKnowledgeAndModeTools={renderKnowledgeAndModeTools}
        renderSendButton={renderSendButton}
      />
    </div>
  );
  return (
    <div className="self-evolution-session-page">
      <div className="self-evolution-workbench">
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
          {renderWorkbenchNavigationPanel()}
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

        <main
          className="self-evolution-workflow-panel"
          aria-label={t("selfEvolutionRun.executionStepsAria")}
          onClick={isArtifactPanelOpen ? onCloseArtifactPanel : undefined}
        >
          <div className="self-evolution-workbench-main-scroll">
            {hasThreadRestoreError ? renderThreadRestoreNotice() : (
              <div className="self-evolution-process-board" aria-label={t("selfEvolutionRun.evoFlowProgressAria")}>
                <div className="self-evolution-process-live">
                  <div className="self-evolution-process-live-main">
                    <Text className="self-evolution-process-live-kicker">{selectedViewStage ? t("selfEvolutionRun.viewingStage") : t("selfEvolutionRun.currentStage")}</Text>
                    <div className="self-evolution-process-live-title">
                      <Title level={4}>{activeStageLabel}</Title>
                      <span className={`self-evolution-process-live-status is-${activeStageStatusKey}`}>
                        {activeStageStatus}
                      </span>
                    </div>
                  </div>
                  {(displayStage === "eval" || displayStage === "abtest") && (
                    <div className="self-evolution-process-observation-actions" aria-label={t("selfEvolutionRun.observationEntryAria")}>
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation();
                          onOpenObservation(displayStage === "abtest" ? "abtest" : "eval");
                        }}
                        aria-label={displayStage === "abtest" ? t("selfEvolutionRun.enterStep5ABObservation") : t("selfEvolutionRun.enterStep2Observation")}
                      >
                        <EyeOutlined />
                        {displayStage === "abtest" ? t("selfEvolutionRun.step5AB") : t("selfEvolutionRun.step2Observation")}
                      </button>
                    </div>
                  )}
                  {shouldShowCutoverCard && (
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
                  )}
                </div>

                {displayStage === "abtest" && abtestPreviewPanel && (
                  <div className="self-evolution-abtest-stage-panel">
                    {abtestPreviewPanel}
                  </div>
                )}

                {shouldShowFinalResultCard && renderFinalResultCard()}

                {shouldShowStageDetail && (
                  <div className="self-evolution-process-activity">
                    <div className="self-evolution-process-activity-head">
                      <Text>{activeCaseProgressGroup ? t("selfEvolutionRun.caseProgressSectionTitle") : t("selfEvolutionRun.keyEventsSectionTitle")}</Text>
                      <span>{activeCaseProgressGroup ? t("selfEvolutionRun.displayByCasePaged") : activeStageLabel}</span>
                    </div>
                    <div className="self-evolution-process-activity-list is-key" onWheel={handleActivityListWheel}>
                      {renderCaseProgressPanel()}
                    </div>
                    <details className="self-evolution-process-debug-log">
                      <summary>{t("selfEvolutionRun.debugLogTitle", { count: processDashboard.recentActivityTotal })}</summary>
                      <div className="self-evolution-process-activity-list is-debug" onWheel={handleActivityListWheel}>
                        {renderActivityRows(processDashboard.recentActivities, t("selfEvolutionRun.debugLogEmptyHint"))}
                      </div>
                    </details>
                  </div>
                )}

              </div>
            )}
          </div>
        </main>

        <aside
          className="self-evolution-interaction-column"
          aria-label={t("selfEvolutionRun.qnaInteractionAria")}
          onClick={isArtifactPanelOpen ? onCloseArtifactPanel : undefined}
        >
          {renderInteractionFeed()}
          {renderMainComposer()}
        </aside>

        {isArtifactPanelOpen && (
          <section className="self-evolution-artifact-drawer" aria-label={t("selfEvolutionRun.artifactDrawerAria")}>
            <div className="self-evolution-artifact-drawer-head">
              <Text strong>{t("selfEvolutionRun.artifactDetail")}</Text>
              <button type="button" onClick={onCloseArtifactPanel} aria-label={t("selfEvolutionRun.closeArtifactDetail")}>
                <CloseOutlined />
              </button>
            </div>
            <div className="self-evolution-artifact-drawer-body">
              {artifactPanel}
            </div>
          </section>
        )}

        <HistorySessionModal
          open={isHistorySessionModalOpen}
          threadHistoryListError={threadHistoryListError}
          isLoadingThreadHistoryList={isLoadingThreadHistoryList}
          historySessionEntries={historySessionEntries}
          deletingHistoryKeys={deletingHistoryKeys}
          onCancel={onCloseHistorySessionModal}
          onRetry={onRetryThreadHistoryList}
          onSelectHistorySession={onSelectHistorySession}
          onEnterHistorySession={onEnterHistorySession}
          onDeleteHistorySession={onDeleteHistorySession}
        />

        <NewSessionConfigModal
          open={isNewSessionConfigOpen}
          optionCards={newSessionOptionCards}
          summaryItems={newSessionSummaryItems}
          isStepOneDone={isNewSessionStepOneDone}
          isStepTwoDone={isNewSessionStepTwoDone}
          isStepThreeDone={isNewSessionStepThreeDone}
          isStepFourDone={isNewSessionStepFourDone}
          isConfirmDisabled={isNewSessionConfirmDisabled}
          isConfirming={isConfirmingNewSession}
          onCancel={onCancelCreateSession}
          onConfirm={onConfirmCreateSession}
        />
      </div>
    </div>
  );
}
