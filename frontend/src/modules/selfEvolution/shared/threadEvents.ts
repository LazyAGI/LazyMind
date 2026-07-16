import { type ChatStreamDeltaKind, type NormalizedThreadEvent, type StepStatus, type ThreadEventFrame, type ThreadEventStage } from "./types";
import { failedThreadEventTypes, inactiveTerminalThreadStatuses, terminalThreadEventTypes } from "./constants";
import { getEventActionLabels, getStageLabels, t } from "./i18n";
import { enrichProjectionEventPayload, getProjectionEventType, mapProjectionAction } from "./projectionEvents";
import { isRepairTraceRawEventType } from "./repairTrace";
import { getEventCaseId, getEventPayloadData, getNumberField, getOperationRunId, getStringField, getThreadEventContentFromPayload, getThreadEventPayloadEnvelope, getThreadEventTypeFromPayload, isRecord } from "./fields";
import { buildCheckpointWaitPrompt, buildFailureRetryPrompt } from "./checkpoint";
import { buildAbtestEventDisplayText, buildAnalysisEventDisplayText, buildApplyEventDisplayText, buildDatasetEventDisplayText, buildEvalEventDisplayText, compactPayloadForDisplay } from "./eventDisplay";
import { getEvalPayloadPhase, getWorkflowProgressSnapshot } from "./progress";
import { localizeErrorCode } from "@/components/request";

const THREAD_EVENT_STAGE_ORDER: ThreadEventStage[] = [
  "dataset",
  "eval",
  "analysis",
  "repair",
  "abtest",
];

export function resolveCompletedStageFromDonePayload(
  payload: Record<string, unknown> | undefined,
): ThreadEventStage | undefined {
  if (!payload) {
    return undefined;
  }

  const retryFromStep = toThreadEventStage(
    getStringField(payload, ["retry_from_step", "retryFromStep"]),
  );
  if (retryFromStep) {
    return retryFromStep;
  }

  const currentStep = toThreadEventStage(
    getStringField(payload, ["current_step", "currentStep", "step"]),
  );
  const flowStatus = getStringField(payload, ["status", "state"])?.trim().toLowerCase();
  if (currentStep && flowStatus === "paused") {
    const currentIndex = THREAD_EVENT_STAGE_ORDER.indexOf(currentStep);
    if (currentIndex > 0) {
      return THREAD_EVENT_STAGE_ORDER[currentIndex - 1];
    }
  }

  return toThreadEventStage(
    getStringField(payload, ["last_released_step", "lastReleasedStep"]),
  ) || currentStep;
}

export function isCheckpointGateFlowStatus(status?: string) {
  const normalized = status?.trim().toLowerCase();
  return (
    normalized === "paused" ||
    normalized === "waiting_checkpoint" ||
    normalized === "completed"
  );
}

export function isEventStreamTerminalFlowStatus(status?: string) {
  const normalized = status?.trim().toLowerCase();
  return (
    normalized === "completed" ||
    normalized === "paused" ||
    normalized === "failed"
  );
}

export function isEventStreamTerminalFlowPayload(
  payload: Record<string, unknown> | undefined,
): boolean {
  if (!payload) {
    return false;
  }
  const eventType = getStringField(payload, ["event_type", "eventType", "type"]);
  if (eventType !== "done" && (!eventType || !isTerminalThreadEvent(eventType))) {
    return false;
  }
  return isEventStreamTerminalFlowStatus(getFlowStatusFromPayload(payload));
}

export function getFlowStatusFromPayload(
  payload: Record<string, unknown> | undefined,
): string | undefined {
  return getStringField(payload, ["status", "state"])?.trim().toLowerCase();
}

export function isPausedFlowPayload(
  payload: Record<string, unknown> | undefined,
): boolean {
  return getFlowStatusFromPayload(payload) === "paused";
}

export function isPausedFlowEvent(
  event: Pick<NormalizedThreadEvent, "payload">,
): boolean {
  return isPausedFlowPayload(event.payload);
}

export function shouldDisconnectThreadEventStream(
  event: Pick<NormalizedThreadEvent, "type" | "payload">,
): boolean {
  return (
    isTerminalThreadEvent(event.type) ||
    isEventStreamTerminalFlowPayload(event.payload) ||
    isPausedFlowPayload(event.payload)
  );
}

