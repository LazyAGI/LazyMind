import {
  ChatConversationsResponseFinishReasonEnum,
  type ChatHistory as BaseChatHistory,
  type Query,
  type Source,
} from "@/api/generated/chatbot-client";
import type { ConversationHistoryItem as CoreConversationHistoryItem } from "@/api/generated/core-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { splitThinkingContent } from "@/modules/chat/utils/thinking";

const CITE_MESSAGE_PATTERN =
  /<cite_message>([\s\S]*?)<\/cite_message>\s*/i;
const CITE_MESSAGE_GLOBAL_PATTERN =
  /<cite_message>([\s\S]*?)<\/cite_message>\s*/gi;

interface ChatUserMessageLike {
  delta?: string;
  inputs?: Query[] | null;
}

export type ConversationHistoryRecord = Omit<
  Partial<BaseChatHistory>,
  "feed_back" | "input" | "sources"
> &
  Omit<
    Partial<CoreConversationHistoryItem>,
    "feed_back" | "input" | "sources"
  > & {
    feed_back?: BaseChatHistory["feed_back"] | number | string;
    input?: Query[] | Array<Record<string, unknown>> | null;
    sources?: Source[] | Array<Record<string, unknown>>;
    second_id?: string;
    second_reasoning_content?: string;
    second_result?: string;
    thinking_time_s?: number | string;
    second_thinking_time_s?: number | string;
  };

interface BuildChatMessageListOptions {
  fallbackCreateTime?: string;
  isGenerating?: boolean;
  reverseHistory?: boolean;
  stripCitations?: boolean;
}

export function normalizeMessageInputs(
  inputs?: Query[] | null,
  fallbackText?: string,
): Query[] {
  const normalizedInputs = Array.isArray(inputs)
    ? inputs
        .filter((item): item is Query => !!item)
        .map((item) => ({ ...item }))
    : [];

  const trimmedFallbackText = fallbackText?.trim();
  const hasTextInput = normalizedInputs.some((item) => {
    const inputType = item.input_type || "text";
    return inputType === "text" && !!item.text?.trim();
  });

  if (!hasTextInput && trimmedFallbackText) {
    normalizedInputs.unshift({
      input_type: "text",
      text: fallbackText,
    });
  }

  return normalizedInputs;
}

export function getRegenerationInputs(
  userMessage?: ChatUserMessageLike,
): Query[] {
  if (!userMessage) {
    return [];
  }

  return normalizeMessageInputs(userMessage.inputs, userMessage.delta);
}

export function getCitationFromText(text?: string) {
  return text?.match(CITE_MESSAGE_PATTERN)?.[1]?.trim() || "";
}

export function getCitationsFromText(text?: string) {
  return Array.from((text || "").matchAll(CITE_MESSAGE_GLOBAL_PATTERN))
    .map((match) => match[1]?.trim())
    .filter(Boolean);
}

export function stripCitationFromText(text?: string) {
  return (text || "").replace(CITE_MESSAGE_GLOBAL_PATTERN, "").trim();
}

export function buildChatMessageListFromHistory(
  history?: ConversationHistoryRecord[] | null,
  options: BuildChatMessageListOptions = {},
) {
  const {
    fallbackCreateTime = "",
    isGenerating = false,
    reverseHistory = true,
    stripCitations = true,
  } = options;
  const records = Array.isArray(history)
    ? reverseHistory
      ? [...history].reverse()
      : history
    : [];
  const lastRecord = records[records.length - 1];
  const list: any[] = [];

  records.forEach((record) => {
    const normalizedInputs = normalizeMessageInputs(
      record.input as Query[] | null | undefined,
      record.query,
    );
    const textInput = normalizedInputs.find((input) => {
      const inputType = input.input_type || "text";
      return inputType === "text" && !!input.text;
    });
    const rawQuery = record.query || textInput?.text || "";
    const citeMessages = getCitationsFromText(rawQuery);
    const displayQuery = stripCitations
      ? stripCitationFromText(rawQuery)
      : rawQuery;

    list.push({
      role: RoleTypes.USER,
      delta: displayQuery,
      display_delta: displayQuery,
      cite_message: citeMessages.join("\n\n"),
      cite_messages: citeMessages,
      images: normalizedInputs
        ?.filter((input) => input.input_type === "image")
        .map((image) => ({
          base64: image?.input_base64,
          uid: image.file_id,
        })),
      files: normalizedInputs
        ?.filter((input) => input.input_type === "file")
        .map((file) => ({
          name: file?.uri?.split("/").pop(),
          uid: file.file_id,
        })),
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      inputs: normalizedInputs,
      create_time: record.create_time || fallbackCreateTime,
    });

    const isLastRecord = record === lastRecord;
    const isActuallyGenerating =
      isGenerating && isLastRecord && !record.result;
    const splitResult = splitThinkingContent(
      record.result,
      record.reasoning_content,
    );
    const assistantMessage: any = {
      role: RoleTypes.ASSISTANT,
      reasoning_content: splitResult.reasoning_content,
      delta: splitResult.content,
      raw_delta: record.result || "",
      finish_reason: isActuallyGenerating
        ? ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
        : ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      history_id: record.id,
      sources: record.sources,
      feed_back: record.feed_back,
      thinking_time_s: record.thinking_time_s,
    };

    list.push(assistantMessage);
  });

  const lastAssistant = list[list.length - 1];
  if (
    isGenerating &&
    (!lastAssistant ||
      lastAssistant.finish_reason ===
        ChatConversationsResponseFinishReasonEnum.FinishReasonStop)
  ) {
    list.push({
      role: RoleTypes.USER,
      delta: "",
      finish_reason: ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      inputs: [],
      is_resumed: true,
    });
    list.push({
      role: RoleTypes.ASSISTANT,
      delta: "",
      reasoning_content: "",
      finish_reason:
        ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified,
      answers: [],
      sources: [],
    });
  }

  return list;
}

/** Prefer the longer list when switching back to a conversation with an active stream. */
export function mergeChatMessageLists(apiList: any[] = [], cachedList?: any[] | null) {
  const api = Array.isArray(apiList) ? apiList : [];
  const cached = Array.isArray(cachedList) ? cachedList : [];
  if (cached.length > api.length) {
    return cached;
  }
  return api;
}
