import i18n from "@/i18n";
import type {
  CloudDataSourceProvider,
  FeishuConnectionStatus,
  FeishuDataSourceConnection,
} from "./types";

// Local envelope unwrap that returns the payload itself when `data` is absent
// or falsy. Kept separate from api/unwrap to preserve the historical behavior
// of the OAuth normalizers.
function unwrapPayload<T>(payload: any): T {
  return (payload?.data || payload) as T;
}

export function getErrorMessage(payload: any, fallback: string) {
  if (typeof payload?.message === "string" && payload.message.trim()) {
    return payload.message;
  }

  if (typeof payload?.detail === "string" && payload.detail.trim()) {
    return payload.detail;
  }

  if (Array.isArray(payload?.detail)) {
    const joined = payload.detail
      .map((item: any) =>
        typeof item === "string" ? item : item?.msg || item?.message,
      )
      .filter(Boolean)
      .join("；");

    if (joined) {
      return joined;
    }
  }

  return fallback;
}

export function hasBusinessError(payload: any) {
  const code = payload?.code ?? payload?.data?.code;
  if (code === undefined || code === null || code === "") {
    return false;
  }

  return !["0", "200"].includes(String(code).trim());
}

export function getFeishuOAuthCallbackErrorMessage(payload: any) {
  const normalizedPayload = unwrapPayload<any>(payload);
  const rawMessage = [
    payload?.code,
    payload?.message,
    payload?.ex_message,
    payload?.ex_mesage,
    normalizedPayload?.code,
    normalizedPayload?.message,
    normalizedPayload?.ex_message,
    normalizedPayload?.ex_mesage,
  ]
    .map((item) => `${item || ""}`.trim().toLowerCase())
    .filter(Boolean)
    .join(" ");

  if (
    rawMessage.includes("1000706") ||
    rawMessage.includes("reauthorized account does not match")
  ) {
    return i18n.t("admin.dataSourceOauthReauthorizeAccountMismatch");
  }

  return getErrorMessage(payload, i18n.t("admin.dataSourceOauthFailedRetry"));
}

function normalizeScopes(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value
      .map((item) => (typeof item === "string" ? item.trim() : ""))
      .filter(Boolean);
  }

  if (typeof value === "string") {
    return value
      .split(/[,\s]+/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  return [];
}

function normalizeStatus(value: unknown): FeishuConnectionStatus {
  const normalized = `${value || ""}`.trim().toLowerCase();
  const tokens = normalized
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
  const hasToken = (candidates: string[]) =>
    candidates.some((candidate) => tokens.includes(candidate));

  if (
    hasToken(["connected", "active", "ok", "success", "succeeded", "enabled"])
  ) {
    return "connected";
  }

  if (hasToken(["expired", "expire", "inactive"])) {
    return "expired";
  }

  if (
    hasToken(["error", "errored", "fail", "failed", "failure", "invalid"])
  ) {
    return "error";
  }

  return "pending";
}

function maskToken(token?: string) {
  if (!token) {
    return undefined;
  }

  if (token.length <= 12) {
    return `${token.slice(0, 3)}***${token.slice(-3)}`;
  }

  return `${token.slice(0, 6)}...${token.slice(-4)}`;
}

export function normalizeConnection(
  payload: any,
  fallbackConnectionId?: string,
  provider: CloudDataSourceProvider = "feishu",
): FeishuDataSourceConnection {
  const raw = unwrapPayload<any>(payload);
  const connection = raw?.connection || raw?.oauth_connection || raw;
  const accessToken = connection?.access_token || raw?.access_token;
  const refreshToken = connection?.refresh_token || raw?.refresh_token;

  return {
    provider,
    connectionId: String(
      connection?.connection_id ||
        connection?.id ||
        connection?.open_id ||
        raw?.connection_id ||
        fallbackConnectionId ||
        `${provider}-${Date.now()}`,
    ),
    status: normalizeStatus(connection?.status || raw?.status || "connected"),
    accountName:
      connection?.account_name ||
      connection?.accountName ||
      connection?.name ||
      connection?.display_name ||
      connection?.tenant_name ||
      connection?.workspace_name ||
      connection?.workspaceName ||
      i18n.t("admin.dataSourceFeishuConnectedAccountFallback"),
    grantedScopes: normalizeScopes(
      connection?.granted_scopes ||
        connection?.scope ||
        connection?.scopes ||
        raw?.granted_scopes ||
        raw?.scope ||
        raw?.scopes,
    ),
    connectedAt: connection?.connected_at || raw?.connected_at,
    expiresAt: connection?.expires_at || raw?.expires_at,
    refreshExpiresAt:
      connection?.refresh_expires_at || raw?.refresh_expires_at,
    tenantKey: connection?.tenant_key || raw?.tenant_key,
    openId: connection?.open_id || raw?.open_id,
    unionId: connection?.union_id || raw?.union_id,
    avatarUrl: connection?.avatar_url || raw?.avatar_url,
    accessTokenMasked:
      connection?.access_token_masked ||
      raw?.access_token_masked ||
      maskToken(accessToken),
    refreshTokenMasked:
      connection?.refresh_token_masked ||
      raw?.refresh_token_masked ||
      maskToken(refreshToken),
  };
}
