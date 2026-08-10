import { axiosInstance, BASE_URL } from '@/components/request';
import type { RawAxiosRequestConfig } from 'axios';

const coreBasePath = `${BASE_URL}/api/core`;

// ─── Built-in workflow types ────────────────────────────────────────────────────

export interface BuiltinWorkflowStep {
  id: string;
  label: string;
}

export interface BuiltinWorkflowSlot {
  id: string;
  label: string;
  type: string;
  cardinality: string;
}

export interface BuiltinWorkflowUiTabSlot {
  id: string;
}

export interface BuiltinWorkflowUiTab {
  id: string;
  label: string;
  layout: string;
  slots: BuiltinWorkflowUiTabSlot[];
  composite_layout?: unknown;
  composite_behavior?: {
    hide_empty_columns?: boolean;
    empty_column_scope?: 'selected' | 'tab';
    mutually_exclusive?: Array<{ slots: string[]; prefer?: string[] }>;
  };
}

export interface BuiltinWorkflow {
  id: string;
  name: string;
  description: string;
  steps: BuiltinWorkflowStep[];
  slots?: BuiltinWorkflowSlot[];
  ui?: { tabs: BuiltinWorkflowUiTab[] };
  i18n?: Record<string, unknown>;
  // Raw YAML texts returned by the backend (populated when fetching single workflow).
  workflow_yaml_raw?: string;
  state_yaml_raw?: string;
  scenario_raw?: string;
  scripts_raw?: string;
}

export interface WorkflowDraftRecord {
  id: string;
  name: string;
  // Legacy content column, kept for backward compatibility.
  content: string;
  // Split content columns (available after migration 20260706120000).
  workflow_yaml_content: string;
  state_yaml_content: string;
  // Layout-only column (migration 20260708120000): x-layout JSON extracted from state.yml.
  // Saved independently with last-write-wins; no version check.
  state_layout_content: string;
  scenario_content: string;
  scripts_content: string;
  // '' | 'generating' | 'brief_done' | 'skeleton_done' | 'state_done' | 'done' | 'failed'
  //   ''              — AI generation never triggered
  //   'generating'    — Phase 0 (design brief) in progress
  //   'brief_done'    — Phase 0 complete; Phase 1 (skeleton) running
  //   'skeleton_done' — Phase 1 complete; workflow_yaml_content available; Phase 2 running
  //   'state_done'    — Phase 2 complete; state_yaml_content available; Phase 3 running; editor usable
  //   'done'          — All phases complete
  //   'failed'        — A phase failed; see generate_error for details
  generate_status: string;
  // Non-empty when generate_status === 'failed'; may also contain non-fatal Phase 3 warnings when 'done'.
  generate_error: string;
  // Non-empty when generate_status === 'done' but Phase 2 had non-fatal field warnings.
  generate_warning: string;
  // Phase 0 design brief Markdown (migration 20260709140000). Empty for old drafts.
  design_brief_content: string;
  // Source tracking.
  // 'ai' | 'skill' | 'blank' | '' (blank/unknown)
  source_type: string;
  source_skill_id: string;
  source_skill_name: string;
  source_skill_revision_id: string;
  source_skill_revision_no: number;
  source_skill_tree_hash: string;
  source_analysis_id: string;
  // Optimistic-lock version. Increment on every save that touches workflow_yaml_content or state_yaml_content.
  version: number;
  created_at: string;
  updated_at: string;
  created_by: string;
  published: boolean;
  published_workflow_ref: string;
  current_revision_id: string;
  current_revision_no: number;
  published_status: string;
  base_revision_id: string;
  draft_dirty: boolean;
  last_repair_run_id: string;
}

export interface ListWorkflowDraftsResponse {
  records: WorkflowDraftRecord[];
  total: number;
}

// Core API wraps responses as { code, message, data: <payload> }.
interface CoreResponse<T> {
  code: number;
  message: string;
  data: T;
}

