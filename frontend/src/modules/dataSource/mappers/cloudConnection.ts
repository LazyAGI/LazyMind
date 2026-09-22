import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import type { FeishuAuthAccount } from "../common/feishuAccounts";
import {
  getFeishuConnectionAppId,
  normalizeFeishuAccountStatus,
  splitScopes,
} from "../utils/feishuAccount";

export function getCloudConnectionItems(payload: unknown): CloudConnectionResponse[] {
  const responsePayload = payload as {
    items?: CloudConnectionResponse[];
    data?: { items?: CloudConnectionResponse[] };
  };

  if (Array.isArray(responsePayload.items)) {
    return responsePayload.items;
  }
  if (Array.isArray(responsePayload.data?.items)) {
    return responsePayload.data.items;
  }
  return [];
}

export function mapCloudConnectionToFeishuAccount(
  connection: CloudConnectionResponse,
  cachedAccounts: FeishuAuthAccount[] = [],
): FeishuAuthAccount {
  const providerMeta = connection.provider_account_meta || {};
  const safeCachedAccounts = Array.isArray(cachedAccounts) ? cachedAccounts : [];
  const cachedAccount =
    safeCachedAccounts.find((item) => item.connection?.connectionId === connection.connection_id) ||
    safeCachedAccounts.find(
      (item) =>
        item.appId &&
        (item.appId === providerMeta.client_id ||
          item.appId === providerMeta.app_id ||
          item.appId === connection.provider_account_id),
    );
  const appId = `${getFeishuConnectionAppId(connection) || cachedAccount?.appId || connection.connection_id}`;
  const displayName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.tenant_name ||
    cachedAccount?.name ||
    appId;
  const status = normalizeFeishuAccountStatus(connection.status);

  // Resolve chat_enabled: server-side provider_options is the source of truth.
  // Fall back to provider_account_meta, then cached local state.
  const providerOptions = connection.provider_options || {};
  const serverChatEnabled =
    providerOptions.chat_enabled ?? providerOptions.chatEnabled ??
    providerMeta.chat_enabled ?? providerMeta.chatEnabled;
  const rawChatEnabled =
    serverChatEnabled != null ? Boolean(serverChatEnabled) : (cachedAccount?.chatEnabled ?? false);

  return {
    id: connection.connection_id,
    name: displayName,
    appId,
    appSecret: cachedAccount?.appSecret || "",
    chatEnabled: rawChatEnabled,
    canUseChat: connection.can_use_chat,
    status,
    connection: {
      provider: "feishu",
      connectionId: connection.connection_id,
      connectionMethod: connection.connection_method,
      status,
      accountName: displayName,
      grantedScopes: splitScopes(connection.scope),
      connectedAt: connection.last_used_at || connection.updated_at || connection.created_at,
      tenantKey: connection.provider_tenant_key,
      openId: connection.provider_account_id,
    },
    createdAt: connection.created_at,
    updatedAt: connection.updated_at || undefined,
    lastAuthorizedAt: connection.last_used_at || connection.updated_at || undefined,
    connection_method:
      connection.connection_method === "cli_personal_app"
        ? "cli_personal_app"
        : connection.connection_method === "managed_oauth"
          ? "managed_oauth"
          : "legacy_byo",
    credential_location:
      connection.credential_location === "cli_sidecar"
        ? "cli_sidecar"
        : connection.credential_location === "cloud"
          ? "cloud"
          : "local",
  };
}
