import { type ThreadRestorePayload } from "./types";

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function getStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "string" && value.trim()) {
      return value.trim();
    }
  }

  return undefined;
}

export function getNumberField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (typeof value === "number" && Number.isFinite(value)) {
      return value;
    }
    if (typeof value === "string" && value.trim() && Number.isFinite(Number(value))) {
      return Number(value);
    }
  }

  return undefined;
}

export function getResultItems(value: unknown): unknown[] {
  if (Array.isArray(value)) {
    return value;
  }
  if (!isRecord(value)) {
    return [];
  }

  for (const key of ["data", "results", "items", "records", "reports", "datasets", "diffs", "abtests", "files"]) {
    const nestedValue = value[key];
    if (Array.isArray(nestedValue)) {
      return nestedValue;
    }
  }

  return [];
}

export function isEmptyResultPayload(value: unknown) {
  if (value === undefined || value === null) {
    return true;
  }
  if (typeof value === "string") {
    return value.trim().length === 0;
  }
  if (Array.isArray(value)) {
    return value.length === 0 || value.every(isEmptyResultPayload);
  }
  if (isRecord(value)) {
    const nestedItems = getResultItems(value);
    return nestedItems.length === 0 && Object.keys(value).length === 0;
  }
  return false;
}

export function stringifyResultPayload(value: unknown) {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function getResultStringField(value: unknown, keys: string[]): string | undefined {
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  if (Array.isArray(value)) {
    for (const item of value) {
      const result = getResultStringField(item, keys);
      if (result) {
        return result;
      }
    }
    return undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  const directValue = getStringField(value, keys);
  if (directValue) {
    return directValue;
  }

  for (const key of ["data", "result", "report", "content", "payload"]) {
    const nestedValue = value[key];
    const nestedString = getResultStringField(nestedValue, keys);
    if (nestedString) {
      return nestedString;
    }
  }

  const firstItem = getResultItems(value).find(Boolean);
  return getResultStringField(firstItem, keys);
}

export function getResultDownloadPath(value: unknown) {
  if (Array.isArray(value)) {
    return getResultDownloadPath(value.find(Boolean));
  }

  return getResultStringField(value, [
    "file_url",
    "file_path",
    "relative_path",
    "stored_path",
    "artifact_path",
    "diff_artifact",
    "report_path",
  ]);
}

export function getNestedStringField(payload: Record<string, unknown> | undefined, keys: string[]) {
  const directValue = getStringField(payload, keys);
  if (directValue) {
    return directValue;
  }

  if (isRecord(payload?.data)) {
    return getStringField(payload.data, keys);
  }

  return undefined;
}

export function getNestedRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (isRecord(value)) {
      return value;
    }
  }

  if (isRecord(payload.data)) {
    return getNestedRecordField(payload.data, keys);
  }

  return undefined;
}

export function getNestedArrayField(payload: ThreadRestorePayload, keys: string[]): unknown[] {
  if (Array.isArray(payload)) {
    return payload;
  }
  if (!isRecord(payload)) {
    return [];
  }

  for (const key of keys) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value;
    }
  }

  for (const key of ["data", "upstream", "result", "results", "thread"]) {
    const nestedValue = payload[key];
    if (isRecord(nestedValue) || Array.isArray(nestedValue)) {
      const nestedArray = getNestedArrayField(nestedValue, keys);
      if (nestedArray.length > 0) {
        return nestedArray;
      }
    }
  }

  return [];
}

export function getEventPayloadData(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  if (isRecord(payload?.data)) {
    return payload.data;
  }
  return payload;
}

export function getThreadEventPayloadEnvelope(payload: Record<string, unknown> | undefined) {
  if (isRecord(payload?.payload)) {
    return payload.payload;
  }
  return undefined;
}

export function getThreadEventTypeFromPayload(payload: Record<string, unknown> | undefined) {
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const directTag = getStringField(payload, ["tag", "type"]);
  const nestedTag = getStringField(eventEnvelope, ["tag", "type"]);
  const eventName =
    getStringField(payload, ["event_name", "event", "kind", "name"]) ||
    getStringField(eventEnvelope, ["event_name", "event", "kind", "name"]);
  const stage =
    getStringField(payload, ["stage"]) ||
    getStringField(eventEnvelope, ["stage"]);

  if (!directTag && !nestedTag && stage === "message" && (eventName === "user" || eventName === "assistant")) {
    return `message.${eventName}`;
  }

  return directTag || nestedTag || eventName;
}

export function getThreadEventContentFromPayload(payload: Record<string, unknown> | undefined) {
  const eventEnvelope = getThreadEventPayloadEnvelope(payload);
  const eventPayload = getEventPayloadData(eventEnvelope) || getEventPayloadData(payload);

  return (
    getNestedStringField(payload, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventEnvelope, ["message", "content", "text", "reply", "thought", "delta"]) ||
    getNestedStringField(eventPayload, ["message", "content", "text", "reply", "thought", "delta"])
  );
}

