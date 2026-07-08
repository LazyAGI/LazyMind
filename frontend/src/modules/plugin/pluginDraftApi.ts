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
  // '' | 'generating' | 'skeleton_done' | 'state_done' | 'done' | 'failed'
  //   ''              — AI generation never triggered
  //   'generating'    — Phase 1 (skeleton) in progress
  //   'skeleton_done' — Phase 1 complete; plugin_yaml_content available; Phase 2 running
  //   'state_done'    — Phase 2 complete; state_yaml_content available; Phase 3 running; editor usable
  //   'done'          — All phases complete
  //   'failed'        — A phase failed; see generate_error for details
  generate_status: string;
  // Non-empty when generate_status === 'failed'; may also contain non-fatal Phase 3 warnings when 'done'.
  generate_error: string;
  // Optimistic-lock version. Increment on every save that touches plugin_yaml_content or state_yaml_content.
  version: number;
  created_at: string;
  updated_at: string;
  created_by: string;
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

export async function createPluginDraft(payload: { name: string; content?: string }): Promise<PluginDraftRecord> {
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
