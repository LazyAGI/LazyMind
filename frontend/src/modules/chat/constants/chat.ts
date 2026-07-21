
export const CHAT_RESUME_CONVERSATION_KEY = "chat_resume_conversation_id";
export const CHAT_NEW_RUN_IN_BACKGROUND_KEY = "chat_new_run_in_background";
export const CHAT_CONVERSATION_FILTER_KEY = "chat_conversation_filter";
export const CHAT_CONVERSATION_FILTER_EVENT = "lazymind:chat-conversation-filter";
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";
export const CHAT_CONVERSATION_ACTIVITY_EVENT =
  "lazymind:chat-conversation-activity";

export type ChatConversationFilter = "normal" | "task";

export function selectChatConversationFilter(filter: ChatConversationFilter) {
  try {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, filter);
  } catch {
    // Ignore storage errors; the live event still updates the current sidebar.
  }
  window.dispatchEvent(
    new CustomEvent(CHAT_CONVERSATION_FILTER_EVENT, { detail: { filter } }),
  );
}

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
