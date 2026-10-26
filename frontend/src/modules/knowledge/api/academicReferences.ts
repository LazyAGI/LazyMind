import { axiosInstance, BASE_URL } from "@/components/request";
import axios from "axios";

interface Envelope<T> { data: T }

export interface AcademicReference {
  ID?: string;
  id?: string;
  ReferenceKey?: string;
  reference_key?: string;
  RawText?: string;
  raw_text?: string;
  Title?: string;
  title?: string;
  AuthorsJSON?: string[];
  authors_json?: string[];
  PublicationYear?: number;
  publication_year?: number;
  SourceDocumentID?: string;
  source_document_id?: string;
  DOINormalized?: string;
  doi_normalized?: string;
  ArxivIDBase?: string;
  arxiv_id_base?: string;
  ResolvedWorkID?: string;
  resolved_work_id?: string;
  ResolutionStatus?: string;
  resolution_status?: string;
  Page?: number;
  page?: number;
  BBoxJSON?: number[] | { regions?: Array<{ page: number; bbox: number[] }> };
  bbox_json?: number[] | { regions?: Array<{ page: number; bbox: number[] }> };
}

export interface FulltextCandidate {
  url: string;
  source_type: string;
  source_provider?: string;
  version?: string;
  license?: string;
  expected_mime?: string;
}

export interface ImportPreviewItem {
  preview_token?: string;
  paper?: import("./paperWorkflows").PaperRef;
  work_id: string;
  reference_ids?: string[];
  title: string;
  doi?: string;
  arxiv_id?: string;
  disposition: "already_present" | "downloadable" | "metadata_only" | "importing";
  presence: {
    status: string;
    current_dataset: Array<{ dataset_id: string; dataset_name: string; document_id: string; document_name: string; source_provider?: string; source_version?: string; content_sha256?: string }>;
    other_datasets: Array<{ dataset_id: string; dataset_name: string; document_id: string; document_name: string; source_provider?: string; source_version?: string; content_sha256?: string }>;
    importing: boolean;
    coverage_complete: boolean;
  };
  fulltext_candidates: FulltextCandidate[];
}

export interface AcademicImportQueueItem {
  target_dataset_id: string;
  source_provider?: string;
  source_version?: string;
  batch_status?: string;
  batch_failed_items?: number;
  current_document?: { document_id: string; processing_status: string; processing_error?: string };
  item_id: string;
  batch_id: string;
  work_id: string;
  title: string;
  status: "pending" | "running" | "succeeded" | "failed" | "skipped" | "canceled";
  stage: string;
  document_id?: string;
  error_code?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
}

export const isAcademicImportActive = (item: AcademicImportQueueItem) => ['pending', 'running'].includes(item.status)
  || (item.status === 'failed' && ['pending', 'running'].includes(item.batch_status || ''));

const core = `${BASE_URL}/api/core/academic`;
const unwrap = <T>(value: T | Envelope<T>): T =>
  value && typeof value === "object" && "data" in value ? (value as Envelope<T>).data : value as T;

interface ImportPreviewResponse { preview_token?: string; items: ImportPreviewItem[] }
function previewItems(value: ImportPreviewResponse | Envelope<ImportPreviewResponse>): ImportPreviewItem[] {
  const result = unwrap<ImportPreviewResponse>(value);
  return (result.items || []).map(item => ({ ...item, preview_token: result.preview_token }));
}
export function isImportPreviewExpired(error: unknown): boolean {
  return axios.isAxiosError(error) && ['IMPORT_PREVIEW_EXPIRED', 'IMPORT_PREVIEW_MISMATCH'].includes(error.response?.data?.code);
}

export async function listAcademicReferences(documentId: string): Promise<AcademicReference[]> {
  const response = await axiosInstance.get(`${core}/documents/${encodeURIComponent(documentId)}/references`, { silentError: true } as never);
  return unwrap<{ references: AcademicReference[] }>(response.data).references || [];
}

export async function extractAcademicReferences(documentId: string, silentError = false): Promise<AcademicReference[]> {
  try {
    const response = await axiosInstance.post(
      `${core}/documents/${encodeURIComponent(documentId)}/references:extract`,
      {},
      silentError ? ({ silentError: true } as never) : undefined,
    );
    return unwrap<{ references: AcademicReference[] }>(response.data).references || [];
  } catch (error) {
    if (axios.isAxiosError(error)) {
      const payload = error.response?.data as { message?: string } | undefined;
      if (payload?.message) throw new Error(payload.message);
    }
    throw error;
  }
}

export async function resolveAcademicReferences(referenceIds: string[]): Promise<void> {
  await axiosInstance.post(`${core}/references:resolve`, { reference_ids: referenceIds });
}

export async function previewReferenceImport(
  referenceIds: string[],
  targetDatasetId: string,
  resolveExternal = true,
): Promise<ImportPreviewItem[]> {
  const response = await axiosInstance.post(`${core}/imports:preview`, {
    reference_ids: referenceIds,
    target_dataset_id: targetDatasetId,
    resolve_external: resolveExternal,
  });
  return previewItems(response.data);
}

export async function previewDocumentReferencesImport(sourceDocumentIds: string[], targetDatasetId: string): Promise<ImportPreviewItem[]> {
  const response = await axiosInstance.post(`${core}/imports:preview`, {
    source_document_ids: sourceDocumentIds,
    target_dataset_id: targetDatasetId,
  });
  return previewItems(response.data);
}

