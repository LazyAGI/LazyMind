import type {
  CloudOAuthAuthorizeURLBody,
  CloudOAuthCallbackBody,
} from "@/api/generated/auth-client";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
import { unwrapApiData } from "@/modules/dataSource/api/unwrap";
import { hasBusinessError } from "@/modules/dataSource/oauth/mappers";
import {
  getAppUrl,
  openCenteredPopup,
} from "@/modules/dataSource/oauth/urls";

export const WORKBUDDY_OAUTH_CHANNEL = "lazymind:workbuddy-oauth";

const PENDING_KEY = "lazymind:workbuddy-oauth:pending";
const REQUIRED_SCOPE =
  "user.localassistant.readable user.localassistant.invokable";

interface PendingWorkBuddyOAuth {
  connectionId: string;
  redirectUri: string;
  state: string;
}

function pendingKey(state: string) {
  return `${PENDING_KEY}:${state}`;
}

function savePending(payload: PendingWorkBuddyOAuth) {
  const encoded = JSON.stringify(payload);
  for (const storage of [sessionStorage, localStorage]) {
    storage.setItem(PENDING_KEY, encoded);
    storage.setItem(pendingKey(payload.state), encoded);
  }
}

function loadPending(state: string): PendingWorkBuddyOAuth | null {
  for (const storage of [sessionStorage, localStorage]) {
    for (const key of [pendingKey(state), PENDING_KEY]) {
      try {
        const value = JSON.parse(storage.getItem(key) || "null");
        if (value?.state === state) return value;
      } catch {
        // Ignore stale browser storage and let the caller report a missing session.
      }
    }
  }
  return null;
}

function clearPending(state: string) {
  for (const storage of [sessionStorage, localStorage]) {
    storage.removeItem(pendingKey(state));
    const current = loadStored(storage.getItem(PENDING_KEY));
    if (!current || current.state === state) storage.removeItem(PENDING_KEY);
  }
}

function loadStored(raw: string | null): PendingWorkBuddyOAuth | null {
  try {
    return JSON.parse(raw || "null");
  } catch {
    return null;
  }
}

export async function startWorkBuddyAuthorization(): Promise<void> {
  const redirectUri = getAppUrl("/oauth/workbuddy/callback");
  const response =
    await dataSourceCloudOauthApi.oauthAuthorizeUrlApiAuthserviceV1CloudProviderOauthAuthorizeUrlPost(
      {
        provider: "workbuddy",
        cloudOAuthAuthorizeURLBody: {
          auth_mode: "oauth_user",
          redirect_uri: redirectUri,
          scope: REQUIRED_SCOPE,
          provider_options: { chat_enabled: true, chatEnabled: true },
        } as CloudOAuthAuthorizeURLBody,
      },
    );
  const payload = response.data;
  const data = unwrapApiData<any>(payload);
  const authorizeUrl = `${data?.authorize_url || ""}`.trim();
  const connectionId = `${data?.connection_id || ""}`.trim();
  const state = `${data?.state || ""}`.trim();
  if (
    hasBusinessError(payload) ||
    !authorizeUrl ||
    !connectionId ||
    !state
  ) {
    throw new Error(getLocalizedErrorMessage({ response: { data: payload } }));
  }
  savePending({ connectionId, redirectUri, state });
  if (!openCenteredPopup(authorizeUrl, "WorkBuddy OAuth")) {
    window.location.assign(authorizeUrl);
  }
}

export async function finishWorkBuddyAuthorization(
  code: string,
  state: string,
): Promise<void> {
  const pending = loadPending(state);
  if (!pending) {
    throw new Error("WorkBuddy authorization session is missing or expired");
  }
  const response =
    await dataSourceCloudOauthApi.oauthCallbackApiAuthserviceV1CloudProviderOauthCallbackPost(
      {
        provider: "workbuddy",
        cloudOAuthCallbackBody: {
          tenant_id: "",
          connection_id: pending.connectionId,
          code,
          state,
          redirect_uri: pending.redirectUri,
        } satisfies CloudOAuthCallbackBody,
      },
    );
  if (hasBusinessError(response.data)) {
    throw new Error(
      getLocalizedErrorMessage({ response: { data: response.data } }),
    );
  }
  clearPending(state);
}
