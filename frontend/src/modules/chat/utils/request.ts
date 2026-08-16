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
  PromptsApiFactory as CorePromptsApiFactory,
  type ConversationHistoryListResponse,
  type ConversationTrailListResponse,
  type DefaultApiApiCoreConversationsNameHistoryGetRequest,
  type DefaultApiApiCoreConversationsNameTrailGetRequest,
  type PromptItem,
  type PromptCategory,
  type PromptCategoryListResponse,
  type PromptCategoryRequest,
  type PromptListResponse,
  type PromptPolishOpenAPIResponse,
  type PromptPatchRequest,
  type PromptRequest,
  type PromptStateResponse,
  type PromptsApiApiCorePromptsPolishPostRequest,
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
const corePromptsClient = CorePromptsApiFactory(
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
  preview_accuracy?: "deterministic" | "rule_only" | "llm_enhanced";
  requires_llm?: boolean;
  llm_reason?: string;
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

// Conversation-level events SSE endpoint.
export const convEventsUrl = (conversationId: string) =>
  `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/events`;

export function decideToolLimit(
  conversationId: string,
  decisionId: string,
  action: "continue" | "summarize",
) {
  return axiosInstance.post(
    `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}:toolLimitDecision`,
    { decision_id: decisionId, action },
  );
}

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

// Workflow Info API — fetches workflow spec (including ui.tabs) from Go /api/core/workflows.
export function WorkflowInfoApi() {
  return {
    getWorkflow(workflowId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflows/${encodeURIComponent(workflowId)}`,
        options,
      );
    },
    listWorkflows(options?: RawAxiosRequestConfig) {
      return axiosInstance.get(`${coreApiBaseUrl}/workflows`, options);
    },
  };
}

export type SlotSaveMode = 'draft' | 'checkpoint';

export interface SyncWriterDocumentRequest {
  base_revision: number;
  source_document: Record<string, unknown>;
  revised_document: Record<string, unknown>;
  /** draft: overwrite selected human artifact; checkpoint (default): new revision. */
  mode?: SlotSaveMode;
}

export interface SyncWriterDocumentPatchResult {
  patch_id?: string;
  success: boolean;
  applied_hunks: string[];
  failed_hunks: string[];
  message: string;
  meta?: Record<string, unknown>;
}

export interface SyncWriterDocumentResult {
  status: "synced" | "no_change";
  revision: number;
  feishu_synced: boolean;
  artifact_saved: boolean;
  patch_result: SyncWriterDocumentPatchResult;
  document: Record<string, unknown>;
}

export interface WriteBackWriterDocumentResult {
  status: "synced";
  revision: number;
  feishu_synced: boolean;
  artifact_saved: boolean;
  patch_result: SyncWriterDocumentPatchResult;
  document: Record<string, unknown>;
}

export interface WriteBackWriterDocumentRequest {
  base_revision: number;
  source_document: Record<string, unknown>;
  revised_document: Record<string, unknown>;
}

export type RewriteSelection =
  | { type: 'ir'; node_id: string }
  | { type: 'markdown'; selected_text: string };

export interface RewriteSelectionPreviewRequest {
  action: 'rewrite_selection';
  base_revision: number;
  input: {
    instruction: string;
    selection: RewriteSelection;
  };
}

export interface RewriteSelectionPreview {
  status: 'ready';
  action: 'rewrite_selection';
  base_revision: number;
  representation: 'ir' | 'markdown';
  target: {
    type: 'block';
    block_type: string;
    node_id?: string;
  };
  preview: {
    old_text: string;
    new_text: string;
  };
  patch: {
    type: 'writer_ir_patch' | 'string_replace_set';
    payload: Record<string, unknown>;
  };
  artifact: {
    content_type: string;
    value: Record<string, unknown>;
  };
}

// Workflow Session API.
export function WorkflowSessionApi() {
  return {
    getLatestSession(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/workflow-sessions:latest`,
        options,
      );
    },
    listSessions(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/workflow-sessions`,
        options,
      );
    },
    getSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}`,
        options,
      );
    },
    getSlots(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots`,
        options,
      );
    },
    getSteps(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/steps`,
        options,
      );
    },
    getProjection(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/projection`,
        options,
      );
    },
    patchSlot(sessionId: string, slotId: string, selectedRevision: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}`,
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
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}:sync-search-config`,
        { search_config: searchConfig },
        options,
      );
    },
    // Phase 3: slot item management — addressed by stable list_index (not sort_order).
    deleteSlotItem(sessionId: string, slotId: string, listIndex: number, orderVersion?: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.delete(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}`,
        { ...options, data: orderVersion !== undefined ? { order_version: orderVersion } : undefined },
      );
    },
    patchSlotItem(
      sessionId: string,
      slotId: string,
      listIndex: number,
      value: any,
      contentType?: string,
      mode?: SlotSaveMode,
      baseRevision?: number,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}`,
        {
          value,
          ...(contentType ? { content_type: contentType } : {}),
          ...(mode ? { mode } : {}),
          ...(baseRevision !== undefined ? { base_revision: baseRevision } : {}),
        },
        options,
      );
    },
    previewRewriteSelection(
      sessionId: string,
      slotId: string,
      listIndex: number,
      payload: RewriteSelectionPreviewRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<{
        code: number;
        message: string;
        data: RewriteSelectionPreview;
      }>(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}:action-preview`,
        payload,
        options,
      );
    },
    syncWriterDocument(
      sessionId: string,
      slotId: string,
      listIndex: number,
      payload: SyncWriterDocumentRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post<{
        code: number;
        message: string;
        data: SyncWriterDocumentResult;
      }>(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}:sync-writer-document`,
        payload,
        options,
      );
    },
    writeBackWriterDocument(
      sessionId: string,
      baseRevision: number,
      sourceDocument?: Record<string, unknown>,
      revisedDocument?: Record<string, unknown>,
      options?: RawAxiosRequestConfig,
    ) {
      const payload: Record<string, unknown> = { base_revision: baseRevision };
      // Keep the legacy IR payload compatible while the server treats the
      // selected revision as the authoritative write-back input.
      if (sourceDocument !== undefined) payload.source_document = sourceDocument;
      if (revisedDocument !== undefined) payload.revised_document = revisedDocument;
      return axiosInstance.post<{
        code: number;
        message: string;
        data: WriteBackWriterDocumentResult;
      }>(
		`${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/writer-document:write-back`,
        payload,
        options,
      );
    },
    reorderSlotItems(sessionId: string, slotId: string, order: number[], version: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/order`,
        { order, version },
        options,
      );
    },
    getSlotOrder(sessionId: string, slotId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/order`,
        options,
      );
    },
    getSlotItemVersions(sessionId: string, slotId: string, listIndex: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/versions`,
        options,
      );
    },
    rollbackSlotItem(sessionId: string, slotId: string, listIndex: number, revision: number, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/rollback`,
        { revision },
        options,
      );
    },
    createSlotItem(sessionId: string, slotId: string, value: any, caption?: string, insertBefore?: number, contentType?: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items`,
        { value, ...(caption !== undefined ? { caption } : {}), ...(insertBefore !== undefined ? { insert_before: insertBefore } : {}), ...(contentType ? { content_type: contentType } : {}) },
        options,
      );
    },
    patchSlotCaption(sessionId: string, slotId: string, listIndex: number, caption: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}/slots/${encodeURIComponent(slotId)}/items/idx/${listIndex}/caption`,
        { caption },
        options,
      );
    },
    dismissSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}:dismiss`,
        {},
        { headers: { 'Content-Type': 'application/json' }, ...options },
      );
    },
    restoreSession(sessionId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/workflow-sessions/${encodeURIComponent(sessionId)}:restore`,
        {},
        { headers: { 'Content-Type': 'application/json' }, ...options },
      );
    },
    listDismissedSessions(conversationId: string, options?: RawAxiosRequestConfig) {
      return axiosInstance.get(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/dismissed-workflow-sessions`,
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
    conversationServiceGetConversationTrail(
      requestParameters: DefaultApiApiCoreConversationsNameTrailGetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreDefaultClient.apiCoreConversationsNameTrailGet(
        requestParameters,
        options,
      ) as Promise<AxiosResponse<ConversationTrailListResponse>>;
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
      requestParameters: PromptsApiApiCorePromptsPolishPostRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return corePromptsClient.apiCorePromptsPolishPost(
        requestParameters,
        options,
      ) as Promise<AxiosResponse<PromptPolishOpenAPIResponse>>;
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

export type ChatExecutor = string;

export interface ChatExecutorDescriptor {
  id: string;
  display_name: string;
  kind: 'internal' | 'external';
  installed: boolean;
  host_online: boolean;
  available: boolean;
  unavailable_reason?: string;
}

export interface ConversationRuntimeSettings {
  workflow_mode?: 'dynamic' | 'auto';
  enable_subagent?: boolean;
  enable_workflow?: boolean;
  chat_executor?: ChatExecutor;
}

export function parseConversationRuntimeSettings(
  conversation?: {
    enable_workflow?: boolean | null;
    workflow_mode?: string | null;
    enable_subagent?: boolean | null;
    chat_executor?: string | null;
  } | null,
): ConversationRuntimeSettings | undefined {
  if (!conversation) {
    return undefined;
  }
  const settings: ConversationRuntimeSettings = {};
  if (conversation.enable_workflow != null) {
    settings.enable_workflow = conversation.enable_workflow;
  }
  const rawMode = conversation.workflow_mode;
  if (rawMode === 'dynamic' || rawMode === 'auto') {
    settings.workflow_mode = rawMode;
  }
  if (conversation.enable_subagent != null) {
    settings.enable_subagent = conversation.enable_subagent;
  }
  if (typeof conversation.chat_executor === 'string' && conversation.chat_executor.trim()) {
    settings.chat_executor = conversation.chat_executor.trim();
  }
  return Object.keys(settings).length > 0 ? settings : undefined;
}

export function ConversationSettingsApi() {
  return {
    getChatSettings(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<ConversationRuntimeSettings>(
        `${coreApiBaseUrl}/user/chat-settings`,
        options,
      );
    },
    patchConversationSettings(
      conversationId: string,
      settings: ConversationRuntimeSettings,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.patch(
        `${coreApiBaseUrl}/conversations/${encodeURIComponent(conversationId)}/settings`,
        settings,
        options,
      );
    },
    listChatExecutors(options?: RawAxiosRequestConfig) {
      return axiosInstance.get<{ executors: ChatExecutorDescriptor[] }>(
        `${coreApiBaseUrl}/chat/executors`,
        options,
      );
    },
  };
}
