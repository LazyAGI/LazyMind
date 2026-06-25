import {
  Configuration,
  type Doc,
  DocumentsApiFactory,
  EvalSetImportsApiFactory,
  EvalSetItemsApiFactory,
  EvalSetsApiFactory,
  type CreateEvalSetItemRequest,
  type CreateEvalSetRequest,
  type EvalSetImportTaskResponse,
  type EvalSetItemResponse,
  type EvalSetResponse,
  type ImportPreviewResponse,
  type UpdateEvalSetItemRequest,
  type UpdateEvalSetRequest,
} from "@/api/generated/core-client";
import { AgentAppsAuth } from "@/components/auth";
import { axiosInstance, BASE_URL } from "@/components/request";
import i18n from "@/i18n";
import {
  KnowledgeBaseServiceApi,
} from "@/modules/knowledge/utils/request";
import type {
  Dataset,
  DatasetImportRecord,
  DatasetItem,
  DatasetItemFormValues,
  DatasetItemSource,
  DatasetListItem,
  KnowledgeBaseOption,
} from "./shared";
import {
  joinListField,
  normalizeItemFormValues,
  splitListField,
} from "./utils/datasetValidation";

const coreConfig = new Configuration({ basePath: BASE_URL });

const evalSetsClient = EvalSetsApiFactory(coreConfig, BASE_URL, axiosInstance);
const evalSetItemsClient = EvalSetItemsApiFactory(coreConfig, BASE_URL, axiosInstance);
const evalSetImportsClient = EvalSetImportsApiFactory(coreConfig, BASE_URL, axiosInstance);
const documentsClient = DocumentsApiFactory(coreConfig, BASE_URL, axiosInstance);

const IMPORT_TASK_PROCESSING_STATUSES = new Set([
  "pending",
  "processing",
  "running",
  "queued",
]);

const IMPORT_TASK_SUCCESS_STATUSES = new Set(["success", "completed", "done", "finished"]);

export interface DatasetItemListResult {
  items: DatasetItem[];
  total: number;
}

export interface KnowledgeDocumentOption {
  documentId: string;
  datasetId?: string;
  name: string;
}

export interface KnowledgeDocumentSearchResult {
  options: KnowledgeDocumentOption[];
  nextPageToken?: string;
  totalSize?: number;
}

export async function findKnowledgeBaseDocumentById(
  knowledgeBaseIds: string[],
  documentId: string,
): Promise<KnowledgeDocumentOption | null> {
  const normalizedKnowledgeBaseIds = Array.from(
    new Set((knowledgeBaseIds || []).map((item) => `${item || ""}`.trim())),
  ).filter(Boolean);
  const normalizedDocumentId = `${documentId || ""}`.trim();
  if (normalizedKnowledgeBaseIds.length === 0 || !normalizedDocumentId) {
    return null;
  }

  for (const knowledgeBaseId of normalizedKnowledgeBaseIds) {
    let pageToken = "";
    for (let attempt = 0; attempt < 20; attempt += 1) {
      const response = await documentsClient.apiCoreDatasetsDatasetDocumentsGet({
        dataset: knowledgeBaseId,
        pageToken: pageToken || undefined,
        pageSize: 100,
      });
      const payload = unwrapPayload(
        response.data as {
          documents?: Doc[];
          next_page_token?: string;
        },
      );
      const matchedDocument = (payload.documents || []).find(
        (item) => `${item.document_id || ""}`.trim() === normalizedDocumentId,
      );
      if (matchedDocument) {
        return {
          documentId: normalizedDocumentId,
          datasetId: knowledgeBaseId,
          name:
            `${matchedDocument.display_name || matchedDocument.name || normalizedDocumentId}`.trim(),
        };
      }

      pageToken = `${payload.next_page_token || ""}`.trim();
      if (!pageToken) {
        break;
      }
    }
  }

  return null;
}

export function mergeKnowledgeDocumentOptions(
  currentOptions: KnowledgeDocumentOption[],
  nextOptions: KnowledgeDocumentOption[],
) {
  const seen = new Set<string>();
  return [...currentOptions, ...nextOptions].filter((option) => {
    const dedupeKey = `${option.name || option.documentId}`.trim().toLowerCase();
    if (!dedupeKey || seen.has(dedupeKey)) {
      return false;
    }
    seen.add(dedupeKey);
    return true;
  });
}

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

