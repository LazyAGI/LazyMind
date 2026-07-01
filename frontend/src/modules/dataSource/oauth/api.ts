import type {
  CloudConnectionUpdateBody,
  CloudOAuthAuthorizeURLBody,
  CloudOAuthCallbackBody,
} from "@/api/generated/auth-client";
import i18n from "@/i18n";
import { dataSourceCloudOauthApi } from "../api/clients";
import { unwrapApiData } from "../api/unwrap";
import {
  getErrorMessage,
  getFeishuOAuthCallbackErrorMessage,
  hasBusinessError,
  normalizeConnection,
} from "./mappers";
import {
  clearPendingCloudOAuthSession,
  loadPendingCloudOAuthSession,
  savePendingCloudOAuthSession,
} from "./storage";
import type { CloudDataSourceProvider, FeishuAuthorizeUrlInput } from "./types";
import {
  getCloudDataSourceCallbackUrl,
  normalizeSameOriginReturnUrl,
} from "./urls";

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

  const data = unwrapApiData<any>(payload);
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

  return unwrapApiData<any>(payload);
}
