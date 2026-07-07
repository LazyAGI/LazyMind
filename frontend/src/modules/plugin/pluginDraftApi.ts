import { axiosInstance, BASE_URL } from '@/components/request';

const coreBasePath = `${BASE_URL}/api/core`;

export interface PluginDraftRecord {
  id: string;
  name: string;
  // Legacy content column, kept for backward compatibility.
  content: string;
  // Split content columns (available after migration 20260706120000).
  plugin_yaml_content: string;
  state_yaml_content: string;
  scenario_content: string;
  scripts_content: string;
  // '' | 'generating' | 'done' | 'failed'
  generate_status: string;
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
  scenario_content?: string;
  scripts_content?: string;
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
