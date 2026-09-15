import { ConversationsApiFactory } from "@/api/generated/core-client";
import type { ForkCreateRequest } from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";

const api = () => ConversationsApiFactory(undefined, BASE_URL, axiosInstance);
const options = { silentError: true, timeout: 30_000 };

export async function previewFork(conversationId: string, historyId: string) {
  const response = await api().previewConversationFork({ conversationId, forkPreviewRequest: { source_history_id: historyId } }, options);
  return response.data;
}

export async function createFork(conversationId: string, key: string, body: ForkCreateRequest) {
  const response = await api().createConversationFork({ conversationId, idempotencyKey: key, forkCreateRequest: body }, options);
  return response.data;
}
