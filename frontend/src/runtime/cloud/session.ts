import { axiosInstance, BASE_URL } from "@/components/request";

export const LAZYMIND_CLOUD_SESSION_CHANGED_EVENT = "lazymind:cloud-session-changed";

export type CloudSessionState =
  | "signed_out"
  | "authorizing"
  | "exchanging"
  | "restoring"
  | "signed_in"
  | "refreshing"
  | "reauth_required"
  | "offline";

export interface CloudSession {
  state: CloudSessionState;
  access_expires_at?: string;
  account_id?: string;
  username?: string;
  email_masked?: string;
  registration_url?: string;
}

interface CoreResponse<T> {
  data?: T;
}

export interface CloudLoginStart {
  authorization_url: string;
  expires_in_seconds: number;
}

export async function getCloudSession(): Promise<CloudSession> {
  const response = await axiosInstance.get<CoreResponse<CloudSession>>(
    `${BASE_URL}/api/core/cloud/session`,
  );
  return response.data.data ?? { state: "signed_out" };
}

export async function logoutCloudSession(): Promise<CloudSession> {
  const response = await axiosInstance.post<CoreResponse<CloudSession>>(
    `${BASE_URL}/api/core/cloud/logout`,
  );
  return response.data.data ?? { state: "signed_out" };
}

export async function beginCloudLogin(): Promise<CloudLoginStart> {
  const response = await axiosInstance.post<CoreResponse<CloudLoginStart>>(
    `${BASE_URL}/api/core/cloud/login`,
  );
  if (!response.data.data?.authorization_url) {
    throw new Error("Cloud login returned no authorization URL");
  }
  return response.data.data;
}