export function resolveTerminalStepStatusFromFlowStatus(
  flowStatus?: string,
): StepStatus {
  const normalized = flowStatus?.trim().toLowerCase();
  if (normalized === "paused") {
    return "done";
  }
  if (normalized === "failed" || normalized === "error") {
    return "failed";
  }
  if (normalized === "cancelled" || normalized === "canceled") {
    return "canceled";
  }
  if (normalized === "completed" || normalized === "done" || normalized === "success") {
    return "done";
  }
  return "done";
}

export function buildTerminalStatusByStage(
  events: NormalizedThreadEvent[],
): Partial<Record<ThreadEventStage, StepStatus>> {
  const result: Partial<Record<ThreadEventStage, StepStatus>> = {};
  for (const event of events) {
    if (
      !isTerminalThreadEvent(event.type) &&
      !isEventStreamTerminalFlowPayload(event.payload) &&
      !isPausedFlowPayload(event.payload)
    ) {
      continue;
    }
    const stage = event.stage;
    if (!stage) {
      continue;
    }
    result[stage] = resolveTerminalStepStatusFromFlowStatus(
      getFlowStatusFromPayload(event.payload),
    );
  }
  return result;
}

export function toThreadEventStage(value: unknown): ThreadEventStage | undefined {
  if (typeof value !== "string") {
    return undefined;
  }

  const normalized = value.trim();
  return {
    dataset: "dataset",
    eval: "eval",
    candidate_eval: "abtest",
    run: "analysis",
    analysis: "analysis",
    apply: "repair",
    repair: "repair",
    abtest: "abtest",
  }[normalized] as ThreadEventStage | undefined;
}

export function getStageLabel(value: unknown) {
  const stage = toThreadEventStage(value);
  if (stage) {
    return getStageLabels()[stage];
  }
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  return undefined;
}

export function getNextStageFromOperation(value: string | undefined): ThreadEventStage | undefined {
  if (!value) {
    return undefined;
  }

  const [operationStage] = value.split(".");
  return toThreadEventStage(operationStage);
}

export function parseSSEFrame(rawFrame: string): ThreadEventFrame | undefined {
  const lines = rawFrame.split(/\r?\n/);
  const dataLines: string[] = [];
  let eventName = "message";
  let id: string | undefined;

  lines.forEach((line) => {
    if (line.startsWith("id:")) {
      id = line.slice("id:".length).trim() || undefined;
    }
    if (line.startsWith("event:")) {
      eventName = line.slice("event:".length).trim() || "message";
    }
    if (line.startsWith("data:")) {
      dataLines.push(line.slice("data:".length).trimStart());
    }
  });

  if (dataLines.length === 0) {
    return undefined;
  }

  return {
    id,
    eventName,
    data: dataLines.join("\n"),
  };
}

export function parseThreadEventPayload(data: string): Record<string, unknown> | undefined {
  try {
    const value = JSON.parse(data);
    return isRecord(value) ? value : { value };
  } catch {
    return undefined;
  }
}

export function getChatStreamDeltaKind(type: string): ChatStreamDeltaKind | undefined {
  if (type === "thinking_delta" || type === "intent.thinking_delta") {
    return "thinking";
  }
  if (type === "answer_delta" || type === "intent.answer_delta") {
    return "answer";
  }
  return undefined;
}

export function isTerminalThreadEvent(type: string) {
  return terminalThreadEventTypes.has(type);
}

export function isMessageStreamAssistantEvent(
  type: string,
  eventName: string,
  payload: Record<string, unknown> | undefined,
) {
  const originalType = getStringField(payload, ["original_type", "originalType"]);
  return (
    type === "message.assistant" ||
    type === "assistant_response" ||
    type === "message_result" ||
    eventName === "assistant_response" ||
    eventName === "message_result" ||
    originalType === "assistant_response"
  );
}

