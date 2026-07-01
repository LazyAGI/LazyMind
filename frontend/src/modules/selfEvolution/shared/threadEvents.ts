import { type ChatStreamDeltaKind, type NormalizedThreadEvent, type ThreadEventFrame, type ThreadEventStage } from "./types";
import { failedThreadEventTypes, inactiveTerminalThreadStatuses, terminalThreadEventTypes } from "./constants";
import { getEventActionLabels, getStageLabels, t } from "./i18n";
import { getEventPayloadData, getNumberField, getOperationRunId, getStringField, getThreadEventContentFromPayload, getThreadEventPayloadEnvelope, getThreadEventTypeFromPayload, isRecord } from "./fields";
import { buildCheckpointWaitPrompt, buildFailureRetryPrompt } from "./checkpoint";
import { buildAbtestEventDisplayText, buildAnalysisEventDisplayText, buildApplyEventDisplayText, buildDatasetEventDisplayText, buildEvalEventDisplayText, compactPayloadForDisplay } from "./eventDisplay";
import { getEvalPayloadPhase, getWorkflowProgressSnapshot } from "./progress";

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
  const payload = parseThreadEventPayload(frame.data);
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const payloadType = getThreadEventTypeFromPayload(payload);
  const eventType = payloadType || (frame.eventName !== "message" ? frame.eventName : "");
  const [typeStage, ...actionParts] = eventType.split(".");
  const isCheckpointEvent = eventType.startsWith("checkpoint.");
  const isAutoOperatorEvent = eventType.startsWith("autooperator.");
  const operationRunId = getOperationRunId(payload);
  const stageFromPayload =
    (operationRunId?.startsWith("candidate_eval.") ? "abtest" : undefined) ||
    toThreadEventStage(payload?.stage) ||
    toThreadEventStage(eventEnvelope?.stage);
  const stage = isCheckpointEvent ? undefined : stageFromPayload || (isAutoOperatorEvent ? undefined : toThreadEventStage(typeStage));
  const action = isCheckpointEvent
    ? actionParts.join(".") || undefined
    : isAutoOperatorEvent
      ? actionParts.join(".") || undefined
    : getStringField(payload, ["action"]) ||
      getStringField(eventEnvelope, ["action"]) ||
      (stage && actionParts.length > 0
        ? actionParts.join(".")
        : stage && eventType && !toThreadEventStage(eventType) && eventType !== "message"
          ? eventType
          : undefined);
  const type = isCheckpointEvent || isAutoOperatorEvent ? eventType : stage && action ? `${stage}.${action}` : eventType || "message";
  const role = type === "message.user" ? "user" : type === "message.assistant" ? "assistant" : undefined;
  const content = getThreadEventContentFromPayload(payload) || (!payload ? frame.data.trim() : undefined);
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
  const key =
    frame.id ||
    [
      getStringField(payload, ["thread_id"]) || getStringField(eventEnvelope, ["thread_id"]),
      typeof sequence === "number" ? String(sequence) : "",
      taskId || messageEventId,
      type,
      timestamp,
    ]
      .filter(Boolean)
      .join(":") ||
    `${type}:${frame.data}`;

  if (isTerminalThreadEvent(frame.eventName) || isTerminalThreadEvent(type)) {
    return {
      key,
      timestamp,
      sequence,
      taskId,
      type,
      payload,
      displayText: t("selfEvolutionRun.eventStreamEnded"),
    };
  }

  if (isFailedThreadEvent(frame.eventName) || isFailedThreadEvent(type)) {
    const errorText = content || t("selfEvolutionRun.messageProcessFailed");
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
      type,
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
  return [
    getStringField(event.payload, ["thread_id"]) || "",
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
