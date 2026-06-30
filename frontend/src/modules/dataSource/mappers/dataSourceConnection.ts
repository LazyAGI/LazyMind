import type { CloudConnectionResponse } from "@/api/generated/auth-client";
import type { FeishuAuthAccount } from "../common/feishuAccounts";
import type {
  CloudDataSourceProvider,
  FeishuDataSourceConnection,
} from "../oauth/types";
import { normalizeFeishuAccountStatus, splitScopes } from "../utils/feishuAccount";

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
  cachedAccounts: FeishuAuthAccount[],
): FeishuAuthAccount {
  const providerMeta = connection.provider_account_meta || {};
  const cachedAccount =
    cachedAccounts.find(
      (item) => item.connection?.connectionId === connection.connection_id,
    ) ||
    cachedAccounts.find(
      (item) =>
        item.appId &&
        (item.appId === providerMeta.client_id ||
          item.appId === providerMeta.app_id ||
          item.appId === connection.provider_account_id),
    );
  const appId = `${providerMeta.client_id || providerMeta.app_id || cachedAccount?.appId || connection.provider_account_id || connection.connection_id}`;
  const displayName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.tenant_name ||
    cachedAccount?.name ||
    appId;
  const status = normalizeFeishuAccountStatus(connection.status);
  const providerOptions = connection.provider_options || {};
  const serverChatEnabled =
    providerOptions.chat_enabled ??
    providerOptions.chatEnabled ??
    providerMeta.chat_enabled ??
    providerMeta.chatEnabled;
  const rawChatEnabled =
    serverChatEnabled != null
      ? Boolean(serverChatEnabled)
      : cachedAccount?.chatEnabled ?? false;

  return {
    id: connection.connection_id,
    name: displayName,
    appId,
    appSecret: cachedAccount?.appSecret || "",
    chatEnabled: status === "connected" ? rawChatEnabled : false,
    status,
    connection: {
      provider: "feishu",
      connectionId: connection.connection_id,
      status,
      accountName: displayName,
      grantedScopes: splitScopes(connection.scope),
      connectedAt:
        connection.last_used_at || connection.updated_at || connection.created_at,
      tenantKey: connection.provider_tenant_key,
      openId: connection.provider_account_id,
    },
    createdAt: connection.created_at,
    updatedAt: connection.updated_at || undefined,
    lastAuthorizedAt: connection.last_used_at || connection.updated_at || undefined,
  };
}

export function mapCloudConnectionToDataSourceConnection(
  connection: CloudConnectionResponse,
  provider: CloudDataSourceProvider,
): FeishuDataSourceConnection {
  const providerMeta = connection.provider_account_meta || {};
  const status = normalizeFeishuAccountStatus(connection.status);
  const accountName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.workspace_name ||
    providerMeta.tenant_name ||
    providerMeta.owner_name ||
    connection.provider_account_id ||
    connection.connection_id;

  return {
    provider,
    connectionId: connection.connection_id,
    status,
    accountName,
    grantedScopes: splitScopes(connection.scope),
    connectedAt:
      connection.last_used_at || connection.updated_at || connection.created_at,
    tenantKey: connection.provider_tenant_key,
    openId: connection.provider_account_id,
    avatarUrl: providerMeta.avatar_url || providerMeta.icon_url,
  };
}
