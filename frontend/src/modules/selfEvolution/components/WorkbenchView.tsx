import { type MouseEvent, type ReactNode, type Ref } from "react";
import { Typography } from "antd";
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
  type SelfEvolutionCheckpointPrompt,
  type SelfEvolutionHistoryEntry,
  type SelfEvolutionLaunchOptionCard,
  type SelfEvolutionSummaryItem,
  type SelfEvolutionWorkbenchTab,
} from "./types";
import { type EvoProcessDashboard, type WorkflowResultKind, type WorkflowStep as SelfEvolutionRuntimeWorkflowStep } from "../shared";

const { Paragraph, Text, Title } = Typography;

type SelfEvolutionSessionSummary = {
  id: string;
  title: string;
};

export type SelfEvolutionWorkbenchViewProps = {
  processDashboard: EvoProcessDashboard;
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
  chatStreamRef: Ref<HTMLDivElement>;
  isAutoMode: boolean;
  isAutoInteractionActive: boolean;
  isSendingMessage: boolean;
  displayedCheckpointWaitPrompt?: SelfEvolutionCheckpointPrompt;
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
  onOpenArtifact: (kind: WorkflowResultKind) => void;
  onWorkbenchTabChange: (tab?: SelfEvolutionWorkbenchTab) => void;
  onCloseArtifactPanel: () => void;
  onCloseHistorySessionModal: () => void;
  onRetryThreadHistoryList: () => void;
  onCancelCreateSession: () => void;
  onConfirmCreateSession: () => void;
};

