import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import type { FeishuAuthAccount } from "../common/feishuAccounts";
import type { FeishuConnectionStatus } from "../oauth/types";

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