function unwrapPayload<T>(payload: T | ApiEnvelope<T> | undefined | null): T {
  if (!payload || typeof payload !== "object") {
    return payload as T;
  }

  const envelope = payload as ApiEnvelope<T>;
  if ("data" in envelope && envelope.data !== undefined) {
    return envelope.data;
  }

  return payload as T;
}

function resolveCurrentGroupId() {
  const userInfo = AgentAppsAuth.getUserInfo() as
    | ({
        groupId?: string;
        group_id?: string;
        defaultGroupId?: string;
        currentGroupId?: string;
        groupIds?: string[];
        group_ids?: string[];
        tenantId?: string;
        tenant_id?: string;
        tenantKey?: string;
        tenant_key?: string;
      } & ReturnType<typeof AgentAppsAuth.getUserInfo>)
    | null;

  const directCandidates = [
    userInfo?.groupId,
    userInfo?.group_id,
    userInfo?.defaultGroupId,
    userInfo?.currentGroupId,
    userInfo?.tenantId,
    userInfo?.tenant_id,
    userInfo?.tenantKey,
    userInfo?.tenant_key,
  ];

  for (const candidate of directCandidates) {
    if (`${candidate || ""}`.trim()) {
      return `${candidate}`.trim();
    }
  }

  const listCandidates = [userInfo?.groupIds, userInfo?.group_ids];
  for (const list of listCandidates) {
    const firstValue = Array.isArray(list) ? list.find((item) => `${item || ""}`.trim()) : "";
    if (firstValue) {
      return `${firstValue}`.trim();
    }
  }

  return "";
}

function ensureGroupId(groupId?: string) {
  return `${groupId || resolveCurrentGroupId()}`.trim();
}

function normalizeItemSource(source?: string): DatasetItemSource {
  if (source === "upload" || source === "manual" || source === "flowback") {
    return source;
  }
  return "manual";
}

function mapEvalSetToDatasetListItem(item: EvalSetResponse): DatasetListItem {
  const datasetIds = Array.isArray(item.dataset_ids) ? item.dataset_ids : [];
  const datasetNames = Array.isArray(item.dataset_names) ? item.dataset_names : [];
  const knowledgeBase = datasetIds
    .map((datasetId, index) => {
      const id = `${datasetId || ""}`.trim();
      if (!id) {
        return null;
      }
      return {
        id,
        name: `${datasetNames[index] || datasetId || ""}`.trim() || id,
      };
    })
    .filter((item): item is KnowledgeBaseOption => Boolean(item));

  return {
    id: item.id,
    name: item.name,
    description: item.description,
    owner_id: item.created_by,
    owner_name: item.created_by_name || item.created_by,
    group_id: item.group_id,
    created_at: item.created_at,
    updated_at: item.updated_at,
    knowledge_bases: knowledgeBase,
    sample_count: item.item_count,
  };
}

function mapEvalSetItemToDatasetItem(item: EvalSetItemResponse): DatasetItem {
  return {
    id: item.id,
    dataset_id: item.eval_set_id,
    case_id: item.case_id,
    question: item.question,
    question_type: item.question_type,
    ground_truth: item.ground_truth,
    key_points: item.key_points,
    reference_context: item.reference_context,
    reference_doc: item.reference_doc,
    reference_doc_ids: splitListField(item.reference_doc_ids),
    reference_chunk_ids: splitListField(item.reference_chunk_ids),
    reference_doc_from_knowledge_base: item.reference_doc_from_knowledge_base,
    reference_chunk_selected: item.reference_chunk_selected,
    generate_reason: item.generate_reason,
    is_deleted: item.is_deleted,
    source: normalizeItemSource(item.source),
    source_session_id: item.source_session_id,
    created_at: item.created_at,
    updated_at: item.updated_at,
    created_by: item.created_by_name || item.created_by,
  };
}

