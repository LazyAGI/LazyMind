import {
  Configuration,
  DefaultApiFactory,
  ModelProvidersApiFactory,
  type AddModelProviderGroupModelOpenAPIRequest,
  type AddModelProviderGroupModelsOpenAPIResponse,
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
  options: RawAxiosRequestConfig & { silentError?: boolean } = {},
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

export async function updateProviderEndpoint(
  providerId: string,
  baseUrl: string,
): Promise<{ id: string; base_url: string }> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdEndpointPut({
      modelProviderId: providerId,
      updateModelProviderEndpointOpenAPIRequest: { base_url: baseUrl },
    });
  return unwrapModelProviderData(response.data);
}

export async function createCustomProvider(input: {
  name: string;
  source_provider_id: string;
  base_url: string;
}): Promise<{ id: string; group_id: string }> {
  const response = await modelProvidersApi.apiCoreModelProvidersPost({
    createCustomModelProviderOpenAPIRequest: input,
  });
  return unwrapModelProviderData(response.data);
}

export async function updateCustomProviderName(
  providerId: string,
  name: string,
): Promise<{ id: string }> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdPatch({
      modelProviderId: providerId,
      updateCustomModelProviderOpenAPIRequest: { name },
    });
  return unwrapModelProviderData(response.data);
}

export async function deleteCustomProvider(
  providerId: string,
  confirmIndexedDowngrade = false,
): Promise<{ id: string; downgraded_knowledge_bases: number }> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdDelete({
      modelProviderId: providerId,
      confirmIndexedDowngrade: confirmIndexedDowngrade || undefined,
    });
  return unwrapModelProviderData(response.data);
}

export async function setProviderActiveKey(
  providerId: string,
  groupId: string,
  keyId: string,
): Promise<{ group_id: string; key_id: string }> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdKeysActivePut(
      {
        modelProviderId: providerId,
        setActiveModelProviderKeyOpenAPIRequest: {
          group_id: groupId,
          key_id: keyId,
        },
      },
      { timeout: 180000 },
    );
  return unwrapModelProviderData(response.data);
}

export async function getCredentialBackupStatus(): Promise<
  CredentialBackupStatus & { available: boolean; reasonCode?: string }
> {
  const response =
    await modelProvidersDefaultApi.apiCoreCredentialVaultBackupGet(
      silentCloudRequest,
    );
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  return {
    available: Boolean(data.available),
    reasonCode:
      typeof data.reason_code === "string" ? data.reason_code : undefined,
    enabled: Boolean(data.enabled),
    backedUp: Number(data.backed_up || 0),
    pending: Number(data.pending || 0),
    failed: Number(data.failed || 0),
    lastSucceededAt:
      typeof data.last_succeeded_at === "string"
        ? data.last_succeeded_at
        : undefined,
  };
}

export async function setCredentialBackupEnabled(
  enabled: boolean,
): Promise<
  CredentialBackupStatus & { available: boolean; reasonCode?: string }
