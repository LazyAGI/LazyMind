import { axiosInstance, BASE_URL } from '@/components/request';

const coreBasePath = `${BASE_URL}/api/core`;

// ─── Built-in plugin types ────────────────────────────────────────────────────

export interface BuiltinPluginStep {
  id: string;
  label: string;
}

export interface BuiltinPluginSlot {
  id: string;
  label: string;
  type: string;
  cardinality: string;
}

export interface BuiltinPluginUiTabSlot {
  id: string;
}

export interface BuiltinPluginUiTab {
  id: string;
  label: string;
  layout: string;
  slots: BuiltinPluginUiTabSlot[];
}

export interface BuiltinPlugin {
  id: string;
  name: string;
  description: string;
  steps: BuiltinPluginStep[];
  slots?: BuiltinPluginSlot[];
  ui?: { tabs: BuiltinPluginUiTab[] };
  i18n?: Record<string, unknown>;
  // Raw YAML texts returned by the backend (populated when fetching single plugin).
  plugin_yaml_raw?: string;
  state_yaml_raw?: string;
  scenario_raw?: string;
  scripts_raw?: string;
}

export interface PluginDraftRecord {
  id: string;
  name: string;
  // Legacy content column, kept for backward compatibility.
  content: string;
  // Split content columns (available after migration 20260706120000).
  plugin_yaml_content: string;
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
  //   'skeleton_done' — Phase 1 complete; plugin_yaml_content available; Phase 2 running
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
  // Optimistic-lock version. Increment on every save that touches plugin_yaml_content or state_yaml_content.
  version: number;
  created_at: string;
  updated_at: string;
  created_by: string;
  published: boolean;
  published_plugin_ref: string;
  current_revision_id: string;
  current_revision_no: number;
  published_status: string;
  base_revision_id: string;
  draft_dirty: boolean;
  last_repair_run_id: string;
}

export interface ListPluginDraftsResponse {
  records: PluginDraftRecord[];
  total: number;
}

// Core API wraps responses as { code, message, data: <payload> }.
interface CoreResponse<T> {
  code: number;
  message: string;
  data: T;
}

export async function listPluginDrafts(params: { page?: number; pageSize?: number } = {}): Promise<ListPluginDraftsResponse> {
  const resp = await axiosInstance.get<CoreResponse<ListPluginDraftsResponse>>(`${coreBasePath}/plugin-drafts`, {
    params: { page: params.page ?? 1, page_size: params.pageSize ?? 20 },
  });
  return resp.data.data;
}

export async function createPluginDraft(payload: { name: string; content?: string; source_type?: string }): Promise<PluginDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<PluginDraftRecord>>(`${coreBasePath}/plugin-drafts`, payload);
  return resp.data.data;
}

export async function getPluginDraft(id: string): Promise<PluginDraftRecord> {
  const resp = await axiosInstance.get<CoreResponse<PluginDraftRecord>>(`${coreBasePath}/plugin-drafts/${id}`);
  return resp.data.data;
}

export interface UpdateDraftPayload {
  content?: string;
  plugin_yaml_content?: string;
  state_yaml_content?: string;
  // Layout-only save: no version check on the server side.
  state_layout_content?: string;
  scenario_content?: string;
  scripts_content?: string;
  // Required when sending plugin_yaml_content or state_yaml_content; ignored otherwise.
  version?: number;
}

export async function updatePluginDraftContent(id: string, payload: UpdateDraftPayload | string): Promise<PluginDraftRecord> {
  // Accept either the legacy string form or the new object form.
  const body: UpdateDraftPayload = typeof payload === 'string' ? { content: payload } : payload;
  const resp = await axiosInstance.post<CoreResponse<PluginDraftRecord>>(`${coreBasePath}/plugin-drafts/${id}:save`, body);
  return resp.data.data;
}

export async function deletePluginDraft(id: string): Promise<void> {
  await axiosInstance.delete(`${coreBasePath}/plugin-drafts/${id}`);
}

export interface PublishedPluginVersion {
  plugin_ref: string;
  revision_id: string;
  revision_no: number;
  remote_root: string;
  enabled: boolean;
}

export async function publishPluginDraft(id: string): Promise<PublishedPluginVersion> {
  const resp = await axiosInstance.post<CoreResponse<PublishedPluginVersion>>(`${coreBasePath}/plugin-drafts/${id}:publish`);
  return resp.data.data;
}

export interface PluginVersionSummary { revision_id: string; revision_no: number; tree_hash: string; message: string; created_by: string; created_at: string; current: boolean }
export interface PluginVersionContent { plugin_ref: string; revision_id: string; revision_no: number; tree_hash: string; plugin_yaml_content: string; state_yaml_content: string; scenario_content: string; scripts_content: string; readonly: true }
export async function listPluginVersions(pluginRef: string): Promise<PluginVersionSummary[]> { const r=await axiosInstance.get<CoreResponse<{versions:PluginVersionSummary[]}>>(`${coreBasePath}/published-plugins/${encodeURIComponent(pluginRef)}/versions`);return r.data.data.versions }
export async function getPluginVersion(pluginRef: string, revisionId: string): Promise<PluginVersionContent> { const r=await axiosInstance.get<CoreResponse<PluginVersionContent>>(`${coreBasePath}/published-plugins/${encodeURIComponent(pluginRef)}/versions/${encodeURIComponent(revisionId)}`);return r.data.data }
export async function editPluginVersion(pluginRef: string, revisionId: string): Promise<PluginDraftRecord> { const r=await axiosInstance.post<CoreResponse<PluginDraftRecord>>(`${coreBasePath}/published-plugins/${encodeURIComponent(pluginRef)}/versions/${encodeURIComponent(revisionId)}:edit`);return r.data.data }

