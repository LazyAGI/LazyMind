import { type EvolutionMode, type ThreadRestorePayload } from "./types";
import { getNestedRecordField, getNestedStringField, isRecord } from "./fields";

export function getThreadTitleFromHistoryPayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  return getNestedStringField(payload, ["title"]);
}

export function getThreadTitleFromPayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  return (
    getNestedStringField(payload, ["title", "name", "thread_name"]) ||
    getNestedStringField(getNestedRecordField(payload, ["thread", "upstream", "data"]), [
      "title",
      "name",
      "thread_name",
    ])
  );
}

export function getThreadKnowledgeBaseId(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadPayload = getThreadPayloadFromRestorePayload(payload);
  const inputs =
    getNestedRecordField(threadPayload, ["inputs", "input", "config"]) ||
    getNestedRecordField(payload, ["inputs", "input", "config"]);
  return (
    getNestedStringField(threadPayload, ["kb_id", "knowledge_base_id", "dataset_id"]) ||
    getNestedStringField(payload, ["kb_id", "knowledge_base_id", "dataset_id"]) ||
    getNestedStringField(inputs, ["kb_id", "knowledge_base_id", "dataset_id"])
  );
}

export function getThreadPayloadFromRestorePayload(payload: ThreadRestorePayload) {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadRecord = getNestedRecordField(payload, ["thread"]);
  return (
    getNestedRecordField(threadRecord, ["thread_payload", "threadPayload", "payload"]) ||
    getNestedRecordField(payload, ["thread_payload", "threadPayload", "payload"])
  );
}

export function getThreadModeFromPayload(payload: ThreadRestorePayload): EvolutionMode | undefined {
  if (!isRecord(payload)) {
    return undefined;
  }

  const threadPayload = getThreadPayloadFromRestorePayload(payload);
  const inputs =
    getNestedRecordField(threadPayload, ["inputs", "input", "config"]) ||
    getNestedRecordField(payload, ["inputs", "input", "config"]);
  const modeValue =
    getNestedStringField(threadPayload, ["mode", "evolution_mode", "interaction_mode"]) ||
    getNestedStringField(payload, ["mode", "evolution_mode", "interaction_mode"]) ||
    getNestedStringField(inputs, ["mode", "evolution_mode", "interaction_mode"]);

  return modeValue === "auto" || modeValue === "interactive" ? modeValue : undefined;
}