> {
  const response = enabled
    ? await modelProvidersDefaultApi.apiCoreCredentialVaultBackupEnablePost()
    : await modelProvidersDefaultApi.apiCoreCredentialVaultBackupDisablePost();
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  return {
    available: Boolean(data.available),
    reasonCode:
      typeof data.reason_code === "string" ? data.reason_code : undefined,
    enabled: Boolean(data.enabled),
    backedUp: Number(data.backed_up || 0),
    pending: Number(data.pending || 0),
    failed: Number(data.failed || 0),
    lastSucceededAt:
      typeof data.last_succeeded_at === "string"
        ? data.last_succeeded_at
        : undefined,
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
  status:
    | "pending"
    | "running"
    | "succeeded"
    | "failed"
    | "expired"
    | "canceled";
  mode: CredentialRestoreMode;
  totalRecords: number;
  completedRecords: number;
  expiresAt: string;
  temporaryExpiresAt?: string;
  failureCode?: string;
};

export async function getCredentialRestoreDiscovery(): Promise<CredentialRestoreDiscovery> {
  const response =
    await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresGet(
      silentCloudRequest,
    );
  const data = unwrapModelProviderData<Record<string, unknown>>(response.data);
  const records = Array.isArray(data.records) ? data.records : [];
  const activeOperation =
    data.active_operation && typeof data.active_operation === "object"
      ? normalizeRestoreOperation(data.active_operation)
      : undefined;
  return {
    available: Boolean(data.available),
    requiresExplicitAction: data.requires_explicit_action !== false,
    records: records.flatMap((record) => {
      if (!record || typeof record !== "object") return [];
      const value = record as Record<string, unknown>;
      if (
        typeof value.record_id !== "string" ||
        !Number.isFinite(Number(value.revision))
      )
        return [];
      return [
        {
          recordId: value.record_id,
          revision: Number(value.revision),
          updatedAt:
            typeof value.updated_at === "string" ? value.updated_at : "",
        },
      ];
    }),
    activeOperation,
  };
}

function normalizeRestoreOperation(
  payload: unknown,
): CredentialRestoreOperation {
  const data = unwrapModelProviderData<Record<string, unknown>>(payload);
  return {
    operationId: String(data.operation_id || ""),
    status: String(
      data.status || "failed",
    ) as CredentialRestoreOperation["status"],
    mode: String(data.mode || "trusted_device") as CredentialRestoreMode,
    totalRecords: Math.max(0, Number(data.total_records || 0)),
    completedRecords: Math.max(0, Number(data.completed_records || 0)),
    expiresAt: String(data.expires_at || ""),
    temporaryExpiresAt:
      typeof data.temporary_expires_at === "string"
        ? data.temporary_expires_at
        : undefined,
    failureCode:
      typeof data.failure_code === "string" ? data.failure_code : undefined,
  };
}

export async function startCredentialRestore(
  mode: CredentialRestoreMode,
  records: CredentialRestoreRecord[],
  resolution: "fail" | "replace_local" | "save_copy" = "fail",
): Promise<CredentialRestoreOperation> {
  const response =
    await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresPost({
      credentialRestoreRequest: {
        mode,
        records: records.map((record) => ({
          record_id: record.recordId,
          revision: record.revision,
          resolution,
        })),
      },
    });
  return normalizeRestoreOperation(response.data);
}

export async function getCredentialRestoreOperation(
  operationId: string,
): Promise<CredentialRestoreOperation> {
  const response =
    await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresOperationIdGet(
      { operationId },
    );
  return normalizeRestoreOperation(response.data);
}

export async function cancelCredentialRestore(
  operationId: string,
): Promise<void> {
  await modelProvidersDefaultApi.apiCoreCredentialVaultRestoresOperationIdDelete(
    { operationId },
  );
}

export interface RemoteGroupModel {
  id: string;
  name: string;
  model_type?: string;
  input?: string[];
  output?: string[];
  sub_types?: string[];
  max_input_tokens?: string;
  added: boolean;
}

export async function listRemoteGroupModels(
  providerId: string,
  groupId: string,
) {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdRemoteModelsGet(
      {
        modelProviderId: providerId,
        groupId,
      },
      withModelProviderJsonOptions({ silentError: true }),
    );
  return unwrapModelProviderData<{ url?: string; models?: RemoteGroupModel[] }>(
    response.data,
  );
}

export interface UpdateGroupModelInput {
  name: string;
  model_type: string;
  vision?: boolean;
  max_input_tokens?: string;
  input_token_limit?: string;
  max_output_tokens?: string;
  customize?: boolean;
}

export async function updateGroupModel(
  providerId: string,
  groupId: string,
  modelId: string,
  input: UpdateGroupModelInput,
) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdPatch({
    modelProviderId: providerId,
    groupId,
    modelId,
    updateModelProviderGroupModelOpenAPIRequest: input,
  });
  return unwrapModelProviderData<{
    id: string;
    name: string;
    model_type: string;
    vision?: boolean;
    is_default?: boolean;
    max_input_tokens?: string;
    input_token_limit?: string | null;
    max_output_tokens?: string | null;
  }>(response.data);
}

export async function addGroupModels(
  providerId: string,
  groupId: string,
  models: AddModelProviderGroupModelOpenAPIRequest[],
): Promise<AddModelProviderGroupModelsOpenAPIResponse> {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsBatchPost({
    modelProviderId: providerId,
    groupId,
    addModelProviderGroupModelsOpenAPIRequest: { models },
  }, withModelProviderJsonOptions({ silentError: true }));
  return unwrapModelProviderData(response.data);
}

export interface StoredProviderKey {
  id: string;
  masked: string;
}

export type RequestProtocol =
  | "openai"
  | "anthropic"
  | "gemini"
  | "openai-responses";

export interface ProviderRequestConfig {
  default_protocol: RequestProtocol;
  protocols: Array<{
    protocol: RequestProtocol;
    name: string;
    base_url: string;
    default_base_url: string;
    supported: boolean;
  }>;
}

export interface ProviderRequestConfigInput {
  default_protocol: RequestProtocol;
  endpoints: { openai?: string; anthropic?: string };
}

export async function getProviderRequestConfig(
  providerId: string,
): Promise<ProviderRequestConfig> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdRequestConfigGet(
      { modelProviderId: providerId },
    );
  return unwrapModelProviderData(response.data);
}

export async function updateProviderRequestConfig(
  providerId: string,
  input: ProviderRequestConfigInput,
): Promise<ProviderRequestConfig> {
  if (
    input.default_protocol !== "openai" &&
    input.default_protocol !== "anthropic"
  )
    throw new Error("Unsupported request protocol");
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdRequestConfigPut(
      {
        modelProviderId: providerId,
        updateProviderRequestConfigOpenAPIRequest: {
          ...input,
          default_protocol: input.default_protocol,
        },
      },
    );
  return unwrapModelProviderData(response.data);
}

export async function revealProviderKey(
  providerId: string,
  groupId: string,
  keyId: string,
): Promise<string> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysKeyIdRevealPost(
      { modelProviderId: providerId, groupId, keyId },
    );
  return unwrapModelProviderData<{ api_key: string }>(response.data).api_key;
}

export async function replaceProviderKey(
  providerId: string,
  groupId: string,
  keyId: string,
  apiKey: string,
): Promise<{ key_id: string; enabled: boolean }> {
  const response =
    await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysKeyIdPut(
      {
        modelProviderId: providerId,
        groupId,
        keyId,
        replaceModelProviderKeyOpenAPIRequest: { api_key: apiKey },
      },
      { timeout: 180000 },
    );
  return unwrapModelProviderData(response.data);
}
