import {
  Configuration,
  DefaultApiFactory,
  ModelProvidersApiFactory,
  type LookupContextWindowOpenAPIResponse,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";
import { DEFAULT_LLM_MAX_INPUT_TOKENS } from "./maxInputTokens";

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

export async function lookupModelContextWindow(name: string): Promise<string> {
  const trimmed = name.trim();
  if (!trimmed) {
    return DEFAULT_LLM_MAX_INPUT_TOKENS;
  }
  const response = await modelProvidersApi.apiCoreModelProvidersContextWindowsGet({ name: trimmed });
  const data = unwrapModelProviderData<LookupContextWindowOpenAPIResponse>(response.data);
  return data.max_input_tokens || DEFAULT_LLM_MAX_INPUT_TOKENS;
}