export async function listWorkflowDrafts(params: { page?: number; pageSize?: number } = {}): Promise<ListWorkflowDraftsResponse> {
  const resp = await axiosInstance.get<CoreResponse<ListWorkflowDraftsResponse>>(`${coreBasePath}/workflow-drafts`, {
    params: { page: params.page ?? 1, page_size: params.pageSize ?? 20 },
  });
  return resp.data.data;
}

export async function createWorkflowDraft(payload: { name: string; content?: string; source_type?: string }): Promise<WorkflowDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<WorkflowDraftRecord>>(`${coreBasePath}/workflow-drafts`, payload);
  return resp.data.data;
}

export async function getWorkflowDraft(
  id: string,
  options?: RawAxiosRequestConfig,
): Promise<WorkflowDraftRecord> {
  const resp = await axiosInstance.get<CoreResponse<WorkflowDraftRecord>>(
    `${coreBasePath}/workflow-drafts/${id}`,
    options,
  );
  return resp.data.data;
}

export interface UpdateDraftPayload {
  content?: string;
  workflow_yaml_content?: string;
  state_yaml_content?: string;
  // Layout-only save: no version check on the server side.
  state_layout_content?: string;
  scenario_content?: string;
  scripts_content?: string;
  // Required when sending workflow_yaml_content or state_yaml_content; ignored otherwise.
  version?: number;
}

export async function updateWorkflowDraftContent(id: string, payload: UpdateDraftPayload | string): Promise<WorkflowDraftRecord> {
  // Accept either the legacy string form or the new object form.
  const body: UpdateDraftPayload = typeof payload === 'string' ? { content: payload } : payload;
  const resp = await axiosInstance.post<CoreResponse<WorkflowDraftRecord>>(`${coreBasePath}/workflow-drafts/${id}:save`, body);
  return resp.data.data;
}

export async function deleteWorkflowDraft(id: string): Promise<void> {
  await axiosInstance.delete(`${coreBasePath}/workflow-drafts/${id}`);
}

export interface PublishedWorkflowVersion {
  workflow_ref: string;
  revision_id: string;
  revision_no: number;
  remote_root: string;
  enabled: boolean;
}

export async function publishWorkflowDraft(id: string): Promise<PublishedWorkflowVersion> {
  const resp = await axiosInstance.post<CoreResponse<PublishedWorkflowVersion>>(`${coreBasePath}/workflow-drafts/${id}:publish`);
  return resp.data.data;
}

export interface WorkflowDiagnostic {
  code: string;
  severity: 'error' | 'warning' | string;
  path?: string;
  node_id?: string;
  edge_id?: string;
  material_id?: string;
  message: string;
  details?: Record<string, unknown>;
  fixable: boolean;
}

export interface WorkflowValidationResult {
  valid: boolean;
  profile: string;
  schema_version: string;
  graph_hash?: string;
  diagnostics: WorkflowDiagnostic[];
}

export async function validateWorkflowDraft(id: string): Promise<WorkflowValidationResult> {
  const resp = await axiosInstance.post<CoreResponse<WorkflowValidationResult>>(
    `${coreBasePath}/workflow-drafts/${id}:validate`,
    { profile: 'editor' },
  );
  return resp.data.data;
}

