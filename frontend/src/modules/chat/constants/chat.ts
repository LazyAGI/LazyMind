
export const CHAT_RESUME_CONVERSATION_KEY = "chat_resume_conversation_id";
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";
export const CHAT_CONVERSATION_ACTIVITY_EVENT =
  "lazymind:chat-conversation-activity";

export type ChatAutoAdvancePhase = "append" | "resume";

export interface ChatAutoAdvanceDetail {
  conversationId: string;
  driverMessage?: string;
  phase: ChatAutoAdvancePhase;
}

export interface ChatConversationActivityDetail {
  conversationId: string;
  /** When set on a conversation not yet in the sidebar list, insert it at the top. */
  displayName?: string;
}
