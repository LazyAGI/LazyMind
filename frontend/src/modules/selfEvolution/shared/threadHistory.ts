import { type ChatMessage, type NormalizedThreadEvent, type ThreadHistoryEntry, type ThreadRestorePayload } from "./types";
import { t } from "./i18n";
import { getNestedArrayField, getNestedRecordField, getNestedStringField, getNumberField, getStringField, isRecord } from "./fields";
import { formatThreadListTime, formatThreadTime, getThreadTimeSortValue } from "./format";
import { dedupeNormalizedEvents, normalizeThreadEvent } from "./threadEvents";

export function getThreadListItemTitle(item: Record<string, unknown>, threadId: string) {
  const payload = getNestedRecordField(item, ["thread_payload", "payload", "inputs", "input"]);
  return (
    getNestedStringField(item, ["title", "name", "thread_name", "display_name"]) ||
    getNestedStringField(payload, ["title", "name", "thread_name", "display_name", "kb_id", "dataset_id"]) ||
    t("selfEvolutionRun.sessionTitle", { prefix: threadId.slice(0, 8) })
  );
}

export function normalizeThreadListPayload(payload: unknown): ThreadHistoryEntry[] {
  const records = getNestedArrayField(payload as ThreadRestorePayload, ["threads", "items", "records", "data"]);

  return records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .reduce<ThreadHistoryEntry[]>((acc, item) => {
      const threadId = getNestedStringField(item, ["thread_id", "threadId", "id"]);
      if (!threadId) {
        return acc;
      }

      acc.push({
        threadId,
        title: getThreadListItemTitle(item, threadId),
        updatedAt: formatThreadListTime(
          item.updated_at || item.update_time || item.created_at || item.create_time || item.timestamp,
        ),
        status: getNestedStringField(item, ["status", "state"]),
      });
      return acc;
    }, []);
}

export function getDialogueEventAgentLabel(event: NormalizedThreadEvent) {
  if (event.type.startsWith("autooperator.")) {
    return "AutoOperator";
  }
  if (event.type === "message.user") {
    return t("selfEvolutionRun.simulatedUser");
  }
  if (event.type === "message.assistant") {
    return t("selfEvolutionRun.replyAgent");
  }
  return undefined;
}

export function buildAutoInteractionMessagesFromEvents(events: NormalizedThreadEvent[]): ChatMessage[] {
  return dedupeNormalizedEvents(events)
    .filter((event) => getDialogueEventAgentLabel(event) && (event.content || event.displayText))
    .map((event) => ({
      id: `event-chat-${event.key}`,
      role: event.role || "assistant",
      content: event.content || event.displayText || "",
      time: formatThreadTime(event.timestamp),
      sortTime:
        getThreadTimeSortValue(event.timestamp) ||
        (typeof event.sequence === "number" ? event.sequence : undefined),
      agentLabel: getDialogueEventAgentLabel(event),
    }))
    .sort((a, b) => {
      if (typeof a.sortTime === "number" && typeof b.sortTime === "number" && a.sortTime !== b.sortTime) {
        return a.sortTime - b.sortTime;
      }
      return a.id.localeCompare(b.id, "zh-CN", { numeric: true });
    });
}

export function getHistoryMessageContent(value: unknown): string | undefined {
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  if (Array.isArray(value)) {
    const text = value
      .map((item) => {
        if (typeof item === "string") {
          return item;
        }
        if (isRecord(item)) {
          return getHistoryMessageContent(item.text) || getHistoryMessageContent(item.content);
        }
        return "";
      })
      .filter(Boolean)
      .join("");
    return text.trim() || undefined;
  }
  if (!isRecord(value)) {
    return undefined;
  }

  return (
    getStringField(value, ["content", "message", "text", "reply", "answer", "input", "output"]) ||
    getHistoryMessageContent(value.content) ||
    getHistoryMessageContent(value.message) ||
    getHistoryMessageContent(value.data) ||
    getHistoryMessageContent(value.payload)
  );
}

export function getHistoryAssistantDeltaContent(value: unknown): string | undefined {
  if (Array.isArray(value)) {
    const text = value
      .map((item) => getHistoryAssistantDeltaContent(item))
      .filter(Boolean)
      .join("");
    return text.trim() || undefined;
  }

  if (isRecord(value)) {
    const type = getStringField(value, ["type", "event_name", "task_id"]);
    const delta = getStringField(value, ["delta", "content", "message"]);
    if ((type === "answer_delta" || type === "thinking_delta") && delta) {
      return delta;
    }
    return (
      getHistoryAssistantDeltaContent(value.records) ||
      getHistoryAssistantDeltaContent(value.events) ||
      getHistoryAssistantDeltaContent(value.data) ||
      getHistoryAssistantDeltaContent(value.payload) ||
      getHistoryAssistantDeltaContent(value.message)
    );
  }

  if (typeof value !== "string" || !value.trim()) {
    return undefined;
  }

  const deltas = value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice("data:".length).trim())
    .reduce<string[]>((acc, rawData) => {
      try {
        const payload = JSON.parse(rawData);
        if (!isRecord(payload)) {
          return acc;
        }
        const type = getStringField(payload, ["type"]);
        const delta = getStringField(payload, ["delta"]);
        if ((type === "answer_delta" || type === "thinking_delta") && delta) {
          acc.push(delta);
        }
      } catch {
        return acc;
      }
      return acc;
    }, []);

  return deltas.join("").trim() || undefined;
}

