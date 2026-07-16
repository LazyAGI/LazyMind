import { axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import { AGENT_API_BASE } from "./constants";

const ROUTER_API_BASE = `${AGENT_API_BASE}/router`;

const silentConfig = { silentError: true } as Parameters<typeof axiosInstance.get>[1];

export type RouterAlgorithmStatus = "starting" | "active" | "disabled" | "missing" | string;
export type RouterAlgorithmAction = "healthcheck" | "restart" | "start" | "stop";

export type RouterOwner = {
  thread_id: string;
  run_id?: string;
  candidate_ref?: string;
};

export type RouterAlgorithm = {
  algorithm_id: string;
  status: RouterAlgorithmStatus;
  expected_state: string;
  healthy_instances: number;
  instance_count: number;
  owner: RouterOwner;
  router_chat_url: string;
  router_admin_url: string;
};

export type RouterStatus = {
  status: string;
  router_admin_url: string;
  algorithms: {
    evo_owned: number;
    active: number;
    healthy: number;
  };
  ab_strategy: {
    active: boolean;
    id: number | null;
    weights: Record<string, number>;
  };
};

export type RouterABStrategy = {
  active: boolean;
  id: number | null;
  weights: Record<string, number>;
  updated_by: {
    thread_id?: string;
    candidate_ref?: string;
    reason?: string;
  };
  router_response?: Record<string, unknown>;
};

export type RegisterRouterAlgorithmPayload = {
  algorithm_id: string;
  code_path: string;
  owner: RouterOwner;
  name?: string;
  instance_count?: number;
  wait_ready_seconds?: number;
  cleanup_policy?: "thread_delete" | "manual";
};

export type PutRouterABStrategyPayload = {
  weights?: Record<string, number> | null;
  reason?: string;
  owner?: RouterOwner;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function asNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function asIntMap(value: unknown): Record<string, number> {
  if (!isRecord(value)) {
    return {};
  }
  const result: Record<string, number> = {};
  for (const [key, item] of Object.entries(value)) {
    const normalizedKey = asString(key);
    if (!normalizedKey || typeof item !== "number" || !Number.isFinite(item)) {
      continue;
    }
    result[normalizedKey] = item;
  }
  return result;
}

function normalizeOwner(value: unknown): RouterOwner {
  if (!isRecord(value)) {
    return { thread_id: "" };
  }
  return {
    thread_id: asString(value.thread_id),
    run_id: asString(value.run_id) || undefined,
    candidate_ref: asString(value.candidate_ref) || undefined,
  };
}

export function normalizeRouterAlgorithm(value: unknown): RouterAlgorithm | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const algorithmId = asString(value.algorithm_id);
  if (!algorithmId) {
    return undefined;
  }
  return {
    algorithm_id: algorithmId,
    status: asString(value.status) || "missing",
    expected_state: asString(value.expected_state),
    healthy_instances: asNumber(value.healthy_instances),
    instance_count: asNumber(value.instance_count),
    owner: normalizeOwner(value.owner),
    router_chat_url: asString(value.router_chat_url),
    router_admin_url: asString(value.router_admin_url),
  };
}

export function normalizeRouterAlgorithmList(value: unknown): RouterAlgorithm[] {
  if (!isRecord(value) || !Array.isArray(value.items)) {
    return [];
  }
  return value.items
    .map(normalizeRouterAlgorithm)
    .filter((item): item is RouterAlgorithm => Boolean(item));
}

export function normalizeRouterStatus(value: unknown): RouterStatus | null {
  if (!isRecord(value)) {
    return null;
  }
  const algorithms = isRecord(value.algorithms) ? value.algorithms : {};
  const abStrategy = isRecord(value.ab_strategy) ? value.ab_strategy : {};
  return {
    status: asString(value.status) || "ok",
    router_admin_url: asString(value.router_admin_url),
    algorithms: {
      evo_owned: asNumber(algorithms.evo_owned),
      active: asNumber(algorithms.active),
      healthy: asNumber(algorithms.healthy),
    },
    ab_strategy: {
      active: Boolean(abStrategy.active),
      id: typeof abStrategy.id === "number" ? abStrategy.id : null,
      weights: asIntMap(abStrategy.weights),
    },
  };
}

export function normalizeRouterABStrategy(value: unknown): RouterABStrategy | null {
  if (!isRecord(value)) {
    return null;
  }
  const updatedBy = isRecord(value.updated_by) ? value.updated_by : {};
  return {
    active: Boolean(value.active),
    id: typeof value.id === "number" ? value.id : null,
    weights: asIntMap(value.weights),
    updated_by: {
      thread_id: asString(updatedBy.thread_id) || undefined,
      candidate_ref: asString(updatedBy.candidate_ref) || undefined,
      reason: asString(updatedBy.reason) || undefined,
    },
    router_response: isRecord(value.router_response) ? value.router_response : undefined,
  };
}

export function getRouterApiErrorMessage(error: unknown, fallback: string) {
  if (isRecord(error) && isRecord((error as { response?: unknown }).response)) {
    const data = (error as { response: { data?: unknown } }).response.data;
    if (isRecord(data) && isRecord(data.detail)) {
      const detailMessage = asString(data.detail.message);
      if (detailMessage) {
        return detailMessage;
      }
    }
  }
  return getLocalizedErrorMessage(error) || fallback;
}

export async function fetchRouterStatus() {
  const response = await axiosInstance.get(`${ROUTER_API_BASE}/status`, silentConfig);
  return normalizeRouterStatus(response.data);
}

export async function fetchRouterAlgorithms(params: {
  threadId?: string;
  algorithmId?: string;
  status?: string;
} = {}) {
  const query: Record<string, string> = {
    status: params.status || "all",
  };
  if (params.threadId) {
    query.thread_id = params.threadId;
  }
  if (params.algorithmId) {
    query.algorithm_id = params.algorithmId;
  }
  const response = await axiosInstance.get(`${ROUTER_API_BASE}/algorithms`, {
    params: query,
    ...silentConfig,
  });
  return normalizeRouterAlgorithmList(response.data);
}

export async function registerRouterAlgorithm(payload: RegisterRouterAlgorithmPayload) {
  const response = await axiosInstance.post(
    `${ROUTER_API_BASE}/algorithms`,
    payload,
    silentConfig,
  );
  return response.data;
}

export async function runRouterAlgorithmAction(
  algorithmId: string,
  action: RouterAlgorithmAction,
  waitReadySeconds?: number,
) {
  const body: Record<string, string | number> = { action };
  if (typeof waitReadySeconds === "number" && waitReadySeconds > 0) {
    body.wait_ready_seconds = waitReadySeconds;
  }
  const response = await axiosInstance.post(
    `${ROUTER_API_BASE}/algorithms/${encodeURIComponent(algorithmId)}/action`,
    body,
    silentConfig,
  );
  return response.data;
}

export async function deleteRouterAlgorithm(algorithmId: string) {
  const response = await axiosInstance.delete(
    `${ROUTER_API_BASE}/algorithms/${encodeURIComponent(algorithmId)}`,
    silentConfig,
  );
  return response.data;
}

export async function fetchRouterABStrategy() {
  const response = await axiosInstance.get(`${ROUTER_API_BASE}/ab-strategy`, silentConfig);
  return normalizeRouterABStrategy(response.data);
}

export async function putRouterABStrategy(payload: PutRouterABStrategyPayload) {
  const response = await axiosInstance.put(
    `${ROUTER_API_BASE}/ab-strategy`,
    payload,
    silentConfig,
  );
  return normalizeRouterABStrategy(response.data);
}
