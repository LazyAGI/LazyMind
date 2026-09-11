import dayjs from "dayjs";
import type { ConversationWithRelation } from "@/modules/chat/utils/conversationRelation";
import type { ConversationOrderResult } from "@/modules/chat/utils/request";

export type SidebarConversation = ConversationWithRelation & {
  metadata_pending?: boolean;
  title_revision?: number;
  pinned_at?: string | null;
  is_pinned?: boolean;
  history_order?: number | null;
  source_type?: string;
  source_display_name?: string;
};

export function isConversationPinned(conversation: SidebarConversation) {
  return conversation.is_pinned === true || Boolean(conversation.pinned_at);
}

function conversationTime(value?: string | null) {
  if (!value) return 0;
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.valueOf() : 0;
}

export function sortConversationHistory(conversations: SidebarConversation[]) {
  return [...conversations].sort((left, right) => {
    const leftPinned = isConversationPinned(left);
    const rightPinned = isConversationPinned(right);
    if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
    if ((left.history_order == null) !== (right.history_order == null)) {
      return left.history_order == null ? -1 : 1;
    }
    const manualOrder = (left.history_order ?? 0) - (right.history_order ?? 0);
    if (manualOrder) return manualOrder;
    const pinnedOrder = leftPinned
      ? conversationTime(right.pinned_at) - conversationTime(left.pinned_at)
      : 0;
    return pinnedOrder
      || conversationTime(right.update_time) - conversationTime(left.update_time)
      || (left.conversation_id || "").localeCompare(right.conversation_id || "");
  });
}

export function applyConversationOrder(
  conversations: SidebarConversation[],
  result: ConversationOrderResult,
) {
  const orders = new Map(result.order_updates?.map((item) => [item.conversation_id, item.history_order]));
  return sortConversationHistory(conversations.map((item) => ({
    ...item,
    ...(orders.has(item.conversation_id || "")
      ? { history_order: orders.get(item.conversation_id || "") }
      : {}),
    ...(item.conversation_id === result.conversation_id
      ? { is_pinned: result.is_pinned, pinned_at: result.pinned_at, history_order: result.history_order }
      : {}),
  })));
}