export function isDoneSSEFrame(frame: ThreadEventFrame): boolean {
  if (isTerminalThreadEvent(frame.eventName)) {
    return true;
  }

  if (frame.data.trim() === "[DONE]") {
    return true;
  }

  const payload = parseThreadEventPayload(frame.data);
  if (!payload) {
    return false;
  }

  if (isEventStreamTerminalFlowPayload(payload)) {
    return true;
  }

  const eventName = getStringField(payload, [
    "event",
    "event_type",
    "eventType",
    "type",
    "flow_kind",
    "flowKind",
  ]);
  return Boolean(eventName && isTerminalThreadEvent(eventName));
}

export function isFailedThreadEvent(type: string) {
  return failedThreadEventTypes.has(type);
}

export function isInactiveTerminalThreadEvent(event: NormalizedThreadEvent) {
  if (!isTerminalThreadEvent(event.type)) {
    return false;
  }
  const status = getStringField(event.payload, ["status"]);
  return Boolean(status && inactiveTerminalThreadStatuses.has(status.toLowerCase()));
}

export function normalizeThreadEvent(frame: ThreadEventFrame): NormalizedThreadEvent {
  let payload = parseThreadEventPayload(frame.data);
  const rawPayloadType = payload ? getThreadEventTypeFromPayload(payload) : undefined;
  const rawEventType =
    rawPayloadType || (frame.eventName !== "message" ? frame.eventName : "");
  const rawRepairStage = toThreadEventStage(payload?.stage);
  const isRepairInternalTrace =
    rawRepairStage === "repair" &&
    rawEventType !== "done" &&
    isRepairTraceRawEventType(rawEventType);
  const projectionEventType =
    payload && !isRepairInternalTrace
      ? getProjectionEventType(payload, frame.eventName)
      : undefined;
  if (payload && projectionEventType) {
    payload = enrichProjectionEventPayload(payload, projectionEventType);
  }
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const payloadType = getThreadEventTypeFromPayload(payload);
  const eventType = payloadType || (frame.eventName !== "message" ? frame.eventName : "");
  const isRepairTraceEvent =
    eventType === "done" ? false : isRepairTraceRawEventType(eventType);
  const [typeStage, ...actionParts] = eventType.split(".");
  const isCheckpointEvent = eventType.startsWith("checkpoint.");
  const isAutoOperatorEvent = eventType.startsWith("autooperator.");
  const operationRunId = getOperationRunId(payload);
  const stageFromPayload =
    (operationRunId?.startsWith("candidate_eval.") ? "abtest" : undefined) ||
    toThreadEventStage(payload?.stage) ||
    toThreadEventStage(eventEnvelope?.stage);
  const stage = isCheckpointEvent
    ? undefined
    : isRepairInternalTrace
      ? "repair"
      : stageFromPayload ||
        (projectionEventType ? toThreadEventStage(projectionEventType.split(".")[0]) : undefined) ||
        (isAutoOperatorEvent ? undefined : toThreadEventStage(typeStage));
  const action = projectionEventType
    ? getStringField(payload, ["action"])
    : isCheckpointEvent
      ? actionParts.join(".") || undefined
      : isAutoOperatorEvent
        ? actionParts.join(".") || undefined
        : getStringField(payload, ["action"]) ||
          getStringField(eventEnvelope, ["action"]) ||
          mapProjectionAction(getStringField(payload, ["action"])) ||
          (stage && actionParts.length > 0
            ? actionParts.join(".")
            : stage && eventType && !toThreadEventStage(eventType) && eventType !== "message"
              ? eventType
              : undefined);
  const type = projectionEventType
    ? projectionEventType
    : isCheckpointEvent || isAutoOperatorEvent
      ? eventType
      : isRepairTraceEvent
        ? eventType
        : stage && action
          ? `${stage}.${action}`
          : eventType || "message";
  const isMessageAssistant = isMessageStreamAssistantEvent(type, frame.eventName, payload);
  const role = type === "message.user" ? "user" : isMessageAssistant ? "assistant" : undefined;
  const content = getThreadEventContentFromPayload(payload) || (!payload ? frame.data.trim() : undefined);
  const normalizedType = isMessageAssistant && type !== "message.user" ? "message.assistant" : type;
  const timestamp =
    getStringField(payload, ["ts", "timestamp", "created_at", "create_time", "updated_at", "update_time"]) ||
    getStringField(eventEnvelope, ["ts", "timestamp", "created_at", "create_time", "updated_at", "update_time"]) ||
    undefined;
  const sequence = getNumberField(payload, ["seq"]) ?? getNumberField(eventEnvelope, ["seq"]);
  const taskId =
    getStringField(payload, ["task_id"]) ||
    getStringField(eventEnvelope, ["task_id"]) ||
    getStringField(getEventPayloadData(payload), ["task_id", "run_id"]) ||
    undefined;
  const messageEventId =
    getStringField(payload, ["message_id", "messageId", "intent_id", "intentId"]) ||
    getStringField(eventEnvelope, ["message_id", "messageId", "intent_id", "intentId"]) ||
    undefined;
  const eventId =
    getStringField(payload, ["event_id"]) ||
    getStringField(eventEnvelope, ["event_id"]);
  const caseId = getEventCaseId(payload);
  const key =
    frame.id ||
    eventId ||
    [
      getStringField(payload, ["thread_id"]) || getStringField(eventEnvelope, ["thread_id"]),
      typeof sequence === "number" ? String(sequence) : "",
      taskId || messageEventId,
      caseId,
      type,
      timestamp,
    ]
      .filter(Boolean)
      .join(":") ||
    `${type}:${frame.data}`;

  if (isTerminalThreadEvent(frame.eventName) || isTerminalThreadEvent(type)) {
    const flowStage = toThreadEventStage(
      getStringField(payload, ["current_step", "currentStep", "step"]),
    );
    const flowStatus = getStringField(payload, ["status", "state"])?.trim().toLowerCase();
    const completedStage =
      resolveCompletedStageFromDonePayload(payload) ||
      (flowStatus === "paused" ? undefined : flowStage);
    const action =
      flowStatus === "paused"
        ? "finish"
        : flowStatus === "failed" || flowStatus === "error"
          ? "failed"
          : flowStatus === "cancelled" || flowStatus === "canceled"
            ? "cancel"
            : flowStatus === "running"
              ? "start"
              : flowStatus
                ? "finish"
                : undefined;
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      stage: completedStage || flowStage,
      action,
      payload,
      displayText: t("selfEvolutionRun.eventStreamEnded"),
    };
  }

  if (isFailedThreadEvent(frame.eventName) || isFailedThreadEvent(type)) {
    const eventData = getEventPayloadData(payload);
    const errorCode =
      getStringField(eventData, ["error_code", "code"]) ||
      getStringField(payload, ["error_code", "code"]);
    const errorText = localizeErrorCode(
      errorCode,
      localizeErrorCode("2000509"),
    );
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content: errorText,
      payload,
      displayText: errorText,
    };
  }

  const chatStreamDeltaKind = getChatStreamDeltaKind(type);
  if (chatStreamDeltaKind) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content,
      payload,
      displayText: content,
    };
  }

  if (role) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type: normalizedType,
      role,
      content,
      payload,
      displayText: content,
    };
  }

  if (type === "intent.thought" || type === "intent.reply") {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      role: "assistant",
      content: type === "intent.thought" && content ? t("selfEvolutionRun.intentThought", { content }) : content,
      payload,
      displayText: content,
    };
  }

  if (type === "checkpoint.wait") {
    const checkpointWait = buildCheckpointWaitPrompt(payload);
    return {
      key,
      timestamp,
      sequence,
      taskId: checkpointWait.taskId || taskId,
      type,
      payload,
      content: checkpointWait.message,
      displayText: checkpointWait.message,
      checkpointWait,
    };
  }

  if (type === "checkpoint.created" || type === "checkpoint.continue" || type === "checkpoint.cancel") {
    const checkpointId = getStringField(payload, ["checkpoint_id"]);
    const displayText =
      type === "checkpoint.created"
        ? checkpointId
          ? t("selfEvolutionRun.checkpointSavedWithId", { checkpointId })
          : t("selfEvolutionRun.checkpointSaved")
        : type === "checkpoint.cancel"
          ? t("selfEvolutionRun.checkpointCanceled")
          : t("selfEvolutionRun.checkpointContinued");
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      content: displayText,
      displayText,
    };
  }

  if (action === "failed") {
    const checkpointWait = buildFailureRetryPrompt(stage, payload);
    return {
      key,
      timestamp,
      sequence,
      taskId: checkpointWait.taskId || taskId,
      type,
      stage,
      action,
      payload,
      content: checkpointWait.message,
      displayText: checkpointWait.message,
      checkpointWait,
    };
  }

  if (!stage) {
    const fallbackText = content || compactPayloadForDisplay(payload);
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      content: fallbackText,
      displayText: fallbackText || (type === "message" ? "" : type),
    };
  }

  const _eventActionLabels = getEventActionLabels();
  const _stageLabels = getStageLabels();
  const actionLabel = action ? _eventActionLabels[action] || action : t("selfEvolutionRun.eventUpdate");
  const detail = content || compactPayloadForDisplay(payload);
  const displayText =
    (stage === "dataset" && buildDatasetEventDisplayText(action, payload)) ||
    (stage === "analysis" && buildAnalysisEventDisplayText(action, type, payload)) ||
    (stage === "repair" && buildApplyEventDisplayText(action, type, payload)) ||
    (stage === "eval" && buildEvalEventDisplayText(action, type, payload)) ||
    (stage === "abtest" && buildAbtestEventDisplayText(action, payload)) ||
    (stage === "dataset" && t("selfEvolutionRun.datasetRunning")) ||
    (detail ? t("selfEvolutionRun.stageActionDetail", { stage: _stageLabels[stage], action: actionLabel, detail }) : t("selfEvolutionRun.stageAction", { stage: _stageLabels[stage], action: actionLabel }));
  const progress = getWorkflowProgressSnapshot(stage, action, payload, type);
  const progressPhase = stage === "eval" ? getEvalPayloadPhase(action, type, payload) : undefined;

  return {
    key,
    timestamp,
    sequence,
    taskId,
    type,
    stage,
    action,
    payload,
    content: detail,
    displayText: progress ? undefined : displayText,
    progress,
    progressPhase,
  };
}

