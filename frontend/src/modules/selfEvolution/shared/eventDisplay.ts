import { t } from "./i18n";
import { getEventArtifactId, getEventCaseId, getEventCaseProgress, getEventDetailField, getEventFlowKind, getEventPayloadData, getNestedRecordField, getNumberField, getOperationRunId, getPayloadCaseTotal, getStringField, getStructuredArrayField, getStructuredRecordField, isRecord } from "./fields";
import { formatAnalysisAgentName, formatAnalysisVerdict } from "./format";
import { getDatasetOperationSegments, getEvalPayloadPhase, getEvalPhaseLabel, getEvalPhasePayloadData, isActionKind } from "./progress";
import { formatOperationRunId } from "./dashboardActivity";

export function buildAnalysisEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);

  if (action === "start") {
    return t("selfEvolutionRun.analysisStarted");
  }

  if (type === "run.indexer.result") {
    const resultRecord = getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
    const hypotheses = getStructuredArrayField(resultRecord, ["hypotheses"]) || [];
    return hypotheses.length > 0
      ? t("selfEvolutionRun.analysisHypothesesGenerated", { count: hypotheses.length })
      : t("selfEvolutionRun.analysisFirstScanDone");
  }

  if (type === "run.conductor.result") {
    const resultRecord = getNestedRecordField(eventData, ["result"]) || getStructuredRecordField(eventData, ["summary"]);
    const iteration = getNumberField(eventData, ["iteration"]) ?? getNumberField(resultRecord, ["iterations"]);
    const converged = resultRecord?.converged === true;
    const totalActions = getNumberField(resultRecord, ["total_actions"]);
    if (converged) {
      const actionText = typeof totalActions === "number" ? t("selfEvolutionRun.analysisConvergedActions", { count: totalActions }) : "";
      return t("selfEvolutionRun.analysisConverged", { iterations: iteration || 0, actionText });
    }
    if (typeof iteration === "number" && iteration > 0) {
      return t("selfEvolutionRun.analysisIterationDone", { iteration });
    }
    return t("selfEvolutionRun.analysisConductorOrganizing");
  }

  if (type === "run.tool.used") {
    const agent = formatAnalysisAgentName(getStringField(eventData, ["agent"]));
    const tool = getStringField(eventData, ["tool"]) || t("selfEvolutionRun.genericTool");
    return t("selfEvolutionRun.analysisToolUsed", { agent, tool });
  }

  if (type === "run.researcher.result") {
    const agent = formatAnalysisAgentName(getStringField(eventData, ["agent"]));
    const resultRecord = getStructuredRecordField(eventData, ["result_summary"]);
    const hypothesisId = getStringField(resultRecord, ["hypothesis_id"]);
    const verdict = formatAnalysisVerdict(getStringField(resultRecord, ["verdict"]));
    return hypothesisId
      ? t("selfEvolutionRun.analysisResearcherConclusionWithHypothesis", { agent, hypothesisId, verdict })
      : t("selfEvolutionRun.analysisResearcherConclusion", { agent });
  }

  if (action === "finish") {
    return t("selfEvolutionRun.analysisDone");
  }

  if (action === "cancel") {
    return t("selfEvolutionRun.analysisCanceled");
  }

  if (action === "pause") {
    return t("selfEvolutionRun.analysisPaused");
  }

  return undefined;
}

export function buildApplyEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);
  const phase = getStringField(eventData, ["phase", "task", "task_type", "step", "name", "kind", "type", "event"]);
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const attempt = getNumberField(eventData, ["attempt"]) ?? getNumberField(detail, ["attempt"]);
  const failure = getStringField(detail, ["failure", "failure_summary"]);

  if (phase === "repair_loop") {
    if (isActionKind(action, "finish")) {
      const decision = getStringField(detail, ["decision"]) || t("selfEvolutionRun.repairDecisionDefault");
      return t("selfEvolutionRun.repairLoopDone", { decision });
    }
    return typeof attempt === "number" ? t("selfEvolutionRun.repairLoopAttempt", { attempt }) : t("selfEvolutionRun.repairLoopRunning");
  }

  if (phase === "opencode") {
    return typeof attempt === "number" ? t("selfEvolutionRun.opencodeAttempt", { attempt }) : t("selfEvolutionRun.opencodeGenerating");
  }

  if (phase === "repair_patch") {
    const status = isActionKind(action, "failed") ? t("selfEvolutionRun.patchFailed") : t("selfEvolutionRun.patchGenerated");
    return failure ? t("selfEvolutionRun.patchStatusWithFailure", { status, failure }) : t("selfEvolutionRun.patchStatusWaiting", { status });
  }

  if (phase === "repair_candidate_service" || phase === "candidate_service") {
    return isActionKind(action, "finish") ? t("selfEvolutionRun.candidateServiceReady") : t("selfEvolutionRun.candidateServiceStarting");
  }

  if (action === "start") {
    return t("selfEvolutionRun.repairStarted");
  }

  if (type === "apply.round.diff") {
    const round = getNumberField(eventData, ["round"]);
    const filesChanged = (getStructuredArrayField(eventData, ["files_changed"]) || []).filter(
      (item): item is string => typeof item === "string" && item.trim().length > 0,
    );
    const diffSummary = getStringField(eventData, ["diff_summary"]);
    const testsText = diffSummary?.includes("tests passed")
      ? t("selfEvolutionRun.testsPassed")
      : diffSummary?.includes("tests not run")
        ? t("selfEvolutionRun.testsNotRun")
        : diffSummary?.includes("tests failed")
          ? t("selfEvolutionRun.testsFailed")
          : "";
    const fileText =
      filesChanged.length > 0 ? t("selfEvolutionRun.filesChanged", { count: filesChanged.length }) : t("selfEvolutionRun.noFileChanges");
    return typeof round === "number"
      ? t("selfEvolutionRun.roundDiffDone", { round, fileText, testsText })
      : t("selfEvolutionRun.diffDone", { fileText, testsText });
  }

  if (action === "finish") {
    return t("selfEvolutionRun.repairDone");
  }

  if (action === "cancel") {
    return t("selfEvolutionRun.repairCanceled");
  }

  return undefined;
}

