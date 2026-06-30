import { type EvoProcessDashboard, type NormalizedThreadEvent, type StepStatus, type WorkflowRuntimeState } from "./types";
import { stepStageMap } from "./constants";
import { getWorkflowStepDefinitions, t } from "./i18n";
import { getLastItem } from "./fields";
import { dedupeNormalizedEvents, isInactiveTerminalThreadEvent } from "./threadEvents";
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
): EvoProcessDashboard {
  const sortedEvents = dedupeNormalizedEvents(events);
  const cutoverCompleted = sortedEvents.some(isCutoverCompletedEvent);
  const hasInactiveTerminalEvent = sortedEvents.some(isInactiveTerminalThreadEvent);
  const checkpoint = cutoverCompleted || hasInactiveTerminalEvent || terminalStepStatus
    ? undefined
    : getPendingCheckpointWaitPrompt(sortedEvents);
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
      : checkpoint?.completedStage === stage
      ? "paused"
      : includeFirstStep && !hasStageEvents && step.id === "dataset"
        ? "running"
        : step.status;
    return {
      step: {
        ...step,
        status,
        progress: cutoverCompleted
          ? { ...getCompletedProgressSnapshot(), statusText: stage === "abtest" ? t("selfEvolutionRun.cutoverCompleted") : t("selfEvolutionRun.statusCompleted") }
          : step.progress || stageProgressFromEvents(sortedEvents, stage),
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
  const activeOverview =
    (latestStage ? overview.find((item) => item.stage === latestStage) : undefined) ||
    overview.find((item) => ["running", "paused", "failed", "canceled"].includes(item.step.status)) ||
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
