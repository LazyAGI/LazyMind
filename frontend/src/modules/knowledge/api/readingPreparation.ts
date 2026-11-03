import { axiosInstance, BASE_URL } from '@/components/request';
export type ReadingScope = 'pdf_parse' | 'formula' | 'figure' | 'table';
export type ReadingCapabilityId = ReadingScope | 'chat_prepare';
export type ReadingMode = 'on_demand' | 'automatic' | 'disabled';
export type ReadingPreset = 'demand' | 'advance' | 'full' | 'custom';
export interface ReadingPolicy {
  parser_ref?: string;
  study_model_ref?: string;
  preset: ReadingPreset;
  modes: Record<ReadingCapabilityId, ReadingMode>;
  limits: { daily_documents: number; study_items_per_document: number; automatic_concurrency: number };
}
export interface ReadingCapability {
  id: ReadingCapabilityId | 'overview'; name: string; available: boolean; unavailable_reason?: string;
  supported_modes: ReadingMode[]; effective_mode: string; dependencies: string[];
}
export interface ReadingProviderSelection { options: { id: string; label: string }[]; default_label?: string; effective_label?: string; unavailable_reason?: string }
export interface ReadingPreferences extends ReadingPolicy { provider_selections?: { parser: ReadingProviderSelection; study: ReadingProviderSelection }; capabilities: ReadingCapability[]; automatic_eligible_after?: string }
export interface ReadingPreparation {
  job_id?: string; status: 'not_requested' | 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled';
  progress_current?: number; progress_total?: number; model_requests?: number; scopes?: ReadingScope[]; error_message?: string;
}
const base = `${BASE_URL}/api/core`;
const documentPath = (dataset: string, document: string) => `${base}/datasets/${encodeURIComponent(dataset)}/documents/${encodeURIComponent(document)}`;
export async function getReadingPreferences(): Promise<ReadingPreferences> {
  return (await axiosInstance.get(`${base}/user/reading-preferences`)).data.data;
}
export async function saveReadingPreferences(policy: ReadingPolicy): Promise<ReadingPreferences> {
  const { preset, modes, limits, parser_ref, study_model_ref } = policy;
  return (await axiosInstance.put(`${base}/user/reading-preferences`, { preset, modes, limits, ...(parser_ref !== undefined ? { parser_ref } : {}), ...(study_model_ref !== undefined ? { study_model_ref } : {}) })).data.data;
}
export async function getReadingPreparation(dataset: string, document: string): Promise<ReadingPreparation> {
  return (await axiosInstance.get(`${documentPath(dataset, document)}/reading-preparation`)).data.data;
}
export async function prepareReading(dataset: string, document: string, scopes: ReadingScope[]): Promise<ReadingPreparation> {
  return (await axiosInstance.post(`${documentPath(dataset, document)}:prepare-reading`, { scopes })).data.data;
}
export function presetModes(preset: Exclude<ReadingPreset, 'custom'>): ReadingPolicy['modes'] {
  return { pdf_parse: preset === 'demand' ? 'on_demand' : 'automatic', chat_prepare: 'on_demand',
    formula: preset === 'full' ? 'automatic' : 'on_demand', figure: preset === 'full' ? 'automatic' : 'on_demand', table: preset === 'full' ? 'automatic' : 'on_demand' };
}
export function readingDependencyError(policy: ReadingPolicy): string | undefined {
  if (policy.modes.pdf_parse !== 'automatic' && (['formula', 'figure', 'table'] as const).some(key => policy.modes[key] === 'automatic'))
    return '自动研读公式、图片或表格需要先将「结构解析」设为自动。';
}
