import type {
  Dataset,
  DatasetImportRecord,
  DatasetItem,
  DatasetItemSource,
  DatasetListItem,
  DatasetItemFormValues,
  KnowledgeBaseOption,
} from "./shared";
import { mockDatasets, mockDatasetItems, mockImportRecords } from "./constants";
import { mockKnowledgeBases } from "./shared";
import { normalizeItemFormValues } from "./utils/datasetValidation";
import { AgentAppsAuth } from "@/components/auth";

let datasets = [...mockDatasets];
let datasetItems: Record<string, DatasetItem[]> = Object.fromEntries(
  Object.entries(mockDatasetItems).map(([key, value]) => [key, [...value]]),
);
let importRecords: Record<string, DatasetImportRecord[]> = Object.fromEntries(
  Object.entries(mockImportRecords).map(([key, value]) => [key, [...value]]),
);

function wait(ms = 160) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function now() {
  return new Date().toISOString();
}

function createId(prefix: string) {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
}

function getCurrentUsername() {
  const userInfo = AgentAppsAuth.getUserInfo();
  return userInfo?.displayName || userInfo?.username || userInfo?.userId || "admin";
}

function refreshDatasetStats(datasetId: string) {
  const items = datasetItems[datasetId] || [];
  const stats: Partial<Record<DatasetItemSource, number>> = {};
  items.forEach((item) => {
    stats[item.source] = (stats[item.source] || 0) + 1;
  });

  datasets = datasets.map((dataset) =>
    dataset.id === datasetId
      ? {
          ...dataset,
          sample_count: items.length,
          source_stats: stats,
          updated_at: now(),
        }
      : dataset,
  );
}

export async function listKnowledgeBases(): Promise<KnowledgeBaseOption[]> {
  await wait();
  return mockKnowledgeBases;
}

export async function listDatasets(keyword?: string): Promise<DatasetListItem[]> {
  await wait();
  const normalizedKeyword = `${keyword || ""}`.trim().toLowerCase();
  if (!normalizedKeyword) {
    return datasets;
  }
  return datasets.filter((dataset) => {
    const haystack = `${dataset.name} ${dataset.description || ""}`.toLowerCase();
    return haystack.includes(normalizedKeyword);
  });
}

export async function createDataset(payload: {
  name: string;
  description?: string;
  knowledge_base_ids?: string[];
}): Promise<DatasetListItem> {
  await wait();
  const dataset: DatasetListItem = {
    id: createId("dataset"),
    name: payload.name,
    description: payload.description,
    owner_id: AgentAppsAuth.getUserInfo()?.userId || "current-user",
    owner_name: getCurrentUsername(),
    group_id: "current-group",
    created_at: now(),
    updated_at: now(),
    knowledge_bases: mockKnowledgeBases.filter((item) =>
      (payload.knowledge_base_ids || []).includes(item.id),
    ),
    sample_count: 0,
    source_stats: { upload: 0, manual: 0, flowback: 0 },
  };
  datasets = [dataset, ...datasets];
  datasetItems[dataset.id] = [];
  importRecords[dataset.id] = [];
  return dataset;
}

export async function updateDataset(
  datasetId: string,
  payload: {
    name: string;
    description?: string;
    knowledge_base_ids?: string[];
  },
): Promise<DatasetListItem> {
  await wait();
  const knowledgeBases = mockKnowledgeBases.filter((item) =>
    (payload.knowledge_base_ids || []).includes(item.id),
  );
  let updated: DatasetListItem | undefined;
  datasets = datasets.map((dataset) => {
    if (dataset.id !== datasetId) {
      return dataset;
    }
    updated = {
      ...dataset,
      name: payload.name,
      description: payload.description,
      knowledge_bases: knowledgeBases,
      updated_at: now(),
    };
    return updated;
  });
  if (!updated) {
    throw new Error("数据集不存在");
  }
  return updated;
}

export async function deleteDataset(datasetId: string) {
  await wait();
  datasets = datasets.filter((dataset) => dataset.id !== datasetId);
  delete datasetItems[datasetId];
  delete importRecords[datasetId];
}

export async function getDataset(datasetId: string): Promise<DatasetListItem> {
  await wait();
  const dataset = datasets.find((item) => item.id === datasetId);
  if (!dataset) {
    throw new Error("数据集不存在");
  }
  return dataset;
}

