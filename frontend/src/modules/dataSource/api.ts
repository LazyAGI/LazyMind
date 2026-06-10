import {
  CloudOauthApiFactory,
  Configuration as AuthConfiguration,
} from "@/api/generated/auth-client";
import {
  Configuration as CoreConfiguration,
  DatasetsApiFactory as CoreDatasetsApiFactory,
  ModelProvidersApiFactory,
} from "@/api/generated/core-client";
import { AgentAppsAuth } from "@/components/auth";
import { BASE_URL, axiosInstance } from "@/components/request";

interface ApiEnvelope<T> {
  data?: T;
}

const baseUrl = BASE_URL || window.location.origin;

const coreConfiguration = new CoreConfiguration({
  basePath: baseUrl,
  baseOptions: {
    headers: { "Content-Type": "application/json" },
  },
});

const authConfiguration = new AuthConfiguration({
  basePath: baseUrl,
  accessToken: () => AgentAppsAuth.getAccessToken(),
  baseOptions: {
    headers: AgentAppsAuth.getAuthHeaders(),
  },
});

export const dataSourceDatasetsApi = CoreDatasetsApiFactory(
  coreConfiguration,
  baseUrl,
  axiosInstance,
);

export const dataSourceModelProvidersApi = ModelProvidersApiFactory(
  coreConfiguration,
  baseUrl,
  axiosInstance,
);

export const dataSourceCloudOauthApi = CloudOauthApiFactory(
  authConfiguration,
  baseUrl,
  axiosInstance,
);

export function unwrapDataSourceApiData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}
