import type { ChatConfig } from "../ChatConfigs";
import {
  THINKING_DEPTH_VALUES,
  type ThinkingDepth,
} from "@/modules/chat/store/chatThink";
import { buildEnvironmentContext } from "@/modules/chat/utils/environment";
import type {
  SideChatConversation,
  SideChatCreateBody,
  SideChatSource,
  SideChatStreamPayloadOptions,
} from "./types";

function asString(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim();
  return normalized || undefined;
}

function asNumber(value: unknown): number | undefined {
  const normalized = Number(value);
  return Number.isFinite(normalized) ? normalized : undefined;
}

function normalizeThinkingDepth(value: unknown): ThinkingDepth {
  return THINKING_DEPTH_VALUES.includes(value as ThinkingDepth)
    ? (value as ThinkingDepth)
    : "medium";
}

export function buildSideChatCreateBody(
  source?: SideChatSource | null,
  thinkingDepth?: ThinkingDepth,
): SideChatCreateBody {
  const historyId = asString(source?.historyId);
  const rawSelectedText = asString(source?.selectedText);
  const selectedText = rawSelectedText
    ? Array.from(rawSelectedText).slice(0, 16_000).join("")
    : undefined;
  const sequence = asNumber(source?.sequence);
  return {
    ...(historyId ? { source_history_id: historyId } : {}),
    ...(sequence && sequence > 0 ? { source_seq: sequence } : {}),
    ...(selectedText ? { selected_text: selectedText } : {}),
    ...(thinkingDepth ? { thinking_depth: thinkingDepth } : {}),
  };
}

export function normalizeSideChatConversation(
  value: unknown,
): SideChatConversation {
  const envelope = value as any;
  const raw =
    envelope?.data?.conversation ??
    envelope?.conversation ??
    envelope?.data?.data?.conversation ??
    envelope?.data ??
    envelope;
  const id = asString(raw?.id) ?? asString(raw?.conversation_id);
  if (!id) {
    throw new Error("Side chat response is missing a conversation id");
  }
  const relationType = asString(raw?.relation_type);
  if (relationType && relationType !== "sidechat") {
    throw new Error("Side chat response has an invalid relation type");
  }
  return {
    id,
    displayName: asString(raw?.display_name) ?? "",
    parentConversationId: asString(raw?.parent_conversation_id) ?? "",
    parentDisplayName: asString(raw?.parent_display_name),
    relationType: "sidechat",
    sourceHistoryId: asString(raw?.source_history_id),
    sourceSequence: asNumber(raw?.source_seq),
    selectedText: asString(raw?.selected_text),
    sourceContext:
      Array.isArray(raw?.source_context) ||
      (raw?.source_context && typeof raw.source_context === "object")
        ? raw.source_context
        : undefined,
    searchConfig:
      raw?.search_config && typeof raw.search_config === "object"
        ? raw.search_config
        : undefined,
    chatModelMode: asString(raw?.chat_model_mode),
    chatModelId: asString(raw?.chat_model_id),
    chatModelVersion: asNumber(raw?.chat_model_version),
    thinkingDepth: normalizeThinkingDepth(raw?.thinking_depth),
    isEphemeral:
      typeof raw?.is_ephemeral === "boolean" ? raw.is_ephemeral : undefined,
  };
}

function datasetIds(searchConfig?: Record<string, unknown>): string[] {
  if (!searchConfig) return [];
  const rawList = searchConfig.dataset_list ?? searchConfig.dataset_ids;
  if (!Array.isArray(rawList)) return [];
  return rawList
    .map((entry) => {
      if (typeof entry === "string") return entry.trim();
      if (entry && typeof entry === "object") {
        return asString((entry as { id?: unknown }).id) ?? "";
      }
      return "";
    })
    .filter(Boolean);
}

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.map(asString).filter((item): item is string => Boolean(item));
}

export function chatConfigFromSideChat(
  conversation: SideChatConversation,
): ChatConfig {
  const config = conversation.searchConfig;
  const databaseIds = stringList(config?.database_ids);
  return {
    knowledgeBaseId: datasetIds(config),
    creators: stringList(config?.creators),
    tags: stringList(config?.tags),
    databaseBaseId: databaseIds[0],
  };
}

export function sideChatSourceText(
  source: SideChatSource | null | undefined,
  conversation?: SideChatConversation | null,
): string {
  const selected = asString(source?.selectedText) ?? conversation?.selectedText;
  if (selected) return selected;
  const rawContext = conversation?.sourceContext;
  const messages = Array.isArray(rawContext)
    ? rawContext
    : Array.isArray(rawContext?.messages)
      ? rawContext.messages
      : [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const item = messages[index] as Record<string, unknown> | undefined;
    const content = asString(item?.content) ?? asString(item?.text);
    if (content) return content;
  }
  return "";
}

export function buildSideChatStreamPayload({
  conversationId,
  action,
  input,
  thinkingDepth,
  chatConfig,
  modelLabel,
  locale,
  createTime,
  clientRequestId,
}: SideChatStreamPayloadOptions) {
  return {
    action,
    conversation_id: conversationId,
    conversation: {
      search_config: {
        dataset_list: (chatConfig.knowledgeBaseId ?? []).map((id) => ({ id })),
        database_ids: chatConfig.databaseBaseId
          ? [chatConfig.databaseBaseId]
          : [],
        creators: chatConfig.creators ?? [],
        tags: chatConfig.tags ?? [],
      },
    },
    models: [modelLabel],
    thinking_depth: thinkingDepth,
    stream: true,
    input,
    mode: "auto",
    basic_chat_only: true,
    use_memory: false,
    ...(clientRequestId ? { client_request_id: clientRequestId } : {}),
    create_time: createTime ?? new Date().toISOString(),
    environment_context: buildEnvironmentContext(locale),
  };
}

export function sideChatSourceKey(source?: SideChatSource | null): string {
  return JSON.stringify(buildSideChatCreateBody(source));
}
