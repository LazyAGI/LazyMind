import { axiosInstance, BASE_URL } from '@/components/request';

export const PRODUCT_STAGES = ['direction', 'competitive', 'design', 'prd', 'prototype', 'review', 'handoff'] as const;
export type ProductStage = typeof PRODUCT_STAGES[number];
export interface ProductArtifact {
  stage: ProductStage; title: string; version: string; status: string;
  available: boolean; stale: boolean; reason?: string; revision_id: string;
  revision: number; slot_id: string; source_session_id: string;
  content?: string; content_format?: string;
}
export interface ProductDecision {
  decision_id: string; title?: string; decision?: string; question?: string; value?: string; risk?: string;
  statement?: string; rationale?: string; decision_content?: Record<string, unknown>;
  status: string; decision_hash: string; confirmation_required?: boolean; deferred?: boolean;
}
export interface ProductSummary {
  supported: boolean; can_relay: boolean; reason?: string; state_version: number;
  current_stage?: ProductStage; next_stages: { id: ProductStage; label: string }[];
  can_accept_current_artifact: boolean; actions: string[];
  project?: { workspace_id: string; name?: string; artifacts: ProductArtifact[];
    drafts: { stage: ProductStage; slot_id: string; revision_id: string }[];
    decisions: ProductDecision[]; can_update_decisions: boolean };
}
export interface ProductDecisionRequest {
  action: 'accept' | 'defer'; expected_state_version: number;
  expected_decision_hash: string; idempotency_key: string;
}
export interface ProductRelayRequest {
  action: 'continue' | 'switch-stage' | 'finish'; selected_stage?: ProductStage;
  expected_state_version: number; idempotency_key: string; request_context?: string;
  accept_current_artifact?: boolean;
}
const headers = { 'Workflow-Contract-Version': 'workflow.v1' };
const url = (session: string) => `${BASE_URL}/api/core/workflow-sessions/${encodeURIComponent(session)}`;
export const productApi = {
  async summary(session: string, signal?: AbortSignal) {
    return (await axiosInstance.get<{ result: ProductSummary }>(`${url(session)}/product-stage-relay`, { headers, signal })).data.result;
  },
  async artifact(session: string, stage: ProductStage, format: string) {
    return (await axiosInstance.get<{ result: ProductArtifact }>(`${url(session)}/product-artifacts/${stage}`, { headers, params: { format } })).data.result;
  },
  async relay(session: string, body: ProductRelayRequest) {
    return (await axiosInstance.post<{ result: { session_id: string; source_session_id: string; status: string } }>(`${url(session)}/product-stage-relay`, body, { headers })).data.result;
  },
  async decide(session: string, id: string, body: ProductDecisionRequest) {
    return (await axiosInstance.post(`${url(session)}/product-decisions/${encodeURIComponent(id)}`, body, { headers })).data.result;
  },
};
