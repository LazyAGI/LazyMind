import { type EvalPayloadPhase, type NormalizedThreadEvent, type ThreadEventStage, type WorkflowProgressPhaseId, type WorkflowProgressPhaseSnapshot, type WorkflowProgressSnapshot } from "./types";
import { t } from "./i18n";
import { getEventArtifactId, getEventCaseId, getEventCaseProgress, getEventDetailField, getEventFlowKind, getEventPayloadData, getNumberField, getOperationRunId, getPayloadCaseTotal, getStringField, isRecord } from "./fields";
import { clampPercent } from "./format";

export function getRuntimeProgressStatusLabel(action: string | undefined) {
  if (action === "finish") {
    return t("selfEvolutionRun.statusDone");
  }
  if (action === "cancel") {
    return t("selfEvolutionRun.statusCanceled");
  }
  if (action === "pause") {
    return t("selfEvolutionRun.statusPaused");
  }
  return t("selfEvolutionRun.statusRunning");
}

export function createSegmentProgressSnapshot(
  label: string,
  base: number,
  span: number,
  action: string | undefined,
  rank: number,
  current?: number,
  total?: number,
): WorkflowProgressSnapshot {
  const operationPercent =
    typeof current === "number" && typeof total === "number" && total > 0
      ? (current / total) * 100
      : typeof current === "number"
        ? 0
      : isActionKind(action, "finish")
        ? 100
        : 0;
  return {
    statusText: isActionKind(action, "finish") ? t("selfEvolutionRun.segmentDone", { label }) : t("selfEvolutionRun.segmentRunning", { label }),
    percent: clampPercent(base + (span * operationPercent) / 100),
    rank: rank + (current || 0),
  };
}

export function getAbtestWorkflowProgressSnapshot(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
): WorkflowProgressSnapshot | undefined {
  const eventData = getEventPayloadData(payload);
  const flowKind = getEventFlowKind(payload);
  const operationProgress = getEventCaseProgress(payload);
  const caseTotal = getPayloadCaseTotal(eventData) || operationProgress?.total;
  const artifactId = getEventArtifactId(payload);
  const decision = getEventDetailField(payload, ["decision_status"]);

  if (flowKind === "abtest.candidate_rag_answer" && getEventCaseId(payload)) {
    return createSegmentProgressSnapshot(t("selfEvolutionRun.segCandidateAnswer"), 8, 40, action, 100, operationProgress?.current, caseTotal);
  }
  if (flowKind === "abtest.candidate_judge" && getEventCaseId(payload)) {
    return createSegmentProgressSnapshot(t("selfEvolutionRun.segCandidateEval"), 48, 40, action, 300, operationProgress?.current, caseTotal);
  }
  if (flowKind === "eval.aggregate" || artifactId === "candidate_eval_report") {
    return createSegmentProgressSnapshot(t("selfEvolutionRun.segCandidateEvalSummary"), 88, 4, isActionKind(action, "finish") ? "progress" : action, 500);
  }
  if (flowKind === "abtest.candidate_service.start") {
    return createSegmentProgressSnapshot(t("selfEvolutionRun.segCandidateServiceStart"), 0, 8, action, 50);
  }
  if (flowKind === "abtest.candidate_service.stop") {
    return {
      statusText: isActionKind(action, "finish") ? t("selfEvolutionRun.segCandidateServiceRecycleDone") : t("selfEvolutionRun.segCandidateServiceRecycling"),
      percent: isActionKind(action, "finish") ? 100 : 98,
      rank: 750,
    };
  }
  if (decision) {
    return {
      statusText: decision.toLowerCase() === "accept" ? t("selfEvolutionRun.segCandidatePassedCutover") : t("selfEvolutionRun.segCandidateFailedCutover"),
      percent: 96,
      rank: 650,
    };
  }
  if (flowKind === "abtest.compare") {
    return createSegmentProgressSnapshot(t("selfEvolutionRun.segAbCompareDecision"), 92, 4, action, 600);
  }
  if (flowKind === "abtest.candidate_cutover" || artifactId === "candidate_algorithm_cutover") {
    return {
      statusText: isActionKind(action, "finish") ? t("selfEvolutionRun.segCandidateCutoverDone") : t("selfEvolutionRun.segWaitingCutoverConfirm"),
      percent: isActionKind(action, "finish") ? 100 : 96,
      rank: 700,
    };
  }

  return undefined;
}

