import { ChatServiceApi } from "@/modules/chat/utils/request";
import { isChildConversation } from "@/modules/chat/utils/conversationRelation";
import type { SidebarConversation } from "./conversationHistory";

export async function readConversationSelection(keyword: string, isTaskConv: boolean, assistants?: string, groupId?: string): Promise<SidebarConversation[]> {
  const response = await ChatServiceApi().conversationServiceListConversations({ keyword: keyword.trim() }, {
    params: {
      is_task_conv: String(isTaskConv), selection_snapshot: true,
      ...(assistants ? { assistants } : {}),
      ...(groupId ? { selection_group_id: groupId } : {}),
    },
  });
  const data = response.data as typeof response.data & { selection_complete?: boolean };
  // Fail closed against an older server or a partial/malformed response.
  if (data.selection_complete !== true || data.next_page_token || !Array.isArray(data.conversations)) {
    throw new Error("Incomplete conversation selection snapshot");
  }
  return [...new Map(data.conversations.filter(item => item.conversation_id && !isChildConversation(item))
    .map(item => [item.conversation_id!, item as SidebarConversation])).values()];
}