function buildCreateEvalSetPayload(payload: {
  name: string;
  description?: string;
  knowledge_base_ids?: string[];
}): CreateEvalSetRequest {
  const datasetIds = Array.from(
    new Set((payload.knowledge_base_ids || []).map((item) => `${item || ""}`.trim())),
  ).filter(Boolean);
  if (datasetIds.length === 0) {
    throw new Error(i18n.t("datasetManagement.form.validation.knowledgeBaseRequired"));
  }
  return {
    name: payload.name.trim(),
    description: `${payload.description || ""}`.trim(),
    dataset_ids: datasetIds,
    group_id: ensureGroupId(),
  };
}

function buildUpdateEvalSetPayload(
  current: DatasetListItem,
  payload: {
    name: string;
    description?: string;
    knowledge_base_ids?: string[];
  },
): UpdateEvalSetRequest {
  // Use submitted IDs when explicitly provided (even if empty), otherwise keep current ones.
  // An empty array is valid when the user intentionally clears all KB associations
  // (e.g. the previously linked KB was deleted).
  const sourceIds =
    payload.knowledge_base_ids !== undefined
      ? payload.knowledge_base_ids
      : (current.knowledge_bases || []).map((item) => `${item.id || ""}`.trim());
  const datasetIds = Array.from(
    new Set(sourceIds.map((item) => `${item || ""}`.trim())),
  ).filter(Boolean);
  return {
    name: payload.name.trim(),
    description: `${payload.description || ""}`.trim(),
    dataset_ids: datasetIds,
    group_id: ensureGroupId(current.group_id),
  };
}

function buildCreateEvalSetItemPayload(values: DatasetItemFormValues): CreateEvalSetItemRequest {
  const normalized = normalizeItemFormValues(values);
  return {
    case_id: normalized.case_id,
    question: normalized.question,
    question_type: normalized.question_type,
    ground_truth: normalized.ground_truth,
    key_points: normalized.key_points,
    reference_context: normalized.reference_context,
    reference_doc: normalized.reference_doc,
    reference_doc_ids: joinListField(normalized.reference_doc_ids),
    reference_chunk_ids: joinListField(normalized.reference_chunk_ids),
    generate_reason: normalized.generate_reason,
    is_deleted: normalized.is_deleted,
  };
}

function buildUpdateEvalSetItemPayload(values: DatasetItemFormValues): UpdateEvalSetItemRequest {
  const normalized = normalizeItemFormValues(values);
  return {
    case_id: normalized.case_id,
    question: normalized.question,
    question_type: normalized.question_type,
    ground_truth: normalized.ground_truth,
    key_points: normalized.key_points,
    reference_context: normalized.reference_context,
    reference_doc: normalized.reference_doc,
    reference_doc_ids: joinListField(normalized.reference_doc_ids),
    reference_chunk_ids: joinListField(normalized.reference_chunk_ids),
    generate_reason: normalized.generate_reason,
    is_deleted: normalized.is_deleted,
  };
}

function getFileType(file: File) {
  const extension = file.name.split(".").pop()?.toLowerCase() || "";
  if (["xlsx", "xls", "csv", "json"].includes(extension)) {
    return extension;
  }
  return undefined;
}

function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForImportTask(taskId: string): Promise<EvalSetImportTaskResponse> {
  let latestTask: EvalSetImportTaskResponse | null = null;

  for (let attempt = 0; attempt < 20; attempt += 1) {
    const response = await evalSetImportsClient.apiCoreEvalSetImportTasksTaskIdGet({
      taskId,
    });
    latestTask = unwrapPayload(response.data);

    const status = `${latestTask.status || ""}`.trim().toLowerCase();
    if (latestTask.finished_at || IMPORT_TASK_SUCCESS_STATUSES.has(status)) {
      return latestTask;
    }
    if (status && !IMPORT_TASK_PROCESSING_STATUSES.has(status)) {
      return latestTask;
    }

    await sleep(1000);
  }

  if (!latestTask) {
    throw new Error(i18n.t("datasetManagement.import.importFailed"));
  }

  return latestTask;
}

async function previewImportFile(file: File): Promise<ImportPreviewResponse> {
  const response = await evalSetImportsClient.apiCoreEvalSetsImportsPreviewPost({
    file: file as unknown as string,
    fileType: getFileType(file),
  });
  return unwrapPayload(response.data);
}

