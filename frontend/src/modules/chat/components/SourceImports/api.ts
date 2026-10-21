import { axiosInstance, BASE_URL } from "@/components/request";

export interface SourceContext { conversation_id?: string; history_id?: string; task_id?: string; workflow_session_id?: string; work_display_key?: string }
export interface InspectedSource { source_id: string; url: string; title: string; eligible: boolean; paper_key?: string; reason?: string }
export interface ImportItem {
  item_id: string; batch_id: string; title: string; paper_key: string;
  target_dataset_id: string; target_name: string; created_library: boolean;
  status: string; stage: string; state_unavailable?: boolean; processing_status?: string; document_id?: string;
  error_message?: string; retryable: boolean; retry_batch_id?: string; retry_item_id?: string;
}
export interface NewLibrary { name: string; tags: string[]; description?: string; scene: string; processing_level: string; algo_id?: string }
export interface SourceImportRequest { source_context: SourceContext; source_ids: string[]; target_dataset_id?: string; new_knowledge_base?: NewLibrary; idempotency_key: string }
export interface SourceTarget { dataset_id: string; display_name: string }
const root = `${BASE_URL}/api/core/academic`;
const options = { silentError: true } as import("axios").AxiosRequestConfig;
const unwrap = <T,>(data: T | { data: T }): T => data && typeof data === "object" && "data" in data ? data.data : data as T;
export async function inspectSources(context: SourceContext) {
  return unwrap<{ items: InspectedSource[] }>((await axiosInstance.post(`${root}/sources:inspect`, { source_context: context }, options)).data).items || [];
}
export async function listSourceImports(context: SourceContext) {
  return unwrap<{ items: ImportItem[] }>((await axiosInstance.get(`${root}/imports`, { ...options, params: context })).data).items || [];
}
export async function sourceTargets() {
  return unwrap<{ items: SourceTarget[] }>((await axiosInstance.get(`${root}/source-targets`, options)).data).items || [];
}
export async function submitSourceImport(request: SourceImportRequest) {
  return unwrap<{ batch_id: string; target_dataset_id: string }>((await axiosInstance.post(`${root}/imports`, request, options)).data);
}
export async function retrySourceImport(batch: string, items: string[], key: string) {
  await axiosInstance.post(`${root}/imports/${encodeURIComponent(batch)}:retry`, { item_ids: items, idempotency_key: key }, options);
}
