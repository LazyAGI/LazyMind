import { axiosInstance, BASE_URL } from "@/components/request";
import type { ChatConfig } from "../ChatConfigs";
import type { ThinkingDepth } from "@/modules/chat/store/chatThink";
import { buildSideChatCreateBody, normalizeSideChatConversation } from "./helpers";
import type { SideChatConversation, SideChatSource } from "./types";

const conversationsBase = `${BASE_URL}/api/core/conversations`;

export async function createSideChat(
  parentConversationId: string,
  source?: SideChatSource | null,
  thinkingDepth?: ThinkingDepth,
): Promise<SideChatConversation> {
  const response = await axiosInstance.post(
    `${conversationsBase}/${encodeURIComponent(parentConversationId)}/sidechat`,
    buildSideChatCreateBody(source, thinkingDepth),
    { silentError: true } as any,
  );
  return normalizeSideChatConversation(response.data);
}

export async function retainSideChat(
  childConversationId: string,
): Promise<SideChatConversation> {
  const response = await axiosInstance.post(
    `${conversationsBase}/${encodeURIComponent(childConversationId)}/retain`,
    undefined,
    { silentError: true } as any,
  );
  return normalizeSideChatConversation(response.data);
}

export async function deleteSideChat(
  childConversationId: string,
): Promise<void> {
  await axiosInstance.delete(
    `${conversationsBase}/${encodeURIComponent(childConversationId)}/sidechat`,
    { silentError: true } as any,
  );
}

export async function patchSideChatKnowledge(
  childConversationId: string,
  chatConfig: ChatConfig,
): Promise<void> {
  await axiosInstance.patch(
    `${conversationsBase}/${encodeURIComponent(childConversationId)}:search-config`,
    {
      dataset_ids: chatConfig.knowledgeBaseId ?? [],
      creators: chatConfig.creators ?? [],
      tags: chatConfig.tags ?? [],
    },
    { silentError: true } as any,
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
