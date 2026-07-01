import {
  Configuration,
  type BatchChatJob,
  type BatchChatJobResult,
  type BatchChatResponse,
  type ConversationDetail,
  type ConversationServiceApiConversationServiceBatchChatRequest,
  type ConversationServiceApiConversationServiceDeleteConversationRequest,
  type ConversationServiceApiConversationServiceFeedBackChatHistoryRequest,
  type ConversationServiceApiConversationServiceGetBatchChatJobRequest,
  type ConversationServiceApiConversationServiceGetChatStatusRequest,
  type ConversationServiceApiConversationServiceGetConversationDetailRequest,
  type ConversationServiceApiConversationServiceListConversationsRequest,
  type ConversationServiceApiConversationServicePreviewBatchChatJobResultRequest,
  type ConversationServiceApiConversationServiceSetChatHistoryRequest,
  type ConversationServiceApiConversationServiceSetMultiAnswersSwitchStatusRequest,
  type ConversationServiceApiConversationServiceStopChatGenerationRequest,
  type FileServiceApiFileServicePresignAttachmentRequest,
  type GetChatStatusResponse,
  type GetMultiAnswersSwitchStatusResponse,
  type ListConversationsResponse,
  type ListPromptsResponse,
  type PresignAttachmentResponse,
  type Prompt,
  type PromptServiceApiPromptServiceCreatePromptRequest,
  type PromptServiceApiPromptServiceDeletePromptRequest,
  type PromptServiceApiPromptServiceGetPromptRequest,
  type PromptServiceApiPromptServiceListPromptsRequest,
  type PromptServiceApiPromptServiceSetDefaultPromptRequest,
  type PromptServiceApiPromptServiceUnsetDefaultPromptRequest,
  type PromptServiceApiPromptServiceUpdatePromptRequest,
  type SetMultiAnswersSwitchStatusResponse,
} from "@/api/generated/chatbot-client";
import {
  Configuration as CoreConfiguration,
  DefaultApiFactory as CoreDefaultApiFactory,
  type ConversationHistoryListResponse,
  type DefaultApiApiCoreConversationsNameHistoryGetRequest,
  type DefaultApiApiCorePromptsPolishPostRequest,
  type PromptPolishResponse,
} from "@/api/generated/core-client";
import {
  type AllDocumentCreatorsResponse,
  type AllDocumentTagsResponse,
  type DatasetServiceApiDatasetServiceListDatasetsRequest,
  type ListDatasetsResponse,
  type UserDatabaseSummary,
} from "@/api/generated/knowledge-client";
import { FileServiceApiFactory } from "@/api/generated/file-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { AxiosResponse, RawAxiosRequestConfig } from "axios";

const coreApiBaseUrl = `${BASE_URL}/api/core`;

axiosInstance.defaults.timeout = 60 * 1000; // 10 seconds

const Config = new Configuration();
const CoreConfig = new CoreConfiguration({ basePath: BASE_URL });
const coreDefaultClient = CoreDefaultApiFactory(
  CoreConfig,
  BASE_URL,
  axiosInstance,
);

type PromptListRequestWithKeyword =
  PromptServiceApiPromptServiceListPromptsRequest & {
    keyword?: string;
  };

export const CHAT_STREAM_URL = `${coreApiBaseUrl}/conversations:chat`;
export const CHAT_RESUME_STREAM_URL = `${coreApiBaseUrl}/conversations:resumeChat`;

// SubAgent Task Center endpoints.
export const taskStreamUrl = (taskId: string) =>
  `${coreApiBaseUrl}/tasks/${encodeURIComponent(taskId)}:stream`;

// Conversation-level events SSE endpoint.
export const convEventsUrl = (conversationId: string) =>
  `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/events`;

export function TaskServiceApi() {
  return {
    listConversationTasks(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/tasks`,
        options,
      );
    },
    getTaskDetail(taskId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/tasks/${encodeURIComponent(taskId)}`,
        options,
      );
    },
    getTaskArtifacts(taskId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/tasks/${encodeURIComponent(taskId)}/artifacts`,
        options,
      );
    },
  };
}

// Plugin Info API — fetches plugin spec (including ui.tabs) from Go /api/core/plugins.
export function PluginInfoApi() {
  return {
    getPlugin(pluginId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugins/${encodeURIComponent(pluginId)}`,
        options,
      );
    },
    listPlugins(options?: RawAxiosRequestConfig) {
      return axiosInstance.get(`${coreApiBaseUrl}/plugins`, options);
    },
  };
}

