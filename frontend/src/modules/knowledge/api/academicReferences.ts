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
  work_id: string;
  reference_ids?: string[];
  title: string;
  doi?: string;
  arxiv_id?: string;
  disposition: "already_present" | "downloadable" | "metadata_only" | "importing";
  presence: {
    status: string;
    current_dataset: Array<{ dataset_id: string; dataset_name: string; document_id: string; document_name: string }>;
    other_datasets: Array<{ dataset_id: string; dataset_name: string; document_id: string; document_name: string }>;
    importing: boolean;
    coverage_complete: boolean;
  };
  fulltext_candidates: FulltextCandidate[];
}

export interface AcademicImportQueueItem {
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

const core = `${BASE_URL}/api/core/academic`;
const unwrap = <T>(value: T | Envelope<T>): T =>
  value && typeof value === "object" && "data" in value ? (value as Envelope<T>).data : value as T;

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
  return unwrap<{ items: ImportPreviewItem[] }>(response.data).items || [];
}

export async function previewDocumentReferencesImport(sourceDocumentIds: string[], targetDatasetId: string): Promise<ImportPreviewItem[]> {
  const response = await axiosInstance.post(`${core}/imports:preview`, {
    source_document_ids: sourceDocumentIds,
    target_dataset_id: targetDatasetId,
  });
  return unwrap<{ items: ImportPreviewItem[] }>(response.data).items || [];
}

export async function startReferenceImport(targetDatasetId: string, sourceDocumentIds: string[], items: ImportPreviewItem[]): Promise<{ batch_id: string }> {
  const response = await axiosInstance.post(`${core}/imports`, {
    entry_type: items.length === 1 ? "single_reference" : "paper_references",
    target_dataset_id: targetDatasetId,
    source_document_ids: sourceDocumentIds,
    idempotency_key: crypto.randomUUID(),
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