export async function listDatasetItems(
  datasetId: string,
  filters: {
    keyword?: string;
    question_type?: string;
    source?: DatasetItem["source"];
  } = {},
): Promise<DatasetItem[]> {
  await wait();
  const keyword = `${filters.keyword || ""}`.trim().toLowerCase();
  return (datasetItems[datasetId] || []).filter((item) => {
    if (filters.question_type && item.question_type !== filters.question_type) {
      return false;
    }
    if (filters.source && item.source !== filters.source) {
      return false;
    }
    if (!keyword) {
      return true;
    }
    return `${item.question} ${item.ground_truth}`.toLowerCase().includes(keyword);
  });
}

export async function createDatasetItem(
  datasetId: string,
  values: DatasetItemFormValues,
): Promise<DatasetItem> {
  await wait();
  const normalized = normalizeItemFormValues(values);
  const item: DatasetItem = {
    id: createId("item"),
    dataset_id: datasetId,
    ...normalized,
    source: "manual",
    created_at: now(),
    updated_at: now(),
    created_by: "当前用户",
  };
  datasetItems[datasetId] = [item, ...(datasetItems[datasetId] || [])];
  refreshDatasetStats(datasetId);
  return item;
}

export async function updateDatasetItem(
  datasetId: string,
  itemId: string,
  values: DatasetItemFormValues,
): Promise<DatasetItem> {
  await wait();
  const normalized = normalizeItemFormValues(values);
  let updated: DatasetItem | undefined;
  datasetItems[datasetId] = (datasetItems[datasetId] || []).map((item) => {
    if (item.id !== itemId) {
      return item;
    }
    updated = {
      ...item,
      ...normalized,
      updated_at: now(),
    };
    return updated;
  });
  if (!updated) {
    throw new Error("样本不存在");
  }
  refreshDatasetStats(datasetId);
  return updated;
}

export async function deleteDatasetItem(datasetId: string, itemId: string) {
  await wait();
  datasetItems[datasetId] = (datasetItems[datasetId] || []).filter(
    (item) => item.id !== itemId,
  );
  refreshDatasetStats(datasetId);
}

export async function batchDeleteDatasetItems(datasetId: string, itemIds: string[]) {
  await wait();
  const removeSet = new Set(itemIds);
  datasetItems[datasetId] = (datasetItems[datasetId] || []).filter(
    (item) => !removeSet.has(item.id),
  );
  refreshDatasetStats(datasetId);
}

export async function importDatasetItems(
  datasetId: string,
  file: File | null,
  items: Array<Partial<DatasetItem>>,
  failedCount: number,
) {
  await wait();
  const nextItems: DatasetItem[] = items.map((item) => ({
    id: createId("item"),
    dataset_id: datasetId,
    case_id: item.case_id || "",
    question: item.question || "",
    question_type: item.question_type || "",
    ground_truth: item.ground_truth || "",
    key_points: item.key_points || "",
    reference_context: item.reference_context || "",
    reference_doc: item.reference_doc || "",
    reference_doc_ids: item.reference_doc_ids || [],
    reference_chunk_ids: item.reference_chunk_ids || [],
    generate_reason: item.generate_reason || "",
    is_deleted: Boolean(item.is_deleted),
    source: "upload",
    created_at: now(),
    updated_at: now(),
    created_by: "当前用户",
  }));
  datasetItems[datasetId] = [...nextItems, ...(datasetItems[datasetId] || [])];
  importRecords[datasetId] = [
    {
      id: createId("import"),
      file_name: file?.name || "dataset-import.json",
      file_type: file?.name.toLowerCase().endsWith(".csv")
        ? "csv"
        : file?.name.toLowerCase().endsWith(".json")
          ? "json"
          : "xlsx",
      file_size: file?.size,
      import_status: "success",
      success_count: nextItems.length,
      failed_count: failedCount,
      created_by: "当前用户",
      created_at: now(),
    },
    ...(importRecords[datasetId] || []),
  ];
  refreshDatasetStats(datasetId);
  return nextItems;
}

export async function listImportRecords(datasetId: string) {
  await wait();
  return importRecords[datasetId] || [];
}

export type { Dataset };
