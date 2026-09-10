import type { Conversation } from "@/api/generated/chatbot-client";
import {
  CHAT_CONVERSATION_ACTIVITY_EVENT,
  CHAT_CONVERSATION_LIST_REFRESH_EVENT,
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

export function emitConversationListRefresh() {
  window.dispatchEvent(new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT));
}

type ActivityConversation = Omit<Conversation, "search_config"> & {
  search_config?: Conversation["search_config"];
};

export function bumpConversationToTop(
  list: ActivityConversation[],
  conversationId: string,
  options?: { displayName?: string },
): ActivityConversation[] {
  const now = new Date().toISOString();
  const existingIndex = list.findIndex(
    (item) => item.conversation_id === conversationId,
  );

  if (existingIndex >= 0) {
    const existing = list[existingIndex];
    const updated: ActivityConversation = {
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

  const placeholder: ActivityConversation = {
    conversation_id: conversationId,
    display_name: options.displayName,
    update_time: now,
  };

  return [placeholder, ...list];
}
