import { BASE_URL, axiosInstance } from "@/components/request";
import { unwrapApiData } from "./unwrap";

interface ApiEnvelope<T> {
  data?: T;
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

const basePath = BASE_URL || window.location.origin;

export async function listDatabaseConnections() {
  const response = await axiosInstance.get<ApiEnvelope<DatabaseConnectionListResponse>>(
    `${basePath}/api/core/data-sources/database-connections`,
  );
  return unwrapApiData<DatabaseConnectionListResponse>(response.data);
}

export async function createDatabaseConnection(payload: DatabaseConnectionPayload) {
  const response = await axiosInstance.post<ApiEnvelope<DatabaseConnectionItem>>(
    `${basePath}/api/core/data-sources/database-connections`,
    payload,
  );
  return unwrapApiData<DatabaseConnectionItem>(response.data);
}

export async function updateDatabaseConnection(
  id: string,
  payload: Partial<DatabaseConnectionPayload>,
) {
  const response = await axiosInstance.patch<ApiEnvelope<DatabaseConnectionItem>>(
    `${basePath}/api/core/data-sources/database-connections/${encodeURIComponent(id)}`,
    payload,
  );
  return unwrapApiData<DatabaseConnectionItem>(response.data);
}

export async function deleteDatabaseConnection(id: string) {
  const response = await axiosInstance.delete<ApiEnvelope<{ deleted: boolean }>>(
    `${basePath}/api/core/data-sources/database-connections/${encodeURIComponent(id)}`,
  );
  return unwrapApiData<{ deleted: boolean }>(response.data);
}

export async function checkDatabaseConnection(id: string) {
  const response = await axiosInstance.post<ApiEnvelope<DatabaseConnectionCheckResponse>>(
    `${basePath}/api/core/data-sources/database-connections/${encodeURIComponent(id)}:check`,
  );
  return unwrapApiData<DatabaseConnectionCheckResponse>(response.data);
}