// Plugin Session API.
export function PluginSessionApi() {
  return {
    getLatestSession(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/plugin-sessions:latest`,
        options,
      );
    },
    listSessions(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/plugin-sessions`,
        options,
      );
    },
    getSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}`,
        options,
      );
    },
    getSlots(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots`,
        options,
      );
    },
    getSteps(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/steps`,
        options,
      );
    },
    patchSlot(sessionId: string, slotId: string, selectedRevision: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}`,
        { selected_revision: selectedRevision },
        options,
      );
    },
    // Phase 3: slot item management — addressed by stable list_index (not sort_order).
    deleteSlotItem(sessionId: string, slotId: string, listIndex: number, orderVersion?: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.delete(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}`,
        { ...options, data: orderVersion !== undefined ? { order_version: orderVersion } : undefined },
      );
    },
    patchSlotItem(sessionId: string, slotId: string, listIndex: number, value: any, contentType?: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}`,
        { value, ...(contentType ? { content_type: contentType } : {}) },
        options,
      );
    },
    reorderSlotItems(sessionId: string, slotId: string, order: number[], version: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/order`,
        { order, version },
        options,
      );
    },
    getSlotOrder(sessionId: string, slotId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/order`,
        options,
      );
    },
    getSlotItemVersions(sessionId: string, slotId: string, listIndex: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/versions`,
        options,
      );
    },
    rollbackSlotItem(sessionId: string, slotId: string, listIndex: number, revision: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/rollback`,
        { revision },
        options,
      );
    },
    createSlotItem(sessionId: string, slotId: string, value: any, caption?: string, insertBefore?: number, contentType?: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items`,
        { value, ...(caption !== undefined ? { caption } : {}), ...(insertBefore !== undefined ? { insert_before: insertBefore } : {}), ...(contentType ? { content_type: contentType } : {}) },
        options,
      );
    },
    patchSlotCaption(sessionId: string, slotId: string, listIndex: number, caption: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/caption`,
        { caption },
        options,
      );
    },
    dismissSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}:dismiss`,
        {},
        { headers: { 'Content-Type': 'application/json' }, ...options },
      );
    },
    restoreSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}:restore`,
        {},
        { headers: { 'Content-Type': 'application/json' }, ...options },
      );
    },
    listDismissedSessions(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/dismissed-plugin-sessions`,
        options,
      );
    },
  };
}

function withJsonOptions(
  options: RawAxiosRequestConfig = {},
): RawAxiosRequestConfig {
  return {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options.headers ?? {}),
    },
  };
}

export function ChatServiceApi() {
  return {
    conversationServiceGetMultiAnswersSwitchStatus(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<GetMultiAnswersSwitchStatusResponse>(
        `${coreApiBaseUrl}/conversation:switchStatus`,
        options,
      );
    },
    conversationServiceSetMultiAnswersSwitchStatus(
      requestParameters: ConversationServiceApiConversationServiceSetMultiAnswersSwitchStatusRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<SetMultiAnswersSwitchStatusResponse>(
        `${coreApiBaseUrl}/conversation:switchStatus`,
        requestParameters.setMultiAnswersSwitchStatusRequest,
        withJsonOptions(options),
      );
    },
    conversationServiceFeedBackChatHistory(
      requestParameters: ConversationServiceApiConversationServiceFeedBackChatHistoryRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/conversations:feedBackChatHistory`,
        requestParameters.feedBackChatHistoryRequest,
        withJsonOptions(options),
      );
    },
    conversationServiceSetChatHistory(
      requestParameters: ConversationServiceApiConversationServiceSetChatHistoryRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/conversations:setChatHistory`,
        requestParameters.setChatHistoryRequest,
        withJsonOptions(options),
      );
    },
    conversationServiceStopChatGeneration(
      requestParameters: ConversationServiceApiConversationServiceStopChatGenerationRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/conversations:stopChatGeneration`,
        requestParameters.stopChatGenerationRequest,
        withJsonOptions(options),
      );
    },
    /** Save partial ask-user answers so they survive page reload. */
    conversationServiceSaveAskAnswers(
      conversationId: string,
      historyId: string,
      answers: Record<string, any>,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}:ask-answers`,
        { history_id: historyId, answers },
        withJsonOptions(options),
      );
    },
    conversationServiceListConversations(
      requestParameters: ConversationServiceApiConversationServiceListConversationsRequest = {},
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<ListConversationsResponse>(
        `${coreApiBaseUrl}/conversations`,
        {
          ...options,
          params: {
            ...(options?.params ?? {}),
            page_token: requestParameters.pageToken,
            page_size: requestParameters.pageSize,
            keyword: requestParameters.keyword,
          },
        },
      );
    },
    conversationServiceDeleteConversation(
      requestParameters: ConversationServiceApiConversationServiceDeleteConversationRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.delete<void>(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(requestParameters.conversation)}`,
        options,
      );
    },
    conversationServiceGetChatStatus(
      requestParameters: ConversationServiceApiConversationServiceGetChatStatusRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<GetChatStatusResponse>(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(requestParameters.conversationId)}:status`,
        options,
      );
    },
    conversationServiceGetConversationDetail(
      requestParameters: ConversationServiceApiConversationServiceGetConversationDetailRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<ConversationDetail>(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(requestParameters.conversation)}:detail`,
        options,
      );
    },
    conversationServiceGetConversationHistory(
      requestParameters: DefaultApiApiCoreConversationsNameHistoryGetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreDefaultClient.apiCoreConversationsNameHistoryGet(
        requestParameters,
        options,
      ) as Promise<AxiosResponse<ConversationHistoryListResponse>>;
    },
    conversationServiceBatchChat(
      requestParameters: ConversationServiceApiConversationServiceBatchChatRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<BatchChatResponse>(
        `${BASE_URL}/api/v1/conversations:batchChat`,
        requestParameters.batchChatRequest,
        withJsonOptions(options),
      );
    },
    conversationServiceGetBatchChatJob(
      requestParameters: ConversationServiceApiConversationServiceGetBatchChatJobRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<BatchChatJob>(
        `${BASE_URL}/api/v1/conversations/jobs/${encodeURIComponent(requestParameters.job)}`,
        options,
      );
    },
    conversationServicePreviewBatchChatJobResult(
      requestParameters: ConversationServiceApiConversationServicePreviewBatchChatJobResultRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<BatchChatJobResult>(
        `${BASE_URL}/api/v1/conversations/jobs/${encodeURIComponent(requestParameters.job)}:result`,
        undefined,
        options,
      );
    },
  };
}

