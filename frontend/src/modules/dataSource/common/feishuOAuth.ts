import { BASE_URL } from "@/components/request";
import i18n from "@/i18n";
import {
  dataSourceCloudOauthApi,
  unwrapDataSourceApiData,
} from "@/modules/dataSource/api";
import type {
  CloudConnectionUpdateBody,
  CloudOAuthAuthorizeURLBody,
  CloudOAuthCallbackBody,
} from "@/api/generated/auth-client";

const RESULT_STORAGE_KEY = "lazymind:datasource:feishu-oauth:result";
const DRAFT_STORAGE_KEY = "lazymind:datasource:feishu-oauth:draft";
const PENDING_STORAGE_KEY = "lazymind:datasource:feishu-oauth:pending";
const PENDING_STORAGE_KEY_PREFIX = `${PENDING_STORAGE_KEY}:`;

export const FEISHU_DATA_SOURCE_OAUTH_CHANNEL =
  "lazymind:datasource:feishu-oauth";
export const CLOUD_DATA_SOURCE_OAUTH_CHANNEL = FEISHU_DATA_SOURCE_OAUTH_CHANNEL;

export type CloudDataSourceProvider = "feishu" | "notion";

export type FeishuConnectionStatus =
  | "pending"
  | "connected"
  | "expired"
  | "error";

export interface FeishuDataSourceConnection {
  provider: CloudDataSourceProvider;
  connectionId: string;
  status: FeishuConnectionStatus;
  accountName: string;
  grantedScopes: string[];
  connectedAt?: string;
  expiresAt?: string;
  refreshExpiresAt?: string;
  tenantKey?: string;
  openId?: string;
  unionId?: string;
  avatarUrl?: string;
  accessTokenMasked?: string;
  refreshTokenMasked?: string;
}

export interface FeishuDataSourceWizardDraft {
  activeView?: "assets" | "connectors";
  authSelectModalOpen?: boolean;
  wizardOpen: boolean;
  wizardStep: number;
  wizardMode: "create" | "edit";
  selectedType: string | null;
  editingId: string | null;
  validatedAgentId?: string | null;
  oauthState: string;
  connectionVerified: boolean;
  oauthConnection: FeishuDataSourceConnection | null;
  formValues: Record<string, unknown>;
}

interface FeishuPendingOAuthSession {
  provider?: CloudDataSourceProvider;
  tenantId: string;
  connectionId: string;
  state: string;
  redirectUri: string;
  returnUrl: string;
}

export type FeishuDataSourceOAuthMessage =
  | {
      channel: typeof FEISHU_DATA_SOURCE_OAUTH_CHANNEL;
      source: "feishu-data-source" | "notion-data-source";
      status: "success";
      connection: FeishuDataSourceConnection;
    }
  | {
      channel: typeof FEISHU_DATA_SOURCE_OAUTH_CHANNEL;
      source: "feishu-data-source" | "notion-data-source";
      status: "error";
      message: string;
      provider?: CloudDataSourceProvider;
    };

export type CloudDataSourceOAuthMessage = FeishuDataSourceOAuthMessage;

function getBaseName() {
  return ((window as Window & { BASENAME?: string }).BASENAME || "").trim();
}

function getApiOrigin() {
  return new URL(BASE_URL || window.location.origin, window.location.origin).origin;
}

