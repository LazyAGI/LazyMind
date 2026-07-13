import { AgentAppsAuth, type UserInfo } from "@/components/auth";
import { apiUrl } from "./apiBase";
import { runtimeFeatures } from "./features";

let localSessionPromise: Promise<UserInfo | null> | null = null;
export let localSessionInitialized = false;

export interface LocalSessionOptions {
  force?: boolean;
}

export function isLocalSessionEnabled(): boolean {
  return runtimeFeatures.localLikeAutoLogin;
}

export function shouldHideLocalUserControls(): boolean {
  return runtimeFeatures.hideLocalUserControls;
}

export async function ensureLocalSession(
  options?: LocalSessionOptions,
): Promise<UserInfo | null> {
  if (!isLocalSessionEnabled()) {
    return AgentAppsAuth.getUserInfo();
  }

  const current = AgentAppsAuth.getUserInfo();
  if (current?.token && localSessionInitialized && !options?.force) {
    return current;
  }

  if (!localSessionPromise) {
    localSessionPromise = (async () => {
      const session = await requestLocalAdminSession(Boolean(options?.force));
      if (!session?.token) {
        throw new Error("Local admin session did not return an access token");
      }
      AgentAppsAuth.setUserInfo(session);
      localSessionInitialized = true;
      return AgentAppsAuth.getUserInfo();
    })().finally(() => {
      localSessionPromise = null;
    });
  }

  return localSessionPromise;
}

export async function restoreLocalSessionAndGetToken(): Promise<string> {
  const userInfo = await ensureLocalSession({ force: true });
  const token = userInfo?.token || "";
  if (!token) {
    throw new Error("Local admin session did not return an access token");
  }
  return token;
}

async function requestLocalAdminSession(force: boolean): Promise<UserInfo> {
  const path = force
    ? "/_local/admin-session?force=true"
    : "/_local/admin-session";
  const response = await fetch(apiUrl(path), {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    const detail =
      payload?.detail ||
      payload?.message ||
      payload?.error ||
      response.statusText;
    throw new Error(`Local admin session request failed (${response.status}): ${detail}`);
  }
  return payload?.data || payload;
}