export async function listKnowledgeBases(): Promise<KnowledgeBaseOption[]> {
  const response = await KnowledgeBaseServiceApi().datasetServiceListDatasets({
    pageSize: 1000,
  });

  const payload = unwrapPayload(response.data as { datasets?: Array<Record<string, unknown>> });

  return (payload.datasets || [])
    .map((item) => ({
      id: `${item.dataset_id || ""}`.trim(),
      name: `${item.display_name || item.name || item.dataset_id || ""}`.trim(),
    }))
    .filter((item) => item.id && item.name);
}

export async function searchKnowledgeBaseDocuments(
  knowledgeBaseIds: string[],
  keyword: string,
  pageToken?: string,
): Promise<KnowledgeDocumentSearchResult> {
  const normalizedKnowledgeBaseIds = Array.from(
    new Set((knowledgeBaseIds || []).map((item) => `${item || ""}`.trim())),
  ).filter(Boolean);
  const normalizedKeyword = `${keyword || ""}`.trim();
  if (normalizedKnowledgeBaseIds.length === 0) {
    return {
      options: [],
    };
  }

  const response = await documentsClient.apiCoreDocumentsListByDatasetsPost({
    listDatasetDocumentsRequest: {
      dataset_ids: normalizedKnowledgeBaseIds,
      keyword: normalizedKeyword || undefined,
      page_size: 10,
      page_token: `${pageToken || ""}`.trim() || undefined,
    },
  });

  const payload = unwrapPayload(
    response.data as {
      documents?: Doc[];
      next_page_token?: string;
      total_size?: number;
    },
  );
  return {
    options: mergeKnowledgeDocumentOptions(
      [],
      (payload.documents || [])
        .map((item) => ({
          documentId: `${item.document_id || ""}`.trim(),
          datasetId: `${item.dataset_id || ""}`.trim() || undefined,
          name: `${item.display_name || item.name || item.document_id || ""}`.trim(),
        }))
        .filter((item) => item.documentId && item.name),
    ),
    nextPageToken: `${payload.next_page_token || ""}`.trim() || undefined,
    totalSize: payload.total_size,
  };
}

export async function listQuestionTypes(): Promise<string[]> {
  const response = await evalSetsClient.apiCoreEvalSetsQuestionTypesGet();
  const payload = unwrapPayload(response.data);
  return (payload.items || [])
    .map((item) => `${item.value || item.label || ""}`.trim())
    .filter(Boolean);
}

export async function listDatasetQuestionTypes(datasetId: string): Promise<string[]> {
  const normalizedDatasetId = `${datasetId || ""}`.trim();
  if (!normalizedDatasetId) {
    return [];
  }
  const response = await axiosInstance.get(
    `/api/core/eval-sets/${encodeURIComponent(normalizedDatasetId)}/question-types`,
  );
  const payload = unwrapPayload(response.data as { items?: Array<{ label?: string; value?: string }> });
  return (payload.items || [])
    .map((item) => `${item.value || item.label || ""}`.trim())
    .filter(Boolean);
}

export async function listDatasets(keyword?: string): Promise<DatasetListItem[]> {
  const response = await evalSetsClient.apiCoreEvalSetsGet({
    keyword: `${keyword || ""}`.trim() || undefined,
    page: 1,
    pageSize: 1000,
  });

  const payload = unwrapPayload(response.data);
  return (payload.items || []).map(mapEvalSetToDatasetListItem);
}

export async function createDataset(payload: {
  name: string;
  description?: string;
  knowledge_base_ids?: string[];
}): Promise<DatasetListItem> {
  const response = await evalSetsClient.apiCoreEvalSetsPost({
    createEvalSetRequest: buildCreateEvalSetPayload(payload),
  });
  return mapEvalSetToDatasetListItem(unwrapPayload(response.data));
}

export async function updateDataset(
  datasetId: string,
  payload: {
    name: string;
    description?: string;
    knowledge_base_ids?: string[];
  },
): Promise<DatasetListItem> {
  const currentDataset = await getDataset(datasetId);
  const response = await evalSetsClient.apiCoreEvalSetsEvalSetIdPatch({
    evalSetId: datasetId,
    updateEvalSetRequest: buildUpdateEvalSetPayload(currentDataset, payload),
  });
  return mapEvalSetToDatasetListItem(unwrapPayload(response.data));
}

export async function deleteDataset(datasetId: string) {
  await evalSetsClient.apiCoreEvalSetsEvalSetIdDelete({
    evalSetId: datasetId,
  });
}