export function getDatasetOperationSegments() {
  return {
    "dataset.load_corpus": { label: t("selfEvolutionRun.segLoadCorpus"), base: 0, span: 18, rank: 10 },
    "dataset.build_corpus_snapshot": { label: t("selfEvolutionRun.segBuildCorpusSnapshot"), base: 18, span: 17, rank: 20 },
    "dataset.generate_case": { label: t("selfEvolutionRun.segGenerateCase"), base: 35, span: 45, rank: 30 },
    "dataset.assemble": { label: t("selfEvolutionRun.segAssembleDataset"), base: 80, span: 20, rank: 50 },
  };
}

export function getDatasetWorkflowProgressSnapshot(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
): WorkflowProgressSnapshot | undefined {
  const eventData = getEventPayloadData(payload);
  const datasetOperationSegments = getDatasetOperationSegments();
  const segment = datasetOperationSegments[getEventFlowKind(payload) as keyof ReturnType<typeof getDatasetOperationSegments>];
  if (!segment) {
    if (isActionKind(action, "finish")) {
      return getStringField(eventData, ["stage"]) === "dataset" ? getCompletedProgressSnapshot() : undefined;
    }
    return undefined;
  }

  const operationProgress = getEventCaseProgress(payload);
  const current =
    getNumberField(eventData, ["current", "completed", "done", "processed"]) ??
    operationProgress?.current;
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const explicitPercent = getNumberField(eventData, ["percent", "percentage", "progress"]);
  const operationPercent =
    typeof explicitPercent === "number"
      ? explicitPercent
      : typeof current === "number" && typeof total === "number" && total > 0
        ? (current / total) * 100
        : isActionKind(action, "finish")
          ? 100
          : isActionKind(action, "start")
            ? 0
            : undefined;

  if (typeof operationPercent !== "number") {
    return {
      statusText: t("selfEvolutionRun.segmentRunning", { label: segment.label }),
      percent: segment.base,
      rank: segment.rank,
    };
  }

  return {
    statusText: isActionKind(action, "finish") ? t("selfEvolutionRun.segmentDone", { label: segment.label }) : t("selfEvolutionRun.segmentRunning", { label: segment.label }),
    percent: clampPercent(segment.base + (segment.span * operationPercent) / 100),
    rank: segment.rank,
  };
}

export function normalizePhaseText(value: unknown) {
  return typeof value === "string" ? value.trim().toLowerCase() : "";
}

export function isActionKind(action: string | undefined, kind: string) {
  const normalized = normalizePhaseText(action);
  return normalized === kind || normalized.endsWith(`.${kind}`);
}

export function getEvalPayloadPhase(
  action: string | undefined,
  type: string | undefined,
  payload: Record<string, unknown> | undefined,
): EvalPayloadPhase | undefined {
  const eventData = getEventPayloadData(payload);
  const candidates = [
    action,
    type,
    getStringField(eventData, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]),
    getStringField(payload, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]),
  ]
    .map(normalizePhaseText)
    .filter(Boolean);

  if (candidates.some((item) => item.includes("judge"))) {
    return "judge";
  }
  if (candidates.some((item) => item.includes("rag"))) {
    return "rag";
  }
  if (isRecord(eventData?.judge) || eventData?.judge === true) {
    return "judge";
  }
  if (isRecord(eventData?.rag) || eventData?.rag === true) {
    return "rag";
  }
  return undefined;
}

export function getEvalPhasePayloadData(payload: Record<string, unknown> | undefined, phase: EvalPayloadPhase | undefined) {
  const eventData = getEventPayloadData(payload);
  if (phase && isRecord(eventData?.[phase])) {
    return eventData[phase];
  }
  return eventData;
}

export function getEvalPhaseLabel(phase: EvalPayloadPhase | undefined) {
  if (phase === "judge") {
    return t("selfEvolutionRun.evalPhaseJudge");
  }
  if (phase === "rag") {
    return t("selfEvolutionRun.evalPhaseRag");
  }
  return t("selfEvolutionRun.evalPhaseDefault");
}

