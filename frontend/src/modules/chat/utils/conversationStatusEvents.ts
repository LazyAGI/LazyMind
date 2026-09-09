export const CONVERSATION_STATUS_REFRESH_EVENT = "lazymind:conversation-status-refresh";

// Existing chat/task events request a fresh snapshot; they never declare the
// entire conversation finished, because other work may still be running.
export function requestConversationStatusRefresh(conversationId: string) {
  if (!conversationId || conversationId.startsWith("temp_")) return;
  window.dispatchEvent(new CustomEvent(CONVERSATION_STATUS_REFRESH_EVENT, {
    detail: { conversationId },
  }));
}
