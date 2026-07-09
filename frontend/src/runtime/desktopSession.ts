import { AgentAppsAuth, type UserInfo } from "@/components/auth";
import { apiUrl } from "./apiBase";
import { runtimeFeatures } from "./features";

let desktopSessionPromise: Promise<UserInfo | null> | null = null;

export interface DesktopSessionOptions {
  force?: boolean;
}

export function isDesktopSessionEnabled(): boolean {
  return runtimeFeatures.localAutoLogin;
}

export function shouldHideDesktopUserControls(): boolean {
  return runtimeFeatures.hideDesktopUserControls;
}

export async function ensureDesktopSession(
  options?: DesktopSessionOptions,
): Promise<UserInfo | null> {
  if (!isDesktopSessionEnabled()) {
    return AgentAppsAuth.getUserInfo();
  }

  const current = AgentAppsAuth.getUserInfo();
  if (current?.token && !options?.force) {
    return current;
  }

  if (!desktopSessionPromise || options?.force) {
    desktopSessionPromise = (async () => {
      const session = await requestLocalAdminSession(Boolean(options?.force));
      if (!session?.token) {
        throw new Error("Local admin session did not return an access token");
      }
      AgentAppsAuth.setUserInfo(session);
      return AgentAppsAuth.getUserInfo();
    })().finally(() => {
      desktopSessionPromise = null;
    });
  }

  return desktopSessionPromise;
}

export async function restoreDesktopSessionAndGetToken(): Promise<string> {
  const userInfo = await ensureDesktopSession({ force: true });
  const token = userInfo?.token || "";
  if (!token) {
    throw new Error("Local admin session did not return an access token");
  }
  return token;
}

async function requestLocalAdminSession(force: boolean): Promise<UserInfo> {
  const path = force ? "/_local/admin-session?force=true" : "/_local/admin-session";
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
