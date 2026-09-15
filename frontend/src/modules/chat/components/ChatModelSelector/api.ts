import { axiosInstance, BASE_URL } from "@/components/request";
import {
  ChatApiFactory,
  ConversationsApiFactory,
} from "@/api/generated/core-client";
import type {
  ChatModelCatalog,
  ChatModelSelection,
  ChatModelSelectionRequest,
} from "@/modules/chat/store/modelSelection";

interface ApiEnvelope<T> {
  data?: T;
}

type UpdateSelectionResponse =
  | ChatModelSelection
  | { selection?: ChatModelSelection };

const chatApi = ChatApiFactory(undefined, BASE_URL, axiosInstance);
const conversationsApi = ConversationsApiFactory(
  undefined, BASE_URL, axiosInstance,
);

function unwrapData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function fetchChatModelCatalog(
  conversationId?: string,
  signal?: AbortSignal,
): Promise<ChatModelCatalog> {
  const response = await chatApi.apiCoreChatModelsGet(
    { conversationId: conversationId || undefined },
    { signal, silentError: true } as never,
  );
  return unwrapData<ChatModelCatalog>(response.data);
}

export async function updateConversationChatModel(
  conversationId: string,
  selection: ChatModelSelectionRequest,
  expectedVersion: number,
  signal?: AbortSignal,
): Promise<ChatModelSelection | undefined> {
  const response = await conversationsApi.apiCoreConversationsConversationIdModelPatch(
    {
      conversationId,
      patchConversationModelOpenAPIRequest: {
        ...selection,
        expected_version: expectedVersion,
      },
    },
    { signal, silentError: true } as never,
  );
  const payload = unwrapData<UpdateSelectionResponse>(response.data);
  if (payload && typeof payload === "object" && "selection" in payload) {
    return payload.selection;
  }
  return payload as ChatModelSelection;
}