export function compareNormalizedThreadEvents(a: NormalizedThreadEvent, b: NormalizedThreadEvent) {
  if (typeof a.sequence === "number" && typeof b.sequence === "number" && a.sequence !== b.sequence) {
    return a.sequence - b.sequence;
  }

  if (a.timestamp && b.timestamp) {
    const aTime = new Date(a.timestamp).getTime();
    const bTime = new Date(b.timestamp).getTime();
    if (!Number.isNaN(aTime) && !Number.isNaN(bTime) && aTime !== bTime) {
      return aTime - bTime;
    }
  }

  return a.key.localeCompare(b.key, "zh-CN", { numeric: true });
}

export function getNormalizedEventDedupeKey(event: NormalizedThreadEvent) {
  const eventId = getStringField(event.payload, ["event_id"]);
  const caseId = getEventCaseId(event.payload);
  return [
    getStringField(event.payload, ["thread_id"]) || "",
    eventId || "",
    caseId || "",
    typeof event.sequence === "number" ? String(event.sequence) : "",
    event.taskId || "",
    event.type,
    event.timestamp || "",
  ]
    .filter(Boolean)
    .join(":") || event.key;
}

export function dedupeNormalizedEvents(events: NormalizedThreadEvent[]) {
  return Array.from(new Map(events.map((item) => [getNormalizedEventDedupeKey(item), item])).values()).sort(compareNormalizedThreadEvents);
}

export function isThreadEventAfter(
  baseEvent: Pick<NormalizedThreadEvent, "sequence" | "timestamp" | "key">,
  candidateEvent: Pick<NormalizedThreadEvent, "sequence" | "timestamp" | "key">,
) {
  if (
    typeof baseEvent.sequence === "number" &&
    typeof candidateEvent.sequence === "number" &&
    baseEvent.sequence !== candidateEvent.sequence
  ) {
    return candidateEvent.sequence > baseEvent.sequence;
  }
  if (baseEvent.timestamp && candidateEvent.timestamp) {
    const baseTime = new Date(baseEvent.timestamp).getTime();
    const candidateTime = new Date(candidateEvent.timestamp).getTime();
    if (!Number.isNaN(baseTime) && !Number.isNaN(candidateTime) && baseTime !== candidateTime) {
      return candidateTime > baseTime;
    }
  }
  return compareNormalizedThreadEvents(baseEvent as NormalizedThreadEvent, candidateEvent as NormalizedThreadEvent) < 0;
}
