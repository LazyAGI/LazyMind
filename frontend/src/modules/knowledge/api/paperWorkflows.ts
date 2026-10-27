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
export type BudgetMode = 'fast' | 'balanced' | 'deep';
export interface ExpansionReport {
 narrative?: string; reading_order?: string[]; limitations?: string[];
 claims?: Array<{ paper_id: string; statement: string; status: string; reason: string; content_hash: string; positions: number[] }>;
 reading?: Record<string, { read_level: string; positions: number[]; pages: number[]; total_blocks: number }>;
 currency_cost?: number | null;
 execution_elapsed_ms?: number;
 stage_timing?: Array<{step_id: string; attempt: number; status: string; queue_and_execution_ms: number}>;
 stage_metrics?: Record<string, {model?: string; provider_usage_records?: unknown[]}>;
 compose_metrics?: {model?: string; provider_usage_records?: unknown[]};
 source_queries?: Array<{query: string; sources?: Array<{source: string; status: string; count: number; elapsed_ms: number}>}>;
 preparation_attempts?: Array<{revision: number; model: string; metrics?: unknown}>;
}
export interface ExpansionPreparation {
 user_instruction: string; summary: string; error?: string;
 questions: Array<{ id: string; question: string; options?: string[] }>;
 directions: Array<{ name: string; question: string; reason: string; difference: string; paper_ids: string[]; papers?: PaperRef[] }>;
 selected_directions: string[];
}
export interface Expansion {
  preparation?: ExpansionPreparation;
  preparation_revision: number;
  budget_mode: BudgetMode;
  reading?: ExpansionReport['reading'];
  report?: ExpansionReport;
  discovery_mode?: string;
  progress?: Record<string, Partial<ReviewWorkflowInput> & { input?: Partial<ReviewWorkflowInput>; plan?: string[]; gaps?: string[] }>;
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
  quality?: { eligible: boolean; downloaded: boolean; parsed: boolean; identity: string; text: string; material: string };
  parser_version?: string;
  error?: string;
}
export interface ExpansionDetail {
  record: Expansion;
  rounds: Array<{ discovery_mode?: string; id: string; version: number; status: string; result: ReviewWorkflowInput; report?: ExpansionReport }>;
  chat_status: 'uncreated' | 'available' | 'trashed' | 'deleted';
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
  budget_mode?: BudgetMode;
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
export async function launchPaperTask(input: PaperLaunch, silentError = false) {
  const res = await axiosInstance.post(`${core}/workflow-launches`, input, { silentError } as never);
  return unwrap<{ conversation_id?: string; expansion_id?: string; status?: string }>(res.data);
}
export async function resolvePaperSelection(dataset: string, documents: string[], silentError = false) {
  const res = await axiosInstance.post(`${core}/paper-selections:resolve`, { dataset_id: dataset, document_ids: documents }, { silentError } as never);
  return unwrap<{ papers: WorkflowPaper[]; invalid: Array<{ document_id: string; reason: string }> }>(res.data);
}
export async function listExpansions(dataset: string, silentError = false) {
  const res = await axiosInstance.get(`${core}/expansions`, { params: { dataset_id: dataset }, silentError } as never);
  return unwrap<{ items: Expansion[] }>(res.data).items || [];
}
export async function getExpansion(id: string, silentError = false) {
  const res = await axiosInstance.get(`${core}/expansions/${encodeURIComponent(id)}`, { silentError } as never);
  return unwrap<ExpansionDetail>(res.data);
}
export async function deleteExpansion(id: string, silentError = false) {
  await axiosInstance.delete(`${core}/expansions/${encodeURIComponent(id)}`, { data: { confirm: true }, silentError } as never);
}
export async function setPaperImportance(datasetId: string, id: string, value: boolean, silentError = false) {
  await axiosInstance.put(`${core}/papers/${encodeURIComponent(id)}/importance`, { dataset_id: datasetId, is_important: value }, { silentError } as never);
}
export async function updatePaperIdentity(id: string, input: {
  kind?: string; reidentify?: boolean;
  paper?: { title?: string; authors?: string[]; year?: number; venue?: string; abstract?: string; doi?: string; arxiv_id?: string };
}, silentError = false) {
  const response = await axiosInstance.patch(`${core}/documents/${encodeURIComponent(id)}/identity`, input, { silentError } as never);
  return unwrap<{ status: string; paper_id?: string }>(response.data);
}
export async function retryPaperContent(id: string, expansionId: string, silentError = false) {
  await axiosInstance.post(`${core}/paper-contents:retry`, { paper_id: id, expansion_id: expansionId }, { silentError } as never);
}

export async function submitPreparation(id: string, input: {
 action: 'answer' | 'revise' | 'retry'; revision: number; request_key: string;
 answers?: Record<string, string>; user_instruction?: string; budget_mode?: BudgetMode;
}) {
 const response = await axiosInstance.post(`${core}/expansions/${encodeURIComponent(id)}/preparation`, input);
 return unwrap<{ expansion_id: string; revision: number; status: string }>(response.data);
}
export async function confirmExpansion(id: string, input: {
 revision: number; request_key: string; selected_directions: string[]; budget_mode: BudgetMode;
}) {
 const response = await axiosInstance.post(`${core}/expansions/${encodeURIComponent(id)}:confirm`, input);
 return unwrap<{ conversation_id: string; expansion_id: string }>(response.data);
}
export async function getExpansionCapabilities() {
 const response = await axiosInstance.get(`${core}/expansion-capabilities`);
 return unwrap<{ openalex_key: boolean; sciverse: boolean; web_search: boolean }>(response.data);
}
