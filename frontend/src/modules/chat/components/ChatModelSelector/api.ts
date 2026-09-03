import { axiosInstance, BASE_URL } from "@/components/request";
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

const CHAT_MODELS_URL = `${BASE_URL}/api/core/chat/models`;

function unwrapData<T>(payload: T | ApiEnvelope<T>): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function fetchChatModelCatalog(
  conversationId?: string,
  signal?: AbortSignal,
): Promise<ChatModelCatalog> {
  const response = await axiosInstance.get<
    ChatModelCatalog | ApiEnvelope<ChatModelCatalog>
  >(CHAT_MODELS_URL, {
    ...(conversationId ? { params: { conversation_id: conversationId } } : {}),
    signal,
    silentError: true,
  } as never);
  return unwrapData(response.data);
}

export async function updateConversationChatModel(
  conversationId: string,
  selection: ChatModelSelectionRequest,
  expectedVersion: number,
  signal?: AbortSignal,
): Promise<ChatModelSelection | undefined> {
  const response = await axiosInstance.patch<
    UpdateSelectionResponse | ApiEnvelope<UpdateSelectionResponse>
  >(
    `${BASE_URL}/api/core/conversations/${encodeURIComponent(conversationId)}/model`,
    {
      ...selection,
      expected_version: expectedVersion,
    },
    { signal, silentError: true } as never,
  );
  const payload = unwrapData(response.data);
  if (payload && typeof payload === "object" && "selection" in payload) {
    return payload.selection;
  }
  return payload as ChatModelSelection;
}