export function buildDatasetEventDisplayText(
  action: string | undefined,
  payload: Record<string, unknown> | undefined,
) {
  const eventData = getEventPayloadData(payload);
  const _datasetOperationSegments = getDatasetOperationSegments();
  const operationSegment = _datasetOperationSegments[getEventFlowKind(payload) as keyof ReturnType<typeof getDatasetOperationSegments>];
  const current = getNumberField(eventData, ["current", "completed", "done", "processed"]);
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const countText =
    typeof current === "number" && typeof total === "number" && total > 0
      ? t("selfEvolutionRun.progressCount", { current, total })
      : typeof total === "number" && total > 0
        ? t("selfEvolutionRun.totalCount", { total })
        : "";

  if (isActionKind(action, "start")) {
    return t("selfEvolutionRun.datasetStarted");
  }
  if (isActionKind(action, "finish")) {
    if (operationSegment && operationSegment.base + operationSegment.span < 100) {
      return t("selfEvolutionRun.segmentDoneWaiting", { label: operationSegment.label });
    }
    return t("selfEvolutionRun.datasetDone");
  }
  if (isActionKind(action, "cancel")) {
    return t("selfEvolutionRun.datasetCanceled");
  }
  if (isActionKind(action, "pause")) {
    return t("selfEvolutionRun.datasetPaused");
  }
  return operationSegment
    ? t("selfEvolutionRun.segmentRunningCount", { label: operationSegment.label, countText })
    : t("selfEvolutionRun.datasetRunningCount", { countText });
}

export function buildEvalEventDisplayText(
  action: string | undefined,
  type: string,
  payload: Record<string, unknown> | undefined,
) {
  const phase = getEvalPayloadPhase(action, type, payload);
  const eventData = getEvalPhasePayloadData(payload, phase);
  const phaseLabel = getEvalPhaseLabel(phase);
  const current = getNumberField(eventData, ["current", "completed", "done", "processed"]);
  const total = getNumberField(eventData, ["total", "num_cases", "cases", "count"]);
  const countText =
    typeof current === "number" && typeof total === "number" && total > 0
      ? t("selfEvolutionRun.progressCount", { current, total })
      : typeof total === "number" && total > 0
        ? t("selfEvolutionRun.totalCount", { total })
        : "";

  if (isActionKind(action, "start")) {
    return phase === "rag"
      ? t("selfEvolutionRun.evalRagStarted")
      : phase === "judge"
        ? t("selfEvolutionRun.evalJudgeStarted")
        : t("selfEvolutionRun.evalStarted");
  }

  if (isActionKind(action, "finish")) {
    return phase === "rag"
      ? t("selfEvolutionRun.evalRagDone")
      : phase === "judge"
        ? t("selfEvolutionRun.evalJudgeDone")
        : t("selfEvolutionRun.evalDone");
  }

  if (isActionKind(action, "cancel")) {
    return t("selfEvolutionRun.segmentCanceled", { label: phaseLabel });
  }

  if (isActionKind(action, "pause")) {
    return t("selfEvolutionRun.segmentPausedWaiting", { label: phaseLabel });
  }

  if (phase) {
    return t("selfEvolutionRun.segmentRunningCount", { label: phaseLabel, countText });
  }

  return undefined;
}

