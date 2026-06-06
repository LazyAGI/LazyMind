import {
  Configuration,
  type DatasetServiceApiDatasetServiceCreateDatasetRequest,
  type DatasetServiceApiDatasetServiceDeleteDatasetRequest,
  type DatasetServiceApiDatasetServiceGetDatasetRequest,
  type DatasetServiceApiDatasetServiceListDatasetsRequest,
  type DatasetServiceApiDatasetServiceUpdateDatasetRequest,
  type DocumentServiceApiDocumentServiceBatchUpdateDocumentTagsRequest,
  type DocumentServiceApiDocumentServiceGetDocumentRequest,
  type DocumentServiceApiDocumentServiceSearchDocumentsRequest,
  type DocumentServiceApiDocumentServiceUpdateDocumentRequest,
  DatasetMemberServiceApiFactory,
  DocumentServiceApiFactory,
  JobServiceApiFactory,
  SegmentServiceApiFactory,
} from "@/api/generated/knowledge-client";
import {
  UsersApiFactory,
  GroupsApiFactory,
} from "@/api/generated/authservice-client";
import {
  DefaultApiFactory as CoreDefaultApiFactory,
  DatasetsApiFactory as CoreDatasetsApiFactory,
  DocumentsApiFactory as CoreDocumentsApiFactory,
  TasksApiFactory as CoreTasksApiFactory,
  type CreateTaskRequest as CoreCreateTaskRequest,
  type CreateTasksResponse as CoreCreateTasksResponse,
  type Dataset,
  type ListTasksResponse as CoreListTasksResponse,
  type SearchTasksRequest as CoreSearchTasksRequest,
  type StartTaskRequest as CoreStartTaskRequest,
  type StartTasksResponse as CoreStartTasksResponse,
  type TaskResponse as CoreTaskResponse,
  type UploadFilesResponse as CoreUploadFilesResponse,
} from "@/api/generated/core-client";
import { axiosInstance, BASE_URL } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";

const baseUrl = `${BASE_URL}/api`;
const coreApiBaseUrl = `${BASE_URL}/api/core`;

export function normalizeProxyableUrl(uri?: string) {
  if (!uri) return "";

  if (import.meta.env.DEV) {
    try {
      const url = new URL(uri);
      if (url.hostname === "localhost") {
        return url.pathname + url.search + url.hash;
      }
    } catch {
      return uri;
    }
  }

  return uri;
}

axiosInstance.defaults.timeout = 60 * 1000;