export interface UserPluginSetting {
  plugin_ref: string; plugin_id: string; name: string; description: string;
  when_to_use: string; source_type: string; revision_id: string;
  revision_no: number; remote_root: string; enabled: boolean; status: string;
}

export async function listUserPluginSettings(): Promise<UserPluginSetting[]> {
  const resp = await axiosInstance.get<CoreResponse<{ plugins: UserPluginSetting[] }>>(`${coreBasePath}/chat/settings/plugins`);
  return resp.data.data.plugins;
}

export async function setUserPluginEnabled(pluginRef: string, enabled: boolean): Promise<void> {
  await axiosInstance.patch(`${coreBasePath}/chat/settings/plugins/${encodeURIComponent(pluginRef)}`, { enabled });
}

// Trigger AI generation for a plugin draft.
// Returns immediately with generate_status == 'generating'; the job runs asynchronously.
export async function aiGeneratePluginDraft(
  id: string,
  payload: { description?: string; skill_id?: string },
): Promise<PluginDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<PluginDraftRecord>>(
    `${coreBasePath}/plugin-drafts/${id}:ai-generate`,
    payload,
  );
  return resp.data.data;
}

export type PolishableField = 'description' | 'when_to_use' | 'overview' | 'notes';

export interface PolishPluginInfoPayload {
  fields: Partial<Record<PolishableField, string>>;
  target_fields: PolishableField[];
}

export type PolishPluginInfoResponse = Partial<Record<PolishableField, string>>;

export async function polishPluginInfo(payload: PolishPluginInfoPayload): Promise<PolishPluginInfoResponse> {
  const resp = await axiosInstance.post<CoreResponse<PolishPluginInfoResponse>>(
    `${coreBasePath}/plugin-drafts:polish-info`,
    payload,
  );
  return resp.data.data;
}

export interface RepairPluginDraftPayload {
  repair_hint?: string;
  // Which part to repair: 'statemachine' | 'ui' | 'scenario'
  // 'statemachine' and 'ui' maps to state.yml repair; 'scenario' maps to scenario.md repair.
  target?: string;
  mode?: 'plugin_local' | 'source_aware';
  draft_version: number;
  source_analysis_id?: string;
}

export interface WorkflowCandidate { id: string; name?: string; goal?: string; inputs?: unknown; outputs?: unknown; steps?: unknown; evidence_paths?: string[] }
export interface PluginGenerationAnalysis { analysis_id: string; status: string; verdict_code: string; message: string; source_skill_revision_id: string; source_skill_revision_no: number; source_skill_tree_hash: string; candidates: WorkflowCandidate[]; selected_candidate_id: string; coverage: unknown; tool_mappings: unknown; scripts: Record<string,{classification:string;reason?:string}> }
export async function getPluginGenerationAnalysis(id: string): Promise<PluginGenerationAnalysis> { const r=await axiosInstance.get<CoreResponse<PluginGenerationAnalysis>>(`${coreBasePath}/plugin-drafts/${id}/generation-analysis`); return r.data.data }
export async function confirmPluginWorkflow(id: string, payload: {analysis_id:string;candidate_id:string;source_skill_revision_id:string;draft_version:number}): Promise<void> { await axiosInstance.post(`${coreBasePath}/plugin-drafts/${id}:confirm-workflow`,payload) }

// Trigger AI repair for a plugin draft with warnings or incomplete state.yml.
// Sends current YAML content to Python /repair endpoint and returns the patched draft.
export async function repairPluginDraft(
  id: string,
  payload: RepairPluginDraftPayload,
): Promise<PluginDraftRecord> {
  const resp = await axiosInstance.post<CoreResponse<PluginDraftRecord>>(
    `${coreBasePath}/plugin-drafts/${id}:ai-repair`,
    payload,
  );
  return resp.data.data;
}
export interface RepairPreview { target:string;mode:string;draft_version:number;diagnostics:Array<{code:string;path:string;message:string;severity:string}>;planned_files:string[] }
export interface PluginRepairRun { repair_id:string;status:string;target:string;diagnostics_after:Array<{code:string;path:string;message:string;severity:string}> }
export async function getPluginRepairRun(draftId:string,repairId:string):Promise<PluginRepairRun>{const r=await axiosInstance.get<CoreResponse<PluginRepairRun>>(`${coreBasePath}/plugin-drafts/${draftId}/repair-runs/${repairId}`);return r.data.data}
export async function previewPluginRepair(id:string,payload:{target:string;mode:string}):Promise<RepairPreview>{
  const r=await axiosInstance.post<CoreResponse<RepairPreview>>(`${coreBasePath}/plugin-drafts/${id}:repair-preview`,payload);
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
    if (payload.target === 'full' || code === 'plugin_yaml_invalid') return true;
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

// ─── Built-in plugin API ──────────────────────────────────────────────────────

export async function listBuiltinPlugins(): Promise<BuiltinPlugin[]> {
  const resp = await axiosInstance.get<{ plugins: BuiltinPlugin[] }>(`${coreBasePath}/plugins`);
  // The endpoint returns { plugins: [...] } directly (not wrapped in { code, data }).
  const data = (resp.data as unknown as { plugins?: BuiltinPlugin[] });
  return data.plugins ?? [];
}

export async function getBuiltinPlugin(pluginId: string): Promise<BuiltinPlugin> {
  const resp = await axiosInstance.get<unknown>(`${coreBasePath}/plugins/${pluginId}`);
  return resp.data as BuiltinPlugin;
}
