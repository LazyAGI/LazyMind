import { isRecord, getEventPayloadData, getNestedRecordField, getNumberField, getStringField } from "./fields";

const PROJECTION_ACTION_MAP: Record<string, string> = {
  completed: "finish",
  running: "progress",
  started: "start",
  failed: "failed",
  canceled: "cancel",
  cancelled: "cancel",
  paused: "pause",
  skipped: "finish",
};

export function mapProjectionAction(action: string | undefined) {
  if (!action) {
    return undefined;
  }
  return PROJECTION_ACTION_MAP[action] || action;
}

export function isProjectionEventName(eventName: string | undefined) {
  if (!eventName || eventName === "message" || eventName === "done") {
    return false;
  }
  if (eventName.startsWith("checkpoint.") || eventName.startsWith("autooperator.")) {
    return false;
  }
  return eventName.includes(".");
}

export function getProjectionEventType(
  payload: Record<string, unknown> | undefined,
  eventName?: string,
) {
  const fromPayload = getStringField(payload, ["event_type", "eventType"]);
  if (fromPayload) {
    if (fromPayload === "done" || !isProjectionEventName(fromPayload)) {
      return undefined;
    }
    return fromPayload;
  }
  return isProjectionEventName(eventName) ? eventName : undefined;
}

export function enrichProjectionEventPayload(
  payload: Record<string, unknown>,
  projectionEventType: string,
): Record<string, unknown> {
  const enriched: Record<string, unknown> = { ...payload };
  const caseRecord = getNestedRecordField(payload, ["case"]);
  const artifactRecord = getNestedRecordField(payload, ["artifact"]);
  const progressRecord = getNestedRecordField(payload, ["progress"]);
  const summaryRecord = getNestedRecordField(payload, ["summary"]);
  const dataRecord = {
    ...(getEventPayloadData(payload) || {}),
  };

  dataRecord.flow_kind = projectionEventType;
  dataRecord.operation_run_id = projectionEventType;

  const caseId = getStringField(caseRecord, ["id"]) || getStringField(payload, ["case_id"]);
  if (caseId) {
    dataRecord.case_id = caseId;
  }
  if (caseRecord) {
    for (const field of ["question", "answer", "question_type", "difficulty", "source"] as const) {
      const value = getStringField(caseRecord, [field]);
      if (value) {
        dataRecord[field] = value;
      }
    }
  }

  if (progressRecord) {
    const current = getNumberField(progressRecord, ["current"]);
    const total = getNumberField(progressRecord, ["total"]);
    if (typeof current === "number") {
      dataRecord.current = current;
    }
    if (typeof total === "number") {
      dataRecord.total = total;
      dataRecord.case_num = total;
    }
  }

  if (artifactRecord) {
    const artifactRef =
      getStringField(artifactRecord, ["ref"]) ||
      getStringField(artifactRecord, ["id"]);
    if (artifactRef) {
      dataRecord.artifact_id = artifactRef;
      dataRecord.runtime_artifact_id = artifactRef;
    }
  }

  if (summaryRecord) {
    enriched.summary = summaryRecord;
  }

  const mappedAction = mapProjectionAction(getStringField(payload, ["action"]));
  if (mappedAction) {
    enriched.action = mappedAction;
  }

  enriched.event_type = projectionEventType;
  if (!getStringField(enriched, ["stage"])) {
    const stagePrefix = projectionEventType.split(".")[0];
    if (stagePrefix && stagePrefix !== "step") {
      enriched.stage = stagePrefix;
    }
  }

  if (isRecord(payload.data)) {
    enriched.data = { ...payload.data, ...dataRecord };
  } else {
    enriched.data = dataRecord;
  }

  return enriched;
}
