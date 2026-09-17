import {
  Configuration,
  DefaultApiFactory,
  ModelProvidersApiFactory,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import type { CredentialBackupStatus } from "./credentialBackupModel";
import type { CredentialRestoreMode } from "./credentialRestoreModel";
import type { RawAxiosRequestConfig } from "axios";
import { silentCloudRequest } from "@/api/cloudClient";

interface ApiEnvelope<T> {
  data?: T;
}

const coreConfig = new Configuration({ basePath: BASE_URL });

export const modelProvidersApi = ModelProvidersApiFactory(
  coreConfig,
  BASE_URL,
  axiosInstance,
);

export const modelProvidersDefaultApi = DefaultApiFactory(
  coreConfig,
  BASE_URL,
  axiosInstance,
);

export function withModelProviderJsonOptions(
  options: RawAxiosRequestConfig = {},
): RawAxiosRequestConfig {
  return {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options.headers ?? {}),
    },
  };
}

export function unwrapModelProviderData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export async function getCredentialBackupStatus(): Promise<CredentialBackupStatus & { available: boolean; reasonCode?: string }> {
  const response = await modelProvidersDefaultApi.apiCoreCredentialVaultBackupGet(silentCloudRequest);
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  return {
    available: Boolean(data.available),
    reasonCode: typeof data.reason_code === "string" ? data.reason_code : undefined,
    enabled: Boolean(data.enabled),
    backedUp: Number(data.backed_up || 0),
    pending: Number(data.pending || 0),
    failed: Number(data.failed || 0),
    lastSucceededAt: typeof data.last_succeeded_at === "string" ? data.last_succeeded_at : undefined,
  };
}

export async function setCredentialBackupEnabled(enabled: boolean): Promise<CredentialBackupStatus & { available: boolean; reasonCode?: string }> {
  const response = enabled
    ? await modelProvidersDefaultApi.apiCoreCredentialVaultBackupEnablePost()
    : await modelProvidersDefaultApi.apiCoreCredentialVaultBackupDisablePost();
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  return {
    available: Boolean(data.available),
    reasonCode: typeof data.reason_code === "string" ? data.reason_code : undefined,
    enabled: Boolean(data.enabled),
    backedUp: Number(data.backed_up || 0),
    pending: Number(data.pending || 0),
    failed: Number(data.failed || 0),
    lastSucceededAt: typeof data.last_succeeded_at === "string" ? data.last_succeeded_at : undefined,
  };
}

export type CredentialRestoreRecord = {
  recordId: string;
  revision: number;
  updatedAt: string;
};

export type CredentialRestoreDiscovery = {
  available: boolean;
  requiresExplicitAction: boolean;
  records: CredentialRestoreRecord[];
  activeOperation?: CredentialRestoreOperation;
};

export type CredentialRestoreOperation = {
  operationId: string;
  status: "pending" | "running" | "succeeded" | "failed" | "expired" | "canceled";
  mode: CredentialRestoreMode;
  totalRecords: number;
  completedRecords: number;
  expiresAt: string;
  temporaryExpiresAt?: string;
  failureCode?: string;
};

export async function getCredentialRestoreDiscovery(): Promise<CredentialRestoreDiscovery> {
  const response = await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresGet(silentCloudRequest);
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  const records = Array.isArray(data.records) ? data.records : [];
  const activeOperation = data.active_operation && typeof data.active_operation === "object"
    ? normalizeRestoreOperation(data.active_operation)
    : undefined;
  return {
    available: Boolean(data.available),
    requiresExplicitAction: data.requires_explicit_action !== false,
    records: records.flatMap((record) => {
      if (!record || typeof record !== "object") return [];
      const value = record as Record<string, unknown>;
      if (typeof value.record_id !== "string" || !Number.isFinite(Number(value.revision))) return [];
      return [{
        recordId: value.record_id,
        revision: Number(value.revision),
        updatedAt: typeof value.updated_at === "string" ? value.updated_at : "",
      }];
    }),
    activeOperation,
  };
}

function normalizeRestoreOperation(payload: unknown): CredentialRestoreOperation {
  const data = unwrapModelProviderData<Record<string, unknown>>(payload);
  return {
    operationId: String(data.operation_id || ""),
    status: String(data.status || "failed") as CredentialRestoreOperation["status"],
    mode: String(data.mode || "trusted_device") as CredentialRestoreMode,
    totalRecords: Math.max(0, Number(data.total_records || 0)),
    completedRecords: Math.max(0, Number(data.completed_records || 0)),
    expiresAt: String(data.expires_at || ""),
    temporaryExpiresAt: typeof data.temporary_expires_at === "string" ? data.temporary_expires_at : undefined,
    failureCode: typeof data.failure_code === "string" ? data.failure_code : undefined,
  };
}

export async function startCredentialRestore(
  mode: CredentialRestoreMode,
  records: CredentialRestoreRecord[],
  resolution: "fail" | "replace_local" | "save_copy" = "fail",
): Promise<CredentialRestoreOperation> {
  const response = await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresPost({
    credentialRestoreRequest: {
      mode,
      records: records.map((record) => ({ record_id: record.recordId, revision: record.revision, resolution })),
    },
  });
  return normalizeRestoreOperation(response.data);
}

export async function getCredentialRestoreOperation(operationId: string): Promise<CredentialRestoreOperation> {
  const response = await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresOperationIdGet({ operationId });
  return normalizeRestoreOperation(response.data);
}

export async function cancelCredentialRestore(operationId: string): Promise<void> {
  await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresOperationIdDelete({ operationId });
}

export interface RemoteGroupModel {
  id: string;
  name: string;
  model_type: string;
  max_input_tokens?: string;
  added: boolean;
}

export async function listRemoteGroupModels(providerId: string, groupId: string) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdRemoteModelsGet({
    modelProviderId: providerId,
    groupId,
  });
  return unwrapModelProviderData<{ url?: string; models?: RemoteGroupModel[] }>(response.data);
}

export async function updateGroupModelMaxInputTokens(
  providerId: string,
  groupId: string,
  modelId: string,
  maxInputTokens: string,
) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdPatch({
    modelProviderId: providerId,
    groupId,
    modelId,
    updateModelProviderGroupModelOpenAPIRequest: { max_input_tokens: maxInputTokens },
  });
  return unwrapModelProviderData<{ max_input_tokens?: string }>(response.data);
}

export interface StoredProviderKey {
  id: string;
  masked: string;
}
