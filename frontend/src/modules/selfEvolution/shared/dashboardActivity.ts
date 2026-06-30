import { type EvoStageActivity, type NormalizedThreadEvent, type ThreadEventStage, type WorkflowResultKind } from "./types";
import { stageResultKindMap } from "./constants";
import { getStageLabels, getWorkflowResultLabels, t } from "./i18n";
import { getEventArtifactId, getEventFlowKind, getEventPayloadData, getLastItem, getNestedRecordField, getOperationRunId, getStringField, getStructuredArrayField, getStructuredRecordField } from "./fields";
import { formatThreadTime } from "./format";
import { isTerminalThreadEvent } from "./threadEvents";
import { compactPayloadForDisplay } from "./eventDisplay";
import { isActionKind } from "./progress";

export function eventActivityTone(event: NormalizedThreadEvent): EvoStageActivity["tone"] {
  if (event.type.startsWith("autooperator.")) {
    return "auto";
  }
  if (event.type.startsWith("checkpoint.")) {
    return "checkpoint";
  }
  if (event.type.startsWith("message.") || event.type.startsWith("intent.")) {
    return "message";
  }
  if (event.action === "failed") {
    return "error";
  }
  return event.progress ? "progress" : "normal";
}

export function eventActivityTitle(event: NormalizedThreadEvent) {
  if (event.type.startsWith("autooperator.")) {
    return t("selfEvolutionRun.activityAutoOperator");
  }
  if (event.type === "checkpoint.wait") {
    return t("selfEvolutionRun.activityWaitConfirm");
  }
  if (event.type === "checkpoint.continue") {
    return t("selfEvolutionRun.activityContinue");
  }
  if (event.type === "checkpoint.cancel") {
    return t("selfEvolutionRun.activityTerminate");
  }
  if (event.type === "message.user") {
    return t("selfEvolutionRun.activityFrontendIntervention");
  }
  if (event.type === "message.assistant" || event.type.startsWith("intent.")) {
    return t("selfEvolutionRun.activityIntentProcessing");
  }
  const operationRunId = getOperationRunId(event.payload);
  if (operationRunId) {
    return formatOperationRunId(operationRunId);
  }
  return event.stage ? getStageLabels()[event.stage] : event.type;
}

export function formatOperationRunId(operationRunId: string) {
  const name = operationRunId
    .replace(/^dataset\./, "dataset · ")
    .replace(/^eval\./, "eval · ")
    .replace(/^analysis\./, "analysis · ")
    .replace(/^repair\./, "repair · ")
    .replace(/^abtest\./, "abtest · ")
    .replace(/_/g, " ");
  return name.replace(/\bcase\.(\d+)/, "case $1");
}

export const repairAnalysisArtifactPrefixes = [
  "repair_loop_plan",
  "repair_evidence_packet",
  "fault_localization",
  "diagnostic_probe_plan",
  "diagnostic_probe_result",
  "repair_diagnosis",
  "opencode_instruction",
  "opencode_explore_instruction",
  "opencode_patch_instruction",
  "opencode_no_patch_instruction",
];

export const repairExecutionArtifactPrefixes = [
  "opencode_probe_trace",
  "opencode_patch_trace",
  "opencode_worker_report",
  "opencode_patch_worker_report",
  "opencode_probe_worker_report",
  "opencode_no_patch_worker_report",
  "repair_hypothesis",
  "repair_plan",
  "opencode_run_trace",
  "code_patch_candidate",
  "candidate_service",
  "candidate_service_run",
  "repair_evaluation",
  "patch_correctness_assessment",
  "patch_critique",
  "branch_decision",
  "repair_branch_state_before",
  "repair_branch_state_after",
  "repair_state_transition",
  "candidate_classification_report",
  "repair_loop_decision",
  "repair_loop_memory",
  "repair_loop_state",
  "verified_repair",
];

