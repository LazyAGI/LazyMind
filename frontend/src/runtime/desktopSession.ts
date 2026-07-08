import { AgentAppsAuth, type UserInfo } from "@/components/auth";
import { requestDesktopAdminSession } from "./desktopBridge";
import { runtimeFeatures } from "./features";

let desktopSessionPromise: Promise<UserInfo | null> | null = null;

export interface DesktopSessionOptions {
  force?: boolean;
}

export function isDesktopSessionEnabled(): boolean {
  return runtimeFeatures.desktopAutoLogin;
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

  if (!desktopSessionPromise) {
    desktopSessionPromise = (async () => {
      const session = await requestDesktopAdminSession();
      if (!session?.token) {
        throw new Error("Desktop admin session did not return an access token");
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
    throw new Error("Desktop admin session did not return an access token");
  }
  return token;
}
