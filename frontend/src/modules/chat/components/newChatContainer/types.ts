import type { ReactNode } from "react";
import {
  ChatConversationsRequestActionEnum,
  Query,
  Source,
} from "@/api/generated/chatbot-client";
import type { SendMessageParams } from "../ChatInput";
import type { ChatConfig } from "../ChatConfigs";

export interface ChatImperativeProps {
  replaceMessageList: (id: string, data: any[]) => void;
  createNewChat: () => void;
  sendMessage: (params: SendMessageParams) => void;
  uploadFiles?: (files: File[]) => void;
  openResumeSSE?: (conversationId: string) => void;
  appendAutoAdvanceTurn?: (conversationId: string, driverMessage: string) => void;
  ensureAutoAdvanceUserTurn?: (conversationId: string, driverMessage: string) => void;
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
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  disabledReason?: string;
  disabledDescription?: string;
  disabledAction?: ReactNode;
  onPluginSettingsChange?: (
    settings: import("@/modules/chat/utils/request").ConversationPluginSettings,
  ) => void;
  initialPluginSettings?: import("@/modules/chat/utils/request").ConversationPluginSettings;
  hasPluginSession?: boolean;
}

export interface ChatMessage {
  role?: string;
  delta?: string;
  raw_delta?: string;
  images?: {
    base64?: string;
    uid?: string;
  }[];
  files?: {
    name?: string;
    uid?: string;
  }[];
  finish_reason?: string;
  inputs?: Query[];
  reasoning_content?: string;
  history_id?: string;
  sources?: Source[];
  feed_back?: string;
  answers?: Array<{
    content: string;
    index: number;
    history_id?: string;
    raw_content?: string;
    reasoning_content?: string;
    sources?: Source[];
    thinking_duration_s?: string;
  }>;
  answer_index?: number;
  create_time?: string;
  is_resumed?: boolean;
  display_delta?: string;
  cite_message?: string;
  cite_messages?: string[];
}
