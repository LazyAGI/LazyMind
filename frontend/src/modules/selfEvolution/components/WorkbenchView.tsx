import { useEffect, useState, type MouseEvent, type ReactNode, type RefObject, type WheelEvent } from "react";
import { message, Popconfirm, Typography } from "antd";
import { useTranslation } from "react-i18next";
import {
  CheckCircleFilled,
  CloseOutlined,
  ClockCircleFilled,
  DownOutlined,
  FileTextOutlined,
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

const completedStepDetails: Record<string, string> = {
  dataset: "数据集已生成并写入本轮自进化流程。",
  eval: "评测已完成，基线结果已用于后续分析。",
  analysis: "错误分析已完成，归因与修复优先级已生成。",
  repair: "代码优化阶段已完成，候选改动已进入对照验证。",
  abtest: "ABTest 已完成，结论已写入最终结果。",
};

const activeStageTitles: Record<string, string> = {
  dataset: "数据集生成",
  eval: "执行评测",
  analysis: "错误分析",
  repair: "代码优化",
  abtest: "ABTest 和切流",
};

type FinalResultMetric = {
  label: string;
  value: string;
  tone: "good" | "bad" | "neutral";
};

export type SelfEvolutionFinalResultSummary = {
  verdict: "accept" | "reject" | "done";
  title: string;
  desc: string;
  metrics: FinalResultMetric[];
  reasons: string[];
};

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
  onDeleteHistorySession: (
    entry: SelfEvolutionHistoryEntry,
    event: MouseEvent<HTMLElement>,
  ) => void;
  onCreateSession: () => void;
  onOpenHistorySessionModal: () => void;
  onPromptChange: (value: string) => void;
  onSend: (command?: string) => void;
  onConfirmIntentCheckpoint: () => void;
  onOpenArtifact: (kind: WorkflowResultKind) => void;
  onOpenCaseArtifact: (kind: WorkflowResultKind, artifactId: string, title: string) => void;
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
  onDeleteHistorySession,
  onCreateSession,
  onOpenHistorySessionModal,
  onPromptChange,
  onSend,
  onConfirmIntentCheckpoint,
  onOpenArtifact,
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
  const [selectedStage, setSelectedStage] = useState<string>();
  const [caseProgressPageByStage, setCaseProgressPageByStage] = useState<Record<string, number>>({});
  const displayStage = selectedStage || processDashboard.activeStage;
  const activeStageOverview = displayStage ? processDashboard.overview.find((item) => item.stage === displayStage) : undefined;
  const activeStageLabel =
    displayStage
      ? activeStageTitles[displayStage] || activeStageOverview?.step.title
      : activeStepText;
  const checkpointDecisionPrompt = processDashboard.checkpoint || displayedCheckpointWaitPrompt;
  const isCutoverDecision = Boolean(
    !processDashboard.cutoverCompleted && checkpointDecisionPrompt?.checkpointKind === "manual_cutover",
  );
  const isIntentConfirmation = checkpointDecisionPrompt?.checkpointKind === "intent_confirmation";
  const shouldShowCutoverCard = displayStage === "abtest" && (isCutoverDecision || processDashboard.cutoverCompleted);
  const checkpointDecisionDesc =
    checkpointDecisionPrompt?.nextOperationLabel
      ? `下一步：${checkpointDecisionPrompt.nextOperationLabel}`
      : checkpointDecisionPrompt?.message || "确认后继续推进当前流程。";
  const cutoverDecisionEvidence = processDashboard.cutoverActivities.filter((item) => item.tone !== "auto").slice(0, 2).map((item) => ({
    ...item,
    title: item.title === "abtest · compare" ? "切流门槛" : item.title,
  }));
  const activeStageStatusKey = activeStageOverview?.step.status || processDashboard.activeStep?.status || "pending";
  const activeStageStatus = displayStage === "abtest" && processDashboard.cutoverCompleted
    ? "已切流"
    : displayStage === "abtest" && isCutoverDecision
    ? "待切流"
    : activeStageStatusKey === "running"
    ? "执行中"
    : getStepStatusLabel(activeStageStatusKey);
  const userMessageAnchors = displayedMessages
    .map((item, index) => ({ ...item, index }))
    .filter((item) => item.role === "user");
  const latestUserMessageIndex = displayedMessages.reduce((latestIndex, item, index) => item.role === "user" ? index : latestIndex, -1);
  const latestDialogueMessages = latestUserMessageIndex >= 0 ? displayedMessages.slice(latestUserMessageIndex) : displayedMessages.slice(-1);
  const visibleInteractionMessages = isInteractionChatOpen ? displayedMessages : latestDialogueMessages;
  const getMessageNavTitle = (content: string) => content.replace(/\s+/g, " ").trim() || "空消息";
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
  useEffect(() => {
    setSelectedStage(undefined);
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
  const renderFinalResultCard = () => finalResultSummary ? (
    <section className={`self-evolution-final-result is-${finalResultSummary.verdict}`} aria-label="最终结果">
      <div className="self-evolution-final-result-main">
        <span className="self-evolution-final-result-icon">
          {finalResultSummary.verdict === "reject" ? <CloseOutlined /> : <CheckCircleFilled />}
        </span>
        <div>
          <Text>最终结果</Text>
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
        查看 ABTest 详情
      </button>
    </section>
  ) : (
    <section className="self-evolution-final-result is-loading" aria-label="最终结果">
      <div className="self-evolution-final-result-main">
        <span className="self-evolution-final-result-icon">
          <ClockCircleFilled />
        </span>
        <div>
          <Text>最终结果</Text>
          <Title level={4}>正在加载最终结果</Title>
          <Paragraph>五步流程已完成，正在读取 ABTest 结论与切流建议。</Paragraph>
        </div>
      </div>
    </section>
  );
  const renderProcessOverview = () => (
    <div className="self-evolution-process-overview">
      {processDashboard.overview.map((item) => {
        const stepDetail = isReadOnlyEnded && item.step.status === "done"
          ? completedStepDetails[item.stage] || "该阶段已完成。"
          : item.stage === "abtest"
          ? item.step.progress?.statusText || completedStepDetails[item.stage] || item.step.desc
          : item.latestActivity?.detail || item.step.progress?.statusText || item.step.runtimeText || item.step.desc;
        return (
          <div
            role="button"
            tabIndex={0}
            key={item.step.id}
            className={`self-evolution-process-step is-${item.step.status}${displayStage === item.stage ? " is-active" : ""}`}
            onClick={() => setSelectedStage(item.stage)}
            onKeyDown={(event) => {
              if (event.key !== "Enter" && event.key !== " ") return;
              event.preventDefault();
              setSelectedStage(item.stage);
            }}
          >
            <div className="self-evolution-process-step-head">
              <span className="self-evolution-process-step-icon">
                {item.step.status === "done" && <CheckCircleFilled />}
                {(item.step.status === "running" || item.step.status === "paused") && <ClockCircleFilled />}
                {item.step.status === "pending" && <FileTextOutlined />}
                {(item.step.status === "failed" || item.step.status === "canceled") && <CloseOutlined />}
              </span>
              <span className="self-evolution-process-step-title">{activeStageTitles[item.stage] || item.step.title.replace(/^Step\s+\d+\s+·\s+/, "")}</span>
            </div>
            <div className="self-evolution-process-step-meta">
              <span>{getStepStatusLabel(item.step.status)}</span>
              <strong>{item.eventCount ? `${item.eventCount} 个事件` : getStepStatusLabel(item.step.status)}</strong>
            </div>
            <Paragraph className="self-evolution-process-step-detail">
              {stepDetail}
            </Paragraph>
            {item.latestActivity?.artifactKind && item.step.status === "done" && (
              <button
                type="button"
                className="self-evolution-process-step-action"
                onClick={(event) => {
                  event.stopPropagation();
                  onOpenArtifact(item.latestActivity!.artifactKind!);
                }}
              >
                {item.latestActivity.artifactLabel || "查看产物"}
              </button>
            )}
          </div>
        );
      })}
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
                {item.artifactLabel || "查看"}
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
      <div className="self-evolution-case-step-list" aria-label={`${item.caseId} 进度`}>
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
            onOpenCaseArtifact(item.artifactKind, item.artifactId, `${item.title} · ${item.artifactLabel}`);
          }
        }}
      >
        查看详情
      </button>
    </div>
  );
  const renderCaseProgressPanel = () => {
    if (!activeCaseProgressGroup) {
      return renderActivityRows(selectedStageActivities.length ? selectedStageActivities : visibleKeyActivities, "启动后会在这里按阶段展示进度。");
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
          <span>{`${activeCaseProgressGroup.title} · ${completedCases}/${activeCaseProgressGroup.cases.length} case 完成`}</span>
          <div className="self-evolution-case-progress-pager">
            <button type="button" disabled={currentPage <= 1} onClick={() => setPage(currentPage - 1)}>上一页</button>
            <span>{`${currentPage}/${totalPages}`}</span>
            <button type="button" disabled={currentPage >= totalPages} onClick={() => setPage(currentPage + 1)}>下一页</button>
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
        <span>{routeThreadId ? `线程 ${routeThreadId}` : "本地会话"}</span>
        <span>{displayedMessages.length ? `${displayedMessages.length} 条消息` : "等待消息"}</span>
      </div>
      <div className="self-evolution-message-nav-list">
        {userMessageAnchors.length ? (
          userMessageAnchors.map((item, index) => (
            <button key={item.id} type="button" onClick={() => handleMessageAnchorClick(item.id)}>
              <strong>{`用户消息 ${index + 1}`}</strong>
              <span>{getMessageNavTitle(item.content)}</span>
              <em>{item.time}</em>
            </button>
          ))
        ) : (
          <span className="self-evolution-message-nav-empty">暂无用户消息</span>
        )}
      </div>
    </div>
  );
  const renderHistoryNavigationPanel = () => (
    <>
      <div className="self-evolution-sidebar-action-row">
        <button type="button" onClick={onRetryThreadHistoryList}>刷新历史</button>
      </div>
      {threadHistoryListError && (
        <div className="self-evolution-process-history-alert">
          <span>{threadHistoryListError}</span>
          <button type="button" onClick={onRetryThreadHistoryList}>重试</button>
        </div>
      )}
      <div className="self-evolution-process-history-list is-navigation">
        {historySessionEntries.length === 0 ? (
          <Paragraph className="self-evolution-process-history-empty">
            {isLoadingThreadHistoryList ? "正在加载历史对话..." : "暂无历史自进化对话。"}
          </Paragraph>
        ) : (
          historySessionEntries.map((entry) => (
            <article
              key={entry.key}
              className={`self-evolution-process-history-item is-navigation${entry.isCurrent ? " is-current" : ""}${entry.isPreviewing ? " is-previewing" : ""}`}
            >
              <button type="button" onClick={() => onSelectHistorySession(entry)} disabled={entry.isCurrent}>
                <strong>{entry.title}</strong>
                <span>{[entry.updatedAt, entry.status, entry.messageCount ? `${entry.messageCount} 条消息` : ""].filter(Boolean).join(" · ")}</span>
                {entry.isPreviewing && <em>预览中，再次点击进入</em>}
              </button>
              <button
                type="button"
                className="self-evolution-process-history-delete"
                disabled={deletingHistoryKeys.includes(entry.key)}
                onClick={(event) => onDeleteHistorySession(entry, event)}
              >
                <CloseOutlined />
              </button>
            </article>
          ))
        )}
      </div>
    </>
  );
  const renderWorkbenchNavigationPanel = () => (
    <div className="self-evolution-workbench-accordion">
      {renderSidebarSection("processes", "历史对话", "查看和切换所有自进化对话", renderHistoryNavigationPanel())}
      {renderSidebarSection("messages", "交互处理", "当前会话与消息入口", renderMessagesNavigationPanel())}
    </div>
  );
  return (
    <div className="self-evolution-session-page">
      <div className="self-evolution-workbench">
        <aside
          className="self-evolution-workbench-nav"
          aria-label="自进化导航面板"
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
              <button type="button" onClick={() => onCloseSession(activeSession.id)} title="关闭当前会话">
                <CloseOutlined />
              </button>
            )}
            <button type="button" onClick={onCreateSession} title={t("selfEvolutionRun.newSession")}>
              <PlusOutlined />
              <span>新建</span>
            </button>
            <button type="button" onClick={onOpenHistorySessionModal} title={t("selfEvolutionRun.openHistoryAria")}>
              <HistoryOutlined />
              <span>历史</span>
            </button>
          </div>
        </aside>

        <main
          className="self-evolution-workflow-panel"
          aria-label={t("selfEvolutionRun.executionStepsAria")}
          onClick={isArtifactPanelOpen ? onCloseArtifactPanel : undefined}
        >
          <div className="self-evolution-workbench-main-scroll">
            <div className="self-evolution-process-board" aria-label="evo 全流程进度">
                <div className="self-evolution-process-live">
                  <div className="self-evolution-process-live-main">
                    <Text className="self-evolution-process-live-kicker">{selectedStage ? "查看阶段" : "当前阶段"}</Text>
                    <div className="self-evolution-process-live-title">
                      <Title level={4}>{activeStageLabel}</Title>
                      <span className={`self-evolution-process-live-status is-${activeStageStatusKey}`}>
                        {activeStageStatus}
                      </span>
                    </div>
                  </div>
                  {shouldShowCutoverCard && (
                    <div className="self-evolution-cutover-decision" aria-label="ABTest 切流确认">
                      <div className="self-evolution-cutover-decision-head">
                        <CheckCircleFilled />
                        <span>
                          <strong>{processDashboard.cutoverCompleted ? "候选算法已切流" : "ABTest 已通过"}</strong>
                          <small>{processDashboard.cutoverCompleted ? "线上 chat 服务已使用候选算法" : "当前线上仍使用原版本"}</small>
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
                            <strong>候选满足条件</strong>
                            <span>{checkpointDecisionPrompt?.message || "确认后才会切换 chat 服务。"}</span>
                          </p>
                        )}
                        {processDashboard.cutoverCompleted ? (
                          <p>
                            <strong>切换已完成</strong>
                            <span>候选算法已注册并切换到线上 chat 服务。</span>
                          </p>
                        ) : (
                          <p>
                            <strong>尚未执行切换</strong>
                            <span>点击确认后才会注册候选算法并切换 chat 服务。</span>
                          </p>
                        )}
                      </div>
                      {!processDashboard.cutoverCompleted && (
                        <div className="self-evolution-cutover-decision-actions">
                          <Popconfirm
                            title="确认切换 chat 服务？"
                            description="确认后会注册候选算法并切换线上 chat 服务。"
                            okText="确认切流"
                            cancelText="取消"
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
                              {checkpointDecisionPrompt?.command || "确认切流"}
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
                            查看 ABTest 详情
                          </button>
                          <button
                            type="button"
                            className="self-evolution-cutover-decision-neutral"
                            onClick={(event) => {
                              event.stopPropagation();
                              void message.info("已保持当前版本；需要切流时再确认。", 1.6);
                            }}
                          >
                            保持当前版本
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
                          查看 ABTest 详情
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

                {!isReadOnlyEnded && (
                  <div className="self-evolution-process-activity">
                    <div className="self-evolution-process-activity-head">
                      <Text>{activeCaseProgressGroup ? "Case 进度" : "关键事件"}</Text>
                      <span>{activeCaseProgressGroup ? "按 case 分页展示" : activeStageLabel}</span>
                    </div>
                    <div className="self-evolution-process-activity-list is-key" onWheel={handleActivityListWheel}>
                      {renderCaseProgressPanel()}
                    </div>
                    <details className="self-evolution-process-debug-log">
                      <summary>调试日志 · 共 {processDashboard.recentActivityTotal} 条</summary>
                      <div className="self-evolution-process-activity-list is-debug" onWheel={handleActivityListWheel}>
                        {renderActivityRows(processDashboard.recentActivities, "启动后会在这里显示 dataset、eval、analysis、repair、abtest 的实时事件。")}
                      </div>
                    </details>
                  </div>
                )}

                {isReadOnlyEnded ? (
                  <details
                    className="self-evolution-workbench-chat-feed is-ended"
                    open={isEndedChatOpen}
                    onToggle={(event) => setIsEndedChatOpen(event.currentTarget.open)}
                  >
                    <summary>
                      <span>
                        <Text>交互记录</Text>
                      </span>
                      <strong>{displayedMessages.length ? `${displayedMessages.length} 条消息` : "暂无消息"}</strong>
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
                        <Text>交互处理</Text>
                      </span>
                      {isPlanningNextStep && <em className="self-evolution-planning-pulse">正在计划下一步</em>}
                      <strong>{displayedMessages.length ? `${displayedMessages.length} 条消息` : "等待消息"}</strong>
                      <em>{isInteractionChatOpen ? "收起" : "查看详情"}</em>
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
                )}

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
                              onSend(checkpointDecisionPrompt.command);
                            }
                          }
                        }}
                      >
                        {checkpointDecisionPrompt.command || "继续执行"}
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
              </div>
          </div>
        </main>

        <aside className="self-evolution-workbench-rail" aria-label="进度与产物">
          <section className="self-evolution-rail-section">
            <div className="self-evolution-rail-section-head">
              <Text>阶段概览</Text>
              <span>{activeStageLabel}</span>
            </div>
            {renderProcessOverview()}
          </section>
          <section className="self-evolution-rail-section is-artifacts">
            <div className="self-evolution-rail-section-head">
              <Text>本次产物</Text>
              <span>点击后从右侧展开详情</span>
            </div>
            <div className="self-evolution-artifact-sidebar">
              {artifactNavigationPanel}
            </div>
          </section>
        </aside>

        {isArtifactPanelOpen && (
          <section className="self-evolution-artifact-drawer" aria-label="产物详情抽屉">
            <div className="self-evolution-artifact-drawer-head">
              <Text strong>产物详情</Text>
              <button type="button" onClick={onCloseArtifactPanel} aria-label="关闭产物详情">
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
