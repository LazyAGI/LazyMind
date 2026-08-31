
export const CHAT_HOME_PATH = "/agent/chat/home";
// Kept as a deprecated export so Vite can hot-reload modules compiled before
// route-based conversation restoration replaced this storage key.
export const CHAT_RESUME_CONVERSATION_KEY = "chat_resume_conversation_id";
export const CHAT_NEW_RUN_IN_BACKGROUND_KEY = "chat_new_run_in_background";
export const CHAT_CONVERSATION_FILTER_KEY = "chat_conversation_filter";
export const CHAT_CONVERSATION_FILTER_EVENT = "lazymind:chat-conversation-filter";
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";
export const CHAT_FFMPEG_DEPENDENCY_MISSING_EVENT =
  "lazymind:chat-ffmpeg-dependency-missing";
export const CHAT_CONVERSATION_ACTIVITY_EVENT =
  "lazymind:chat-conversation-activity";
export const CHAT_CONVERSATION_LIST_REFRESH_EVENT =
  "lazymind:chat-conversation-list-refresh";
export const WORKFLOW_PANEL_EXPANDED_EVENT = "lazymind:workflow-panel-expanded";
export const WORKFLOW_PANEL_EXPANDED_STORAGE_PREFIX =
  "lazymind:workflow-panel-expanded:";

export function getChatConversationPath(conversationId: string) {
  return `${CHAT_HOME_PATH}/${encodeURIComponent(conversationId)}`;
}

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
