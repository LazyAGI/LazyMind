import {
  ChatConversationsResponseFinishReasonEnum,
  type ChatHistory as BaseChatHistory,
  type Query,
} from "@/api/generated/chatbot-client";
import type {
  ConversationHistoryItem as CoreConversationHistoryItem,
  ConversationTrailItem,
} from "@/api/generated/core-client";
import { RoleTypes } from "@/modules/chat/constants/common";
import { splitThinkingContent } from "@/modules/chat/utils/thinking";
import type { ChatMention } from "@/modules/chat/components/ChatInput/MentionEditor";
import type { ChatSourceCollection } from "@/modules/chat/utils/sourceAdapter";

const CITE_MESSAGE_PATTERN = /<cite_message>([\s\S]*?)<\/cite_message>\s*/i;
const CITE_MESSAGE_GLOBAL_PATTERN =
  /<cite_message>([\s\S]*?)<\/cite_message>\s*/gi;
const ASK_USER_RECEIPT_PATTERN =
  /^Question sent to user \(ask_id=[^)]+\)\.\s*Waiting for answer on next turn\.?$/i;
const CODEX_REQUEST_MARKER_PATTERN = /^#+\s*My request for Codex:\s*$/im;
const CODEX_IMAGE_TAG_PATTERN = /<\/?image\b[^>]*>/gi;

export function normalizeImportedUserText(text: string | undefined) {
  const value = text || "";
  const marker = CODEX_REQUEST_MARKER_PATTERN.exec(value);
  if (!marker) return value;
  return value
    .slice(marker.index + marker[0].length)
    .replace(CODEX_IMAGE_TAG_PATTERN, "")
    .trim();
}

export function stripAskUserReceipt(
  text: string | undefined,
  hasAskPending: boolean,
) {
  const content = text || "";
  return hasAskPending && ASK_USER_RECEIPT_PATTERN.test(content.trim())
    ? ""
    : content;
}

export function isAskPendingReadOnly(
  askAnswered: boolean | undefined,
  isLatestMessage: boolean,
  hasLaterUserMessage = false,
) {
  return !!askAnswered || (!isLatestMessage && hasLaterUserMessage);
}

interface ChatUserMessageLike {
  delta?: string;
  inputs?: Query[] | null;
}

export type ConversationHistoryRecord = Omit<
  Partial<BaseChatHistory>,
  "feed_back" | "input" | "sources" | "execution"
> &
  Omit<
    Partial<CoreConversationHistoryItem>,
    "feed_back" | "input" | "sources" | "execution"
  > & {
    feed_back?: BaseChatHistory["feed_back"] | number | string;
    input?: Query[] | Array<Record<string, unknown>> | null;
    sources?: ChatSourceCollection | Array<Record<string, unknown>>;
    second_id?: string;
    second_reasoning_content?: string;
    second_result?: string;
    thinking_time_s?: number | string;
    second_thinking_time_s?: number | string;
    tool_call_turns?: number | string;
    mentions?: ChatMention[] | null;
    execution?: ExternalExecutionProjection;
    external_user_only?: boolean;
    run_id?: string;
    run_status?: "completed" | "interrupted" | "failed" | "cancelled";
    run_terminal?: Record<string, unknown>;
  };

