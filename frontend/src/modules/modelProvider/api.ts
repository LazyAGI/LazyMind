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
