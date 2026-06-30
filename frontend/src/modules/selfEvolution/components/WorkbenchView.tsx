import { useEffect, useState } from "react";
import { Typography } from "antd";
import { useTranslation } from "react-i18next";
import { CloseOutlined, DownOutlined, EyeOutlined } from "@ant-design/icons";
import {
  ChatComposer,
  ChatMessageStream,
  HistorySessionModal,
  NewSessionConfigModal,
} from ".";
import { FinalResultCard } from "./workbench/FinalResultCard";
import { CutoverDecisionCard } from "./workbench/CutoverDecisionCard";
import { ProcessActivitySection } from "./workbench/ProcessActivitySection";
import { WorkbenchSidebar } from "./workbench/WorkbenchSidebar";
import type { SelfEvolutionWorkbenchViewProps } from "./workbench/types";

export type {
  SelfEvolutionFinalResultSummary,
  SelfEvolutionObservationKind,
  SelfEvolutionWorkbenchViewProps,
} from "./workbench/types";

const { Paragraph, Text, Title } = Typography;

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
  const latestUserMessageIndex = displayedMessages.reduce((latestIndex, item, index) => item.role === "user" ? index : latestIndex, -1);
  const latestDialogueMessages = latestUserMessageIndex >= 0 ? displayedMessages.slice(latestUserMessageIndex) : displayedMessages.slice(-1);
  const visibleInteractionMessages = isInteractionChatOpen ? displayedMessages : latestDialogueMessages;
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
  const keyActivities = processDashboard.recentActivities
    .filter((item) => item.artifactKind || item.artifactId || item.stage || ["checkpoint", "auto", "error", "message", "progress"].includes(item.tone))
    .slice(0, 16);
  const visibleKeyActivities = keyActivities.length ? keyActivities : processDashboard.recentActivities.slice(0, 16);
  const selectedStageActivities = displayStage ? processDashboard.recentActivities.filter((item) => item.stage === displayStage).slice(0, 16) : visibleKeyActivities;
  const activeCaseProgressGroup = processDashboard.caseProgressGroups.find((group) => group.stage === displayStage);
  const isReadOnlyEnded = Boolean(!checkpointDecisionPrompt && processDashboard.overview.every((item) => item.step.status === "done"));
  const shouldShowFinalResultCard = isReadOnlyEnded && !selectedViewStage;
  const shouldShowStageDetail = !isReadOnlyEnded || Boolean(selectedViewStage);
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
        <WorkbenchSidebar
          activeStepText={activeStepText}
          routeThreadId={routeThreadId}
          isRestoringThread={isRestoringThread}
          threadRestoreError={threadRestoreError}
          activeWorkbenchTab={activeWorkbenchTab}
          activeStageLabel={activeStageLabel}
          activeSession={activeSession}
          displayedMessages={displayedMessages}
          chatSessionsCount={chatSessionsCount}
          artifactNavigationPanel={artifactNavigationPanel}
          isArtifactPanelOpen={isArtifactPanelOpen}
          onCloseArtifactPanel={onCloseArtifactPanel}
          onWorkbenchTabChange={onWorkbenchTabChange}
          onRetryRestoreThread={onRetryRestoreThread}
          onOpenHistorySessionModal={onOpenHistorySessionModal}
          onCloseSession={onCloseSession}
          onCreateSession={onCreateSession}
          onMessageAnchorClick={handleMessageAnchorClick}
        />

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
                    <CutoverDecisionCard
                      processDashboard={processDashboard}
                      checkpointDecisionPrompt={checkpointDecisionPrompt}
                      cutoverDecisionEvidence={cutoverDecisionEvidence}
                      isSendingMessage={isSendingMessage}
                      onSend={onSend}
                      onOpenArtifact={onOpenArtifact}
                    />
                  )}
                </div>

                {displayStage === "abtest" && abtestPreviewPanel && (
                  <div className="self-evolution-abtest-stage-panel">
                    {abtestPreviewPanel}
                  </div>
                )}

                {shouldShowFinalResultCard && (
                  <FinalResultCard finalResultSummary={finalResultSummary} onOpenArtifact={onOpenArtifact} />
                )}

                {shouldShowStageDetail && (
                  <ProcessActivitySection
                    processDashboard={processDashboard}
                    activeCaseProgressGroup={activeCaseProgressGroup}
                    selectedStageActivities={selectedStageActivities}
                    visibleKeyActivities={visibleKeyActivities}
                    activeStageLabel={activeStageLabel}
                    getStepStatusLabel={getStepStatusLabel}
                    onOpenArtifact={onOpenArtifact}
                    onOpenCaseArtifact={onOpenCaseArtifact}
                  />
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