export function buildAbtestEventDisplayText(action: string | undefined, payload?: Record<string, unknown>) {
  const eventData = getEventPayloadData(payload);
  const flowKind = getEventFlowKind(payload);
  const operationProgress = getEventCaseProgress(payload);
  const caseTotal = getPayloadCaseTotal(eventData) || operationProgress?.total;
  const status = getStringField(eventData, ["status"]);
  const decision = getEventDetailField(payload, ["decision_status"]);
  const caseText = operationProgress?.current
    ? `，case ${operationProgress.current}${caseTotal ? `/${caseTotal}` : ""}`
    : "";
  if (flowKind === "abtest.candidate_rag_answer" && getEventCaseId(payload)) {
    return t("selfEvolutionRun.abtestCandidateGenerating", { caseText });
  }
  if (flowKind === "abtest.candidate_judge" && getEventCaseId(payload)) {
    return t("selfEvolutionRun.abtestCandidateEvaluating", { caseText });
  }
  if (flowKind === "eval.aggregate" || getEventArtifactId(payload) === "candidate_eval_report") {
    return isActionKind(action, "finish") ? t("selfEvolutionRun.abtestEvalReportDone") : t("selfEvolutionRun.abtestEvalReportSummarizing");
  }
  if (flowKind === "abtest.candidate_cutover") {
    return isActionKind(action, "finish") ? t("selfEvolutionRun.abtestCutoverDone") : t("selfEvolutionRun.abtestCutoverPending");
  }
  if (flowKind === "abtest.candidate_service.stop") {
    return status === "success" || isActionKind(action, "finish")
      ? t("selfEvolutionRun.abtestCandidateServiceStopped")
      : t("selfEvolutionRun.abtestCandidateServiceStopping");
  }
  if (flowKind === "abtest.candidate_service.start") {
    return isActionKind(action, "finish") ? t("selfEvolutionRun.abtestCandidateServiceReady") : t("selfEvolutionRun.abtestCandidateServiceStarting");
  }
  if (decision) {
    return decision === "accept"
      ? t("selfEvolutionRun.abtestDecisionPassed")
      : t("selfEvolutionRun.abtestDecisionFailed");
  }
  if (flowKind === "abtest.compare") {
    return isActionKind(action, "finish") ? t("selfEvolutionRun.abtestCompareDone") : t("selfEvolutionRun.abtestCompareRunning");
  }
  if (action === "start") {
    return t("selfEvolutionRun.abtestStarted");
  }
  if (action === "finish") {
    return t("selfEvolutionRun.abtestDone");
  }
  if (action === "cancel") {
    return t("selfEvolutionRun.abtestCanceled");
  }
  return undefined;
}

export function compactPayloadForDisplay(payload: Record<string, unknown> | undefined) {
  if (!payload) {
    return "";
  }

  const eventData = getEventPayloadData(payload);
  const status = getStringField(eventData, ["status"]);
  const phase = getStringField(eventData, ["phase", "stage", "task", "task_type", "step", "name", "kind", "type", "event"]);
  const operationRunId = getOperationRunId(payload);
  const currentItem = getStringField(eventData, ["current_item", "item_ref", "case_id", "artifact_id"]);
  const detailRecord = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const metrics = [
    getNumberField(eventData, ["current", "completed", "done", "processed"]) !== undefined &&
    getNumberField(eventData, ["total", "num_cases", "cases", "count"]) !== undefined
      ? t("selfEvolutionRun.compactProgress", { current: getNumberField(eventData, ["current", "completed", "done", "processed"]), total: getNumberField(eventData, ["total", "num_cases", "cases", "count"]) })
      : "",
    getStringField(detailRecord, ["artifact_id"]) ? t("selfEvolutionRun.compactArtifact", { id: getStringField(detailRecord, ["artifact_id"]) }) : "",
    currentItem ? t("selfEvolutionRun.compactCurrentItem", { item: currentItem }) : "",
  ].filter(Boolean);
  const structured = [
    operationRunId ? formatOperationRunId(operationRunId) : phase,
    status,
    ...metrics,
  ].filter(Boolean);
  if (structured.length > 0) {
    return structured.join(" · ");
  }

  const entries = Object.entries(payload).filter(
    ([key, value]) =>
      ![
        "type",
        "event",
        "event_name",
        "kind",
        "stage",
        "action",
        "message",
        "content",
        "text",
        "reply",
        "thought",
        "seq",
        "event_id",
        "created_at",
        "checkpoint_id",
        "payload",
      ].includes(key) &&
      value !== undefined &&
      value !== null &&
      value !== "",
  );
  if (entries.length === 0) {
    return "";
  }

  return entries.slice(0, 4).map(([key, value]) => {
    if (Array.isArray(value)) {
      return t("selfEvolutionRun.compactArrayCount", { key, count: value.length });
    }
    if (isRecord(value)) {
      return t("selfEvolutionRun.compactRecordUpdated", { key });
    }
    return `${key} ${String(value).slice(0, 80)}`;
  }).join(" · ");
}
