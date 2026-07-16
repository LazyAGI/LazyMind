import type { NormalizedThreadEvent, StepStatus } from "./types";
import {
  getEventCaseId,
  getNestedRecordField,
  getNumberField,
  getStringField,
  isRecord,
} from "./fields";
import { getStepStatusLabel } from "./runtimeState";
import { t } from "./i18n";

export type RepairTraceCategory =
  | "attempt"
  | "loop"
  | "opencode"
  | "verify"
  | "candidate"
  | "delta";

export type RepairTraceRow = {
  key: string;
  eventType: string;
  category: RepairTraceCategory;
  action: StepStatus;
  statusLabel: string;
  attempt?: number;
  caseId?: string;
  title: string;
  detail?: string;
  chips: string[];
  timestamp?: string;
  order: number;
};

const REPAIR_TRACE_RAW_EVENT_PREFIXES = [
  "repair.",
  "opencode.",
  "verify.",
  "candidate.",
  "analysis.",
] as const;

const REPAIR_STAGE_ALIASES = new Set([
  "repair",
  "apply",
  "code-optimize",
  "code_optimize",
  "diffs",
]);

const LEGACY_REPAIR_TRACE_EVENT_TYPES = new Set([
  "repair.attempt",
  "repair.loop",
  "repair.verify",
  "repair.opencode",
  "repair.opencode_code",
  "repair.opencode_tool",
  "repair.candidate_eval",
  "repair.delta",
]);

const REPAIR_TRACE_CATEGORY_ORDER: RepairTraceCategory[] = [
  "attempt",
  "loop",
  "opencode",
  "verify",
  "candidate",
  "delta",
];

const REPAIR_TRACE_EVENT_TITLE_KEYS: Record<string, string> = {
  "repair.attempt_started": "repairTraceEventAttemptStarted",
  "repair.attempt_completed": "repairTraceEventAttemptCompleted",
  "repair.base_selected": "repairTraceEventBaseSelected",
  "repair.loop_completed": "repairTraceEventLoopCompleted",
  "repair.patch_verified": "repairTraceEventPatchVerified",
  "opencode.setup": "repairTraceEventOpencodeSetup",
  "opencode.process_start": "repairTraceEventOpencodeProcessStart",
  "opencode.process_exit": "repairTraceEventOpencodeProcessExit",
  "opencode.code": "repairTraceEventOpencodeCode",
  "opencode.message": "repairTraceEventOpencodeMessage",
  "opencode.error": "repairTraceEventOpencodeError",
  "opencode.tool_use.search": "repairTraceEventOpencodeToolSearch",
  "opencode.tool_use.read_file": "repairTraceEventOpencodeToolReadFile",
  "opencode.tool_use.edit_file": "repairTraceEventOpencodeToolEditFile",
  "opencode.tool_use.run_command": "repairTraceEventOpencodeToolRunCommand",
  "verify.pre_validation_started": "repairTraceEventVerifyPreValidationStarted",
  "verify.pre_validation_completed": "repairTraceEventVerifyPreValidationCompleted",
  "verify.diff_scope_completed": "repairTraceEventVerifyDiffScopeCompleted",
  "verify.hardcode_check_completed": "repairTraceEventVerifyHardcodeCheckCompleted",
  "verify.behaviorful_diff_completed": "repairTraceEventVerifyBehaviorfulDiffCompleted",
  "verify.patch_policy_completed": "repairTraceEventVerifyPatchPolicyCompleted",
  "verify.command_started": "repairTraceEventVerifyCommandStarted",
  "verify.command_completed": "repairTraceEventVerifyCommandCompleted",
  "candidate.service_started": "repairTraceEventCandidateServiceStarted",
  "candidate.service_ready": "repairTraceEventCandidateServiceReady",
  "candidate.service_failed": "repairTraceEventCandidateServiceFailed",
  "candidate.service_stopped": "repairTraceEventCandidateServiceStopped",
  "candidate.case_started": "repairTraceEventCandidateCaseStarted",
  "candidate.case_completed": "repairTraceEventCandidateCaseCompleted",
  "candidate.eval_summary_completed": "repairTraceEventCandidateEvalSummaryCompleted",
  "analysis.candidate_started": "repairTraceEventAnalysisCandidateStarted",
  "analysis.candidate_completed": "repairTraceEventAnalysisCandidateCompleted",
  "analysis.delta_completed": "repairTraceEventAnalysisDeltaCompleted",
};

