import { axiosInstance, BASE_URL } from '@/components/request';
export type ReadingScope = 'pdf_parse' | 'ai_study';
export type ReadingCapabilityId = ReadingScope;
type ReadingResultScope = ReadingScope | 'formula' | 'figure' | 'table' | 'chat_prepare';
export type ReadingMode = 'on_demand' | 'automatic' | 'disabled';
export type ReadingPreset = 'demand' | 'advance' | 'full' | 'custom';
export interface ReadingPolicy {
  parser_ref?: string;
  study_model_ref?: string;
  preset: ReadingPreset;
  modes: Record<ReadingCapabilityId, ReadingMode>;
  limits: { daily_pdf_documents: number; daily_study_documents: number; automatic_concurrency: number };
}
export interface ReadingCapability {
  id: ReadingCapabilityId; name: string; available: boolean; unavailable_reason?: string;
  supported_modes: ReadingMode[]; effective_mode: string; dependencies: string[];
}
export interface ReadingProviderSelection { options: { id: string; label: string }[]; default_label?: string; effective_label?: string; unavailable_reason?: string }
export interface ReadingQuota { used: number; limit: number }
export interface ReadingPreferences extends ReadingPolicy { usage?: Record<ReadingCapabilityId, ReadingQuota>; provider_selections?: { parser: ReadingProviderSelection; study: ReadingProviderSelection }; capabilities: ReadingCapability[]; automatic_eligible_after?: string }
export interface ReadingCapabilityStatus {
  id: 'formula' | 'figure' | 'table';
  status: 'not_requested' | 'pending' | 'running' | 'partial' | 'succeeded' | 'failed' | 'unavailable' | 'empty';
  completed: number; total: number; running?: number; failed?: number; error_message?: string;
}
export interface ReadingPreparation {
  capabilities?: ReadingCapabilityStatus[];
  job_id?: string; status: 'not_requested' | 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled';
  progress_current?: number; progress_total?: number; model_requests?: number; scopes?: ReadingResultScope[]; error_code?: string; error_message?: string;
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
  return { pdf_parse: preset === 'demand' ? 'on_demand' : 'automatic',
    ai_study: preset === 'full' ? 'automatic' : 'on_demand' };
}
export function readingScopeLabels(scopes: ReadingResultScope[] = []): string[] {
  return [...new Set(scopes.map(scope => {
    if (scope === 'pdf_parse') return 'PDF 解析';
    if (scope === 'chat_prepare') return '文档对话准备';
    return 'AI 论文精读';
  }))];
}