export async function startReferenceImport(targetDatasetId: string, sourceDocumentIds: string[], items: ImportPreviewItem[], idempotencyKey: string = crypto.randomUUID()): Promise<{ batch_id: string }> {
  const response = await axiosInstance.post(`${core}/imports`, {
    entry_type: items.length === 1 ? "single_reference" : "paper_references",
    preview_token: items[0]?.preview_token,
    target_dataset_id: targetDatasetId,
    source_document_ids: sourceDocumentIds,
    idempotency_key: idempotencyKey,
    items: items.filter((item) => item.fulltext_candidates.length).map((item) => ({
      work_id: item.work_id,
      reference_ids: item.reference_ids || [],
      candidate: item.fulltext_candidates[0],
    })),
  });
  return unwrap<{ batch_id: string }>(response.data);
}

export async function enqueueReferenceImport(targetDatasetId: string, sourceDocumentIds: string[], referenceIds: string[]): Promise<{ batch_id: string; total_items: number }> {
  const response = await axiosInstance.post(`${core}/imports`, {
    entry_type: referenceIds.length === 1 ? "single_reference" : "paper_references",
    target_dataset_id: targetDatasetId,
    source_document_ids: sourceDocumentIds,
    idempotency_key: crypto.randomUUID(),
    items: referenceIds.map((referenceId) => ({ reference_ids: [referenceId] })),
  });
  return unwrap<{ batch_id: string; total_items: number }>(response.data);
}

export async function listAcademicImportQueue(targetDatasetId: string, sourceDocumentId: string): Promise<AcademicImportQueueItem[]> {
  const response = await axiosInstance.get(`${core}/imports`, {
    params: { target_dataset_id: targetDatasetId, source_document_id: sourceDocumentId },
    silentError: true,
  } as never);
  return unwrap<{ items: AcademicImportQueueItem[] }>(response.data).items || [];
}

export async function previewPaperImport(workIds: string[], targetDatasetId: string, expansionId?: string): Promise<ImportPreviewItem[]> {
  const response = await axiosInstance.post(`${core}/imports:preview`, { work_ids: workIds, target_dataset_id: targetDatasetId, resolve_external: false, expansion_id: expansionId });
  return previewItems(response.data);
}

export async function getPaperImportStatus(batchId: string): Promise<string> {
  const response = await axiosInstance.get(`${core}/imports/${encodeURIComponent(batchId)}`, { silentError: true } as never);
  return unwrap<{ batch: { Status: string } }>(response.data).batch.Status;
}

export async function previewPaperLink(identifier: string, targetDatasetId: string): Promise<ImportPreviewItem> {
  const response = await axiosInstance.post(`${core}/imports:preview`, { identifier, target_dataset_id: targetDatasetId });
  const item = previewItems(response.data)[0];
  if (!item) throw new Error('没有查询到有效文献');
  return item;
}
export async function importPaperLink(targetDatasetId: string, item: ImportPreviewItem, idempotencyKey: string, folderId?: string | null) {
  const response = await axiosInstance.post(`${core}/imports`, {
    entry_type: 'paper_link', target_dataset_id: targetDatasetId, idempotency_key: idempotencyKey, preview_token: item.preview_token, folder_id: folderId || '',
    items: [{ work_id: item.work_id, candidate: item.fulltext_candidates[0] }],
  });
  return unwrap<{ batch_id?: string; status?: string }>(response.data);
}

export async function retryAcademicImport(batchId: string) {
  await axiosInstance.post(`${core}/imports/${encodeURIComponent(batchId)}:retry`, {});
}

export interface PaperUploadContext {
 source_version?: string;
  idempotency_key: string;
  library_paper_id: string;
  title: string;
  paper: import('../paper/model').PaperRecord;
}
export interface ImportOperationItem extends AcademicImportQueueItem {
  library_paper_id?: string;
  result_kind: string;
  current_state: 'importing' | 'processing' | 'ready' | 'metadata_only' | 'skipped' | 'failed' | 'processing_failed' | 'canceled' | 'deleted' | 'awaiting_association';
  document_deleted: boolean;
  resolved_later: boolean;
  can_retry: boolean;
  upload_request_key?: string;
  pending_paper?: import('../paper/model').PaperRecord;
  content_sha256?: string;
}
export interface ImportOperation {
  id: string;
  entry_type: string;
  target_dataset_id: string;
  source: { title?: string; expansion_id?: string; expansion_version?: number; file_name?: string; format?: string };
  status: string;
  created_at: string;
  summary: { total: number; ready: number; metadata_only: number; skipped: number; active: number; failed: number; canceled: number; deleted: number; resolved_later: number; retryable: number; needs_action: number };
}
export interface ImportBatchPage {
  items: ImportOperation[];
  next_cursor: string;
  notification: { active: number; needs_action: number };
}
export interface ImportBatchDetail {
  operation: ImportOperation;
  items: ImportOperationItem[];
  next_cursor: string;
}
export const emptyImportBatchPage = (): ImportBatchPage => ({ items: [], next_cursor: '', notification: { active: 0, needs_action: 0 } });
export async function listAcademicImportBatches(datasetId: string, cursor?: string): Promise<ImportBatchPage> {
  const response = await axiosInstance.get(`${core}/imports`, { params: { target_dataset_id: datasetId, cursor, view: 'operations' }, silentError: true } as never);
  return unwrap<ImportBatchPage>(response.data);
}
export async function getAcademicImportBatch(batchId: string, cursor?: string): Promise<ImportBatchDetail> {
  const response = await axiosInstance.get(`${core}/imports/${encodeURIComponent(batchId)}`, { params: { cursor, view: 'operations' }, silentError: true } as never);
  return unwrap<ImportBatchDetail>(response.data);
}
