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

export interface LocalFSChatSetting {
  enabled: boolean;
}

export interface DatabaseConnectionPayload {
  display_name: string;
  description?: string;
  db_type: "mysql" | "postgresql";
  host: string;
  port?: number;
  database_name: string;
  username: string;
  password?: string;
  options?: Record<string, string>;
}

export interface DatabaseConnectionItem {
  id: string;
  display_name: string;
  description: string;
  db_type: "mysql" | "postgresql";
  host: string;
  port: number;
  database_name: string;
  username: string;
  options: Record<string, string>;
  is_verified: boolean;
  last_checked_at?: string;
  last_check_error?: string;
  create_time: string;
  update_time: string;
}

export interface DatabaseConnectionListResponse {
  connections: DatabaseConnectionItem[];
}

export interface DatabaseConnectionCheckResponse {
  success: boolean;
  message: string;
  table_count: number;
  tables?: string[];
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

export async function getLocalFSChatSetting() {
  const response = await axiosInstance.get<ApiEnvelope<LocalFSChatSetting> | LocalFSChatSetting>(
    `${baseUrl}/api/core/data-sources/local-fs-chat-setting`,
  );
  return unwrapDataSourceApiData<LocalFSChatSetting>(response.data);
}

export async function updateLocalFSChatSetting(enabled: boolean) {
  const response = await axiosInstance.put<ApiEnvelope<LocalFSChatSetting> | LocalFSChatSetting>(
    `${baseUrl}/api/core/data-sources/local-fs-chat-setting`,
    { enabled },
  );
  return unwrapDataSourceApiData<LocalFSChatSetting>(response.data);
}

export async function listDatabaseConnections() {
  const response = await axiosInstance.get<ApiEnvelope<DatabaseConnectionListResponse>>(
    `${baseUrl}/api/core/data-sources/database-connections`,
  );
  return unwrapDataSourceApiData<DatabaseConnectionListResponse>(response.data);
}

export async function createDatabaseConnection(payload: DatabaseConnectionPayload) {
  const response = await axiosInstance.post<ApiEnvelope<DatabaseConnectionItem>>(
    `${baseUrl}/api/core/data-sources/database-connections`,
    payload,
  );
  return unwrapDataSourceApiData<DatabaseConnectionItem>(response.data);
}

export async function updateDatabaseConnection(id: string, payload: Partial<DatabaseConnectionPayload>) {
  const response = await axiosInstance.patch<ApiEnvelope<DatabaseConnectionItem>>(
    `${baseUrl}/api/core/data-sources/database-connections/${encodeURIComponent(id)}`,
    payload,
  );
  return unwrapDataSourceApiData<DatabaseConnectionItem>(response.data);
}

export async function deleteDatabaseConnection(id: string) {
  const response = await axiosInstance.delete<ApiEnvelope<{ deleted: boolean }>>(
    `${baseUrl}/api/core/data-sources/database-connections/${encodeURIComponent(id)}`,
  );
  return unwrapDataSourceApiData<{ deleted: boolean }>(response.data);
}

export async function checkDatabaseConnection(id: string) {
  const response = await axiosInstance.post<ApiEnvelope<DatabaseConnectionCheckResponse>>(
    `${baseUrl}/api/core/data-sources/database-connections/${encodeURIComponent(id)}:check`,
  );
  return unwrapDataSourceApiData<DatabaseConnectionCheckResponse>(response.data);
}

export function unwrapDataSourceApiData<T>(payload: unknown): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}
