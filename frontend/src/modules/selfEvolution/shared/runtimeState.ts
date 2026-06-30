import { type CheckpointWaitPrompt, type EvolutionMode, type NormalizedThreadEvent, type StepStatus, type WorkflowProgressPhaseSnapshot, type WorkflowProgressSnapshot, type WorkflowResultsState, type WorkflowRuntimeState, type WorkflowStep, type WorkflowStepId } from "./types";
import { stageStepMap, workflowStepOrder } from "./constants";
import { getWorkflowStepDefinitions, t } from "./i18n";
import { getLastItem, getOperationRunId } from "./fields";
import { dedupeNormalizedEvents } from "./threadEvents";
import { getCompletedEvalProgressPhases, getCompletedProgressSnapshot, getEvalOverallProgressSnapshot, getRuntimeProgressStatusLabel, isIntentSidecarOperation, isStepFinishEvent, mergeProgressSnapshot, updateEvalProgressPhases, updateProgressStatusText } from "./progress";

export function createInitialWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "running" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

export function createThreadRestoreWorkflowRuntimeState(): WorkflowRuntimeState {
  return {
    dataset: { status: "pending" },
    "px-report": { status: "pending" },
    analysis: { status: "pending" },
    "code-optimize": { status: "pending" },
    "ab-test": { status: "pending" },
  };
}

export function createCheckpointRestoreWorkflowRuntimeState(checkpoint: CheckpointWaitPrompt | undefined): WorkflowRuntimeState {
  const state = createThreadRestoreWorkflowRuntimeState();
  if (!checkpoint?.completedStage) {
    return state;
  }

  const currentStepId = stageStepMap[checkpoint.completedStage];
  const currentStepIndex = getWorkflowStepIndex(currentStepId);
  workflowStepOrder.forEach((stepId, index) => {
    if (index < currentStepIndex) {
      state[stepId] = { status: "done", progress: getCompletedProgressSnapshot() };
    }
  });

  state[currentStepId] = {
    status: "paused",
    runtimeText: checkpoint.message,
    progress: getCompletedProgressSnapshot(),
  };
  if (currentStepId === "px-report") {
    const progressPhases = getCompletedEvalProgressPhases();
    state[currentStepId] = {
      ...state[currentStepId],
      progress: getEvalOverallProgressSnapshot(progressPhases),
      progressPhases,
    };
  }
  return state;
}

export function createWorkflowRuntimeStateForMode(mode: EvolutionMode): WorkflowRuntimeState {
  return mode === "auto" ? createInitialWorkflowRuntimeState() : createThreadRestoreWorkflowRuntimeState();
}

export function createInitialWorkflowResultsState(): WorkflowResultsState {
  return {
    datasets: { loading: false, loaded: false },
    "eval-reports": { loading: false, loaded: false },
    "analysis-reports": { loading: false, loaded: false },
    diffs: { loading: false, loaded: false },
    abtests: { loading: false, loaded: false },
  };
}

export function getStepStatusLabel(status: StepStatus) {
  if (status === "running") {
    return t("selfEvolutionRun.statusRunning");
  }
  if (status === "done") {
    return t("selfEvolutionRun.statusDone");
  }
  if (status === "paused") {
    return t("selfEvolutionRun.statusPaused");
  }
  if (status === "canceled") {
    return t("selfEvolutionRun.statusCanceled");
  }
  if (status === "failed") {
    return t("selfEvolutionRun.statusFailed");
  }
  return t("selfEvolutionRun.statusPending");
}

export function getTerminalFlowStepStatus(status?: string): StepStatus | undefined {
  const normalizedStatus = status?.trim().toLowerCase();
  if (!normalizedStatus) {
    return undefined;
  }
  if (["cancel", "cancelled", "canceled"].includes(normalizedStatus)) {
    return "canceled";
  }
  if (["error", "failed"].includes(normalizedStatus)) {
    return "failed";
  }
  if (["completed", "done", "ended", "success", "succeeded"].includes(normalizedStatus)) {
    return "done";
  }
  return undefined;
}

export function getWorkflowStepIndex(stepId: WorkflowStepId | undefined) {
  if (!stepId) {
    return -1;
  }
  return workflowStepOrder.indexOf(stepId);
}