export function getEvalProgressStatusLabel(action: string | undefined, phase: EvalPayloadPhase | undefined) {
  const label = getEvalPhaseLabel(phase);
  if (isActionKind(action, "finish")) {
    return t("selfEvolutionRun.segmentDone", { label });
  }
  if (isActionKind(action, "cancel")) {
    return t("selfEvolutionRun.segmentCanceled", { label });
  }
  if (isActionKind(action, "pause")) {
    return t("selfEvolutionRun.segmentPaused", { label });
  }
  return t("selfEvolutionRun.segmentActive", { label });
}

export function getCompletedProgressSnapshot(): WorkflowProgressSnapshot {
  return {
    statusText: t("selfEvolutionRun.statusDone"),
    percent: 100,
  };
}

export function updateProgressStatusText(
  progress: WorkflowProgressSnapshot | undefined,
  statusText: string,
): WorkflowProgressSnapshot | undefined {
  return progress ? { ...progress, statusText } : progress;
}

export function mergeProgressSnapshot(
  current: WorkflowProgressSnapshot | undefined,
  next: WorkflowProgressSnapshot | undefined,
): WorkflowProgressSnapshot | undefined {
  if (!next || !current) {
    return next || current;
  }
  if ((next.rank ?? -1) < (current.rank ?? -1)) {
    return current;
  }
  return next.percent < current.percent && next.statusText === current.statusText ? current : next;
}

export const evalProgressPhaseDefinitions: Record<WorkflowProgressPhaseId, Omit<WorkflowProgressPhaseSnapshot, "statusText" | "percent">> = {
  rag: {
    id: "rag",
    title: t("selfEvolutionRun.evalPhaseRagTitle"),
    desc: t("selfEvolutionRun.evalPhaseRagDesc"),
  },
  judge: {
    id: "judge",
    title: t("selfEvolutionRun.evalPhaseJudgeTitle"),
    desc: t("selfEvolutionRun.evalPhaseJudgeDesc"),
  },
};

export function createEvalProgressPhaseSnapshot(
  phase: WorkflowProgressPhaseId,
  progress?: WorkflowProgressSnapshot,
): WorkflowProgressPhaseSnapshot {
  return {
    ...evalProgressPhaseDefinitions[phase],
    statusText: progress?.statusText || t("selfEvolutionRun.waitingToStart"),
    percent: progress?.percent ?? 0,
  };
}

export function getDefaultEvalProgressPhases(): WorkflowProgressPhaseSnapshot[] {
  return [
    createEvalProgressPhaseSnapshot("rag"),
    createEvalProgressPhaseSnapshot("judge"),
  ];
}

export function getCompletedEvalProgressPhases(): WorkflowProgressPhaseSnapshot[] {
  return [
    createEvalProgressPhaseSnapshot("rag", getCompletedProgressSnapshot()),
    createEvalProgressPhaseSnapshot("judge", getCompletedProgressSnapshot()),
  ];
}

export function getEvalOverallProgressSnapshot(phases: WorkflowProgressPhaseSnapshot[] | undefined): WorkflowProgressSnapshot | undefined {
  if (!phases?.length) {
    return undefined;
  }
  const activePhase =
    phases.find((item) => item.percent > 0 && item.percent < 100) ||
    phases.find((item) => item.percent < 100);
  return {
    statusText: phases.every((item) => item.percent >= 100) ? t("selfEvolutionRun.statusDone") : activePhase?.statusText || t("selfEvolutionRun.statusRunning"),
    percent: clampPercent(phases.reduce((sum, item) => sum + item.percent, 0) / phases.length),
  };
}

export function updateEvalProgressPhases(
  current: WorkflowProgressPhaseSnapshot[] | undefined,
  phase: WorkflowProgressPhaseId | undefined,
  progress: WorkflowProgressSnapshot | undefined,
  action: string | undefined,
  isOperationScoped = false,
): WorkflowProgressPhaseSnapshot[] {
  if (isActionKind(action, "finish") && !isOperationScoped && (!phase || phase === "judge")) {
    return getCompletedEvalProgressPhases();
  }

  const next = current?.length ? [...current] : getDefaultEvalProgressPhases();
  if (!phase) {
    return progress
      ? next.map((item) => ({
          ...item,
          statusText: progress.statusText,
          percent: progress.percent,
        }))
      : next;
  }

  const currentPhase = next.find((item) => item.id === phase);
  const progressSnapshot = progress || {
    statusText: isActionKind(action, "finish") && isOperationScoped
      ? t("selfEvolutionRun.segmentActive", { label: getEvalPhaseLabel(phase) })
      : getEvalProgressStatusLabel(action, phase),
    percent: isActionKind(action, "finish") && !isOperationScoped ? 100 : currentPhase?.percent ?? 0,
  };

  return next.map((item) => {
    if (item.id === phase) {
      return createEvalProgressPhaseSnapshot(phase, progressSnapshot);
    }

    if (phase === "judge" && item.id === "rag" && item.percent < 100) {
      return createEvalProgressPhaseSnapshot("rag", getCompletedProgressSnapshot());
    }

    return item;
  });
}