function buildAppUrlFromApiOrigin(path: string) {
  const baseName = getBaseName().replace(/\/$/, "");
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${getApiOrigin()}${baseName}${normalizedPath}`;
}

function normalizeSameOriginReturnUrl(value?: string) {
  const fallbackUrl = getDataSourceManagementUrl();

  if (!value) {
    return fallbackUrl;
  }

  try {
    const url = new URL(value, window.location.href);
    if (url.origin !== window.location.origin) {
      return fallbackUrl;
    }

    if (/\/oauth\/(feishu|notion)(\/data-source)?\/callback$/.test(url.pathname)) {
      return fallbackUrl;
    }

    return url.href;
  } catch {
    return fallbackUrl;
  }
}

function unwrapPayload<T>(payload: any): T {
  return (payload?.data || payload) as T;
}

function savePendingFeishuOAuthSession(payload: FeishuPendingOAuthSession) {
  const serialized = JSON.stringify(payload);
  sessionStorage.setItem(PENDING_STORAGE_KEY, serialized);
  sessionStorage.setItem(`${PENDING_STORAGE_KEY_PREFIX}${payload.state}`, serialized);
}

function parsePendingFeishuOAuthSession(raw: string | null) {
  if (!raw) {
    return null;
  }

  try {
    return JSON.parse(raw) as FeishuPendingOAuthSession;
  } catch {
    return null;
  }
}

function loadPendingFeishuOAuthSession(state: string) {
  const pendingByState = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(`${PENDING_STORAGE_KEY_PREFIX}${state}`),
  );

  if (pendingByState?.state === state) {
    return pendingByState;
  }

  const pending = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(PENDING_STORAGE_KEY),
  );

  if (pending?.state === state) {
    return pending;
  }

  return null;
}

function getProviderStorageKey(baseKey: string, provider: CloudDataSourceProvider) {
  return provider === "feishu" ? baseKey : baseKey.replace(":feishu-oauth", `:${provider}-oauth`);
}

function savePendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  payload: FeishuPendingOAuthSession,
) {
  if (provider === "feishu") {
    savePendingFeishuOAuthSession({ ...payload, provider });
    return;
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  const serialized = JSON.stringify({ ...payload, provider });
  sessionStorage.setItem(storageKey, serialized);
  sessionStorage.setItem(`${storageKeyPrefix}${payload.state}`, serialized);
}

function loadPendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  state: string,
) {
  if (provider === "feishu") {
    return loadPendingFeishuOAuthSession(state);
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  const pendingByState = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(`${storageKeyPrefix}${state}`),
  );

  if (pendingByState?.state === state) {
    return pendingByState;
  }

  const pending = parsePendingFeishuOAuthSession(sessionStorage.getItem(storageKey));
  if (pending?.state === state) {
    return pending;
  }

  return null;
}

function clearPendingCloudOAuthSession(
  provider: CloudDataSourceProvider,
  state: string,
) {
  if (provider === "feishu") {
    clearPendingFeishuOAuthSession(state);
    return;
  }

  const storageKey = getProviderStorageKey(PENDING_STORAGE_KEY, provider);
  const storageKeyPrefix = `${storageKey}:`;
  sessionStorage.removeItem(`${storageKeyPrefix}${state}`);
  const pending = parsePendingFeishuOAuthSession(sessionStorage.getItem(storageKey));

  if (!pending || pending.state === state) {
    sessionStorage.removeItem(storageKey);
  }
}

function clearPendingFeishuOAuthSession(state: string) {
  sessionStorage.removeItem(`${PENDING_STORAGE_KEY_PREFIX}${state}`);
  const pending = parsePendingFeishuOAuthSession(
    sessionStorage.getItem(PENDING_STORAGE_KEY),
  );

  if (!pending || pending.state === state) {
    sessionStorage.removeItem(PENDING_STORAGE_KEY);
  }
}

function getErrorMessage(payload: any, fallback: string) {
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

function hasBusinessError(payload: any) {
  const code = payload?.code ?? payload?.data?.code;
  if (code === undefined || code === null || code === "") {
    return false;
  }

  return !["0", "200"].includes(String(code).trim());
}

function getFeishuOAuthCallbackErrorMessage(payload: any) {
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

function normalizeConnection(
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

export function getFeishuDataSourceCallbackUrl() {
  return buildAppUrlFromApiOrigin("/oauth/feishu/callback");
}

export function getCloudDataSourceCallbackUrl(provider: CloudDataSourceProvider) {
  if (provider === "feishu") {
    return getFeishuDataSourceCallbackUrl();
  }
  return buildAppUrlFromApiOrigin(`/oauth/${provider}/data-source/callback`);
}

export function getDataSourceManagementUrl(provider: CloudDataSourceProvider = "feishu") {
  return `${window.location.origin}${getBaseName()}/data-sources/providers/${provider}`;
}

export function getFeishuDataSourceOAuthReturnUrl(state?: string | null) {
  return getCloudDataSourceOAuthReturnUrl("feishu", state);
}

export function getCloudDataSourceOAuthReturnUrl(
  provider: CloudDataSourceProvider,
  state?: string | null,
) {
  if (!state) {
    return getDataSourceManagementUrl(provider);
  }

  const pending = loadPendingCloudOAuthSession(provider, state);
  return normalizeSameOriginReturnUrl(pending?.returnUrl);
}

export function openCenteredPopup(url: string, title: string) {
  const width = 560;
  const height = 760;
  const dualScreenLeft =
    window.screenLeft !== undefined ? window.screenLeft : window.screenX;
  const dualScreenTop =
    window.screenTop !== undefined ? window.screenTop : window.screenY;
  const viewportWidth = window.innerWidth || document.documentElement.clientWidth;
  const viewportHeight =
    window.innerHeight || document.documentElement.clientHeight;
  const left = Math.max(0, dualScreenLeft + (viewportWidth - width) / 2);
  const top = Math.max(0, dualScreenTop + (viewportHeight - height) / 2);

  return window.open(
    url,
    title,
    [
      `width=${width}`,
      `height=${height}`,
      `left=${Math.round(left)}`,
      `top=${Math.round(top)}`,
      "resizable=yes",
      "scrollbars=yes",
    ].join(","),
  );
}

type FeishuAuthorizeUrlInput =
  | {
      tenantId: string;
      appId: string;
      appSecret: string;
      scopes: string[];
      returnUrl?: string;
      reauthorizeConnectionId?: never;
    }
  | {
      tenantId: string;
      scopes: string[];
      returnUrl?: string;
      appId?: never;
      appSecret?: never;
      reauthorizeConnectionId: string;
    };

export async function requestCloudDataSourceAuthorizeUrl(
  provider: CloudDataSourceProvider,
  input: FeishuAuthorizeUrlInput,
) {
  const redirectUri = getCloudDataSourceCallbackUrl(provider);
  const appId = input.appId?.trim();
  const appSecret = input.appSecret?.trim();
  const reauthorizeConnectionId = input.reauthorizeConnectionId?.trim();
  const body: Record<string, unknown> = {
    auth_mode: "oauth_user",
    redirect_uri: redirectUri,
  };

  if (reauthorizeConnectionId) {
    body.reauthorize_connection_id = reauthorizeConnectionId;
  } else {
    body.tenant_id = input.tenantId;
    body.scope = input.scopes.join(" ");
    if (appId && appSecret) {
      body.client_id = appId;
      body.client_secret = appSecret;
    }
  }

  let payload: unknown;
  try {
    const response = await dataSourceCloudOauthApi.oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost({
      provider,
      cloudOAuthAuthorizeURLBody: body as CloudOAuthAuthorizeURLBody,
    });
    payload = response.data;
  } catch (error: any) {
    payload = error?.response?.data || error;
    throw new Error(getErrorMessage(payload, i18n.t("admin.dataSourceAuthorizeUrlFailed")));
  }

  const data = unwrapDataSourceApiData<any>(payload);
  const authorizeUrl = data?.authorize_url || data?.authorizeUrl;
  const connectionId = data?.connection_id || data?.connectionId;
  const state = data?.state;

  if (
    hasBusinessError(payload) ||
    typeof authorizeUrl !== "string" ||
    !authorizeUrl.trim() ||
    typeof connectionId !== "string" ||
    !connectionId.trim() ||
    typeof state !== "string" ||
    !state.trim()
  ) {
    throw new Error(getErrorMessage(payload, i18n.t("admin.dataSourceAuthorizeUrlFailed")));
  }

  savePendingCloudOAuthSession(provider, {
    tenantId: input.tenantId.trim(),
    connectionId: connectionId.trim(),
    state: state.trim(),
    redirectUri,
    returnUrl: normalizeSameOriginReturnUrl(input.returnUrl || window.location.href),
  });

  return authorizeUrl;
}

export async function requestFeishuDataSourceAuthorizeUrl(input: FeishuAuthorizeUrlInput) {
  return requestCloudDataSourceAuthorizeUrl("feishu", input);
}

export async function finishCloudDataSourceOAuth(
  provider: CloudDataSourceProvider,
  code: string,
  state: string,
) {
  const pending = loadPendingCloudOAuthSession(provider, state);
  if (!pending?.tenantId || !pending.connectionId || !pending.redirectUri) {
    throw new Error(i18n.t("admin.dataSourceOauthSessionMissing"));
  }

  if (pending.state && pending.state !== state) {
    throw new Error(i18n.t("admin.dataSourceOauthStateMismatch"));
  }

  let payload: unknown;
  try {
    const response = await dataSourceCloudOauthApi.oauthCallbackApiAuthserviceV1CloudProviderOauthCallbackPost({
      provider,
      cloudOAuthCallbackBody: {
        tenant_id: pending.tenantId,
        connection_id: pending.connectionId,
        code,
        state,
        redirect_uri: pending.redirectUri,
      } satisfies CloudOAuthCallbackBody,
    });
    payload = response.data;
  } catch (error: any) {
    payload = error?.response?.data || error;
    throw new Error(getFeishuOAuthCallbackErrorMessage(payload));
  }

  if (
    hasBusinessError(payload)
  ) {
    throw new Error(getFeishuOAuthCallbackErrorMessage(payload));
  }

  clearPendingCloudOAuthSession(provider, state);
  return normalizeConnection(payload, pending.connectionId, provider);
}

export async function finishFeishuDataSourceOAuth(code: string, state: string) {
  return finishCloudDataSourceOAuth("feishu", code, state);
}

export async function enableCloudConnectionForChat(connectionId: string) {
  const body = {
    chat_enabled: true,
    chatEnabled: true,
  } satisfies CloudConnectionUpdateBody;
  let payload: unknown;

  try {
    const response = await dataSourceCloudOauthApi.patchConnectionApiAuthserviceV1CloudConnectionsConnectionIdPatch({
      connectionId,
      cloudConnectionUpdateBody: body,
    });
    payload = response.data;
  } catch (error: any) {
    payload = error?.response?.data || error;
    throw new Error(getErrorMessage(payload, i18n.t("common.requestFailed")));
  }

  if (hasBusinessError(payload)) {
    throw new Error(getErrorMessage(payload, i18n.t("common.requestFailed")));
  }

  return unwrapDataSourceApiData<any>(payload);
}

export function saveFeishuDataSourceOAuthResult(
  payload: FeishuDataSourceOAuthMessage,
) {
  const provider =
    payload.status === "success" ? payload.connection.provider : payload.provider || "feishu";
  saveCloudDataSourceOAuthResult(provider, payload);
}

export function saveCloudDataSourceOAuthResult(
  provider: CloudDataSourceProvider,
  payload: CloudDataSourceOAuthMessage,
) {
  sessionStorage.setItem(
    getProviderStorageKey(RESULT_STORAGE_KEY, provider),
    JSON.stringify(payload),
  );
}

export function consumeFeishuDataSourceOAuthResult() {
  return consumeCloudDataSourceOAuthResult("feishu");
}

export function consumeCloudDataSourceOAuthResult(provider: CloudDataSourceProvider) {
  const storageKey = getProviderStorageKey(RESULT_STORAGE_KEY, provider);
  const raw = sessionStorage.getItem(storageKey);
  if (!raw) {
    return null;
  }

  sessionStorage.removeItem(storageKey);

  try {
    return JSON.parse(raw) as CloudDataSourceOAuthMessage;
  } catch {
    return null;
  }
}

export function saveFeishuDataSourceWizardDraft(
  payload: FeishuDataSourceWizardDraft,
) {
  sessionStorage.setItem(DRAFT_STORAGE_KEY, JSON.stringify(payload));
}

export function clearFeishuDataSourceWizardDraft() {
  sessionStorage.removeItem(DRAFT_STORAGE_KEY);
}

export function consumeFeishuDataSourceWizardDraft() {
  const raw = sessionStorage.getItem(DRAFT_STORAGE_KEY);
  if (!raw) {
    return null;
  }

  sessionStorage.removeItem(DRAFT_STORAGE_KEY);

  try {
    return JSON.parse(raw) as FeishuDataSourceWizardDraft;
  } catch {
    return null;
  }
}