type RepairTraceLifecycle = {
  id: string;
  phase?: string;
  terminal?: boolean;
};

export function isRepairTraceRawEventType(eventType: string | undefined): boolean {
  if (!eventType) {
    return false;
  }
  return REPAIR_TRACE_RAW_EVENT_PREFIXES.some((prefix) => eventType.startsWith(prefix));
}

function isRepairPayloadStage(payload: Record<string, unknown> | undefined): boolean {
  const stage = getStringField(payload, ["stage"])?.trim().toLowerCase();
  return Boolean(stage && REPAIR_STAGE_ALIASES.has(stage));
}

export function isRepairTraceStageEvent(
  event: Pick<NormalizedThreadEvent, "stage" | "payload">,
): boolean {
  return event.stage === "repair" || isRepairPayloadStage(event.payload);
}

export function getRepairTraceCategory(eventType: string): RepairTraceCategory {
  if (eventType.startsWith("opencode.")) {
    return "opencode";
  }
  if (eventType.startsWith("verify.")) {
    return "verify";
  }
  if (eventType.startsWith("candidate.")) {
    return "candidate";
  }
  if (eventType.startsWith("analysis.")) {
    return "delta";
  }
  if (eventType === "repair.loop" || eventType === "repair.loop_completed") {
    return "loop";
  }
  if (LEGACY_REPAIR_TRACE_EVENT_TYPES.has(eventType)) {
    if (eventType.startsWith("repair.opencode")) {
      return "opencode";
    }
    if (eventType === "repair.candidate_eval" || eventType.startsWith("repair.candidate")) {
      return "candidate";
    }
    if (eventType === "repair.delta") {
      return "delta";
    }
    if (eventType === "repair.verify") {
      return "verify";
    }
    if (eventType === "repair.loop") {
      return "loop";
    }
  }
  return "attempt";
}

function getRepairTraceEventType(event: NormalizedThreadEvent): string | undefined {
  if (!isRepairTraceStageEvent(event)) {
    return undefined;
  }

  const fromPayload =
    getStringField(event.payload, ["event_type", "eventType", "type"]) || event.type;
  if (!fromPayload || fromPayload === "done" || fromPayload === "message") {
    return undefined;
  }
  if (isRepairTraceRawEventType(fromPayload)) {
    return fromPayload;
  }
  if (LEGACY_REPAIR_TRACE_EVENT_TYPES.has(fromPayload)) {
    return fromPayload;
  }
  if (fromPayload.startsWith("repair.")) {
    return fromPayload;
  }
  return undefined;
}

function getRepairTraceSummary(event: NormalizedThreadEvent) {
  return (
    getNestedRecordField(event.payload, ["summary"]) ||
    getNestedRecordField(event.payload, ["data", "summary"])
  );
}

const LIFECYCLE_FINISH_EVENT_SUFFIXES = [
  "_completed",
  "_exit",
  "_ready",
  "_failed",
  "_stopped",
] as const;

function getRepairTracePayloadEventType(event: NormalizedThreadEvent): string {
  return getStringField(event.payload, ["event_type", "eventType", "type"]) || event.type;
}

function isLifecycleFinishEventType(eventType: string): boolean {
  if (eventType === "opencode.error") {
    return false;
  }
  return LIFECYCLE_FINISH_EVENT_SUFFIXES.some((suffix) => eventType.endsWith(suffix));
}

const ONE_SHOT_LOG_EVENT_TYPES = new Set([
  "opencode.message",
  "opencode.code",
]);

const LIFECYCLE_START_EVENT_SUFFIXES = ["_started"] as const;

function isLifecycleStartEventType(eventType: string): boolean {
  return LIFECYCLE_START_EVENT_SUFFIXES.some((suffix) => eventType.endsWith(suffix));
}

function resolveNoLifecycleRowAction(
  eventType: string,
  status?: string,
  action?: string,
): StepStatus {
  if (ONE_SHOT_LOG_EVENT_TYPES.has(eventType)) {
    return "done";
  }
  if (isLifecycleStartEventType(eventType)) {
    return "running";
  }
  if (isLifecycleFinishEventType(eventType)) {
    return normalizeRepairTraceAction(action || status);
  }
  const normalized = normalizeRepairTraceAction(action || status);
  if (normalized !== "running") {
    return normalized;
  }
  return "done";
}