export async function getDataset(datasetId: string): Promise<DatasetListItem> {
  const response = await evalSetsClient.apiCoreEvalSetsEvalSetIdGet({
    evalSetId: datasetId,
  });
  return mapEvalSetToDatasetListItem(unwrapPayload(response.data));
}

export async function listDatasetItems(
  datasetId: string,
  filters: {
    keyword?: string;
    question_type?: string;
    source?: DatasetItem["source"];
    page?: number;
    pageSize?: number;
  } = {},
): Promise<DatasetItemListResult> {
  const response = await evalSetItemsClient.apiCoreEvalSetsEvalSetIdItemsGet({
    evalSetId: datasetId,
    keyword: `${filters.keyword || ""}`.trim() || undefined,
    questionType: `${filters.question_type || ""}`.trim() || undefined,
    source: filters.source || undefined,
    page: filters.page,
    pageSize: filters.pageSize,
  });

  const payload = unwrapPayload(response.data);
  return {
    items: (payload.items || []).map(mapEvalSetItemToDatasetItem),
    total: payload.total || 0,
  };
}

export async function createDatasetItem(
  datasetId: string,
  values: DatasetItemFormValues,
): Promise<DatasetItem> {
  const response = await evalSetItemsClient.apiCoreEvalSetsEvalSetIdItemsPost({
    evalSetId: datasetId,
    createEvalSetItemRequest: buildCreateEvalSetItemPayload(values),
  });
  return mapEvalSetItemToDatasetItem(unwrapPayload(response.data));
}

export async function updateDatasetItem(
  datasetId: string,
  itemId: string,
  values: DatasetItemFormValues,
): Promise<DatasetItem> {
  const response = await evalSetItemsClient.apiCoreEvalSetsEvalSetIdItemsItemIdPatch({
    evalSetId: datasetId,
    itemId,
    updateEvalSetItemRequest: buildUpdateEvalSetItemPayload(values),
  });
  return mapEvalSetItemToDatasetItem(unwrapPayload(response.data));
}

export async function deleteDatasetItem(datasetId: string, itemId: string) {
  await evalSetItemsClient.apiCoreEvalSetsEvalSetIdItemsItemIdDelete({
    evalSetId: datasetId,
    itemId,
  });
}

export async function batchDeleteDatasetItems(datasetId: string, itemIds: string[]) {
  await evalSetItemsClient.apiCoreEvalSetsEvalSetIdItemsBatchDeletePost({
    evalSetId: datasetId,
    batchDeleteEvalSetItemsRequest: {
      item_ids: itemIds,
    },
  });
}

export async function importDatasetItems(
  datasetId: string,
  file: File | null,
  items: Array<Partial<DatasetItem>>,
  _failedCount: number,
) {
  if (file) {
    const preview = await previewImportFile(file);
    const appendResponse = await evalSetImportsClient.apiCoreEvalSetsEvalSetIdImportsPost({
      evalSetId: datasetId,
      appendEvalSetImportRequest: {
        import_token: preview.import_token,
      },
    });

    const appendPayload = unwrapPayload(appendResponse.data);
    const task = await waitForImportTask(appendPayload.task_id);
    const status = `${task.status || ""}`.trim().toLowerCase();
    if (!IMPORT_TASK_SUCCESS_STATUSES.has(status) && task.error_message) {
      throw new Error(task.error_message);
    }

    return task;
  }

  const createdItems = await Promise.all(
    items.map((item) =>
      createDatasetItem(datasetId, {
        case_id: item.case_id || "",
        question: item.question || "",
        question_type: item.question_type || "",
        ground_truth: item.ground_truth || "",
        key_points: item.key_points || "",
        reference_context: item.reference_context || "",
        reference_doc: item.reference_doc || "",
        reference_doc_ids: joinListField(item.reference_doc_ids),
        reference_chunk_ids: joinListField(item.reference_chunk_ids),
        generate_reason: item.generate_reason || "",
        is_deleted: Boolean(item.is_deleted),
      }),
    ),
  );

  return createdItems;
}

export async function listImportRecords(_datasetId: string): Promise<DatasetImportRecord[]> {
  return [];
}

export type { Dataset };
