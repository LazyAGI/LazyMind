import { type CheckpointWaitPrompt, type EvoProcessDashboard, type NormalizedThreadEvent, type StepStatus, type ThreadEventStage, type WorkflowRuntimeState } from "./types";
import { stepStageMap } from "./constants";
import { getWorkflowStepDefinitions, t } from "./i18n";
import { getLastItem } from "./fields";
import { buildTerminalStatusByStage, dedupeNormalizedEvents, isInactiveTerminalThreadEvent } from "./threadEvents";
import { getPendingCheckpointWaitPrompt } from "./checkpoint";
import { getCompletedProgressSnapshot } from "./progress";
import { buildVisibleWorkflowSteps, createWorkflowStepFromRuntime } from "./runtimeState";
import { buildEventActivity, getStageLogicalTaskCount, isCutoverActivity, isCutoverCompletedEvent, shouldShowProcessActivity, stageProgressFromEvents } from "./dashboardActivity";
import { buildCaseProgressGroups } from "./caseProgress";

export function buildEvoProcessDashboard(
  events: NormalizedThreadEvent[],
  runtimeState: WorkflowRuntimeState,
  includeFirstStep: boolean,
  terminalStepStatus?: StepStatus,
  threadStepStatusByStage?: Partial<Record<ThreadEventStage, StepStatus>>,
  checkpointOverride?: CheckpointWaitPrompt,
): EvoProcessDashboard {
  const sortedEvents = dedupeNormalizedEvents(events);
  const cutoverCompleted = sortedEvents.some(isCutoverCompletedEvent);
  const hasInactiveTerminalEvent = sortedEvents.some(isInactiveTerminalThreadEvent);
  const terminalStatusByStage = buildTerminalStatusByStage(sortedEvents);
  const checkpoint = cutoverCompleted || hasInactiveTerminalEvent || terminalStepStatus
    ? undefined
    : checkpointOverride || getPendingCheckpointWaitPrompt(sortedEvents);
  const visibleStepsById = new Map(
    buildVisibleWorkflowSteps(sortedEvents, runtimeState, includeFirstStep, terminalStepStatus)
      .map((step) => [step.id, step]),
  );
  const runtimeSteps = getWorkflowStepDefinitions().map((definition) =>
    visibleStepsById.get(definition.id) || createWorkflowStepFromRuntime(definition.id, runtimeState),
  );
  const hasStageEvents = sortedEvents.some((event) => event.stage);
  const overview = runtimeSteps.map((step) => {
    const stage = stepStageMap[step.id];
    const stageEvents = sortedEvents.filter((event) => event.stage === stage);
    const status: StepStatus = cutoverCompleted
      ? "done"
      : terminalStatusByStage[stage]
      ?? (checkpoint?.completedStage === stage
      ? "done"
      : threadStepStatusByStage?.[stage]
      ?? (includeFirstStep && !hasStageEvents && step.id === "dataset"
        ? "running"
        : step.status));
    const resolvedStatus =
      status === "paused" && checkpoint?.completedStage === stage ? "done" : status;
    const resolvedProgress = cutoverCompleted
      ? { ...getCompletedProgressSnapshot(), statusText: stage === "abtest" ? t("selfEvolutionRun.cutoverCompleted") : t("selfEvolutionRun.statusCompleted") }
      : resolvedStatus === "failed"
      ? { statusText: t("selfEvolutionRun.statusFailed"), percent: step.progress?.percent ?? 0 }
      : resolvedStatus === "canceled"
      ? { statusText: t("selfEvolutionRun.statusCanceled"), percent: step.progress?.percent ?? 0 }
      : checkpoint?.completedStage === stage
      ? { ...getCompletedProgressSnapshot(), statusText: t("selfEvolutionRun.statusCompleted") }
      : step.progress || stageProgressFromEvents(sortedEvents, stage);
    return {
      step: {
        ...step,
        status: resolvedStatus,
        progress: resolvedProgress,
      },
      stage,
      eventCount: getStageLogicalTaskCount(stageEvents, stage),
      latestActivity: stageEvents.length ? buildEventActivity(stageEvents[stageEvents.length - 1]) : undefined,
    };
  });
  const visibleActivityEvents = sortedEvents.filter(shouldShowProcessActivity);
  const activities = visibleActivityEvents.map(buildEventActivity);
  const caseProgressGroups = buildCaseProgressGroups(sortedEvents);
  const latestStage = cutoverCompleted ? "abtest" : checkpoint?.completedStage || getLastItem(visibleActivityEvents.filter((event) => event.stage))?.stage;
  const latestActiveOverview = getLastItem(
    overview.filter((item) => ["running", "failed", "canceled"].includes(item.step.status)),
  );
  const activeOverview =
    latestActiveOverview ||
    (latestStage ? overview.find((item) => item.stage === latestStage) : undefined) ||
    overview.find((item) => item.step.status === "pending") ||
    getLastItem(overview);
  const recentActivities = activities.slice().reverse();
  const cutoverActivities = activities.filter(isCutoverActivity).slice(-3).reverse();
  return {
    overview,
    activeStage: activeOverview?.stage,
    activeStep: activeOverview?.step,
    activeProgress: activeOverview?.step.progress,
    activeProgressPhases: activeOverview?.step.progressPhases,
    recentActivities,
    recentActivityTotal: visibleActivityEvents.length,
    checkpoint,
    cutoverActivities,
    cutoverCompleted,
    caseProgressGroups,
  };
}