export interface ExternalExecutionProjection {
  run_id: string;
  history_id: string;
  provider: string;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  host_id?: string;
  host_online: boolean;
  claim_count: number;
  recovery_count: number;
  event_count: number;
  invocation: {
    total: number;
    running: number;
    succeeded: number;
    failed: number;
    interrupted: number;
    tools: string[];
  };
  workflows: Array<{
    session_id: string;
    workflow_id: string;
    status: string;
    current_step_id?: string;
    state_version: number;
    artifact_count: number;
    artifact_revision_count: number;
  }>;
  artifact_count: number;
  artifact_revision_count: number;
  error_message?: string;
  claimed_at?: string;
  last_heartbeat_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export type ConversationTrailRecord = ConversationTrailItem & {
  history_id?: string;
  seq?: number;
  depth?: number;
  parent_history_id?: string;
  source?: string;
  summary?: string;
  question?: string;
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
    const rawQuery = normalizeImportedUserText(record.query || textInput?.text || "");
    const citeMessages = getCitationsFromText(rawQuery);
    const displayQuery = stripCitations
      ? stripCitationFromText(rawQuery)
      : rawQuery;

    list.push({
      role: RoleTypes.USER,
      history_id: record.id,
      seq: record.seq,
      delta: displayQuery,
      display_delta: displayQuery,
      cite_message: citeMessages.join("\n\n"),
      cite_messages: citeMessages,
      images: normalizedInputs
        ?.filter((input) => input.input_type === "image")
        .map((image) => ({
          base64: image?.input_base64 || image?.uri,
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
      mentions: Array.isArray(record.mentions) ? record.mentions : [],
      collected_inputs: Array.isArray((record as any).collected_inputs)
        ? (record as any).collected_inputs
        : [],
    });

    if (record.external_user_only) {
      return;
    }

    const isLastRecord = record === lastRecord;
    const isActuallyGenerating = isGenerating && isLastRecord;
    const splitResult = splitThinkingContent(
      record.result,
      record.reasoning_content,
    );
    const hasAskPending = !!(record as any).ask_pending;
    const displayAssistantContent = stripAskUserReceipt(
      splitResult.content,
      hasAskPending,
    );
    const assistantMessage: any = {
      role: RoleTypes.ASSISTANT,
      reasoning_content: splitResult.reasoning_content,
      delta: displayAssistantContent,
      raw_delta: record.result || "",
      finish_reason: isActuallyGenerating
        ? ChatConversationsResponseFinishReasonEnum.FinishReasonUnspecified
        : ChatConversationsResponseFinishReasonEnum.FinishReasonStop,
      history_id: record.id,
      sources: record.sources,
      feed_back: record.feed_back,
      thinking_time_s: record.thinking_time_s,
      tool_call_turns: record.tool_call_turns,
      intent_updated: (record as any).intent_updated,
      execution: record.execution,
      run_id: record.run_id,
      run_status: isActuallyGenerating ? undefined : record.run_status,
      run_terminal: isActuallyGenerating ? undefined : record.run_terminal,
    };

    // Restore ask_pending from persisted ext so the AskCard is visible after page reload.
    if (hasAskPending) {
      assistantMessage.ask_pending = (record as any).ask_pending;
      // Restore partially-filled answers so the wizard resumes where the user left off.
      if ((record as any).ask_saved_answers) {
        assistantMessage.ask_saved_answers = (record as any).ask_saved_answers;
      }
      // Mark as answered so the card is disabled when the user already replied.
      if ((record as any).ask_answered) {
        assistantMessage.ask_answered = true;
      }
    }

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

export function mergeConversationTrailIntoMessageList(
  messageList: any[],
  trailItems?: ConversationTrailRecord[] | null,
) {
  if (!Array.isArray(trailItems) || trailItems.length === 0) {
    return messageList;
  }
  const trailByHistoryID = new Map(
    trailItems
      .filter((item) => Boolean(item?.history_id))
      .map((item) => [item.history_id as string, item]),
  );
  if (trailByHistoryID.size === 0) {
    return messageList;
  }

  let changed = false;
  const mergedList = messageList.map((item) => {
    const historyID = item?.history_id || item?.id;
    const trail = historyID ? trailByHistoryID.get(historyID) : undefined;
    if (!trail) {
      return item;
    }
    if (item?.role === RoleTypes.ASSISTANT) {
      return item;
    }
    const nextItem = {
      ...item,
      seq: trail.seq ?? item.seq,
      trail_depth: trail.depth ?? 0,
      trail_parent_history_id: trail.parent_history_id || "",
      trail_source: trail.source || "",
      trail_summary: trail.summary || "",
      trail_question: trail.question || "",
      cite_history_ids:
        item.cite_history_ids ??
        (trail.parent_history_id ? [trail.parent_history_id] : undefined),
    };
    if (
      nextItem.seq !== item.seq ||
      nextItem.trail_depth !== item.trail_depth ||
      nextItem.trail_parent_history_id !== item.trail_parent_history_id ||
      nextItem.trail_source !== item.trail_source ||
      nextItem.trail_summary !== item.trail_summary ||
      nextItem.trail_question !== item.trail_question ||
      nextItem.cite_history_ids !== item.cite_history_ids
    ) {
      changed = true;
    }
    return nextItem;
  });
  return changed ? mergedList : messageList;
}

/** Prefer cached (in-memory) list over API list when switching back to a
 * conversation with an active stream. The cache always reflects the latest
 * client-side state (including edits and truncations), whereas the API list
 * may lag behind or contain messages that were already truncated by the user.
 * Fall back to the API list only when the cache is empty. */
export function mergeChatMessageLists(
  apiList: any[] = [],
  cachedList?: any[] | null,
) {
  const api = Array.isArray(apiList) ? apiList : [];
  const cached = Array.isArray(cachedList) ? cachedList : [];
  if (cached.length === 0) {
    return api;
  }
  if (api.length === 0) {
    return cached;
  }

  const messageKey = (item: any) => item?.history_id || item?.id || "";
  const apiKeys = new Set(api.map(messageKey).filter(Boolean));
  const hasPersistedOverlap = cached.some((item) =>
    apiKeys.has(messageKey(item)),
  );
  if (!hasPersistedOverlap) {
    return cached;
  }

  const apiUserTexts = new Set(
    api
      .filter((item) => item?.role === RoleTypes.USER)
      .map((item) => String(item?.display_delta || item?.delta || "").trim())
      .filter(Boolean),
  );
  const apiAssistantTexts = new Set(
    api
      .filter((item) => item?.role === RoleTypes.ASSISTANT)
      .map((item) => String(item?.raw_delta || item?.delta || "").trim())
      .filter(Boolean),
  );
  const apiHasAskPending = api.some(
    (item) => item?.role === RoleTypes.ASSISTANT && item?.ask_pending,
  );

  const unpersistedTail = cached.filter((item) => {
    const key = messageKey(item);
    if (key) {
      return !apiKeys.has(key);
    }
    const text = String(
      item?.display_delta || item?.raw_delta || item?.delta || "",
    ).trim();
    if (item?.role === RoleTypes.USER && text && apiUserTexts.has(text)) {
      return false;
    }
    if (item?.role === RoleTypes.ASSISTANT) {
      if (item?.ask_pending && apiHasAskPending) {
        return false;
      }
      if (text && apiAssistantTexts.has(text)) {
        return false;
      }
    }
    return true;
  });

  return [...api, ...unpersistedTail];
}
