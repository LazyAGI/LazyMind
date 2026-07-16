import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import type { FeishuAuthAccount } from "../common/feishuAccounts";
import type {
  CloudDataSourceProvider,
  FeishuConnectionStatus,
  FeishuDataSourceConnection,
} from "../oauth/types";

export function isFeishuAppId(value?: string | null) {
  return /^cli_[a-z0-9]+$/i.test(`${value || ""}`.trim());
}

export function getFeishuConnectionAppId(connection: CloudConnectionResponse) {
  const providerMeta = connection.provider_account_meta || {};
  const connectionMeta = connection as CloudConnectionResponse & {
    appid?: unknown;
    appId?: unknown;
    app_id?: unknown;
  };
  return [
    providerMeta.client_id,
    providerMeta.app_id,
    providerMeta.appid,
    providerMeta.appId,
    connectionMeta.appid,
    connectionMeta.appId,
    connectionMeta.app_id,
    connection.provider_account_id,
  ].find((value) => isFeishuAppId(`${value || ""}`));
}

export function normalizeFeishuAccountStatus(status?: string): FeishuConnectionStatus {
  const normalized = `${status || ""}`.trim().toLowerCase();
  if (["active", "connected", "success", "succeeded", "enabled"].includes(normalized)) {
    return "connected";
  }
  if (["expired", "inactive"].includes(normalized)) {
    return "expired";
  }
  if (["error", "failed", "failure", "invalid"].includes(normalized)) {
    return "error";
  }
  return "pending";
}

export function isFeishuAccountAuthValid(account: FeishuAuthAccount) {
  return account.status === "connected" && Boolean(account.connection?.connectionId?.trim());
}

/**
 * Resolve a usable OAuth connection for browse/save.
 * List API usually drops the hydrated oauthConnection object and only keeps authConnectionId.
 */
export function resolveCloudAuthConnection(
  preferred: FeishuDataSourceConnection | null | undefined,
  authConnectionId: string | null | undefined,
  accounts: FeishuAuthAccount[],
  provider: CloudDataSourceProvider,
): FeishuDataSourceConnection | null {
  const preferredId = `${preferred?.connectionId || ""}`.trim();
  if (preferredId) {
    return preferred!;
  }

  const connectionId = `${authConnectionId || ""}`.trim();
  if (!connectionId) {
    return null;
  }

  const matched = accounts.find(
    (account) => `${account.connection?.connectionId || ""}`.trim() === connectionId,
  )?.connection;
  if (matched) {
    return matched;
  }

  return {
    provider,
    connectionId,
    status: "connected",
    accountName: connectionId,
    grantedScopes: [],
  };
}

export function formatValidFeishuAccountNames(
  accounts: FeishuAuthAccount[],
  maxDisplay = 3,
): string {
  const names = accounts.map(
    (account) => account.connection?.accountName || account.name,
  );
  const displayed = names.slice(0, maxDisplay);
  const hasMore = names.length > maxDisplay;
  return `${displayed.join("、")}${hasMore ? "..." : ""}`;
}

export function splitScopes(value?: string | null) {
  return `${value || ""}`
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function parseFeishuOAuthCallbackInput(value: string) {
  const normalized = value.trim();
  if (!normalized) {
    return null;
  }

  try {
    const url = new URL(normalized);
    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    if (code && state) {
      return { code, state };
    }
  } catch {
  }

  const search = normalized.startsWith("?") ? normalized.slice(1) : normalized;
  const params = new URLSearchParams(search);
  const code = params.get("code");
  const state = params.get("state");
  if (code && state) {
    return { code, state };
  }

  const matchCode = normalized.match(/[?&]code=([^&]+)/);
  const matchState = normalized.match(/[?&]state=([^&]+)/);
  if (matchCode?.[1] && matchState?.[1]) {
    return {
      code: decodeURIComponent(matchCode[1]),
      state: decodeURIComponent(matchState[1]),
    };
  }

  return null;
}