function getRepairTraceLifecycle(event: NormalizedThreadEvent): RepairTraceLifecycle | undefined {
  const lifecycleRecord =
    getNestedRecordField(event.payload, ["lifecycle"]) ||
    getNestedRecordField(event.payload, ["data", "lifecycle"]);
  const id = getStringField(lifecycleRecord, ["id"]);
  const phase =
    getStringField(lifecycleRecord, ["phase"]) ||
    getStringField(event.payload, ["phase"]) ||
    undefined;
  const eventType = getRepairTracePayloadEventType(event);
  const lifecycleTerminal = lifecycleRecord?.terminal === true;
  const terminal =
    lifecycleTerminal ||
    event.payload?.terminal === true ||
    phase === "finish" ||
    isLifecycleFinishEventType(eventType);

  if (!id) {
    if (!terminal && !phase) {
      return undefined;
    }
    const eventId = getStringField(event.payload, ["event_id", "eventId"]);
    if (!eventId) {
      return undefined;
    }
    return {
      id: eventId,
      phase,
      terminal,
    };
  }

  return {
    id,
    phase,
    terminal,
  };
}

function getRepairTraceCaseId(event: NormalizedThreadEvent): string | undefined {
  const caseRecord = getNestedRecordField(event.payload, ["case"]);
  return (
    getStringField(caseRecord, ["case_id", "caseId", "id"]) ||
    getEventCaseId(event.payload) ||
    undefined
  );
}

function normalizeRepairTraceAction(action?: string): StepStatus {
  if (
    action === "completed" ||
    action === "finish" ||
    action === "done" ||
    action === "skipped"
  ) {
    return "done";
  }
  if (action === "failed") {
    return "failed";
  }
  if (action === "cancel" || action === "canceled" || action === "cancelled") {
    return "canceled";
  }
  if (action === "pause" || action === "paused") {
    return "paused";
  }
  if (
    action === "running" ||
    action === "start" ||
    action === "progress" ||
    action === "started"
  ) {
    return "running";
  }
  return "running";
}

const TERMINAL_ROW_ACTIONS = new Set<StepStatus>(["done", "failed", "canceled", "paused"]);

function isTerminalRowAction(action: StepStatus): boolean {
  return TERMINAL_ROW_ACTIONS.has(action);
}

function coalesceRowAction(existing: RepairTraceRow | undefined, next: StepStatus): StepStatus {
  if (!existing || existing.action === "running") {
    return next;
  }
  if (next === "running") {
    return existing.action;
  }
  if (existing.action === "failed" || next === "failed") {
    return "failed";
  }
  return next;
}

function compareRepairTraceEventsForBuild(
  left: NormalizedThreadEvent,
  right: NormalizedThreadEvent,
): number {
  if (
    typeof left.sequence === "number" &&
    typeof right.sequence === "number" &&
    left.sequence !== right.sequence
  ) {
    return left.sequence - right.sequence;
  }

  if (left.timestamp && right.timestamp) {
    const leftTime = new Date(left.timestamp).getTime();
    const rightTime = new Date(right.timestamp).getTime();
    if (!Number.isNaN(leftTime) && !Number.isNaN(rightTime) && leftTime !== rightTime) {
      return leftTime - rightTime;
    }
  }

  const leftLifecycle = getRepairTraceLifecycle(left);
  const rightLifecycle = getRepairTraceLifecycle(right);
  if (
    leftLifecycle?.id &&
    rightLifecycle?.id &&
    leftLifecycle.id === rightLifecycle.id
  ) {
    const leftPhaseRank = leftLifecycle.phase === "start" ? 0 : 1;
    const rightPhaseRank = rightLifecycle.phase === "start" ? 0 : 1;
    if (leftPhaseRank !== rightPhaseRank) {
      return leftPhaseRank - rightPhaseRank;
    }
  }

  return left.key.localeCompare(right.key, "zh-CN", { numeric: true });
}

