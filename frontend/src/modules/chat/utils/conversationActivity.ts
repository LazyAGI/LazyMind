import type { Conversation } from "@/api/generated/chatbot-client";
import {
  CHAT_CONVERSATION_ACTIVITY_EVENT,
  type ChatConversationActivityDetail,
} from "@/modules/chat/constants/chat";

export function emitConversationActivity(
  detail: ChatConversationActivityDetail,
) {
  const conversationId = detail.conversationId?.trim();
  if (!conversationId || conversationId.startsWith("temp_")) {
    return;
  }

  window.dispatchEvent(
    new CustomEvent(CHAT_CONVERSATION_ACTIVITY_EVENT, {
      detail: { ...detail, conversationId },
    }),
  );
}

export function bumpConversationToTop(
  list: Conversation[],
  conversationId: string,
  options?: { displayName?: string },
): Conversation[] {
  const now = new Date().toISOString();
  const existingIndex = list.findIndex(
    (item) => item.conversation_id === conversationId,
  );

  if (existingIndex >= 0) {
    const existing = list[existingIndex];
    const updated: Conversation = {
      ...existing,
      update_time: now,
      ...(options?.displayName
        ? { display_name: options.displayName }
        : {}),
    };
    return [
      updated,
      ...list.filter((_, index) => index !== existingIndex),
    ];
  }

  if (!options?.displayName) {
    return list;
  }

  const placeholder: Conversation = {
    conversation_id: conversationId,
    display_name: options.displayName,
    update_time: now,
    search_config: {},
  };

  return [placeholder, ...list];
}