export function createWorkflowStepFromRuntime(
  stepId: WorkflowStepId,
  runtimeState: WorkflowRuntimeState,
  renderKey = stepId,
): WorkflowStep {
  const _workflowStepDefinitions = getWorkflowStepDefinitions();
  const definition = _workflowStepDefinitions.find((step) => step.id === stepId) || _workflowStepDefinitions[0];
  const runtime = runtimeState[stepId];
  return {
    ...definition,
    renderKey,
    status: runtime.status,
    runtimeText: runtime.runtimeText,
    progress: runtime.progress || (stepId === "px-report" ? getEvalOverallProgressSnapshot(runtime.progressPhases) : undefined),
    progressPhases: runtime.progressPhases,
  };
}

export function getTerminalFlowRuntimeText(): Partial<Record<StepStatus, string>> {
  return {
    canceled: t("selfEvolutionRun.flowCanceled"),
    done: t("selfEvolutionRun.flowDone"),
    failed: t("selfEvolutionRun.flowFailed"),
  };
}

export function getTerminalOverrideStepIndex(steps: WorkflowStep[]) {
  for (let index = steps.length - 1; index >= 0; index -= 1) {
    if (["running", "paused", "failed", "canceled"].includes(steps[index].status)) {
      return index;
    }
  }
  for (let index = 0; index < steps.length; index += 1) {
    if (steps[index].status === "pending") {
      return index;
    }
  }
  return steps.length > 0 ? steps.length - 1 : -1;
}

export function applyTerminalFlowStepStatus(
  steps: WorkflowStep[],
  terminalStepStatus?: StepStatus,
) {
  if (!terminalStepStatus || steps.length === 0) {
    return steps;
  }
  const terminalStepIndex = getTerminalOverrideStepIndex(steps);
  if (terminalStepIndex < 0) {
    return steps;
  }
  return steps.map((step, index) =>
    index === terminalStepIndex
      ? {
          ...step,
          status: terminalStepStatus,
          runtimeText: getTerminalFlowRuntimeText()[terminalStepStatus] || step.runtimeText,
          progress: terminalStepStatus === "done"
            ? step.progress || getCompletedProgressSnapshot()
            : step.progress,
        }
      : step,
  );
}

export function buildWorkflowStepRuntimeFromEvents(events: NormalizedThreadEvent[], isSuperseded: boolean) {
  const snapshot: {
    status: StepStatus;
    runtimeText?: string;
    progress?: WorkflowProgressSnapshot;
    progressPhases?: WorkflowProgressPhaseSnapshot[];
  } = {
    status: "running",
  };

  events.forEach((event) => {
    if (snapshot.status === "done" && isIntentSidecarOperation(event)) {
      return;
    }

    if (event.stage === "eval") {
      snapshot.progressPhases = updateEvalProgressPhases(
        snapshot.progressPhases,
        event.progressPhase,
        event.progress,
        event.action,
        Boolean(getOperationRunId(event.payload)),
      );
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    }

    const isFinished = isStepFinishEvent(event);

    if (isFinished) {
      snapshot.status = "done";
      if (event.stage === "eval") {
        snapshot.progressPhases = getCompletedEvalProgressPhases();
        snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
      } else {
        snapshot.progress = event.progress || getCompletedProgressSnapshot();
      }
    } else if (event.action === "cancel") {
      snapshot.status = "canceled";
    } else if (event.action === "failed") {
      snapshot.status = "failed";
    } else if (event.action === "pause") {
      snapshot.status = "paused";
      if (event.stage !== "eval") {
        snapshot.progress = mergeProgressSnapshot(
          snapshot.progress,
          event.progress || updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action)),
        );
      }
    } else {
      snapshot.status = "running";
      if (event.stage !== "eval") {
        snapshot.progress = mergeProgressSnapshot(
          snapshot.progress,
          event.progress || updateProgressStatusText(snapshot.progress, getRuntimeProgressStatusLabel(event.action)),
        );
      }
    }
    snapshot.runtimeText = event.progress ? undefined : event.displayText;
  });

  if (isSuperseded && snapshot.status === "running") {
    snapshot.status = "done";
    if (snapshot.progressPhases) {
      snapshot.progressPhases = getCompletedEvalProgressPhases();
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    } else {
      snapshot.progress = getCompletedProgressSnapshot();
    }
  }

  if (snapshot.status === "done") {
    if (snapshot.progressPhases) {
      snapshot.progressPhases = getCompletedEvalProgressPhases();
      snapshot.progress = getEvalOverallProgressSnapshot(snapshot.progressPhases);
    } else {
      snapshot.progress = getCompletedProgressSnapshot();
    }
  }

  return snapshot;
}