function resolveStreamDoneStatus(events: NormalizedThreadEvent[]): StepStatus | undefined {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    const event = events[index];
    const eventType = getRepairTracePayloadEventType(event);
    if (eventType !== "done" && event.type !== "done") {
      continue;
    }
    const status = getStringField(event.payload, ["status"])?.toLowerCase();
    if (status === "failed" || status === "error") {
      return "failed";
    }
    if (status === "cancelled" || status === "canceled") {
      return "canceled";
    }
    if (status === "paused") {
      return "paused";
    }
    return "done";
  }
  return undefined;
}

function applyStreamDoneClosure(
  rows: RepairTraceRow[],
  events: NormalizedThreadEvent[],
): RepairTraceRow[] {
  const doneStatus = resolveStreamDoneStatus(events);
  if (!doneStatus) {
    return rows;
  }
  return rows.map((row) => {
    if (row.action !== "running") {
      return row;
    }
    return closeRunningRow(row, doneStatus);
  });
}

function collectAttemptTerminalStatuses(
  events: NormalizedThreadEvent[],
): Map<number, StepStatus> {
  const attemptTerminal = new Map<number, StepStatus>();
  events.forEach((event) => {
    if (getRepairTracePayloadEventType(event) !== "repair.attempt_completed") {
      return;
    }
    const lifecycle = getRepairTraceLifecycle(event);
    if (
      lifecycle &&
      lifecycle.phase === "start" &&
      lifecycle.terminal !== true
    ) {
      return;
    }
    const summary = getRepairTraceSummary(event);
    const summaryRecord = isRecord(summary) ? summary : undefined;
    const attempt = getNumberField(summaryRecord, ["attempt"]);
    if (typeof attempt !== "number") {
      return;
    }
    const status = getStringField(event.payload, ["status"]);
    const action = getStringField(event.payload, ["action"]);
    attemptTerminal.set(
      attempt,
      status === "failed" || action === "failed" ? "failed" : "done",
    );
  });
  return attemptTerminal;
}

function isRepairAttemptAnchorRow(row: RepairTraceRow): boolean {
  return (
    row.eventType === "repair.attempt_started" ||
    row.eventType === "repair.attempt_completed"
  );
}

function applyAttemptScopedClosure(
  rows: RepairTraceRow[],
  events: NormalizedThreadEvent[],
): RepairTraceRow[] {
  const attemptTerminal = collectAttemptTerminalStatuses(events);
  if (attemptTerminal.size === 0) {
    return rows;
  }
  return rows.map((row) => {
    if (row.action !== "running" || typeof row.attempt !== "number") {
      return row;
    }
    const terminal = attemptTerminal.get(row.attempt);
    if (!terminal) {
      return row;
    }
    return closeRunningRow(
      row,
      isRepairAttemptAnchorRow(row) ? terminal : "done",
    );
  });
}

function applyRepairStepTerminalClosure(
  rows: RepairTraceRow[],
  repairStepStatus?: StepStatus,
): RepairTraceRow[] {
  if (
    !repairStepStatus ||
    repairStepStatus === "running" ||
    repairStepStatus === "pending"
  ) {
    return rows;
  }
  const closeStatus: StepStatus =
    repairStepStatus === "failed" ? "done" : repairStepStatus;
  return rows.map((row) => {
    if (row.action !== "running") {
      return row;
    }
    return closeRunningRow(row, closeStatus);
  });
}

function closeRunningRow(row: RepairTraceRow, status: StepStatus): RepairTraceRow {
  return {
    ...row,
    action: status,
    statusLabel: getStepStatusLabel(status),
  };
}

function resolveLifecycleRowAction(
  event: NormalizedThreadEvent,
  lifecycle: RepairTraceLifecycle | undefined,
  existing?: RepairTraceRow,
): StepStatus {
  const status = getStringField(event.payload, ["status"]);
  const action = getStringField(event.payload, ["action"]);
  const eventType = getRepairTracePayloadEventType(event);

  if (!lifecycle) {
    return resolveNoLifecycleRowAction(eventType, status, action);
  }

  const isFinish =
    lifecycle.terminal === true ||
    lifecycle.phase === "finish" ||
    isLifecycleFinishEventType(eventType);

  if (isFinish) {
    if (
      status === "failed" ||
      status === "failure" ||
      action === "failed"
    ) {
      return "failed";
    }
    if (status === "skipped" || action === "skipped") {
      return "done";
    }
    return "done";
  }

  if (lifecycle?.phase === "start" && !isLifecycleFinishEventType(eventType)) {
    if (existing && isTerminalRowAction(existing.action)) {
      return existing.action;
    }
    return "running";
  }

  if (lifecycle?.id && existing?.action === "running") {
    const normalized = normalizeRepairTraceAction(action || status || event.action);
    if (normalized !== "running") {
      return normalized;
    }
    return "running";
  }

  return normalizeRepairTraceAction(action || status || event.action);
}