export function PromptServiceApi() {
  return {
    promptServiceListPrompts(
      requestParameters: PromptListRequestWithKeyword = {},
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<ListPromptsResponse>(`${coreApiBaseUrl}/prompts`, {
        ...options,
        params: {
          ...(options?.params ?? {}),
          page_size: requestParameters.pageSize,
          page_token: requestParameters.pageToken,
          keyword: requestParameters.keyword,
        },
      });
    },
    promptServiceGetPrompt(
      requestParameters: PromptServiceApiPromptServiceGetPromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<Prompt>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(requestParameters.prompt)}`,
        options,
      );
    },
    promptServiceCreatePrompt(
      requestParameters: PromptServiceApiPromptServiceCreatePromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<Prompt>(
        `${coreApiBaseUrl}/prompts`,
        requestParameters.prompt,
        withJsonOptions(options),
      );
    },
    promptServiceUpdatePrompt(
      requestParameters: PromptServiceApiPromptServiceUpdatePromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch<Prompt>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(requestParameters.prompt)}`,
        requestParameters.prompt2,
        withJsonOptions(options),
      );
    },
    promptServiceDeletePrompt(
      requestParameters: PromptServiceApiPromptServiceDeletePromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.delete<void>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(requestParameters.prompt)}`,
        options,
      );
    },
    promptServiceSetDefaultPrompt(
      requestParameters: PromptServiceApiPromptServiceSetDefaultPromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(requestParameters.prompt)}:setDefault`,
        requestParameters.setDefaultPromptRequest,
        withJsonOptions(options),
      );
    },
    promptServiceUnsetDefaultPrompt(
      requestParameters: PromptServiceApiPromptServiceUnsetDefaultPromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(requestParameters.prompt)}:unsetDefault`,
        requestParameters.unsetDefaultPromptRequest,
        withJsonOptions(options),
      );
    },
    promptServicePolishPrompt(
      requestParameters: DefaultApiApiCorePromptsPolishPostRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreDefaultClient.apiCorePromptsPolishPost(
        requestParameters,
        options,
      ) as Promise<AxiosResponse<PromptPolishResponse>>;
    },
  };
}

export function DocumentServiceApi() {
  return {
    documentServiceAllDocumentCreators(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<AllDocumentCreatorsResponse>(
        `${coreApiBaseUrl}/document/creators`,
        options,
      );
    },
    documentServiceAllDocumentTags(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<AllDocumentTagsResponse>(
        `${coreApiBaseUrl}/document/tags`,
        options,
      );
    },
  };
}

export function KnowledgeBaseServiceApi() {
  return {
    datasetServiceListDatasets(
      requestParameters: DatasetServiceApiDatasetServiceListDatasetsRequest = {},
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<ListDatasetsResponse>(`${coreApiBaseUrl}/datasets`, {
        ...options,
        params: {
          ...(options?.params ?? {}),
          page_token: requestParameters.pageToken,
          page_size: requestParameters.pageSize,
          order_by: requestParameters.orderBy,
          keyword: requestParameters.keyword,
          tags: requestParameters.tags,
        },
      });
    },
    datasetServiceSetDefaultDataset(
      dataset: string,
      name: string,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/datasets/${encodeURIComponent(dataset)}:setDefault`,
        { name },
        withJsonOptions(options),
      );
    },
    datasetServiceUnsetDefaultDataset(
      dataset: string,
      name: string,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/datasets/${encodeURIComponent(dataset)}:unsetDefault`,
        { name },
        withJsonOptions(options),
      );
    },
  };
}

export function FileServiceApi() {
  return FileServiceApiFactory(
    Config,
    `${BASE_URL}/api/fileservice`,
    axiosInstance,
  );
}

export function DatabaseBaseServiceApi() {
  return {
    databaseServiceGetUserDatabaseSummaries(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<UserDatabaseSummary[]>(
        `${coreApiBaseUrl}/rag/databases/summary`,
        options,
      );
    },
  };
}

export function ChatFileServiceApi() {
  return {
    fileServicePresignAttachment(
      requestParameters: FileServiceApiFileServicePresignAttachmentRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<PresignAttachmentResponse>(
        `${BASE_URL}/api/v1/attachment:presign`,
        requestParameters.presignAttachmentRequest,
        withJsonOptions(options),
      );
    },
  };
}

export function TempUploadServiceApi() {
  return {
    initUpload(request: any, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/temp/uploads:initUpload`,
        request,
        withJsonOptions(options),
      );
    },
    uploadPart(
      uploadId: string,
      partNumber: number,
      data: Blob,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.put(
        `${coreApiBaseUrl}/temp/uploads/${encodeURIComponent(uploadId)}/parts/${partNumber}`,
        data,
        {
          ...options,
          headers: {
            "Content-Type": "application/octet-stream",
            ...(options?.headers ?? {}),
          },
        },
      );
    },
    completeUpload(
      uploadId: string,
      request: any,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/temp/uploads/${encodeURIComponent(uploadId)}:complete`,
        request,
        withJsonOptions(options),
      );
    },
    abortUpload(uploadId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/temp/uploads/${encodeURIComponent(uploadId)}:abort`,
        {},
        withJsonOptions(options),
      );
    },
  };
}