export function getActivityArtifactKind(event: NormalizedThreadEvent): WorkflowResultKind | undefined {
  if (!event.stage || event.type === "checkpoint.created") {
    return undefined;
  }
  if (event.checkpointWait) {
    return stageResultKindMap[event.stage];
  }
  const eventData = getEventPayloadData(event.payload);
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  const artifactId =
    getStringField(detail, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(eventData, ["artifact_id", "writes_artifact_id", "current_item"]) ||
    getOperationRunId(event.payload);
  const finalArtifactIds: Record<ThreadEventStage, string[]> = {
    dataset: ["eval_dataset"],
    eval: ["eval_report", "candidate_eval_report"],
    analysis: ["classification_report", "repair_loop_plan"],
    repair: ["verified_repair", "repair_loop_agent", "candidate_workspace"],
    abtest: ["abtest_comparison", "candidate_algorithm_cutover"],
  };
  const repairArtifactId = artifactId || "";
  const isRepairAnalysisArtifact = event.stage === "repair" && repairArtifactId.length > 0 &&
    repairAnalysisArtifactPrefixes.some((prefix) => repairArtifactId === prefix || repairArtifactId.startsWith(`${prefix}_`));
  if (isRepairAnalysisArtifact) {
    return "analysis-reports";
  }
  const isRepairExecutionArtifact = event.stage === "repair" && repairArtifactId.length > 0 &&
    repairExecutionArtifactPrefixes.some((prefix) => repairArtifactId === prefix || repairArtifactId.startsWith(`${prefix}_`));
  return artifactId && (finalArtifactIds[event.stage].includes(artifactId) || isRepairExecutionArtifact)
    ? stageResultKindMap[event.stage]
    : undefined;
}

export function getActivityArtifactLabel(artifactKind: WorkflowResultKind | undefined) {
  if (!artifactKind) {
    return undefined;
  }
  return t("selfEvolutionRun.viewArtifact", { label: getWorkflowResultLabels()[artifactKind] });
}

export function buildEventActivity(event: NormalizedThreadEvent): EvoStageActivity {
  const progressText = event.progress ? `${event.progress.statusText} ${event.progress.percent}%` : "";
  const stageProgressText = event.stage === "abtest" ? progressText : "";
  const detail = event.displayText || stageProgressText || event.content || progressText || compactPayloadForDisplay(event.payload) || event.type;
  const artifactKind = getActivityArtifactKind(event);
  const artifactId = getEventArtifactId(event.payload);
  const flowKind = getEventFlowKind(event.payload);
  return {
    key: event.key,
    stage: event.stage,
    title: eventActivityTitle(event),
    detail,
    time: formatThreadTime(event.timestamp),
    tone: eventActivityTone(event),
    flowKind,
    artifactKind,
    artifactId,
    artifactLabel: getActivityArtifactLabel(artifactKind),
  };
}

export function stageProgressFromEvents(events: NormalizedThreadEvent[], stage: ThreadEventStage) {
  return getLastItem(
    events.filter((event) => event.stage === stage && event.progress &&
      !(stage === "eval" && getEventFlowKind(event.payload) === "eval.answer_and_judge")),
  )?.progress;
}

export function shouldShowProcessActivity(event: NormalizedThreadEvent) {
  if (event.type === "checkpoint.created" || isTerminalThreadEvent(event.type)) {
    return false;
  }
  return Boolean(event.displayText || event.content || event.progress || event.checkpointWait || event.type.startsWith("autooperator."));
}

export function isCutoverActivity(item: EvoStageActivity) {
  return item.stage === "abtest" && item.artifactId === "candidate_algorithm_cutover";
}

export function isCutoverCompletedEvent(event: NormalizedThreadEvent) {
  return event.stage === "abtest" &&
    (getEventFlowKind(event.payload) === "abtest.candidate_cutover" ||
      getEventArtifactId(event.payload) === "candidate_algorithm_cutover") &&
    (isActionKind(event.action, "finish") || event.progress?.percent === 100);
}

export function getStageLogicalTaskCount(events: NormalizedThreadEvent[], stage: ThreadEventStage) {
  const keys = new Set<string>();
  events.forEach((event) => {
    const payload = event.payload;
    const operationRefs = getStructuredArrayField(payload, ["operation_refs"]);
    operationRefs?.forEach((item) => {
      if (typeof item !== "string") {
        return;
      }
      const flowKind = operationFlowKindFromRef(item);
      if (stage === "eval" && flowKind !== "eval.rag_answer" && flowKind !== "eval.judge_answer") {
        return;
      }
      keys.add(item);
    });
    const operationRunId = getOperationRunId(payload);
    if (!operationRunId) {
      return;
    }
    const flowKind = getEventFlowKind(payload) || operationFlowKindFromRef(operationRunId);
    if (stage === "eval" && flowKind !== "eval.rag_answer" && flowKind !== "eval.judge_answer") {
      return;
    }
    keys.add(operationRunId);
  });
  return keys.size || events.length;
}

export function operationFlowKindFromRef(ref: string) {
  if (/^(?:eval|eval_retry_\d+)\.rag\./.test(ref)) {
    return "eval.rag_answer";
  }
  if (/^(?:eval|eval_retry_\d+)\.judge\./.test(ref)) {
    return "eval.judge_answer";
  }
  if (/^(?:eval|eval_retry_\d+)\.aggregate$/.test(ref)) {
    return "eval.aggregate";
  }
  return "";
}