function buildRepairTraceRowKey(
  event: NormalizedThreadEvent,
  lifecycle: RepairTraceLifecycle | undefined,
): string {
  if (lifecycle?.id) {
    return `lifecycle:${lifecycle.id}`;
  }
  const eventId = getStringField(event.payload, ["event_id", "eventId"]);
  return `event:${eventId || event.key}`;
}

function pickRepairTraceDetail(
  summary: Record<string, unknown> | undefined,
  event: NormalizedThreadEvent,
  existing?: RepairTraceRow,
): string | undefined {
  if (event.action === "failed") {
    return event.displayText || event.content || existing?.detail;
  }
  const message = getStringField(summary, ["message"]);
  const detail = message || event.displayText || event.content;
  if (detail && detail.trim()) {
    return detail.trim();
  }
  return existing?.detail;
}

function mergeRepairTraceChips(existing: RepairTraceRow | undefined, chips: string[]): string[] {
  if (!existing?.chips.length) {
    return chips;
  }
  const merged = new Set([...existing.chips, ...chips]);
  return Array.from(merged);
}

function buildRepairTraceChips(
  summary: Record<string, unknown> | undefined,
  options?: { includeAttempt?: boolean },
): string[] {
  if (!summary) {
    return [];
  }
  const chips: string[] = [];
  const includeAttempt = options?.includeAttempt ?? true;
  const attempt = getNumberField(summary, ["attempt"]);
  const toolKind = getStringField(summary, ["tool_kind", "tool"]);
  const exitCode = getNumberField(summary, ["exit_code", "returncode", "exitCode"]);
  const decision = getStringField(summary, ["decision", "decision_status"]);
  const executionType = getStringField(summary, ["execution_type", "executionType"]);
  const command = getStringField(summary, ["command"]);

  if (includeAttempt && typeof attempt === "number") {
    chips.push(t("selfEvolutionRun.repairTraceChipAttempt", { attempt }));
  }
  if (toolKind) {
    chips.push(t("selfEvolutionRun.repairTraceChipTool", { tool: toolKind }));
  }
  if (executionType) {
    chips.push(executionType);
  }
  if (command) {
    chips.push(command);
  }
  if (typeof exitCode === "number") {
    chips.push(t("selfEvolutionRun.repairTraceChipExitCode", { code: exitCode }));
  }
  if (decision) {
    chips.push(t("selfEvolutionRun.repairTraceChipDecision", { decision }));
  }
  return chips;
}

function resolveRepairTraceAggregateStatus(rows: RepairTraceRow[]): StepStatus {
  if (rows.some((row) => row.action === "running")) {
    return "running";
  }
  if (rows.some((row) => row.action === "failed")) {
    return "failed";
  }
  if (rows.some((row) => row.action === "canceled")) {
    return "canceled";
  }
  if (rows.some((row) => row.action === "paused")) {
    return "paused";
  }
  return rows.length > 0 ? "done" : "pending";
}

function resolveRepairTraceAttemptGroupStatus(rows: RepairTraceRow[]): StepStatus {
  const attemptRows = rows.filter(
    (row) =>
      row.eventType === "repair.attempt_started" ||
      row.eventType === "repair.attempt_completed",
  );
  if (attemptRows.length > 0) {
    const failedAttempt = attemptRows.find((row) => row.action === "failed");
    if (failedAttempt) {
      return "failed";
    }
    const terminalAttempt = attemptRows.find((row) => isTerminalRowAction(row.action));
    if (terminalAttempt) {
      return terminalAttempt.action;
    }
    const runningAttempt = attemptRows.find((row) => row.action === "running");
    if (runningAttempt) {
      return "running";
    }
    return attemptRows[attemptRows.length - 1].action;
  }
  const loopAnchor = rows.find((row) => row.eventType === "repair.loop_completed");
  if (loopAnchor) {
    return loopAnchor.action;
  }
  return resolveRepairTraceAggregateStatus(rows);
}

