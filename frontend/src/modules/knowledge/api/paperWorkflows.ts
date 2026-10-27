import { axiosInstance, BASE_URL } from '@/components/request';
import type { ImportPreviewItem } from './academicReferences';
const core = `${BASE_URL}/api/core/academic`;
function unwrap<T>(value: T | { data: T }): T {
  return value && typeof value === 'object' && 'data' in value ? (value as { data: T }).data : value as T;
}
export interface PaperRef {
 citation_count?: number;
  document_id?: string;
  source_version?: string;
  source_provider?: string;
  paper_id: string;
  title?: string;
  authors?: string[];
  abstract: string | null;
  year?: number;
  venue?: string;
  doi?: string;
  arxiv_id?: string;
  paper_url?: string;
  pdf_url?: string;
}
export interface WorkflowPaper {
  paper: PaperRef;
  is_seed: boolean;
  is_important: boolean;
  seed_relation?: string;
  direction_relation?: string;
  key_contribution?: string;
}
export interface ReviewWorkflowInput {
  user_instruction: string;
  candidate_directions: string[];
  selected_directions: string[];
  papers: WorkflowPaper[];
}
export interface Expansion {
  discovery_mode?: string;
  progress?: Record<string, Partial<ReviewWorkflowInput>>;
  id: string;
  dataset_id: string;
  conversation_id: string;
  title: string;
  goal: string;
  status: string;
  version: number;
  result: ReviewWorkflowInput;
  created_at: string;
  updated_at: string;
  auto_review_id?: string;
}
export interface PaperContent {
  id: string;
  paper_id: string;
  status: string;
  content_hash: string;
  parser_version?: string;
  error?: string;
}
export interface ExpansionDetail {
  record: Expansion;
  rounds: Array<{ discovery_mode?: string; id: string; version: number; status: string; result: ReviewWorkflowInput }>;
  chat_status: 'available' | 'trashed' | 'deleted';
  contents: PaperContent[];
  paper_importance: Record<string, boolean>;
  presence?: Array<{ work_id: string; metadata_only?: boolean; current_dataset: ImportPreviewItem['presence']['current_dataset']; importing: boolean }>;
  handoffs: Array<{ id: string; kind: string; conversation_id: string; version: number }>;
}
export interface DocumentAcademic {
  source_url?: string;
  import_method?: import('../paper/model').PaperImportMethod;
  expansion_id?: string;
  expansion_version?: number;
  source_version?: string;
  source_provider?: string;
  content_sha256?: string;
  processing_status?: string;
  processing_error?: string;
  state: { document_id: string; kind: 'paper' | 'other' | 'unknown'; status: string; paper_id: string; revision?: number; manual: boolean; error?: string };
  paper?: PaperRef;
  is_important: boolean;
}
export type PaperTask = 'expansion' | 'review' | 'reflection' | 'ppt';
export interface PaperLaunch {
  kind: PaperTask;
  dataset_id: string;
  document_ids?: string[];
  direction?: string;
  user_instruction?: string;
  request_key: string;
  expansion_id?: string;
  expansion_version?: number;
  paper_ids?: string[];
  reflection_first?: boolean;
}
export async function launchPaperTask(input: PaperLaunch) {
  const res = await axiosInstance.post(`${core}/workflow-launches`, input);
  return unwrap<{ conversation_id: string; expansion_id?: string }>(res.data);
}
export async function resolvePaperSelection(dataset: string, documents: string[]) {
  const res = await axiosInstance.post(`${core}/paper-selections:resolve`, { dataset_id: dataset, document_ids: documents });
  return unwrap<{ papers: WorkflowPaper[]; invalid: Array<{ document_id: string; reason: string }> }>(res.data);
}
export async function listExpansions(dataset: string) {
  const res = await axiosInstance.get(`${core}/expansions`, { params: { dataset_id: dataset } });
  return unwrap<{ items: Expansion[] }>(res.data).items || [];
}
export async function getExpansion(id: string) {
  const res = await axiosInstance.get(`${core}/expansions/${encodeURIComponent(id)}`);
  return unwrap<ExpansionDetail>(res.data);
}
export async function deleteExpansion(id: string) {
  await axiosInstance.delete(`${core}/expansions/${encodeURIComponent(id)}`, { data: { confirm: true } });
}
export async function setPaperImportance(datasetId: string, id: string, value: boolean) {
  await axiosInstance.put(`${core}/papers/${encodeURIComponent(id)}/importance`, { dataset_id: datasetId, is_important: value });
}
export async function updatePaperIdentity(id: string, input: {
  kind?: string; reidentify?: boolean;
  paper?: { title?: string; authors?: string[]; year?: number; venue?: string; abstract?: string; doi?: string; arxiv_id?: string };
}) {
  const response = await axiosInstance.patch(`${core}/documents/${encodeURIComponent(id)}/identity`, input);
  return unwrap<{ status: string; paper_id?: string }>(response.data);
}
export async function retryPaperContent(id: string, expansionId: string) {
  await axiosInstance.post(`${core}/paper-contents:retry`, { paper_id: id, expansion_id: expansionId });
}
