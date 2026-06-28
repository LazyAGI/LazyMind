
export const CHAT_RESUME_CONVERSATION_KEY = "chat_resume_conversation_id";
export const CHAT_SELECT_CONVERSATION_EVENT = "lazymind:chat-select-conversation";
export const CHAT_AUTO_ADVANCE_EVENT = "lazymind:chat-auto-advance";

export type ChatAutoAdvancePhase = "append" | "resume";

export interface ChatAutoAdvanceDetail {
  conversationId: string;
  driverMessage?: string;
  phase: ChatAutoAdvancePhase;
}
