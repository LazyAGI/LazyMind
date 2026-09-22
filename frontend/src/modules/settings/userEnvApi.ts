import { axiosInstance, BASE_URL } from "@/components/request";

const root = `${BASE_URL}/api/core/user/env-vars`;

export interface UserEnvironmentVariable {
  id: string;
  name: string;
  enabled: boolean;
  description: string;
  masked_value: string;
  credential_status?: "available" | "unavailable";
  created_at: string;
  updated_at: string;
}

interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
}

export interface UserEnvironmentVariablePayload {
  name?: string;
  value?: string;
  enabled?: boolean;
  description?: string;
  expected_updated_at?: string;
}

export async function listUserEnvironmentVariables(): Promise<UserEnvironmentVariable[]> {
  const response = await axiosInstance.get<ApiEnvelope<{ items: UserEnvironmentVariable[] }>>(root);
  return response.data.data?.items ?? [];
}

export async function createUserEnvironmentVariable(
  payload: Required<Pick<UserEnvironmentVariablePayload, "name" | "value">> &
    Pick<UserEnvironmentVariablePayload, "enabled" | "description">,
): Promise<UserEnvironmentVariable> {
  const response = await axiosInstance.post<ApiEnvelope<UserEnvironmentVariable>>(root, payload);
  return response.data.data;
}

export async function patchUserEnvironmentVariable(
  id: string,
  payload: UserEnvironmentVariablePayload,
): Promise<UserEnvironmentVariable> {
  const response = await axiosInstance.patch<ApiEnvelope<UserEnvironmentVariable>>(
    `${root}/${encodeURIComponent(id)}`,
    payload,
  );
  return response.data.data;
}

export async function deleteUserEnvironmentVariable(id: string): Promise<void> {
  await axiosInstance.delete(`${root}/${encodeURIComponent(id)}`);
}