export interface WorkflowVersionSummary { revision_id: string; revision_no: number; tree_hash: string; message: string; created_by: string; created_at: string; current: boolean }
export interface WorkflowVersionContent { workflow_ref: string; revision_id: string; revision_no: number; tree_hash: string; workflow_yaml_content: string; state_yaml_content: string; scenario_content: string; scripts_content: string; readonly: true }
export async function listWorkflowVersions(workflowRef: string): Promise<WorkflowVersionSummary[]> { const r=await axiosInstance.get<CoreResponse<{versions:WorkflowVersionSummary[]}>>(`${coreBasePath}/published-workflows/${encodeURIComponent(workflowRef)}/versions`);return r.data.data.versions }
export async function getWorkflowVersion(workflowRef: string, revisionId: string): Promise<WorkflowVersionContent> { const r=await axiosInstance.get<CoreResponse<WorkflowVersionContent>>(`${coreBasePath}/published-workflows/${encodeURIComponent(workflowRef)}/versions/${encodeURIComponent(revisionId)}`);return r.data.data }
export async function editWorkflowVersion(workflowRef: string, revisionId: string): Promise<WorkflowDraftRecord> { const r=await axiosInstance.post<CoreResponse<WorkflowDraftRecord>>(`${coreBasePath}/published-workflows/${encodeURIComponent(workflowRef)}/versions/${encodeURIComponent(revisionId)}:edit`);return r.data.data }

export interface UserWorkflowSetting {
  workflow_ref: string; workflow_id: string; name: string; description: string;
  when_to_use: string; source_type: string; revision_id: string;
  revision_no: number; remote_root: string; enabled: boolean; status: string;
}

export async function listUserWorkflowSettings(): Promise<UserWorkflowSetting[]> {
  const resp = await axiosInstance.get<CoreResponse<{ workflows: UserWorkflowSetting[] }>>(`${coreBasePath}/chat/settings/workflows`);
  return resp.data.data.workflows;
}

export async function setUserWorkflowEnabled(workflowRef: string, enabled: boolean): Promise<void> {
  await axiosInstance.patch(`${coreBasePath}/chat/settings/workflows/${encodeURIComponent(workflowRef)}`, { enabled });
}

// Trigger AI generation for a workflow draft.
// Returns immediately with generate_status == 'generating'; the job runs asynchronously.
export async function aiGenerateWorkflowDraft(
  id: string,
  payload: { description?: string; skill_id?: string },
): Promise<WorkflowDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<WorkflowDraftRecord>>(
    `${coreBasePath}/workflow-drafts/${id}:ai-generate`,
    payload,
  );
  return resp.data.data;
}

export type PolishableField = 'description' | 'when_to_use' | 'overview' | 'notes';

export interface PolishWorkflowInfoPayload {
  fields: Partial<Record<PolishableField, string>>;
  target_fields: PolishableField[];
}

export type PolishWorkflowInfoResponse = Partial<Record<PolishableField, string>>;

export async function polishWorkflowInfo(payload: PolishWorkflowInfoPayload): Promise<PolishWorkflowInfoResponse> {
  const resp = await axiosInstance.post<CoreResponse<PolishWorkflowInfoResponse>>(
    `${coreBasePath}/workflow-drafts:polish-info`,
    payload,
  );
  return resp.data.data;
}

export interface RepairWorkflowDraftPayload {
  repair_hint?: string;
  // Which part to repair: 'statemachine' | 'ui' | 'scenario'
  // 'statemachine' and 'ui' maps to state.yml repair; 'scenario' maps to scenario.md repair.
  target?: string;
  mode?: 'workflow_local' | 'source_aware';
  draft_version: number;
  source_analysis_id?: string;
}

export interface WorkflowCandidate { id: string; name?: string; goal?: string; inputs?: unknown; outputs?: unknown; steps?: unknown; evidence_paths?: string[] }
export interface WorkflowGenerationAnalysis { analysis_id: string; status: string; verdict_code: string; message: string; source_skill_revision_id: string; source_skill_revision_no: number; source_skill_tree_hash: string; candidates: WorkflowCandidate[]; selected_candidate_id: string; coverage: unknown; tool_mappings: unknown; scripts: Record<string,{classification:string;reason?:string}> }
export async function getWorkflowGenerationAnalysis(id: string): Promise<WorkflowGenerationAnalysis> {
  const r = await axiosInstance.get<CoreResponse<WorkflowGenerationAnalysis>>(
    `${coreBasePath}/workflow-drafts/${id}/generation-analysis`,
    { silentError: true } as never,
  );
  return r.data.data;
}
export async function confirmWorkflowWorkflow(id: string, payload: {analysis_id:string;candidate_id:string;source_skill_revision_id:string;draft_version:number}): Promise<void> { await axiosInstance.post(`${coreBasePath}/workflow-drafts/${id}:confirm-workflow`,payload) }

