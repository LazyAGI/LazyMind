import { cloudApi, silentCloudRequest } from "@/api/cloudClient";
import type { CloudSessionStatus, CloudLoginStart as LoginStart } from "@/api/generated/core-client";

export const LAZYMIND_CLOUD_SESSION_CHANGED_EVENT = "lazymind:cloud-session-changed";

export type CloudSessionState = CloudSessionStatus["state"];
export type CloudReachability = CloudSessionStatus["reachability"];
export type CloudSession = CloudSessionStatus;
export type CloudLoginStart = LoginStart;

export async function getCloudSession(): Promise<CloudSession> {
  const response = await cloudApi.apiCoreCloudSessionGet(silentCloudRequest);
	return response.data.data ?? {
	  configured: false,
	  reachability: "unknown",
	  state: "signed_out",
	};
}

export function isCloudBusinessAvailable(session?: Partial<CloudSession> | null): boolean {
	return Boolean(
	  session?.configured === true &&
	  session.reachability === "reachable" &&
	  session.state === "signed_in",
	);
}

export async function logoutCloudSession(): Promise<CloudSession> {
  const response = await cloudApi.apiCoreCloudLogoutPost();
	return response.data.data ?? {
	  configured: false,
	  reachability: "unknown",
	  state: "signed_out",
	};
}

export async function beginCloudLogin(): Promise<CloudLoginStart> {
  const response = await cloudApi.apiCoreCloudLoginPost();
  if (!response.data.data?.authorization_url) {
    throw new Error("Cloud login returned no authorization URL");
  }
  return response.data.data;
}
