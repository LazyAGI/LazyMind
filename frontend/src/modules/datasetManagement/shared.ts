export type DatasetItemSource = "upload" | "flowback" | "manual";
export type DatasetFileType = "xlsx" | "xls" | "csv" | "json";
export type ImportStep = "selectFile" | "preview" | "result";

export interface KnowledgeBaseOption {
  id: string;
  name: string;
}

export interface Dataset {
  id: string;
  name: string;
  description?: string;
  owner_id: string;
  owner_name?: string;
  group_id: string;
  created_at: string;
  updated_at: string;
}

export interface DatasetListItem extends Dataset {
  knowledge_bases?: KnowledgeBaseOption[];
  sample_count?: number;
  source_stats?: Partial<Record<DatasetItemSource, number>>;
}

export interface DatasetItem {
  id: string;
  dataset_id: string;
  case_id?: string;
  question: string;
  question_type: string;
  ground_truth: string;
  key_points?: string;
  reference_context?: string;
  reference_doc?: string;
  reference_doc_ids?: string[];
  reference_chunk_ids?: string[];
  reference_doc_from_knowledge_base?: boolean;
  reference_chunk_selected?: boolean;
  generate_reason?: string;
  is_deleted?: boolean;
  source: DatasetItemSource;
  source_session_id?: string;
  created_at: string;
  updated_at: string;
  created_by: string;
}

export interface DatasetItemFormValues {
  case_id?: string;
  question: string;
  question_type: string;
  ground_truth: string;
  key_points?: string;
  reference_context?: string;
  reference_doc?: string;
  reference_doc_ids?: string;
  reference_chunk_ids?: string;
  generate_reason?: string;
  is_deleted?: boolean;
}

export interface DatasetFormValues {
  name: string;
  description?: string;
  knowledge_base_ids?: string[];
}

export interface DatasetImportRecord {
  id: string;
  file_name: string;
  file_type: DatasetFileType;
  file_size?: number;
  import_status: "processing" | "success" | "failed";
  success_count: number;
  failed_count: number;
  created_by: string;
  created_at: string;
}

export type DatasetItemField =
  | "case_id"
  | "question"
  | "question_type"
  | "ground_truth"
  | "key_points"
  | "reference_context"
  | "reference_doc"
  | "reference_doc_ids"
  | "reference_chunk_ids"
  | "generate_reason"
  | "is_deleted";

export type FieldMapping = Record<string, DatasetItemField | "">;

export interface DatasetImportRow {
  rowIndex: number;
  raw: Record<string, unknown>;
  normalized: Partial<DatasetItem>;
  errors: string[];
}

export interface DatasetImportResultState {
  successCount: number;
  failedCount: number;
  failedRows: DatasetImportRow[];
}

export const datasetItemFields: DatasetItemField[] = [
  "case_id",
  "question",
  "question_type",
  "ground_truth",
  "key_points",
  "reference_context",
  "reference_doc",
  "reference_doc_ids",
  "reference_chunk_ids",
  "generate_reason",
  "is_deleted",
];

export const requiredDatasetItemFields: DatasetItemField[] = [
  "question",
  "question_type",
  "ground_truth",
];

export const questionTypeOptions = [
  "事实问答",
  "总结问答",
  "推理问答",
  "多跳问答",
  "操作问答",
  "排障问答",
];

export const mockKnowledgeBases: KnowledgeBaseOption[] = [
  { id: "kb-after-sales", name: "售后知识库" },
  { id: "kb-product-manual", name: "产品手册库" },
  { id: "kb-model-config", name: "模型配置库" },
  { id: "kb-troubleshooting", name: "排障手册库" },
];

export const sourceLabelMap: Record<DatasetItemSource, string> = {
  upload: "上传",
  manual: "手动",
  flowback: "回流",
};

export const sourceColorMap: Record<DatasetItemSource, string> = {
  upload: "blue",
  manual: "green",
  flowback: "purple",
};

export function formatDateTime(value?: string) {
  if (!value) {
    return "-";
  }
  return value.replace("T", " ").slice(0, 16);
}

export function formatFileSize(size?: number) {
  if (!size) {
    return "-";
  }
  if (size < 1024) {
    return `${size} B`;
  }
  if (size < 1024 * 1024) {
    return `${(size / 1024).toFixed(1)} KB`;
  }
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}