export interface ConversationPluginSettings {
  plugin_mode?: 'dynamic' | 'auto';
  enable_subagent?: boolean;
  enable_plugin?: boolean;
}

export function parseConversationPluginSettings(
  conversation?: {
    enable_plugin?: boolean | null;
    plugin_mode?: string | null;
    enable_subagent?: boolean | null;
  } | null,
): ConversationPluginSettings | undefined {
  if (!conversation) {
    return undefined;
  }
  const settings: ConversationPluginSettings = {};
  if (conversation.enable_plugin != null) {
    settings.enable_plugin = conversation.enable_plugin;
  }
  const rawMode = conversation.plugin_mode;
  if (rawMode === 'dynamic' || rawMode === 'auto') {
    settings.plugin_mode = rawMode;
  }
  if (conversation.enable_subagent != null) {
    settings.enable_subagent = conversation.enable_subagent;
  }
  return Object.keys(settings).length > 0 ? settings : undefined;
}

export function ConversationSettingsApi() {
  return {
    getChatSettings(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<ConversationPluginSettings>(
        `${coreApiBaseUrl}/user/chat-settings`,
        options,
      );
    },
    patchPluginSettings(
      conversationId: string,
      settings: ConversationPluginSettings,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/plugin-settings`,
        settings,
        options,
      );
    },
  };
}