export type RepairTraceAttemptGroup = {
  key: string;
  attempt: number;
  label: string;
  status: StepStatus;
  statusLabel: string;
  rows: RepairTraceRow[];
  phaseSummaries: RepairTracePhaseSummary[];
};

export function buildRepairTraceAttemptGroups(
  rows: RepairTraceRow[],
): RepairTraceAttemptGroup[] {
  const grouped = new Map<number, RepairTraceRow[]>();
  const ungrouped: RepairTraceRow[] = [];

  rows.forEach((row) => {
    if (typeof row.attempt === "number") {
      const bucket = grouped.get(row.attempt) || [];
      bucket.push(row);
      grouped.set(row.attempt, bucket);
      return;
    }
    ungrouped.push(row);
  });

  const groups = Array.from(grouped.entries())
    .sort(([left], [right]) => left - right)
    .map(([attempt, attemptRows]) => {
      const sortedRows = [...attemptRows].sort((left, right) => left.order - right.order);
      const status = resolveRepairTraceAttemptGroupStatus(sortedRows);
      return {
        key: `attempt-${attempt}`,
        attempt,
        label: t("selfEvolutionRun.repairTraceAttemptGroupTitle", { attempt }),
        status,
        statusLabel: getStepStatusLabel(status),
        rows: sortedRows,
        phaseSummaries: buildRepairTracePhaseSummaries(sortedRows),
      };
    });

  if (ungrouped.length > 0) {
    const sortedRows = [...ungrouped].sort((left, right) => left.order - right.order);
    const status = resolveRepairTraceAggregateStatus(sortedRows);
    groups.push({
      key: "attempt-unknown",
      attempt: 0,
      label: t("selfEvolutionRun.repairTraceAttemptGroupOther"),
      status,
      statusLabel: getStepStatusLabel(status),
      rows: sortedRows,
      phaseSummaries: buildRepairTracePhaseSummaries(sortedRows),
    });
  }

  return groups;
}

export function buildRepairTraceEventTitle(eventType: string): string {
  const legacyTitles: Record<string, string> = {
    "repair.attempt": t("selfEvolutionRun.repairTraceEventAttempt"),
    "repair.loop": t("selfEvolutionRun.repairTraceEventLoop"),
    "repair.verify": t("selfEvolutionRun.repairTraceEventVerify"),
    "repair.opencode": t("selfEvolutionRun.repairTraceEventOpencode"),
    "repair.opencode_code": t("selfEvolutionRun.repairTraceEventOpencodeCode"),
    "repair.opencode_tool": t("selfEvolutionRun.repairTraceEventOpencodeTool"),
    "repair.candidate_eval": t("selfEvolutionRun.repairTraceEventCandidateEval"),
    "repair.delta": t("selfEvolutionRun.repairTraceEventDelta"),
  };
  if (legacyTitles[eventType]) {
    return legacyTitles[eventType];
  }

  const titleKey = REPAIR_TRACE_EVENT_TITLE_KEYS[eventType];
  if (titleKey) {
    return t(`selfEvolutionRun.${titleKey}`);
  }

  if (eventType.startsWith("opencode.tool_use.")) {
    const tool = eventType.replace("opencode.tool_use.", "");
    return t("selfEvolutionRun.repairTraceEventOpencodeToolNamed", { tool });
  }

  return eventType
    .replace(/^repair\./, "")
    .replace(/^opencode\./, "opencode · ")
    .replace(/^verify\./, "verify · ")
    .replace(/^candidate\./, "candidate · ")
    .replace(/^analysis\./, "analysis · ")
    .replace(/_/g, " ");
}

