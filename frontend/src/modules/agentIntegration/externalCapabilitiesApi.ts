import { axiosInstance } from "@/components/request";
import { coreApiUrl } from "@/runtime/apiBase";

export type ExternalCapabilityType = "model" | "tool";

export interface ExternalCapabilityItem {
  id: string;
  type: ExternalCapabilityType;
  name: string;
  source: string;
  description?: string;
  input_schema?: Record<string, unknown>;
  available: boolean;
  reason?: string;
  authorized: boolean;
}

export interface ExternalCapabilityInventory {
  agent: string;
  capabilities: ExternalCapabilityItem[];
}

export interface ExternalCapabilityInvocation {
  id: string;
  agent: string;
  invocation_id?: string;
  capability_type: ExternalCapabilityType;
  capability_id: string;
  capability_name: string;
  status: "running" | "succeeded" | "failed";
  usage: Record<string, number>;
  result?: {
    data?: unknown;
    preview?: string;
    truncated?: boolean;
  };
  error_code?: string;
  error_message?: string;
  started_at: string;
  finished_at?: string;
}

export interface ExternalCapabilityInvocationAggregate {
  agent: string;
  capability_type: ExternalCapabilityType;
  capability_id: string;
  capability_name: string;
  call_count: number;
  succeeded: number;
  failed: number;
}

export interface ExternalCapabilityInvocationPage {
  invocations: ExternalCapabilityInvocation[];
  total: number;
  summary: {
    total: number;
    succeeded: number;
    failed: number;
    running: number;
    model_calls: number;
    tool_calls: number;
    capabilities: ExternalCapabilityInvocationAggregate[];
  };
}

interface Envelope<T> { data: T }

export async function loadExternalCapabilities(agent: string): Promise<ExternalCapabilityInventory> {
  const response = await axiosInstance.get<Envelope<ExternalCapabilityInventory>>(
    coreApiUrl("external-agent-capabilities"),
    { params: { agent } },
  );
  return response.data.data;
}

export async function setExternalCapabilityGrant(
  agent: string,
  capability: ExternalCapabilityItem,
  enabled: boolean,
): Promise<void> {
  await axiosInstance.put(coreApiUrl("external-agent-capabilities"), {
    agent,
    capability_type: capability.type,
    capability_id: capability.id,
    enabled,
  });
}

export async function loadExternalCapabilityInvocations(
  agent: string,
  pageSize = 50,
): Promise<ExternalCapabilityInvocationPage> {
  const response = await axiosInstance.get<Envelope<ExternalCapabilityInvocationPage>>(
    coreApiUrl("external-agent-capability-invocations"),
    { params: { agent, page_size: pageSize } },
  );
  return response.data.data;
}