export function getWorkflowProgressSnapshot(
  stage: ThreadEventStage | undefined,
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
  type?: string,
): WorkflowProgressSnapshot | undefined {
  if (stage !== "dataset" && stage !== "eval" && stage !== "abtest") {
    return undefined;
  }

  if (stage === "dataset") {
    return getDatasetWorkflowProgressSnapshot(action, payload);
  }

  if (stage === "abtest") {
    return getAbtestWorkflowProgressSnapshot(action, payload);
  }

  const eventData = getEventPayloadData(payload);
  const evalPhase = stage === "eval" ? getEvalPayloadPhase(action, type, payload) : undefined;
  const progressData = stage === "eval" ? getEvalPhasePayloadData(payload, evalPhase) : eventData;
  const operationRunId = getOperationRunId(payload);
  const isEvalOperationScoped = stage === "eval" && Boolean(operationRunId);
  const operationProgress = getEventCaseProgress(payload);
  const current = getNumberField(progressData, ["current", "completed", "done", "processed"]) ?? operationProgress?.current;
  const total = getNumberField(progressData, ["total", "num_cases", "cases", "count"]) ?? operationProgress?.total;
  const explicitPercent = getNumberField(progressData, ["percent", "percentage", "progress"]);
  const hasProgressValue =
    typeof explicitPercent === "number" ||
    (typeof current === "number" && typeof total === "number" && total > 0);
  const percent =
    typeof explicitPercent === "number"
      ? explicitPercent
      : typeof current === "number" && typeof total === "number" && total > 0
        ? (current / total) * 100
        : isActionKind(action, "finish")
          ? isEvalOperationScoped
            ? undefined
            : 100
          : isActionKind(action, "start") && hasProgressValue
            ? 0
            : undefined;

  if (typeof percent !== "number") {
    return undefined;
  }

  const rank = operationProgress?.current ?? (getEventFlowKind(payload) === "dataset.assemble" ? current : undefined);
  return {
    statusText: rank ? t("selfEvolutionRun.statusRunning") : stage === "eval" ? getEvalProgressStatusLabel(action, evalPhase) : getRuntimeProgressStatusLabel(action),
    percent: clampPercent(percent),
    rank,
  };
}

export function isAbtestStageCompleteEvent(event: Pick<NormalizedThreadEvent, "action" | "progress" | "payload" | "stage">) {
  if (event.stage !== "abtest" || !isActionKind(event.action, "finish")) {
    return false;
  }
  return getEventArtifactId(event.payload) === "abtest_comparison" ||
    getEventArtifactId(event.payload) === "candidate_algorithm_cutover" ||
    getEventFlowKind(event.payload) === "abtest.candidate_cutover";
}

export function isIntentSidecarOperation(event: Pick<NormalizedThreadEvent, "payload">) {
  const operationRunId = getOperationRunId(event.payload) || "";
  return (
    operationRunId.startsWith("intent.") ||
    operationRunId.startsWith("dataset.assemble.intervention.")
  );
}

export function isStepFinishEvent(event: Pick<NormalizedThreadEvent, "action" | "progress" | "progressPhase" | "payload" | "stage">) {
  if (!isActionKind(event.action, "finish")) {
    return false;
  }
  if (isAbtestStageCompleteEvent(event)) {
    return true;
  }
  if (getOperationRunId(event.payload)) {
    return false;
  }
  if (event.stage === "dataset" && getStringField(getEventPayloadData(event.payload), ["stage"]) === "dataset_corpus") {
    return false;
  }
  return event.stage === "eval" ? !event.progressPhase || event.progressPhase === "judge" : true;
}
