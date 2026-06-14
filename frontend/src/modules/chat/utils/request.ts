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
  type DatasetServiceApiDatasetServiceResetDefaultDatasetsRequest,
  type DatasetServiceApiDatasetServiceSetDefaultDatasetRequest,
  type DatasetServiceApiDatasetServiceUnsetDefaultDatasetRequest,
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
      requestParameters: DatasetServiceApiDatasetServiceSetDefaultDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/datasets/${encodeURIComponent(requestParameters.dataset)}:setDefault`,
        requestParameters.setDefaultDatasetRequest,
        withJsonOptions(options),
      );
    },
    datasetServiceUnsetDefaultDataset(
      requestParameters: DatasetServiceApiDatasetServiceUnsetDefaultDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return axiosInstance.post(
        `${coreApiBaseUrl}/datasets/${encodeURIComponent(requestParameters.dataset)}:unsetDefault`,
        requestParameters.unsetDefaultDatasetRequest,
        withJsonOptions(options),
      );
    },
    async datasetServiceResetDefaultDatasets(
      _requestParameters: DatasetServiceApiDatasetServiceResetDefaultDatasetsRequest,
      options?: RawAxiosRequestConfig,
    ): Promise<AxiosResponse<ListDatasetsResponse>> {
      const listResponse = await axiosInstance.get<ListDatasetsResponse>(
        `${coreApiBaseUrl}/datasets`,
        {
          ...options,
          params: {
            ...(options?.params ?? {}),
            page_size: 1000,
          },
        },
      );
      const defaultDatasets =
        listResponse.data.datasets?.filter((item) => item.default_dataset) ?? [];

      await Promise.all(
        defaultDatasets
          .filter((item) => item.dataset_id && item.name)
          .map((item) =>
            axiosInstance.post(
              `${coreApiBaseUrl}/datasets/${encodeURIComponent(item.dataset_id!)}:unsetDefault`,
              { name: item.name },
              withJsonOptions(options),
            ),
          ),
      );

      return listResponse;
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