export function getOperationRunId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  return getStringField(data, ["operation_run_id"]) || getStringField(getNestedRecordField(data, ["after"]) || getNestedRecordField(data, ["before"]), ["operation_run_id"]) ||
    getStringField(payload, ["operation_run_id"]);
}

export function getEventFlowKind(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  const value = getStringField(data, ["flow_kind"]) || getStringField(payload, ["flow_kind"]);
  return ({
    load_corpus: "dataset.load_corpus",
    build_corpus_snapshot: "dataset.build_corpus_snapshot",
    generate_case: "dataset.generate_case",
    assemble: "dataset.assemble",
  } as Record<string, string>)[value || ""] || value;
}

export function getEventCaseId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  return getStringField(data, ["case_id"]) || getStringField(payload, ["case_id"]);
}

export function getEventCaseProgress(payload: Record<string, unknown> | undefined): { current: number; total?: number } | undefined {
  const data = getEventPayloadData(payload);
  const current = getNumberField(data, ["case_index"]) ?? getNumberField(payload, ["case_index"]);
  return typeof current === "number" ? { current } : undefined;
}

export function getEventArtifactId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  const detail = getNestedRecordField(data, ["detail"]) || getStructuredRecordField(data, ["detail"]);
  return getStringField(data, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(detail, ["artifact_id", "writes_artifact_id"]) ||
    getStringField(payload, ["artifact_id", "writes_artifact_id"]);
}

export function getEventRuntimeArtifactId(payload: Record<string, unknown> | undefined) {
  const data = getEventPayloadData(payload);
  const detail = getNestedRecordField(data, ["detail"]) || getStructuredRecordField(data, ["detail"]);
  return getStringField(data, ["runtime_artifact_id", "source_artifact_id"]) ||
    getStringField(detail, ["runtime_artifact_id", "source_artifact_id"]) ||
    getStringField(payload, ["runtime_artifact_id", "source_artifact_id"]);
}

export function getEventDetailField(payload: Record<string, unknown> | undefined, keys: string[]) {
  const data = getEventPayloadData(payload);
  const detail = getNestedRecordField(data, ["detail"]) || getStructuredRecordField(data, ["detail"]);
  return getStringField(data, keys) || getStringField(detail, keys) || getStringField(payload, keys);
}

export function getPayloadCaseTotal(eventData: Record<string, unknown> | undefined) {
  const detail = getNestedRecordField(eventData, ["detail"]) || getStructuredRecordField(eventData, ["detail"]);
  return getNumberField(eventData, ["total", "num_cases", "case_count", "count"]) ||
    getNumberField(detail, ["total", "num_cases", "case_count", "count"]);
}

export function parseStructuredRecord(value: unknown): Record<string, unknown> | undefined {
  if (isRecord(value)) {
    return value;
  }
  if (typeof value !== "string") {
    return undefined;
  }

  const candidates: string[] = [];
  const trimmed = value.trim();
  if (trimmed) {
    candidates.push(trimmed);
  }

  const fencedMatch = trimmed.match(/```(?:json)?\s*([\s\S]*?)```/i);
  if (fencedMatch?.[1]?.trim()) {
    candidates.unshift(fencedMatch[1].trim());
  }

  const firstBrace = trimmed.indexOf("{");
  const lastBrace = trimmed.lastIndexOf("}");
  if (firstBrace >= 0 && lastBrace > firstBrace) {
    candidates.push(trimmed.slice(firstBrace, lastBrace + 1));
  }

  for (const candidate of candidates) {
    try {
      const parsed = JSON.parse(candidate);
      if (isRecord(parsed)) {
        return parsed;
      }
    } catch {
      continue;
    }
  }

  return undefined;
}

export function parseStructuredArray(value: unknown): unknown[] | undefined {
  if (Array.isArray(value)) {
    return value;
  }
  if (typeof value !== "string") {
    return undefined;
  }

  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

export function getStructuredRecordField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const parsed = parseStructuredRecord(payload[key]);
    if (parsed) {
      return parsed;
    }
  }

  return undefined;
}

export function getStructuredArrayField(payload: Record<string, unknown> | undefined, keys: string[]) {
  if (!payload) {
    return undefined;
  }

  for (const key of keys) {
    const value = payload[key];
    if (Array.isArray(value)) {
      return value;
    }
    const parsed = parseStructuredArray(value);
    if (parsed) {
      return parsed;
    }
  }

  return undefined;
}

export function getLastItem<T>(items: T[]): T | undefined {
  return items.length ? items[items.length - 1] : undefined;
}
