import { axiosInstance, BASE_URL } from "@/components/request";
import { ConversationsApiFactory } from "@/api/generated/core-client";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";
import { buildSideChatCreateBody, normalizeSideChatConversation } from "./helpers";
import type { SideChatConversation, SideChatSource } from "./types";

const conversationsBase = `${BASE_URL}/api/core/conversations`;
const conversationsApi = ConversationsApiFactory(
  undefined, BASE_URL, axiosInstance,
);

export async function createSideChat(
  parentConversationId: string,
  source?: SideChatSource | null,
  thinkingDepth?: ThinkingDepth,
): Promise<SideChatConversation> {
  const response = await conversationsApi.apiCoreConversationsParentIdSidechatPost(
    {
      parentId: parentConversationId,
      createSidechatOpenAPIRequest: buildSideChatCreateBody(source, thinkingDepth),
    },
    { silentError: true } as never,
  );
  return normalizeSideChatConversation(response.data);
}

export async function retainSideChat(
  childConversationId: string,
): Promise<SideChatConversation> {
  const response = await conversationsApi.apiCoreConversationsChildIdRetainPost(
    { childId: childConversationId },
    { silentError: true } as never,
  );
  return normalizeSideChatConversation(response.data);
}

export async function deleteSideChat(
  childConversationId: string,
): Promise<void> {
  await conversationsApi.apiCoreConversationsChildIdSidechatDelete(
    { childId: childConversationId },
    { silentError: true } as never,
  );
}

export async function patchSideChatThinkingDepth(
  childConversationId: string,
  thinkingDepth: ThinkingDepth,
): Promise<void> {
  await axiosInstance.patch(
    `${conversationsBase}/${encodeURIComponent(childConversationId)}/settings`,
    { thinking_depth: thinkingDepth },
    { silentError: true } as any,
  );
}