export function getRepairTraceCategoryLabel(category: RepairTraceCategory): string {
  const labels: Record<RepairTraceCategory, string> = {
    attempt: t("selfEvolutionRun.repairTraceCategoryAttempt"),
    loop: t("selfEvolutionRun.repairTraceCategoryLoop"),
    opencode: t("selfEvolutionRun.repairTraceCategoryOpencode"),
    verify: t("selfEvolutionRun.repairTraceCategoryVerify"),
    candidate: t("selfEvolutionRun.repairTraceCategoryCandidate"),
    delta: t("selfEvolutionRun.repairTraceCategoryDelta"),
  };
  return labels[category];
}

export type BuildRepairTraceRowsOptions = {
  repairStepStatus?: StepStatus;
};

export function buildRepairTraceRows(
  events: NormalizedThreadEvent[],
  options?: BuildRepairTraceRowsOptions,
): RepairTraceRow[] {
  const rowMap = new Map<string, RepairTraceRow>();
  let order = 0;
  let currentAttempt: number | undefined;

  const sortedEvents = [...events].sort(compareRepairTraceEventsForBuild);

  sortedEvents.forEach((event) => {
    const eventType = getRepairTraceEventType(event);
    if (!eventType) {
      return;
    }

    const lifecycle = getRepairTraceLifecycle(event);
    const summary = getRepairTraceSummary(event);
    const summaryRecord = isRecord(summary) ? summary : undefined;
    const summaryAttempt = getNumberField(summaryRecord, ["attempt"]);
    if (typeof summaryAttempt === "number") {
      currentAttempt = summaryAttempt;
    }
    const attempt = summaryAttempt ?? currentAttempt;
    const rowKey = buildRepairTraceRowKey(event, lifecycle);
    const existing = rowMap.get(rowKey);
    const resolvedAction = resolveLifecycleRowAction(event, lifecycle, existing);
    const action = coalesceRowAction(existing, resolvedAction);
    const displayEventType =
      existing?.eventType &&
      (lifecycle?.phase === "finish" ||
        lifecycle?.terminal === true ||
        isLifecycleFinishEventType(eventType))
        ? existing.eventType
        : eventType;
    const nextOrder = existing?.order ?? order;
    if (!existing) {
      order += 1;
    }

    rowMap.set(rowKey, {
      key: rowKey,
      eventType: displayEventType,
      category: getRepairTraceCategory(displayEventType),
      action,
      statusLabel: getStepStatusLabel(action),
      attempt,
      caseId: getRepairTraceCaseId(event) ?? existing?.caseId,
      title: buildRepairTraceEventTitle(displayEventType),
      detail: pickRepairTraceDetail(summaryRecord, event, existing),
      chips: mergeRepairTraceChips(
        existing,
        buildRepairTraceChips(summaryRecord, { includeAttempt: typeof attempt !== "number" }),
      ),
      timestamp: event.timestamp || existing?.timestamp,
      order: nextOrder,
    });
  });

  const rows = Array.from(rowMap.values()).sort((left, right) => left.order - right.order);
  const attemptClosedRows = applyAttemptScopedClosure(rows, sortedEvents);
  const streamClosedRows = applyStreamDoneClosure(attemptClosedRows, sortedEvents);
  return applyRepairStepTerminalClosure(
    streamClosedRows,
    options?.repairStepStatus,
  );
}

export type RepairTracePhaseSummary = {
  category: RepairTraceCategory;
  label: string;
  status: StepStatus;
  statusLabel: string;
  count: number;
};

export function buildRepairTracePhaseSummaries(
  rows: RepairTraceRow[],
): RepairTracePhaseSummary[] {
  const summaries: RepairTracePhaseSummary[] = [];

  REPAIR_TRACE_CATEGORY_ORDER.forEach((category) => {
    const categoryRows = rows.filter((row) => row.category === category);
    if (categoryRows.length === 0) {
      return;
    }
    const status = resolveRepairTraceAggregateStatus(categoryRows);
    summaries.push({
      category,
      label: getRepairTraceCategoryLabel(category),
      status,
      statusLabel: getStepStatusLabel(status),
      count: categoryRows.length,
    });
  });

  return summaries;
}

export function getRepairTraceProgress(rows: RepairTraceRow[]) {
  const completed = rows.filter((row) => row.action === "done").length;
  const failed = rows.filter((row) => row.action === "failed").length;
  const running = rows.filter((row) => row.action === "running").length;
  return {
    total: rows.length,
    completed,
    failed,
    running,
    hasEvents: rows.length > 0,
  };
}
