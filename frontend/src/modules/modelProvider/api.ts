import {
  Configuration,
  DefaultApiFactory,
  ModelProvidersApiFactory,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";

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
