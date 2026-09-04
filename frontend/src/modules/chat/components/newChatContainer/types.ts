import type { ReactNode } from "react";
import {
  ChatConversationsRequestActionEnum,
  Query,
} from "@/api/generated/chatbot-client";
import type { ChatSourceCollection } from "@/modules/chat/utils/sourceAdapter";
import type { SendMessageParams } from "../ChatInput/types";
import type { ChatMention } from "../ChatInput/MentionEditor";
import type { ChatConfig } from "../ChatConfigs";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";
import type { ChatModelRoute } from "@/api/generated/core-client";
import type { RunPerformanceMetrics } from "@/modules/chat/utils/performanceStats";

export interface ChatImperativeProps {
  replaceMessageList: (id: string, data: any[]) => void;
  createNewChat: () => void;
  sendMessage: (params: SendMessageParams) => void;
  prepareMessage: (
    params: Pick<SendMessageParams, "text" | "citeMessage" | "citeMessages"> & {
      appendCitations?: boolean;
    },
  ) => void;
  disconnectConversationStream?: (conversationId: string) => void;
  uploadFiles?: (files: File[]) => void;
  openResumeSSE?: (conversationId: string) => void;
  appendAutoAdvanceTurn?: (
    conversationId: string,
    driverMessage: string,
  ) => void;
  ensureAutoAdvanceUserTurn?: (
    conversationId: string,
    driverMessage: string,
  ) => void;
  focusInput?: () => void;
}

export interface ChatContainerProps {
  canChat?: boolean;
  initialCard?: ReactNode;
  sessionId?: string;
  onOpenSSE: (
    input: any[],
    action: ChatConversationsRequestActionEnum,
    callbacks: Record<string, (e: CustomEvent) => void>,
    extras?: Record<string, unknown>,
  ) => any;
  onOpenResumeSSE?: (
    conversationId: string,
    callbacks: Record<string, (e: CustomEvent) => void>,
    cursor?: { historyId?: string; afterSequence?: number },
  ) => any;
  onConversationIdChange?: (conversationId: string) => void;
  parseErrorData: (data: string) => string;
  setShowHistoryList?: (show: boolean) => void;
  showHistoryList?: boolean;
  showHistoryButton?: boolean;
  setIsChatContent: (isChatContent: boolean) => void;
  chatConfig?: ChatConfig;
  setChatConfig?: (chatConfig: ChatConfig) => void;
  setChatConfigFn: (chatConfig: ChatConfig) => void;
  knowledgeRefreshKey?: number | string;
  allowKnowledgeBaseSelection?: boolean;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  disabledReason?: string;
  disabledDescription?: ReactNode;
  disabledAction?: ReactNode;
  onConversationSettingsChange?: (
    settings: import("@/modules/chat/utils/request").ConversationRuntimeSettings,
  ) => void;
  initialConversationSettings?: import("@/modules/chat/utils/request").ConversationRuntimeSettings;
  hasWorkflowSession?: boolean;
  conversationTrailEnabled?: boolean;
  showThinkingDepth?: boolean;
  showSkillDeposit?: boolean;
  showConversationConfig?: boolean;
  showModelSelector?: boolean;
  fixedThinkingDepth?: ThinkingDepth;
  /** Isolates this container's SSE lifecycle from the main conversation. */
  concurrentStream?: boolean;
  /** Controlled thinking depth for embedded chat surfaces. */
  thinkingDepth?: ThinkingDepth;
  onThinkingDepthChange?: (thinkingDepth: ThinkingDepth) => void;
  onStreamingChange?: (streaming: boolean) => void;
  /** Reports the gap between a submitted request and an attached SSE stream. */
  onRequestPendingChange?: (pending: boolean) => void;
  onOpenSideChat?: (source: {
    selectedText?: string;
    historyId?: string;
    sequence?: number;
  }) => void;
}

export interface ChatMessage {
  role?: string;
  delta?: string;
  raw_delta?: string;
  delta_mode?: "append" | "replace";
  images?: {
    base64?: string;
    uid?: string;
  }[];
  files?: {
    name?: string;
    uid?: string;
  }[];
  finish_reason?: string;
  run_status?: "completed" | "interrupted" | "failed" | "cancelled";
  model_retry?: {
    retry_index: number;
    max_attempts: number;
  };
  model_route?: ChatModelRoute;
  run_terminal?: {
    status: "completed" | "interrupted" | "failed" | "cancelled";
    reason:
      | "normal"
      | "awaiting_user_input"
      | "model_incomplete"
      | "model_failure"
      | "runtime_failure"
      | "user_cancelled";
    code?: string;
    partial_output: boolean;
    model_call_id?: string;
    diagnostic_id?: string;
  };
  performance_metrics?: RunPerformanceMetrics;
  inputs?: Query[];
  reasoning_content?: string;
  thinking_duration_s?: number | string;
  thinking_time_s?: number | string;
  history_id?: string;
  external_event_sequence?: number;
  sources?: ChatSourceCollection;
  feed_back?: string;
  answers?: Array<{
    content: string;
    index: number;
    history_id?: string;
    raw_content?: string;
    reasoning_content?: string;
    sources?: ChatSourceCollection;
    thinking_duration_s?: string;
    performance_metrics?: RunPerformanceMetrics;
  }>;
  answer_index?: number;
  create_time?: string;
  is_resumed?: boolean;
  display_delta?: string;
  cite_message?: string;
  cite_messages?: string[];
  cite_history_ids?: string[];
  seq?: number;
  trail_depth?: number;
  trail_parent_history_id?: string;
  trail_source?: string;
  trail_summary?: string;
  trail_question?: string;
  tool_call_turns?: number;
  tool_limit_pending?: {
    decision_id: string;
    used_rounds: number;
    round_limit: number;
    expanded_max_rounds: number;
    timeout_seconds: number;
  };
  resolved_tool_limit_decision_id?: string;
  mentions?: ChatMention[];
  collected_inputs?: Array<{
    task_id: string;
    conversation_id?: string;
    source_name?: string;
    executed_at?: string;
    mode?: string;
    summary?: string;
  }>;
  intent_updated?: {
    scope: "conversation";
    intent_context: Record<string, unknown>;
  };
  ask_pending?: {
    ask_id: string;
    questions: Array<{
      text: string;
      type: "boolean" | "single" | "multiple" | "text";
      choices?: string[];
      allow_other?: boolean;
    }>;
    title?: string;
    description?: string;
  };
  ask_answered?: boolean;
  ask_saved_answers?: Record<number, unknown>;
}