export function SelfEvolutionWorkbenchView({
  processDashboard,
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
  onOpenArtifact,
  onWorkbenchTabChange,
  onCloseArtifactPanel,
  onCloseHistorySessionModal,
  onRetryThreadHistoryList,
  onCancelCreateSession,
  onConfirmCreateSession,
}: SelfEvolutionWorkbenchViewProps) {
  const { t } = useTranslation();
  const activeStageLabel =
    processDashboard.activeStage
      ? processDashboard.overview.find((item) => item.stage === processDashboard.activeStage)?.step.title
      : activeStepText;
  const activeActivity = processDashboard.activeStage
    ? processDashboard.overview.find((item) => item.stage === processDashboard.activeStage)?.latestActivity
    : undefined;
  const activeProgressText = processDashboard.activeProgress
    ? `${processDashboard.activeProgress.statusText}，${processDashboard.activeProgress.percent}%`
    : processDashboard.checkpoint
      ? processDashboard.checkpoint.message
      : activeActivity
        ? `${activeActivity.detail}${activeActivity.time ? ` · ${activeActivity.time}` : ""}`
        : "等待后端事件刷新。";
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
      <strong>{activeSession.title}</strong>
      <span>{routeThreadId ? `线程 ${routeThreadId}` : "本地会话"}</span>
      <span>{displayedMessages.length ? `${displayedMessages.length} 条消息` : "等待消息"}</span>
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
      {renderSidebarSection("artifacts", "产物内容", "查看 Step 1-5 的阶段产物", artifactNavigationPanel)}
      {renderSidebarSection("processes", "历史对话", "查看和切换所有自进化对话", renderHistoryNavigationPanel())}
      {renderSidebarSection("messages", "交互处理", "当前会话与消息入口", renderMessagesNavigationPanel())}
    </div>
  );
  return (
    <div className="self-evolution-session-page">
      <div className="self-evolution-workbench">
        <section
          className="self-evolution-workflow-panel"
          aria-label={t("selfEvolutionRun.executionStepsAria")}
          onClick={isArtifactPanelOpen ? onCloseArtifactPanel : undefined}
        >
          <div className="self-evolution-workflow-head">
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

          <div className="self-evolution-step-list">
            <div className="self-evolution-step-scroll">
              <div className="self-evolution-process-board" aria-label="evo 全流程进度">
                <div className="self-evolution-process-overview">
                  {processDashboard.overview.map((item) => {
                    const hasStepProgress = typeof item.step.progress?.percent === "number";
                    const isStepIndeterminate = !hasStepProgress && item.step.status === "running";
                    const stepProgressWidth = hasStepProgress
                      ? item.step.progress?.percent ?? 0
                      : item.step.status === "done"
                        ? 100
                        : 0;
                    const stepTrackClass = isStepIndeterminate
                      ? "is-indeterminate"
                      : stepProgressWidth === 0
                        ? "is-zero"
                        : undefined;
                    return (
                      <div
                        key={item.step.id}
                        className={`self-evolution-process-step is-${item.step.status}${processDashboard.activeStage === item.stage ? " is-active" : ""}`}
                      >
                        <div className="self-evolution-process-step-head">
                          <span className="self-evolution-process-step-icon">
                            {item.step.status === "done" && <CheckCircleFilled />}
                            {(item.step.status === "running" || item.step.status === "paused") && <ClockCircleFilled />}
                            {item.step.status === "pending" && <FileTextOutlined />}
                            {(item.step.status === "failed" || item.step.status === "canceled") && <CloseOutlined />}
                          </span>
                          <span className="self-evolution-process-step-title">{item.step.title.replace(/^Step\s+\d+\s+·\s+/, "")}</span>
                        </div>
                        <div className="self-evolution-process-step-track">
                          <span
                            className={stepTrackClass}
                            style={{ width: `${stepProgressWidth}%` }}
                          />
                        </div>
                        <div className="self-evolution-process-step-meta">
                          <span>{getStepStatusLabel(item.step.status)}</span>
                          <strong>{item.eventCount ? `${item.eventCount} 个事件` : "等待事件"}</strong>
                        </div>
                      </div>
                    );
                  })}
                </div>

                <div className="self-evolution-process-live">
                  <div className="self-evolution-process-live-main">
                    <Text className="self-evolution-process-live-kicker">当前阶段</Text>
                    <Title level={4}>{activeStageLabel}</Title>
                    <Paragraph>{activeProgressText}</Paragraph>
                    <div className="self-evolution-process-live-track">
                      <span
                        className={!processDashboard.activeProgress && !processDashboard.checkpoint ? "is-indeterminate" : undefined}
                        style={{
                          width: `${processDashboard.activeProgress?.percent ?? (processDashboard.checkpoint ? 100 : 0)}%`,
                        }}
                      />
                    </div>
                    {processDashboard.activeProgressPhases?.length ? (
                      <div className="self-evolution-process-phase-list">
                        {processDashboard.activeProgressPhases.map((phase) => (
                          <div key={phase.id} className="self-evolution-process-phase">
                            <div>
                              <strong>{phase.title}</strong>
                              <span>{phase.statusText}</span>
                            </div>
                            <em>{phase.percent}%</em>
                            <span className="self-evolution-process-phase-track">
                              <i style={{ width: `${phase.percent}%` }} />
                            </span>
                          </div>
                        ))}
                      </div>
                    ) : null}
                  </div>
                </div>

                {(processDashboard.opencodeActivities.length > 0 || processDashboard.cutoverActivities.length > 0) && (
                  <div className="self-evolution-process-focus-grid">
                    {processDashboard.opencodeActivities.length > 0 && (
                      <div className="self-evolution-process-focus">
                        <Text>opencode / repair</Text>
                        <div className="self-evolution-process-focus-list">
                          {processDashboard.opencodeActivities.map((item) => (
                            <p key={item.key}>
                              <strong>{item.title}</strong>
                              <span>{item.detail}</span>
                            </p>
                          ))}
                        </div>
                      </div>
                    )}
                    {processDashboard.cutoverActivities.length > 0 && (
                      <div className="self-evolution-process-focus">
                        <Text>ABTest / 切流</Text>
                        <div className="self-evolution-process-focus-list">
                          {processDashboard.cutoverActivities.map((item) => (
                            <p key={item.key}>
                              <strong>{item.title}</strong>
                              <span>{item.detail}</span>
                            </p>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                )}

                <div className="self-evolution-process-activity">
                  <div className="self-evolution-process-activity-head">
                    <Text>运行事件流</Text>
                    <span>
                      {processDashboard.recentActivities.length
                        ? `共 ${processDashboard.recentActivityTotal} 条`
                        : "暂无事件"}
                    </span>
                  </div>
                  <div className="self-evolution-process-activity-list">
                    {processDashboard.recentActivities.length === 0 ? (
                      <Paragraph className="self-evolution-process-activity-empty">
                        启动后会在这里显示 dataset、eval、analysis、repair、abtest 的实时事件。
                      </Paragraph>
                    ) : (
                      processDashboard.recentActivities.map((item) => (
                        <div key={item.key} className={`self-evolution-process-activity-row is-${item.tone}`}>
                          <span className="self-evolution-process-activity-dot" />
                          <div>
                            <div className="self-evolution-process-activity-title">
                              <strong>{item.title}</strong>
                              <span>{item.time}</span>
                            </div>
                            <Paragraph>{item.detail}</Paragraph>
                            {item.artifactKind && (
                              <button
                                type="button"
                                className="self-evolution-process-activity-action"
                                onClick={(event) => {
                                  event.stopPropagation();
                                  onOpenArtifact(item.artifactKind!);
                                }}
                              >
                                {item.artifactLabel || "查看产物"}
                              </button>
                            )}
                          </div>
                        </div>
                      ))
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section className="self-evolution-chat-panel" aria-label={t("selfEvolutionRun.historyWindowAria")}>
          <aside className="self-evolution-workbench-sidebar" aria-label="自进化导航面板">
            {renderWorkbenchNavigationPanel()}
            <div className="self-evolution-workbench-sidebar-composer">
              <ChatComposer
                activeStepText={activeStepText}
                isAutoMode={isAutoMode}
                isSendingMessage={isSendingMessage}
                pendingCheckpointWaitPrompt={displayedCheckpointWaitPrompt}
                prompt={prompt}
                onPromptChange={onPromptChange}
                onSend={onSend}
                renderKnowledgeAndModeTools={renderKnowledgeAndModeTools}
                renderSendButton={renderSendButton}
              />
            </div>
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

          <div className="self-evolution-workbench-main">
            <div className="self-evolution-workbench-tab-body">
              {isArtifactPanelOpen ? (
                artifactPanel
              ) : (
                <ChatMessageStream
                  isAutoInteractionActive={isAutoInteractionActive}
                  messages={displayedMessages}
                  streamRef={chatStreamRef}
                />
              )}
            </div>
          </div>
        </section>

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
