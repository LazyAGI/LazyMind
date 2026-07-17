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
  type PresignAttachmentResponse,
  type SetMultiAnswersSwitchStatusResponse,
} from "@/api/generated/chatbot-client";
import {
  Configuration as CoreConfiguration,
  DefaultApiFactory as CoreDefaultApiFactory,
  type ConversationHistoryListResponse,
  type DefaultApiApiCoreConversationsNameHistoryGetRequest,
  type DefaultApiApiCorePromptsPolishPostRequest,
  type PromptItem,
  type PromptCategory,
  type PromptCategoryListResponse,
  type PromptCategoryRequest,
  type PromptListResponse,
  type PromptPatchRequest,
  type PromptPolishResponse,
  type PromptRequest,
  type PromptStateResponse,
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

export interface PromptLibraryListParams {
  pageSize?: number; // 每页数量
  pageToken?: string; // 下一页游标
  keyword?: string; // 搜索关键词
  category?: string; // 固定分类或用户自定义分类编码
  scope?: string; // 展示范围
  sort?: string; // 排序方式
  locale?: string; // 界面语言
}

export const CHAT_STREAM_URL = `${coreApiBaseUrl}/conversations:chat`;
export const CHAT_RESUME_STREAM_URL = `${coreApiBaseUrl}/conversations:resumeChat`;

export interface ContextUsageItem {
  item_id: string;
  category: string;
  title: string;
  source: string;
  estimated_tokens: number;
  char_count: number;
  item_count: number;
  channel?: string;
  content_kind?: string;
  authoritative?: boolean;
  content: string;
}

export interface ContextUsageCategory {
  category_id: string;
  title: string;
  estimated_tokens: number;
  char_count: number;
  item_count: number;
  items: ContextUsageItem[];
}

export interface ContextUsageReport {
  scope: "next_request";
  estimated_tokens: number;
  max_input_tokens?: number;
  estimated_ratio?: number;
  categories: ContextUsageCategory[];
  estimation_version: string;
}

export function estimateContextUsage(payload: Record<string, unknown>) {
  return axiosInstance
    .post<{ data: ContextUsageReport }>(
      `${coreApiBaseUrl}/conversations:estimateContextUsage`,
      payload,
    )
    .then((response) => response.data.data);
}

export function exportContextPrompt(payload: Record<string, unknown>) {
  return axiosInstance
    .post(`${coreApiBaseUrl}/conversations:exportContextPrompt`, payload, {
      responseType: "blob",
    })
    .then((response) => response.data as Blob);
}

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
    listConversationArtifacts(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/artifacts`,
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
    getProjection(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}/projection`,
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
    syncSessionSearchConfig(
      sessionId: string,
      searchConfig: Record<string, unknown>,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/plugin-sessions/${encodeURIComponent(sessionId)}:sync-search-config`,
        { search_config: searchConfig },
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
    listPromptCategories(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<PromptCategoryListResponse>(
        `${coreApiBaseUrl}/prompt_categories`,
        options,
      );
    },
    createPromptCategory(
      category: PromptCategoryRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<PromptCategory>(
        `${coreApiBaseUrl}/prompt_categories`,
        category,
        withJsonOptions(options),
      );
    },
    deletePromptCategory(categoryID: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.delete<void>(
        `${coreApiBaseUrl}/prompt_categories/${encodeURIComponent(categoryID)}`,
        options,
      );
    },
    listPrompts(
      requestParameters: PromptLibraryListParams = {},
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<PromptListResponse>(`${coreApiBaseUrl}/prompts`, {
        ...options,
        params: {
          ...(options?.params ?? {}),
          page_size: requestParameters.pageSize,
          page_token: requestParameters.pageToken,
          keyword: requestParameters.keyword,
          category: requestParameters.category,
          scope: requestParameters.scope,
          sort: requestParameters.sort,
          locale: requestParameters.locale,
        },
      });
    },
    getPrompt(
      promptID: string,
      locale?: string,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.get<PromptItem>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}`,
        { ...options, params: { ...(options?.params ?? {}), locale } },
      );
    },
    createPrompt(
      prompt: PromptRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<PromptItem>(
        `${coreApiBaseUrl}/prompts`,
        prompt,
        withJsonOptions(options),
      );
    },
    updatePrompt(
      promptID: string,
      prompt: PromptPatchRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch<PromptItem>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}`,
        prompt,
        withJsonOptions(options),
      );
    },
    deletePrompt(promptID: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.delete<void>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}`,
        options,
      );
    },
    favoritePrompt(promptID: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post<PromptStateResponse>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}:favorite`,
        undefined,
        options,
      );
    },
    unfavoritePrompt(promptID: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post<PromptStateResponse>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}:unfavorite`,
        undefined,
        options,
      );
    },
    usePrompt(promptID: string, options?: RawAxiosRequestConfig) {
      const silentOptions = {
        ...options,
        silentError: true, // 使用统计失败不触发全局错误提示
      } as RawAxiosRequestConfig;
      return axiosInstance.post<PromptStateResponse>(
        `${coreApiBaseUrl}/prompts/${encodeURIComponent(promptID)}:use`,
        undefined,
        silentOptions,
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