axiosInstance.interceptors.request.use((config) => {
  if (config.url) {
    config.url = config.url.replace(/\/api\/core\/v1\//, "/api/core/");
  }
  return config;
});

const Config = new Configuration();
const CoreConfig = new Configuration({ basePath: BASE_URL });

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

export function KnowledgeBaseServiceApi() {
  const datasetsClient = CoreDatasetsApiFactory(
    CoreConfig,
    BASE_URL,
    axiosInstance,
  );
  const defaultClient = CoreDefaultApiFactory(
    CoreConfig,
    BASE_URL,
    axiosInstance,
  );

  return {
    datasetServiceListDatasets(
      requestParameters: DatasetServiceApiDatasetServiceListDatasetsRequest = {},
      options?: RawAxiosRequestConfig,
    ) {
      return datasetsClient.apiCoreDatasetsGet(
        {
          pageToken: requestParameters.pageToken,
          pageSize: requestParameters.pageSize,
          orderBy: requestParameters.orderBy,
          keyword: requestParameters.keyword,
          tags: requestParameters.tags,
        },
        options,
      );
    },

    datasetServiceGetDataset(
      requestParameters: DatasetServiceApiDatasetServiceGetDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return datasetsClient.apiCoreDatasetsDatasetGet(
        { dataset: requestParameters.dataset },
        options,
      );
    },

    datasetServiceCreateDataset(
      requestParameters: DatasetServiceApiDatasetServiceCreateDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return datasetsClient.apiCoreDatasetsPost(
        {
          dataset: requestParameters.dataset as Dataset,
        },
        withJsonOptions(options),
      );
    },

    datasetServiceUpdateDataset(
      requestParameters: DatasetServiceApiDatasetServiceUpdateDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return datasetsClient.apiCoreDatasetsDatasetPatch(
        {
          dataset: requestParameters.dataset,
          dataset2: requestParameters.dataset2 as Dataset,
        },
        {
          ...withJsonOptions(options),
          params: {
            ...(options?.params ?? {}),
            update_mask: requestParameters.updateMask,
          },
        },
      );
    },

    datasetServiceDeleteDataset(
      requestParameters: DatasetServiceApiDatasetServiceDeleteDatasetRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return datasetsClient.apiCoreDatasetsDatasetDelete(
        { dataset: requestParameters.dataset },
        options,
      );
    },

    datasetServiceListAlgos(options?: RawAxiosRequestConfig) {
      return defaultClient.apiCoreDatasetAlgosGet(options);
    },

    datasetServiceAllDatasetTags(options?: RawAxiosRequestConfig) {
      return defaultClient.apiCoreDatasetTagsGet({}, options);
    },
  };
}

export function DocumentServiceApi() {
  const coreClient = CoreDocumentsApiFactory(
    CoreConfig,
    BASE_URL,
    axiosInstance,
  );
  const legacyClient = DocumentServiceApiFactory(
    Config,
    coreApiBaseUrl,
    axiosInstance,
  );

  return {
    ...legacyClient,
    documentServiceGetDocument(
      requestParameters: DocumentServiceApiDocumentServiceGetDocumentRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetDocumentsDocumentGet(
        {
          dataset: requestParameters.dataset,
          document: requestParameters.document,
        },
        options,
      );
    },
    documentServiceSearchDocuments(
      requestParameters: DocumentServiceApiDocumentServiceSearchDocumentsRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetDocumentsSearchPost(
        {
          dataset: requestParameters.dataset,
          searchDocumentsRequest: requestParameters.searchDocumentsRequest,
        },
        options,
      );
    },
    documentServiceUpdateDocument(
      requestParameters: DocumentServiceApiDocumentServiceUpdateDocumentRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetDocumentsDocumentPatch(
        {
          dataset: requestParameters.dataset,
          document: requestParameters.document,
          doc: requestParameters.doc as never,
        },
        {
          ...withJsonOptions(options),
          params: {
            ...(options?.params ?? {}),
            update_mask: requestParameters.updateMask,
          },
        },
      );
    },
    documentServiceBatchUpdateDocumentTags(
      requestParameters: DocumentServiceApiDocumentServiceBatchUpdateDocumentTagsRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return legacyClient.documentServiceBatchUpdateDocumentTags(
        requestParameters,
        options,
      );
    },
  };
}

export function MemberServiceApi() {
  const coreClient = CoreDefaultApiFactory(CoreConfig, BASE_URL, axiosInstance);
  const legacyClient = DatasetMemberServiceApiFactory(
    Config,
    coreApiBaseUrl,
    axiosInstance,
  );

  return {
    ...legacyClient,
    datasetMemberServiceBatchAddDatasetMember(
      requestParameters: {
        dataset: string;
        batchAddDatasetMemberRequest: {
          parent?: string;
          role?: { role?: string };
          user_id_list?: string[];
          group_id_list?: string[];
          user_name_list?: string[];
          group_name_list?: string[];
        };
      },
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetBatchAddMemberPost(
        requestParameters,
        withJsonOptions(options),
      );
    },
    datasetMemberServiceListDatasetMembers(
      requestParameters: {
        dataset: string;
      },
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetMembersGet(
        { dataset: requestParameters.dataset },
        options,
      );
    },
    datasetMemberServiceDeleteDatasetMember(
      requestParameters: {
        dataset: string;
        member?: string;
        userId?: string;
        groupId?: string;
      },
      options?: RawAxiosRequestConfig,
    ) {
      const groupId =
        requestParameters.groupId ||
        requestParameters.member?.match(/\/members\/groups\/([^/]+)/)?.[1] ||
        "";

      if (groupId) {
        return coreClient.apiCoreDatasetsDatasetMembersGroupsGroupIdDelete(
          {
            dataset: requestParameters.dataset,
            groupId,
          },
          options,
        );
      }

      const userId =
        requestParameters.userId ||
        requestParameters.member?.match(/(?:^|\/)id\/([^/]+)/)?.[1] ||
        "";

      return coreClient.apiCoreDatasetsDatasetMembersUserIdDelete(
        {
          dataset: requestParameters.dataset,
          userId,
        },
        options,
      );
    },
    datasetMemberServiceUpdateDatasetMember(
      requestParameters: {
        dataset: string;
        member?: string;
        userId?: string;
        groupId?: string;
        datasetMember?: {
          user_id?: string;
          group_id?: string;
          role?: { role?: string; display_name?: string };
        };
        updateMask?: string;
      },
      options?: RawAxiosRequestConfig,
    ) {
      const groupId =
        requestParameters.groupId ||
        requestParameters.datasetMember?.group_id ||
        requestParameters.member?.match(/\/members\/groups\/([^/]+)/)?.[1] ||
        "";

      if (groupId) {
        return coreClient.apiCoreDatasetsDatasetMembersGroupsGroupIdPatch(
          {
            dataset: requestParameters.dataset,
            groupId,
            updateDatasetMemberRequest: {
              dataset_member: {
                role: requestParameters.datasetMember?.role,
              },
              update_mask: {
                paths: requestParameters.updateMask
                  ? requestParameters.updateMask.split(",")
                  : ["role"],
              },
            },
          },
          withJsonOptions(options),
        );
      }

      const userId =
        requestParameters.userId ||
        requestParameters.datasetMember?.user_id ||
        requestParameters.member?.match(/(?:^|\/)id\/([^/]+)/)?.[1] ||
        "";

      return coreClient.apiCoreDatasetsDatasetMembersUserIdPatch(
        {
          dataset: requestParameters.dataset,
          userId,
          updateDatasetMemberRequest: {
            dataset_member: {
              role: requestParameters.datasetMember?.role,
            },
            update_mask: {
              paths: requestParameters.updateMask
                ? requestParameters.updateMask.split(",")
                : ["role"],
            },
          },
        },
        withJsonOptions(options),
      );
    },
  };
}

export function JobServiceApi() {
  return JobServiceApiFactory(Config, coreApiBaseUrl, axiosInstance);
}


export interface UploadFileResponse {
  upload_file_id: string;
  filename: string;
  relative_path: string;
  stored_name: string;
  stored_path: string;
  file_size: number;
  content_type: string;
  dataset_id: string;
  document_pid: string;
  document_tags: string[];
  status: string;
}

export type UploadFilesResponse = CoreUploadFilesResponse;

export interface TaskFile {
  display_name: string;
  relative_path: string;
  stored_name: string;
  stored_path: string;
  file_size: number;
  content_type: string;
}

export interface TaskPayload {
  task_type?: string;
  data_source_type?: string;
  display_name?: string;
  document_id?: string;
  document_ids?: string[];
  document_pid?: string;
  document_tags?: string[];
  files?: TaskFile[];
  reparse_groups?: string[];
  reparse_mode?: string;
  target_dataset_id?: string;
  target_pid?: string;
  target_path?: string;
}

export interface CreateTaskItem {
  upload_file_id: string;
  task_id?: string;
  task?: TaskPayload;
  cross_dataset?: boolean;
}

export type CreateTaskRequest = CoreCreateTaskRequest;

export interface TaskDocumentInfo {
  display_name: string;
  document_id: string;
  document_size: number;
  document_state: string;
}

export interface TaskInfo {
  total_document_count: number;
  total_document_size: number;
  succeed_document_count: number;
  succeed_document_size: number;
  failed_document_count: number;
  failed_document_size: number;
}

export type TaskResponse = CoreTaskResponse;

export type CreateTasksResponse = CoreCreateTasksResponse;

export type StartTaskRequest = CoreStartTaskRequest;

export interface StartTaskResult {
  task_id: string;
  task_state: string;
  display_name: string;
  document_id: string;
  status: string;
  message: string;
}

export type StartTasksResponse = CoreStartTasksResponse;

export type SearchTasksRequest = CoreSearchTasksRequest;

export type ListTasksResponse = CoreListTasksResponse;

export function TaskServiceApi() {
  const coreClient = CoreTasksApiFactory(CoreConfig, BASE_URL, axiosInstance);
  const uploadClient = CoreDefaultApiFactory(CoreConfig, BASE_URL, axiosInstance);

  return {

    uploadFiles(
      dataset: string,
      formData: FormData,
      options?: RawAxiosRequestConfig,
    ) {
      const documentPid = formData.get("document_pid")?.toString();
      const relativePath = formData.get("relative_path")?.toString();
      const files = formData
        .getAll("files")
        .filter((value): value is File => value instanceof File);
      const documentTagsEntries = formData
        .getAll("document_tags")
        .map((value) => value.toString())
        .filter(Boolean);
      const documentTags =
        documentTagsEntries.length > 0 ? documentTagsEntries.join(",") : undefined;

      return uploadClient.apiCoreDatasetsDatasetUploadsPost(
        {
          dataset,
          documentPid,
          documentTags,
          files: files.length > 0 ? files : undefined,
          relativePath,
        },
        options,
      );
    },


    createTasks(
      dataset: string,
      body: CreateTaskRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksPost(
        {
          dataset,
          createTaskRequest: body,
        },
        options,
      );
    },


    startTasks(
      dataset: string,
      body: StartTaskRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksStartPost(
        {
          dataset,
          startTaskRequest: body,
        },
        options,
      );
    },


    listTasks(
      dataset: string,
      params?: { taskStatus?: string; pageSize?: number; pageToken?: string },
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksGet(
        {
          dataset,
          pageSize: params?.pageSize,
          pageToken: params?.pageToken,
          taskState: params?.taskStatus,
        },
        options,
      );
    },


    searchTasks(
      dataset: string,
      body: SearchTasksRequest,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksSearchPost(
        {
          dataset,
          searchTasksRequest: body,
        },
        options,
      );
    },


    suspendTask(
      dataset: string,
      task: string,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksTaskSuspendPost(
        {
          dataset,
          task,
          suspendJobRequest: {},
        },
        options,
      );
    },


    resumeTask(
      dataset: string,
      task: string,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksTaskResumePost(
        {
          dataset,
          task,
        },
        options,
      );
    },


    deleteTask(
      dataset: string,
      task: string,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksTaskDelete(
        {
          dataset,
          task,
        },
        options,
      );
    },


    getTask(
      dataset: string,
      task: string,
      options?: RawAxiosRequestConfig,
    ) {
      return coreClient.apiCoreDatasetsDatasetTasksTaskGet(
        {
          dataset,
          task,
        },
        options,
      );
    },
  };
}

export function SegmentServiceApi() {
  return SegmentServiceApiFactory(
    Config,
    coreApiBaseUrl,
    axiosInstance,
  );
}


export interface LargeFileUploadOptions {
  documentPid?: string;
  relativePath?: string;
  onProgress?: (loaded: number, total: number) => void;
  signal?: AbortSignal;
}


export async function uploadLargeFileToDataset(
  dataset: string,
  file: File,
  options?: LargeFileUploadOptions,
): Promise<string> {
  const coreClient = CoreTasksApiFactory(CoreConfig, BASE_URL, axiosInstance);

  const initRes = await coreClient.apiCoreDatasetsDatasetUploadsInitUploadPost({
    dataset,
    initUploadRequest: {
      filename: file.name,
      file_size: file.size,
      content_type: file.type || "application/octet-stream",
      document_pid: options?.documentPid,
      relative_path: options?.relativePath,
    },
  });

  const { upload_id, total_parts, part_size } = initRes.data;
  const chunkSize = part_size || 5 * 1024 * 1024;
  const parts = total_parts || Math.ceil(file.size / chunkSize);
  let uploadedBytes = 0;

  try {
    for (let i = 0; i < parts; i++) {
      if (options?.signal?.aborted) {
        throw new DOMException("Upload aborted", "AbortError");
      }

      const partNumber = i + 1;
      const start = i * chunkSize;
      const end = Math.min(start + chunkSize, file.size);
      const chunkBlob = file.slice(start, end);
      const chunkFile = new File([chunkBlob], file.name, {
        type: file.type || "application/octet-stream",
      });

      await coreClient.apiCoreDatasetsDatasetUploadsUploadIdPartsPartNumberPut({
        dataset,
        uploadId: upload_id,
        partNumber: String(partNumber),
        body: chunkFile,
      });

      uploadedBytes += end - start;
      options?.onProgress?.(uploadedBytes, file.size);
    }

    const completeRes = await coreClient.apiCoreDatasetsDatasetUploadsUploadIdCompletePost({
      dataset,
      uploadId: upload_id,
    });

    const uploadFileId = completeRes.data.upload_file_id;
    if (!uploadFileId) {
      throw new Error("large file upload complete did not return upload_file_id");
    }

    return uploadFileId;
  } catch (err) {
    try {
      await coreClient.apiCoreDatasetsDatasetUploadsUploadIdAbortPost({
        dataset,
        uploadId: upload_id,
      });
    } catch {
    }
    throw err;
  }
}

export function UsersServiceApi() {
  return UsersApiFactory(Config, `${baseUrl}/authservice/v1`, axiosInstance);
}

export function GroupsServiceApi() {
  return GroupsApiFactory(Config, `${baseUrl}/authservice/v1`, axiosInstance);
}