export function buildVisibleWorkflowSteps(
  events: NormalizedThreadEvent[],
  runtimeState: WorkflowRuntimeState,
  includeFirstStep: boolean,
  terminalStepStatus?: StepStatus,
): WorkflowStep[] {
  const stageEvents = dedupeNormalizedEvents(events).filter((event) => event.stage);
  if (stageEvents.length === 0) {
    return applyTerminalFlowStepStatus(
      includeFirstStep ? [createWorkflowStepFromRuntime("dataset", runtimeState)] : [],
      terminalStepStatus,
    );
  }

  const groups: Array<{ stepId: WorkflowStepId; events: NormalizedThreadEvent[] }> = [];
  stageEvents.forEach((event) => {
    if (!event.stage) {
      return;
    }
    const stepId = stageStepMap[event.stage];
    const latestGroup = getLastItem(groups);
    if (latestGroup?.stepId === stepId) {
      latestGroup.events.push(event);
      return;
    }
    groups.push({ stepId, events: [event] });
  });

  const steps = groups.map((group, index) => {
    const _wsd = getWorkflowStepDefinitions();
    const definition = _wsd.find((step) => step.id === group.stepId) || _wsd[0];
    return {
      ...definition,
      renderKey: `${group.stepId}-${index}`,
      ...buildWorkflowStepRuntimeFromEvents(group.events, index < groups.length - 1),
    };
  });
  return applyTerminalFlowStepStatus(steps, terminalStepStatus);
}

export function reduceWorkflowRuntimeState(
  prev: WorkflowRuntimeState,
  event: NormalizedThreadEvent,
): WorkflowRuntimeState {
  if (!event.stage) {
    return prev;
  }

  const stepId = stageStepMap[event.stage];
  const stepIndex = getWorkflowStepIndex(stepId);
  const action = event.action;
  const next: WorkflowRuntimeState = { ...prev };
  getWorkflowStepDefinitions().forEach((step, index) => {
    next[step.id] = { ...prev[step.id] };
    if (index < stepIndex && next[step.id].status === "pending") {
      next[step.id].status = "done";
    }
  });

  const current = next[stepId];
  if (current.status === "done" && isIntentSidecarOperation(event)) {
    return next;
  }

  if (event.stage === "eval") {
    current.progressPhases = updateEvalProgressPhases(
      current.progressPhases,
      event.progressPhase,
      event.progress,
      action,
      Boolean(getOperationRunId(event.payload)),
    );
    current.progress = getEvalOverallProgressSnapshot(current.progressPhases);
  }

  const isFinished = isStepFinishEvent(event);

  if (isFinished) {
    current.status = "done";
    if (event.stage === "eval") {
      current.progressPhases = getCompletedEvalProgressPhases();
      current.progress = getEvalOverallProgressSnapshot(current.progressPhases);
    } else {
      current.progress = event.progress || getCompletedProgressSnapshot();
    }
  } else if (action === "cancel") {
    current.status = "canceled";
  } else if (action === "failed") {
    current.status = "failed";
  } else if (action === "pause") {
    current.status = "paused";
    if (event.stage !== "eval") {
      current.progress = mergeProgressSnapshot(
        current.progress,
        event.progress || updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action)),
      );
    }
  } else {
    current.status = "running";
    if (event.stage !== "eval") {
      current.progress = mergeProgressSnapshot(
        current.progress,
        event.progress || updateProgressStatusText(current.progress, getRuntimeProgressStatusLabel(action)),
      );
    }
  }
  current.runtimeText = event.progress ? undefined : event.displayText;
  return next;
}

export function reduceWorkflowRuntimeStateFromEvents(
  prev: WorkflowRuntimeState,
  events: NormalizedThreadEvent[],
): WorkflowRuntimeState {
  return dedupeNormalizedEvents(events).reduce(reduceWorkflowRuntimeState, prev);
}