// Trigger AI repair for a workflow draft with warnings or incomplete state.yml.
// Sends current YAML content to Python /repair endpoint and returns the patched draft.
export async function repairWorkflowDraft(
  id: string,
  payload: RepairWorkflowDraftPayload,
): Promise<WorkflowDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<WorkflowDraftRecord>>(
    `${coreBasePath}/workflow-drafts/${id}:ai-repair`,
    payload,
  );
  return resp.data.data;
}
export interface RepairPreview { target:string;mode:string;draft_version:number;diagnostics:Array<{code:string;path:string;message:string;severity:string}>;planned_files:string[] }
export interface WorkflowRepairRun { repair_id:string;status:'queued'|'repairing'|'succeeded'|'failed'|'stale'|string;target:string;diagnostics_after:Array<{code:string;path:string;message:string;severity:string}> }
export async function getWorkflowRepairRun(draftId:string,repairId:string):Promise<WorkflowRepairRun>{const r=await axiosInstance.get<CoreResponse<WorkflowRepairRun>>(`${coreBasePath}/workflow-drafts/${draftId}/repair-runs/${repairId}`);return r.data.data}
export async function previewWorkflowRepair(id:string,payload:{target:string;mode:string}):Promise<RepairPreview>{
  const r=await axiosInstance.post<CoreResponse<RepairPreview>>(`${coreBasePath}/workflow-drafts/${id}:repair-preview`,payload);
  const data = r.data.data;
  const normalized = Array.isArray(data.diagnostics) ? data.diagnostics.map((raw) => {
    // Compatibility with Core versions that serialized Go field names as
    // Code/Path/Message/Severity instead of the lowercase API contract.
    const item = raw as unknown as Record<string, unknown>;
    return {
      code: String(item.code ?? item.Code ?? 'unknown'),
      path: String(item.path ?? item.Path ?? ''),
      message: String(item.message ?? item.Message ?? ''),
      severity: String(item.severity ?? item.Severity ?? 'error'),
    };
  }) : [];
  const appliesToTarget = (code: string) => {
    if (payload.target === 'full' || code === 'workflow_yaml_invalid') return true;
    if (payload.target === 'statemachine') return code.startsWith('state_');
    if (payload.target === 'ui') return code.startsWith('ui_');
    if (payload.target === 'scenario') return code.startsWith('scenario_');
    if (payload.target === 'scripts') return code.startsWith('scripts_') || code.startsWith('tool_script_');
    return true;
  };
  const seen = new Set<string>();
  const diagnostics = normalized.filter((item) => {
    if (!appliesToTarget(item.code)) return false;
    const key = `${item.code}\u0000${item.path}\u0000${item.message}\u0000${item.severity}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  return { ...data, planned_files: data.planned_files ?? [], diagnostics };
}

// ─── Built-in workflow API ──────────────────────────────────────────────────────

export async function listBuiltinWorkflows(): Promise<BuiltinWorkflow[]> {
  const resp = await axiosInstance.get<{ workflows: BuiltinWorkflow[] }>(`${coreBasePath}/workflows`);
  // The endpoint returns { workflows: [...] } directly (not wrapped in { code, data }).
  const data = (resp.data as unknown as { workflows?: BuiltinWorkflow[] });
  return data.workflows ?? [];
}

export async function getBuiltinWorkflow(workflowId: string): Promise<BuiltinWorkflow> {
  const resp = await axiosInstance.get<unknown>(`${coreBasePath}/workflows/${workflowId}`);
  return resp.data as BuiltinWorkflow;
}
