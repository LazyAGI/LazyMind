import type { RawAxiosRequestConfig } from "axios";
import { modelProvidersApi, unwrapModelProviderData } from "./api";

export const modelProbeCodes = [
  "ok", "accepted", "unsupported_type", "unsupported_provider", "unknown_type",
  "authentication_failed", "permission_denied", "model_not_found", "rate_limited",
  "quota_exceeded", "invalid_request", "timeout", "connection_error", "invalid_response",
  "empty_response", "upstream_error", "configuration_changed", "key_disabled", "key_required", "probe_in_progress",
] as const;
export type ModelProbeCode = typeof modelProbeCodes[number];
export type ModelProbeStatus = "passed" | "failed" | "unsupported" | "accepted";
export interface ModelProbeIdentity {
  providerId: string;
  groupId: string;
  modelId: string;
}
export interface ModelProbeContext {
  providerId: string;
  groupId: string;
  configurationKey: string;
  disabledReason?: string;
}
export interface ModelProbeResult {
  status: ModelProbeStatus;
  code: ModelProbeCode;
  model_type: string;
  elapsed_ms: number;
  http_status?: number;
}
const isCode = (value: unknown): value is ModelProbeCode => typeof value === "string" && (modelProbeCodes as readonly string[]).includes(value);
const invalidResult = (): ModelProbeResult => ({ status: "failed", code: "invalid_response", model_type: "", elapsed_ms: 0 });
const matchesOutcome = (status: unknown, code: ModelProbeCode): status is ModelProbeStatus => {
  const expected = code === "ok" ? "passed" : code === "accepted" ? "accepted" :
    ["unsupported_type", "unsupported_provider", "unknown_type"].includes(code) ? "unsupported" : "failed";
  return status === expected;
};

/** Sends only saved identities; Core resolves the active credential and task. */
export async function probeModel(identity: ModelProbeIdentity, signal?: AbortSignal): Promise<ModelProbeResult> {
  const options: RawAxiosRequestConfig & { silentError: boolean } = { signal, timeout: 55000, silentError: true };
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdProbePost({
    modelProviderId: identity.providerId, groupId: identity.groupId, modelId: identity.modelId,
  }, options);
  const result = unwrapModelProviderData<Partial<ModelProbeResult>>(response.data);
  if (!result || !isCode(result.code) || !matchesOutcome(result.status, result.code) ||
      typeof result.elapsed_ms !== "number" || !Number.isFinite(result.elapsed_ms) || result.elapsed_ms < 0) return invalidResult();
  return {
    status: result.status,
    code: result.code,
    model_type: typeof result.model_type === "string" ? result.model_type : "",
    elapsed_ms: Math.round(result.elapsed_ms),
    ...(Number.isInteger(result.http_status) && result.http_status! >= 100 && result.http_status! <= 599 ? { http_status: result.http_status } : {}),
  };
}

/** Localized allowlisted reasons only: provider URLs and response bodies stay private. */
export function probeFailureCode(error: unknown): ModelProbeCode {
  if (!error || typeof error !== "object") return "connection_error";
  const failure = error as { code?: string; response?: { status?: number; data?: { data?: { code?: unknown; reason?: unknown } } } };
  const detail = failure.response?.data?.data;
  const reason = detail?.code ?? detail?.reason;
  if (isCode(reason)) return reason;
  if (failure.code === "ECONNABORTED" || failure.code === "ETIMEDOUT") return "timeout";
  switch (failure.response?.status) {
    case 401: return "authentication_failed";
    case 403: return "permission_denied";
    case 404: return "model_not_found";
    case 409: return "probe_in_progress";
    case 429: return "rate_limited";
    case 408:
    case 504: return "timeout";
    default: return failure.response ? "upstream_error" : "connection_error";
  }
}
