import type { RefObject } from "react";
import type { ChatConfig } from "../ChatConfigs";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";

export interface SideChatSource {
  selectedText?: string;
  historyId?: string;
  sequence?: number;
}

export type OnOpenSideChat = (source?: SideChatSource) => void;

export interface SideChatConversation {
  id: string;
  displayName: string;
  parentConversationId: string;
  parentDisplayName?: string;
  relationType: "sidechat";
  sourceHistoryId?: string;
  sourceSequence?: number;
  selectedText?: string;
  sourceContext?:
    | unknown[]
    | { messages?: unknown[] };
  searchConfig?: Record<string, unknown>;
  chatModelMode?: string;
  chatModelId?: string;
  chatModelVersion?: number;
  thinkingDepth: ThinkingDepth;
  isEphemeral?: boolean;
}

export interface SideChatPanelProps {
  open: boolean;
  parentConversationId: string;
  source?: SideChatSource | null;
  onClose: () => void;
  onRetained?: (conversation: SideChatConversation) => void;
  canChat?: boolean;
  embeddingReady?: boolean | null;
  multimodalEmbeddingReady?: boolean | null;
  rerankReady?: boolean | null;
  returnFocusRef?: RefObject<HTMLElement | null>;
}

export interface SideChatCreateBody {
  source_history_id?: string;
  source_seq?: number;
  selected_text?: string;
  thinking_depth?: ThinkingDepth;
}

export interface SideChatStreamPayloadOptions {
  conversationId: string;
  action: string;
  input: unknown[];
  thinkingDepth: ThinkingDepth;
  chatConfig: ChatConfig;
  modelLabel: string;
  locale?: string;
  createTime?: string;
  clientRequestId?: string;
}
