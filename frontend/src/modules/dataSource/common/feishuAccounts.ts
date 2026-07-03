import type { FeishuDataSourceConnection } from "@/modules/dataSource/common/feishuOAuth";
import { FEISHU_APP_SETUP_STORAGE_KEY } from "../constants/options";
import type { FeishuAppSetup, OAuthState } from "../constants/types";

export const FEISHU_AUTH_ACCOUNTS_STORAGE_KEY =
  "lazymind:datasource:feishu:auth-accounts";

export interface FeishuAccountFormValues extends FeishuAppSetup {
  name?: string;
}

export interface FeishuAuthAccount {
  id: string;
  name: string;
  appId: string;
  appSecret: string;
  chatEnabled: boolean;
  status: OAuthState;
  connection: FeishuDataSourceConnection | null;
  createdAt: string;
  updatedAt?: string;
  lastAuthorizedAt?: string;
}

export function createFeishuAccountId() {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `feishu-account-${crypto.randomUUID()}`;
  }
  return `feishu-account-${Date.now()}`;
}

export function loadFeishuAppSetup(): FeishuAppSetup | null {
  try {
    const raw = localStorage.getItem(FEISHU_APP_SETUP_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<FeishuAppSetup>;
    const appId = typeof parsed.appId === "string" ? parsed.appId.trim() : "";
    const appSecret =
      typeof parsed.appSecret === "string" ? parsed.appSecret.trim() : "";
    if (!appId || !appSecret) {
      return null;
    }
    return { appId, appSecret };
  } catch {
    return null;
  }
}

export function persistFeishuAppSetup(setup: FeishuAppSetup) {
  localStorage.setItem(FEISHU_APP_SETUP_STORAGE_KEY, JSON.stringify(setup));
}

export function clearFeishuAppSetup() {
  localStorage.removeItem(FEISHU_APP_SETUP_STORAGE_KEY);
}

export function loadFeishuAuthAccounts(): FeishuAuthAccount[] {
  try {
    const raw = localStorage.getItem(FEISHU_AUTH_ACCOUNTS_STORAGE_KEY);
    if (!raw) {
      return [];
    }
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }

    return parsed
      .map((item) => {
        const appId = typeof item?.appId === "string" ? item.appId.trim() : "";
        const appSecret =
          typeof item?.appSecret === "string" ? item.appSecret.trim() : "";
        if (!appId || !appSecret) {
          return null;
        }

        return {
          id:
            typeof item?.id === "string" && item.id.trim()
              ? item.id.trim()
              : createFeishuAccountId(),
          name:
            typeof item?.name === "string" && item.name.trim()
              ? item.name.trim()
              : appId,
          appId,
          appSecret,
          chatEnabled: item?.chatEnabled === true,
          status: (item?.status || "pending") as OAuthState,
          connection: item?.connection || null,
          createdAt:
            typeof item?.createdAt === "string" && item.createdAt.trim()
              ? item.createdAt
              : new Date().toISOString(),
          updatedAt: item?.updatedAt,
          lastAuthorizedAt: item?.lastAuthorizedAt,
        } satisfies FeishuAuthAccount;
      })
      .filter(Boolean) as FeishuAuthAccount[];
  } catch {
    return [];
  }
}

export function persistFeishuAuthAccounts(accounts: FeishuAuthAccount[]) {
  localStorage.setItem(
    FEISHU_AUTH_ACCOUNTS_STORAGE_KEY,
    JSON.stringify(accounts),
  );
}

export function getOAuthStateFromConnection(
  connection?: FeishuDataSourceConnection | null,
): OAuthState {
  if (!connection) {
    return "pending";
  }

  if (connection.status === "connected") {
    return "connected";
  }
  if (connection.status === "expired") {
    return "expired";
  }
  if (connection.status === "error") {
    return "error";
  }

  return "pending";
}