export function normalizeHistoryEventMessages(payload: ThreadRestorePayload): ChatMessage[] {
  const rounds = getNestedArrayField(payload, ["rounds"]);
  const nestedRoundRecords = rounds.flatMap((item) =>
    isRecord(item)
      ? [
          ...getNestedArrayField(item, ["messages"]),
          ...getNestedArrayField(item, ["events"]),
          ...getNestedArrayField(item, ["records"]),
          ...getNestedArrayField(item, ["history"]),
        ]
      : [],
  );
  const records = [
    ...getNestedArrayField(payload, ["messages"]),
    ...getNestedArrayField(payload, ["events"]),
    ...getNestedArrayField(payload, ["records"]),
    ...getNestedArrayField(payload, ["history"]),
    ...nestedRoundRecords,
  ];

  return records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .flatMap<ChatMessage>((item, index) => {
      const event = normalizeThreadEvent({
        eventName: "message",
        data: JSON.stringify(item),
      });
      const directRole = getStringField(item, ["role"]);
      const role =
        event.role ||
        (directRole === "user" || directRole === "assistant" ? directRole : undefined);
      const content =
        event.content ||
        getNestedStringField(item, ["content", "message", "text", "reply", "answer"]);

      if (!role || !content) {
        return [];
      }

      const sortTime =
        getThreadTimeSortValue(event.timestamp) ||
        (typeof event.sequence === "number" ? event.sequence : undefined) ||
        index;

      return [
        {
          id: `thread-history-event-${event.key || index}`,
          role,
          content,
          time: formatThreadTime(event.timestamp),
          sortTime,
          agentLabel: getDialogueEventAgentLabel(event),
        },
      ];
    });
}

export function dedupeAndSortChatMessages(messages: ChatMessage[]) {
  const seen = new Set<string>();
  return messages
    .filter((item) => {
      const key = `${item.role}:${item.content}:${item.sortTime ?? item.time}`;
      if (seen.has(key)) {
        return false;
      }
      seen.add(key);
      return true;
    })
    .sort((a, b) => {
      if (typeof a.sortTime === "number" && typeof b.sortTime === "number" && a.sortTime !== b.sortTime) {
        return a.sortTime - b.sortTime;
      }
      return a.id.localeCompare(b.id, "zh-CN", { numeric: true });
    });
}

export function normalizeThreadHistoryMessages(payload: ThreadRestorePayload): ChatMessage[] {
  const records = getNestedArrayField(payload, ["rounds"]);
  const roundMessages = records
    .filter((item): item is Record<string, unknown> => isRecord(item))
    .flatMap<ChatMessage>((item, index) => {
      const requestPayload = getNestedRecordField(item, ["request_payload"]);
      const userContent =
        getStringField(item, ["user_message", "userMessage"]) ||
        getHistoryMessageContent(requestPayload);
      const assistantContent =
        getStringField(item, ["assistant_message", "assistantMessage"]) ||
        getHistoryAssistantDeltaContent(item.records) ||
        getHistoryAssistantDeltaContent(item.assistant_message);
      const roundId = getStringField(item, ["round_id", "id"]) || `round-${index + 1}`;
      const createdAt = item.created_at || item.create_time || item.timestamp;
      const updatedAt = item.updated_at || item.update_time || createdAt;
      const baseSortTime =
        getThreadTimeSortValue(createdAt) ||
        getNumberField(item, ["sequence", "seq", "index"]) ||
        index * 2;
      const messages: ChatMessage[] = [];

      if (userContent) {
        messages.push({
          id: `thread-history-${roundId}-user-${index}`,
          role: "user",
          content: userContent,
          time: formatThreadTime(createdAt),
          sortTime: baseSortTime,
        });
      }

      if (assistantContent) {
        messages.push({
          id: `thread-history-${roundId}-assistant-${index}`,
          role: "assistant",
          content: assistantContent,
          time: formatThreadTime(updatedAt),
          sortTime: baseSortTime + 1,
        });
      }

      return messages;
    });

  return dedupeAndSortChatMessages([...normalizeHistoryEventMessages(payload), ...roundMessages]);
}
